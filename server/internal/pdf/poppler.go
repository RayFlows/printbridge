package pdf

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RayFlows/printbridge/server/internal/documents"
	"golang.org/x/image/font/opentype"
)

var (
	pagesPattern     = regexp.MustCompile(`(?m)^Pages:\s+([0-9]+)\s*$`)
	encryptedPattern = regexp.MustCompile(`(?m)^Encrypted:\s+yes\b`)
)

type Poppler struct {
	info, render  string
	timeout       time.Duration
	office        Office
	fontPath      string
	fontOnce      sync.Once
	watermarkFont *opentype.Font
	fontError     error
}

func (p *Poppler) WithWatermarkFont(path string) *Poppler { p.fontPath = path; return p }

func New(info, render string, timeout time.Duration) *Poppler {
	return &Poppler{info: info, render: render, timeout: timeout, office: Office{Command: "soffice", Timeout: 2 * time.Minute}}
}

func (p *Poppler) Inspect(ctx context.Context, source string) (int, error) {
	file, err := os.Open(source)
	if err != nil {
		return 0, err
	}
	var magic [5]byte
	_, readErr := io.ReadFull(file, magic[:])
	_ = file.Close()
	if readErr != nil || string(magic[:]) != "%PDF-" {
		return 0, documents.ErrInvalidPDF
	}
	output, err := p.run(ctx, p.info, filepath.Dir(source), "-enc", "UTF-8", source)
	if err != nil {
		return 0, toolError(err, documents.ErrInvalidPDF)
	}
	match := pagesPattern.FindSubmatch(output)
	if match == nil || encryptedPattern.Match(output) {
		return 0, documents.ErrInvalidPDF
	}
	pages, err := strconv.Atoi(string(match[1]))
	if err != nil || pages <= 0 {
		return 0, documents.ErrInvalidPDF
	}
	if pages > documents.MaxPages {
		return 0, documents.ErrTooManyPages
	}
	return pages, nil
}

func (p *Poppler) Render(ctx context.Context, source string, page int, output string) error {
	return p.renderPage(ctx, source, page, output, false)
}

func (p *Poppler) RenderPreview(ctx context.Context, source string, page int, output, colorMode string) error {
	return p.renderPage(ctx, source, page, output, colorMode == "monochrome" || colorMode == "auto-monochrome")
}

func (p *Poppler) renderPage(ctx context.Context, source string, page int, output string, gray bool) error {
	args := []string{"-f", strconv.Itoa(page), "-l", strconv.Itoa(page), "-singlefile", "-scale-to", "1600"}
	if gray {
		args = append(args, "-gray")
	}
	args = append(args, "-png", source, strings.TrimSuffix(output, ".png"))
	_, err := p.run(ctx, p.render, filepath.Dir(source), args...)
	return toolError(err, documents.ErrPreview)
}

func (p *Poppler) run(ctx context.Context, executable, directory string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	command := exec.CommandContext(ctx, executable, args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	command.WaitDelay = time.Second
	output := &limitedBuffer{remaining: 64 << 10}
	command.Stdout = output
	// Parser diagnostics can contain document text; do not persist or return them.
	command.Stderr = io.Discard
	err := command.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return output.Bytes(), err
}

func toolError(err, fallback error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return documents.ErrTimeout
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) || errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
		return documents.ErrUnavailable
	}
	return fallback
}

type limitedBuffer struct {
	bytes.Buffer
	remaining int
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	n := len(data)
	if n > b.remaining {
		data = data[:b.remaining]
	}
	_, _ = b.Buffer.Write(data)
	b.remaining -= len(data)
	return n, nil
}
