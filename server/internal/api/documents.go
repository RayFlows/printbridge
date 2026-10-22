package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/RayFlows/printbridge/server/internal/documents"
	"github.com/RayFlows/printbridge/server/internal/pairing"
)

type DocumentService interface {
	Create(context.Context, string, string, io.Reader) (documents.Document, error)
	Get(context.Context, string, string) (documents.Document, error)
	Preview(context.Context, string, string, int) ([]byte, error)
}

func registerDocuments(mux *http.ServeMux, sessions *pairing.Service, docs DocumentService) {
	uploads := make(chan struct{}, 1)
	mux.HandleFunc("/api/v1/documents", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		owner, ok := authorize(w, r, sessions)
		if !ok {
			return
		}
		select {
		case uploads <- struct{}{}:
			defer func() { <-uploads }()
		default:
			documentError(w, r, documents.ErrBusy)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, documents.MaxUploadBytes+(64<<10))
		err := r.ParseMultipartForm(1 << 20)
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				documentError(w, r, documents.ErrTooLarge)
			} else {
				writeError(w, r, 400, "invalid_request", "one multipart file is required")
			}
			return
		}
		form := r.MultipartForm
		if form == nil || len(form.File) != 1 || len(form.File["file"]) != 1 ||
			len(form.Value) > 1 || (len(form.Value) == 1 && len(form.Value["name"]) != 1) ||
			len(r.FormValue("name")) > 720 {
			writeError(w, r, 400, "invalid_request", "one file field and optional name are required")
			return
		}
		header := form.File["file"][0]
		if header.Size > documents.MaxUploadBytes {
			documentError(w, r, documents.ErrTooLarge)
			return
		}
		file, err := header.Open()
		if err != nil {
			documentError(w, r, err)
			return
		}
		defer file.Close()
		name := header.Filename
		if value := form.Value["name"]; len(value) == 1 {
			name = value[0]
		}
		doc, err := docs.Create(r.Context(), owner, name, file)
		if err != nil {
			documentError(w, r, err)
			return
		}
		if _, ok := authorize(w, r, sessions); !ok {
			return
		}
		writeJSON(w, r, http.StatusCreated, doc)
	})
	mux.HandleFunc("/api/v1/documents/{id}", readOnly(func(w http.ResponseWriter, r *http.Request) {
		owner, ok := authorize(w, r, sessions)
		if !ok {
			return
		}
		doc, err := docs.Get(r.Context(), owner, r.PathValue("id"))
		if err != nil {
			documentError(w, r, err)
			return
		}
		writeJSON(w, r, http.StatusOK, doc)
	}))
	mux.HandleFunc("/api/v1/documents/{id}/pages/{page}", readOnly(func(w http.ResponseWriter, r *http.Request) {
		owner, ok := authorize(w, r, sessions)
		if !ok {
			return
		}
		page, err := strconv.Atoi(r.PathValue("page"))
		if err != nil || page < 1 {
			documentError(w, r, documents.ErrPage)
			return
		}
		data, err := docs.Preview(r.Context(), owner, r.PathValue("id"), page)
		if err != nil {
			documentError(w, r, err)
			return
		}
		if _, ok := authorize(w, r, sessions); !ok {
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(data)
		}
	}))
}

func documentError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := 500, "document_error", "document operation failed"
	switch {
	case errors.Is(err, documents.ErrUnsupported):
		status, code, message = 415, "unsupported_format", "this file type cannot be printed"
	case errors.Is(err, documents.ErrConversion):
		status, code, message = 422, "conversion_failed", "document conversion failed"
	case errors.Is(err, documents.ErrConverter):
		status, code, message = 503, "office_unavailable", "Office converter is not installed or not executable"
	case errors.Is(err, documents.ErrNotFound):
		status, code, message = 404, "document_not_found", "document is missing, expired or belongs to another session"
	case errors.Is(err, documents.ErrInvalidPDF):
		status, code, message = 422, "invalid_pdf", "a valid unencrypted PDF is required"
	case errors.Is(err, documents.ErrTooLarge):
		status, code, message = 413, "document_too_large", "file exceeds 20 MiB or prepared PDF exceeds 128 MiB"
	case errors.Is(err, documents.ErrTooManyPages):
		status, code, message = 422, "too_many_pages", "document exceeds 500 pages"
	case errors.Is(err, documents.ErrPage):
		status, code, message = 400, "invalid_page", "page is outside the document"
	case errors.Is(err, documents.ErrBusy):
		status, code, message = 503, "document_busy", "document processor is busy; retry shortly"
		w.Header().Set("Retry-After", "2")
	case errors.Is(err, documents.ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		status, code, message = 504, "document_timeout", "document processing timed out"
	case errors.Is(err, documents.ErrUnavailable):
		status, code, message = 503, "pdf_tools_unavailable", "PDF tools are not installed or not executable"
	case errors.Is(err, documents.ErrPreview):
		status, code, message = 422, "preview_failed", "this page could not be rendered"
	case errors.Is(err, documents.ErrStorageFull):
		status, code, message = 507, "document_storage_full", "document storage is full"
	}
	writeError(w, r, status, code, message)
}
