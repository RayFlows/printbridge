package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RayFlows/printbridge/server/internal/api"
	"github.com/RayFlows/printbridge/server/internal/documents"
	"github.com/RayFlows/printbridge/server/internal/pairing"
	"github.com/RayFlows/printbridge/server/internal/printers"
	"github.com/RayFlows/printbridge/server/internal/store"
)

type documentProcessor struct {
	inspectErr error
	renderErr  error
	onRender   func()
	renders    int
}

func (p *documentProcessor) Prepare(_ context.Context, source, _ string, output string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(output, data, 0600)
}

func (p *documentProcessor) Inspect(context.Context, string) (int, error) { return 16, p.inspectErr }
func (p *documentProcessor) Render(_ context.Context, _ string, _ int, output string) error {
	p.renders++
	if p.onRender != nil {
		p.onRender()
	}
	if p.renderErr != nil {
		return p.renderErr
	}
	f, err := os.Create(output)
	if err != nil {
		return err
	}
	err = png.Encode(f, image.NewRGBA(image.Rect(0, 0, 8, 12)))
	return errors.Join(err, f.Close())
}

func documentHandler(t *testing.T, processor *documentProcessor) (http.Handler, *pairing.Service, string) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	docs, err := documents.New(filepath.Join(root, "documents"), db, processor, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	sessions := pairing.New(code, time.Hour)
	session, err := sessions.Pair(code)
	if err != nil {
		t.Fatal(err)
	}
	reader := printerReaderFunc(func(context.Context) ([]printers.Printer, error) {
		t.Error("document endpoints must never contact CUPS")
		return nil, nil
	})
	return api.NewHandler("test", sessions, reader, docs), sessions, session.Token
}

func upload(t *testing.T, handler http.Handler, token string, content []byte, files int) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for i := 0; i < files; i++ {
		part, err := form.CreateFormFile("file", "sample.pdf")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.WriteField("name", "sample.pdf"); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/v1/documents", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func TestDocumentUploadPreviewAndAuthorization(t *testing.T) {
	t.Parallel()
	processor := &documentProcessor{}
	handler, sessions, token := documentHandler(t, processor)
	assertError(t, upload(t, handler, "", []byte("%PDF-test"), 1), 401, "session_expired")
	response := upload(t, handler, token, []byte("%PDF-test"), 1)
	if response.Code != 201 {
		t.Fatal(response.Body.String())
	}
	var doc documents.Document
	if err := json.Unmarshal(response.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.PageCount != 16 || doc.Owner != "" || strings.Contains(response.Body.String(), token) {
		t.Fatal("incorrect public document")
	}
	path := "/api/v1/documents/" + doc.ID
	response = call(handler, "GET", path, "", token)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	for _, page := range []int{1, 16, 1} {
		response = call(handler, "GET", path+"/pages/"+strconv.Itoa(page), "", token)
		if response.Code != 200 || response.Header().Get("Content-Type") != "image/png" ||
			response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(response.Code, response.Body.String())
		}
		if _, err := png.Decode(bytes.NewReader(response.Body.Bytes())); err != nil {
			t.Fatal(err)
		}
	}
	if processor.renders != 2 {
		t.Fatal("server did not reuse preview")
	}
	response = call(handler, "HEAD", path+"/pages/1", "", token)
	if response.Code != 200 || response.Body.Len() != 0 {
		t.Fatal("HEAD returned a body")
	}
	for _, page := range []string{"0", "17", "abc"} {
		assertError(t, call(handler, "GET", path+"/pages/"+page, "", token), 400, "invalid_page")
	}
	other, _ := sessions.Pair(code)
	assertError(t, call(handler, "GET", path, "", other.Token), 404, "document_not_found")
	assertError(t, call(handler, "GET", path+"/pages/1", "", other.Token), 404, "document_not_found")
	assertError(t, call(handler, "POST", path+"/pages/1", "", token), 405, "method_not_allowed")
	sessions.Revoke(token)
	assertError(t, call(handler, "GET", path, "", token), 401, "session_expired")
	assertError(t, call(handler, "GET", path+"/pages/1", "", token), 401, "session_expired")
	assertError(t, call(handler, "POST", "/api/v1/print", "", other.Token), 404, "not_found")
}

func TestDocumentInputLimitsAndFailures(t *testing.T) {
	t.Parallel()
	handler, _, token := documentHandler(t, &documentProcessor{})
	for _, count := range []int{0, 2} {
		assertError(t, upload(t, handler, token, []byte("%PDF-test"), count), 400, "invalid_request")
	}
	assertError(t, upload(t, handler, token, make([]byte, documents.MaxUploadBytes+1), 1), 413, "document_too_large")
	assertError(t, upload(t, handler, token, make([]byte, documents.MaxUploadBytes+(128<<10)), 1), 413, "document_too_large")
	assertError(t, call(handler, "POST", "/api/v1/documents", "{}", token), 400, "invalid_request")
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{documents.ErrInvalidPDF, 422, "invalid_pdf"}, {documents.ErrTimeout, 504, "document_timeout"},
		{documents.ErrTooManyPages, 422, "too_many_pages"}, {documents.ErrUnavailable, 503, "pdf_tools_unavailable"},
		{documents.ErrStorageFull, 507, "document_storage_full"}, {documents.ErrBusy, 503, "document_busy"},
		{errors.New("private filesystem path and document details"), 500, "document_error"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			h, _, tok := documentHandler(t, &documentProcessor{inspectErr: tc.err})
			response := upload(t, h, tok, []byte("%PDF-test"), 1)
			assertError(t, response, tc.status, tc.code)
			if strings.Contains(response.Body.String(), "private filesystem") {
				t.Fatal("diagnostic leak")
			}
		})
	}
}

func TestRevocationDuringRenderingPreventsDelivery(t *testing.T) {
	t.Parallel()
	p := &documentProcessor{}
	h, sessions, token := documentHandler(t, p)
	response := upload(t, h, token, []byte("%PDF-test"), 1)
	var doc documents.Document
	if err := json.Unmarshal(response.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	p.onRender = func() { sessions.Revoke(token) }
	assertError(t, call(h, "GET", "/api/v1/documents/"+doc.ID+"/pages/1", "", token), 401, "session_expired")
}
