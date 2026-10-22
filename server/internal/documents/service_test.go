package documents

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type memoryRepo struct {
	mu   sync.Mutex
	docs map[string]Document
}

func (r *memoryRepo) Insert(_ context.Context, d Document) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.docs[d.ID] = d
	return nil
}
func (r *memoryRepo) Get(_ context.Context, id string) (Document, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d, ok := r.docs[id]; ok {
		return d, nil
	}
	return Document{}, ErrNotFound
}
func (r *memoryRepo) List(context.Context) ([]Document, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []Document
	for _, d := range r.docs {
		result = append(result, d)
	}
	return result, nil
}
func (r *memoryRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.docs, id)
	return nil
}

type fakeProcessor struct {
	pages        int
	inspectErr   error
	renderErr    error
	invalidImage bool
	renders      int
	started      chan struct{}
	resume       chan struct{}
}

func (p *fakeProcessor) Prepare(_ context.Context, source, _ string, output string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(output, data, 0600)
}

func (p *fakeProcessor) Inspect(context.Context, string) (int, error) {
	if p.started != nil {
		close(p.started)
		<-p.resume
	}
	return p.pages, p.inspectErr
}
func (p *fakeProcessor) Render(_ context.Context, _ string, page int, output string) error {
	p.renders++
	if p.renderErr != nil {
		return p.renderErr
	}
	if p.invalidImage {
		return os.WriteFile(output, []byte("partial PNG"), 0600)
	}
	img := image.NewRGBA(image.Rect(0, 0, 3, 4))
	img.Set(1, 1, color.RGBA{R: uint8(page), A: 255})
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		return err
	}
	return os.WriteFile(output, data.Bytes(), 0600)
}

func fixture(t *testing.T, p *fakeProcessor) (*Service, *memoryRepo) {
	t.Helper()
	r := &memoryRepo{docs: map[string]Document{}}
	s, err := New(t.TempDir(), r, p, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return s, r
}

func TestDiscardFinishedDocumentRemovesAllFilesAndChecksOwnership(t *testing.T) {
	t.Parallel()
	s, repo := fixture(t, &fakeProcessor{pages: 1})
	ctx := context.Background()
	doc, err := s.Create(ctx, "owner", "sample.pdf", strings.NewReader("synthetic"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Preview(ctx, "owner", doc.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Discard(ctx, "other", doc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("another owner discarded the document", err)
	}
	if _, err := s.Get(ctx, "owner", doc.ID); err != nil {
		t.Fatal("ownership failure removed the document", err)
	}
	s.gate <- struct{}{}
	if err := s.Discard(ctx, "owner", doc.ID); !errors.Is(err, ErrBusy) {
		t.Fatal("discard raced with document processing", err)
	}
	<-s.gate
	for i := 0; i < 2; i++ {
		if err := s.Discard(ctx, "owner", doc.ID); err != nil {
			t.Fatal("discard must be retryable", err)
		}
	}
	if _, err := os.Stat(filepath.Join(s.root, doc.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("original, source or preview retained", err)
	}
	if _, err := repo.Get(ctx, doc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("document metadata retained", err)
	}
	if err := s.Discard(ctx, "owner", "../outside"); !errors.Is(err, ErrNotFound) {
		t.Fatal("unsafe document ID accepted", err)
	}
}

func TestDocumentOwnershipPreviewCacheAndExpiry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p := &fakeProcessor{pages: 16}
	s, repo := fixture(t, p)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	doc, err := s.Create(ctx, "owner-a", `C:\outside\sample.pdf`, strings.NewReader("%PDF-test"))
	if err != nil {
		t.Fatal(err)
	}
	if doc.PageCount != 16 || doc.Name != "sample.pdf" || doc.Bytes != 9 {
		t.Fatalf("document: %+v", doc)
	}
	if _, err := s.Get(ctx, "owner-b", doc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("other session read document")
	}
	for _, id := range []string{"../../outside", strings.Repeat("a", 32)} {
		if _, err := s.Get(ctx, "owner-a", id); !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
	}
	first, err := s.Preview(ctx, "owner-a", doc.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Preview(ctx, "owner-a", doc.ID, 1)
	if err != nil || !bytes.Equal(first, again) || p.renders != 1 {
		t.Fatal("preview was not reused", err)
	}
	second, err := s.Preview(ctx, "owner-a", doc.ID, 2)
	if err != nil || bytes.Equal(first, second) || p.renders != 2 {
		t.Fatal("wrong page preview", err)
	}
	for _, page := range []int{0, 17} {
		if _, err := s.Preview(ctx, "owner-a", doc.ID, page); !errors.Is(err, ErrPage) {
			t.Fatal(err)
		}
	}
	now = now.Add(time.Hour)
	if _, err := s.Preview(ctx, "owner-a", doc.ID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired cached preview returned")
	}
	if err := s.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.root, doc.ID)); !os.IsNotExist(err) {
		t.Fatal("expired files remain")
	}
	all, _ := repo.List(ctx)
	if len(all) != 0 {
		t.Fatal("expired metadata remains")
	}
}

func TestFailedPreparationAndUploadLeaveNoReadyDocument(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		pages     int
		err, want error
	}{
		{"invalid", 0, ErrInvalidPDF, ErrInvalidPDF}, {"zero", 0, nil, ErrInvalidPDF},
		{"page limit", 501, nil, ErrTooManyPages}, {"timeout", 0, ErrTimeout, ErrTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, repo := fixture(t, &fakeProcessor{pages: tc.pages, inspectErr: tc.err})
			_, err := s.Create(context.Background(), "owner", "test.pdf", strings.NewReader("%PDF-test"))
			if !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
			all, _ := repo.List(context.Background())
			entries, _ := os.ReadDir(s.root)
			if len(all) != 0 || len(entries) != 0 {
				t.Fatal("failed preparation retained output")
			}
		})
	}
	s, _ := fixture(t, &fakeProcessor{pages: 1})
	_, err := s.Create(context.Background(), "owner", "test.pdf", io.LimitReader(zeroReader{}, MaxUploadBytes+1))
	if !errors.Is(err, ErrTooLarge) {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(s.root)
	if len(entries) != 0 {
		t.Fatal("oversize upload retained output")
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestFailedPreviewCanRetryWithoutUploading(t *testing.T) {
	t.Parallel()
	p := &fakeProcessor{pages: 1, invalidImage: true}
	s, repo := fixture(t, p)
	doc, err := s.Create(context.Background(), "owner", "test.pdf", strings.NewReader("%PDF-test"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Preview(context.Background(), "owner", doc.ID, 1)
	if !errors.Is(err, ErrPreview) {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(s.root, doc.ID))
	if len(entries) != 2 || entries[0].Name() != "original" || entries[1].Name() != "source.pdf" {
		t.Fatal("partial preview retained")
	}
	p.invalidImage = false
	if _, err := s.Preview(context.Background(), "owner", doc.ID, 1); err != nil {
		t.Fatal(err)
	}
	all, _ := repo.List(context.Background())
	if len(all) != 1 {
		t.Fatal("preview retry created a document")
	}
}

func TestCleanupPreservesLiveDocumentsAndReconcilesOrphans(t *testing.T) {
	t.Parallel()
	s, _ := fixture(t, &fakeProcessor{pages: 1})
	doc, err := s.Create(context.Background(), "owner", "test.pdf", strings.NewReader("%PDF-test"))
	if err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(s.root, strings.Repeat("a", 32))
	if err := os.Mkdir(orphan, 0700); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(s.root, "operator-notes")
	if err := os.Mkdir(unrelated, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("orphan not removed")
	}
	for _, path := range []string{filepath.Join(s.root, doc.ID, "source.pdf"), unrelated} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConcurrentWorkIsBoundedAndCleanupDoesNotRemoveActiveUpload(t *testing.T) {
	t.Parallel()
	p := &fakeProcessor{pages: 1, started: make(chan struct{}), resume: make(chan struct{})}
	s, _ := fixture(t, p)
	done := make(chan error, 1)
	go func() {
		_, err := s.Create(context.Background(), "owner", "test.pdf", strings.NewReader("%PDF-test"))
		done <- err
	}()
	<-p.started
	_, err := s.Create(context.Background(), "other", "test.pdf", strings.NewReader("%PDF-test"))
	if !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Cleanup(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(p.resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
