package documents

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"
)

func IsImageName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".webp", ".bmp", ".tif", ".tiff", ".gif":
		return true
	}
	return false
}

const (
	MaxUploadBytes  int64 = 20 << 20
	MaxPreviewBytes int64 = 12 << 20
	MaxPages              = 500
	MaxDocuments          = 64
	MaxStorageBytes int64 = 512 << 20
)

var (
	ErrNotFound     = errors.New("document not found")
	ErrInvalidPDF   = errors.New("invalid or encrypted PDF")
	ErrTooLarge     = errors.New("document exceeds size limit")
	ErrTooManyPages = errors.New("document exceeds page limit")
	ErrPage         = errors.New("page is outside the document")
	ErrBusy         = errors.New("document processor is busy")
	ErrTimeout      = errors.New("document processing timed out")
	ErrUnavailable  = errors.New("PDF tools are unavailable")
	ErrPreview      = errors.New("page could not be rendered")
	ErrStorageFull  = errors.New("document storage is full")
	ErrUnsupported  = errors.New("unsupported document format")
	ErrConversion   = errors.New("document conversion failed")
	ErrConverter    = errors.New("document converter is unavailable")
)

type Document struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Bytes     int64     `json:"bytes"`
	PageCount int       `json:"pageCount"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	Owner     string    `json:"-"`
}

type Repository interface {
	Insert(context.Context, Document) error
	Get(context.Context, string) (Document, error)
	List(context.Context) ([]Document, error)
	Delete(context.Context, string) error
}

// Processor implementations inspect and rasterize a PDF without submitting jobs.
type Processor interface {
	Prepare(context.Context, string, string, string) error
	Inspect(context.Context, string) (int, error)
	Render(context.Context, string, int, string) error
}
