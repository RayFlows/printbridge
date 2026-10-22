package documents

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
)

var documentID = regexp.MustCompile(`^[a-f0-9]{32}$`)

type Service struct {
	root      string
	repo      Repository
	processor Processor
	ttl       time.Duration
	now       func() time.Time
	gate      chan struct{}
}

func New(root string, repo Repository, processor Processor, ttl time.Duration) (*Service, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	return &Service{root: root, repo: repo, processor: processor, ttl: ttl,
		now: time.Now, gate: make(chan struct{}, 1)}, nil
}

func (s *Service) acquire() error {
	select {
	case s.gate <- struct{}{}:
		return nil
	default:
		return ErrBusy
	}
}

func (s *Service) release() { <-s.gate }

func (s *Service) Create(ctx context.Context, owner, name string, input io.Reader) (Document, error) {
	if err := s.acquire(); err != nil {
		return Document{}, err
	}
	defer s.release()
	if err := s.cleanup(ctx); err != nil {
		return Document{}, err
	}
	all, err := s.repo.List(ctx)
	if err != nil {
		return Document{}, err
	}
	used, err := s.diskUsage()
	if err != nil {
		return Document{}, err
	}
	if len(all) >= MaxDocuments || used+MaxUploadBytes > MaxStorageBytes {
		return Document{}, ErrStorageFull
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return Document{}, err
	}
	id := hex.EncodeToString(idBytes)
	dir := filepath.Join(s.root, id)
	if err := os.Mkdir(dir, 0700); err != nil {
		return Document{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(dir)
		}
	}()
	original := filepath.Join(dir, "original")
	source := filepath.Join(dir, "source.pdf")
	file, err := os.OpenFile(original, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return Document{}, err
	}
	count, copyErr := io.Copy(file, io.LimitReader(input, MaxUploadBytes+1))
	closeErr := file.Close()
	if count > MaxUploadBytes {
		return Document{}, ErrTooLarge
	}
	if copyErr != nil {
		return Document{}, copyErr
	}
	if closeErr != nil {
		return Document{}, closeErr
	}
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}
	if err := s.processor.Prepare(ctx, original, name, source); err != nil {
		return Document{}, err
	}
	prepared, err := os.Stat(source)
	if err != nil {
		return Document{}, err
	}
	if prepared.Size() > 128<<20 {
		return Document{}, ErrTooLarge
	}
	used, err = s.diskUsage()
	if err != nil {
		return Document{}, err
	}
	if used > MaxStorageBytes {
		return Document{}, ErrStorageFull
	}
	pages, err := s.processor.Inspect(ctx, source)
	if err != nil {
		return Document{}, err
	}
	if pages < 1 {
		return Document{}, ErrInvalidPDF
	}
	if pages > MaxPages {
		return Document{}, ErrTooManyPages
	}
	now := s.now().UTC()
	doc := Document{ID: id, Name: displayName(name), Bytes: count, PageCount: pages,
		CreatedAt: now, ExpiresAt: now.Add(s.ttl), Owner: owner}
	if err := s.repo.Insert(ctx, doc); err != nil {
		return Document{}, err
	}
	committed = true
	return doc, nil
}

// CopySource pins a prepared PDF while the caller creates an immutable print plan.
func (s *Service) CopySource(ctx context.Context, owner, id string, output io.Writer) (Document, error) {
	if err := s.acquire(); err != nil {
		return Document{}, err
	}
	defer s.release()
	doc, err := s.Get(ctx, owner, id)
	if err != nil {
		return Document{}, err
	}
	file, err := os.Open(filepath.Join(s.root, id, "source.pdf"))
	if err != nil {
		return Document{}, err
	}
	defer file.Close()
	_, err = io.Copy(output, file)
	return doc, err
}

func (s *Service) Get(ctx context.Context, owner, id string) (Document, error) {
	if !documentID.MatchString(id) {
		return Document{}, ErrNotFound
	}
	doc, err := s.repo.Get(ctx, id)
	if err != nil {
		return Document{}, err
	}
	if doc.Owner != owner || !s.now().Before(doc.ExpiresAt) {
		return Document{}, ErrNotFound
	}
	return doc, nil
}

// Discard removes a finished print's source and previews under the document gate.
func (s *Service) Discard(ctx context.Context, owner, id string) error {
	if !documentID.MatchString(id) {
		return ErrNotFound
	}
	if err := s.acquire(); err != nil {
		return err
	}
	defer s.release()
	doc, err := s.repo.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if doc.Owner != owner {
		return ErrNotFound
	}
	if err := os.RemoveAll(filepath.Join(s.root, id)); err != nil {
		return err
	}
	return s.repo.Delete(ctx, id)
}

func (s *Service) Preview(ctx context.Context, owner, id string, page int) ([]byte, error) {
	if err := s.acquire(); err != nil {
		return nil, err
	}
	defer s.release()
	doc, err := s.Get(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	if page < 1 || page > doc.PageCount {
		return nil, ErrPage
	}
	dir := filepath.Join(s.root, id)
	output := filepath.Join(dir, fmt.Sprintf("page-%d.png", page))
	if data, err := readPreview(output); err == nil {
		return data, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	temp, err := os.CreateTemp(dir, "render-*.png")
	if err != nil {
		return nil, err
	}
	tempPath := temp.Name()
	_ = temp.Close()
	defer os.Remove(tempPath)
	if err := s.processor.Render(ctx, filepath.Join(dir, "source.pdf"), page, tempPath); err != nil {
		return nil, err
	}
	data, err := readPreview(tempPath)
	if err != nil {
		return nil, ErrPreview
	}
	used, err := s.diskUsage()
	if err != nil {
		return nil, err
	}
	if used > MaxStorageBytes {
		return nil, ErrStorageFull
	}
	if _, err := s.Get(ctx, owner, id); err != nil {
		return nil, err
	}
	if err := os.Rename(tempPath, output); err != nil {
		return nil, err
	}
	return data, nil
}

func readPreview(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxPreviewBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > MaxPreviewBytes {
		return nil, ErrPreview
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 1600 || config.Height > 1600 {
		return nil, ErrPreview
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return nil, ErrPreview
	}
	return data, nil
}

// Cleanup also reconciles directories left by interrupted uploads or migrations.
// It holds the same gate as Create/Preview, so active work cannot be removed.
func (s *Service) Cleanup(ctx context.Context) error {
	select {
	case s.gate <- struct{}{}:
		defer s.release()
		return s.cleanup(ctx)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) cleanup(ctx context.Context) error {
	all, err := s.repo.List(ctx)
	if err != nil {
		return err
	}
	live := make(map[string]bool, len(all))
	for _, doc := range all {
		if !documentID.MatchString(doc.ID) {
			return errors.New("invalid document identifier in storage")
		}
		if !s.now().Before(doc.ExpiresAt) {
			if err := os.RemoveAll(filepath.Join(s.root, doc.ID)); err != nil {
				return err
			}
			if err := s.repo.Delete(ctx, doc.ID); err != nil {
				return err
			}
		} else {
			live[doc.ID] = true
		}
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if documentID.MatchString(entry.Name()) && !live[entry.Name()] {
			if err := os.RemoveAll(filepath.Join(s.root, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) diskUsage() (int64, error) {
	var size int64
	err := filepath.WalkDir(s.root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("symbolic links are not allowed in document storage")
		}
		if !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			size += info.Size()
		}
		return nil
	})
	return size, err
}

func displayName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name))
	runes := []rune(name)
	if len(runes) > 180 {
		name = string(runes[:180])
	}
	if name == "" || name == "." || name == "/" {
		return "document.pdf"
	}
	return name
}
