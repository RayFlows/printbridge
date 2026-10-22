package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/RayFlows/printbridge/server/internal/api"
	"github.com/RayFlows/printbridge/server/internal/pairing"
)

func TestMetadataResponses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path string
		body map[string]string
	}{
		{"/healthz", map[string]string{"status": "ok"}},
		{"/api/v1/server", map[string]string{
			"name": "PrintBridge", "version": "v0.1.0-test", "apiVersion": "v1",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()
			recorder := httptest.NewRecorder()
			metadataHandler("v0.1.0-test").ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
			assertJSONHeaders(t, recorder)
			var body map[string]string
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(body, tt.body) {
				t.Fatalf("body = %#v, want %#v", body, tt.body)
			}
		})
	}
}

func TestUnsupportedMethodsAndPaths(t *testing.T) {
	t.Parallel()
	tests := []struct {
		method string
		path   string
		status int
		code   string
		allow  string
	}{
		{http.MethodPost, "/healthz", 405, "method_not_allowed", "GET, HEAD"},
		{http.MethodPut, "/api/v1/server", 405, "method_not_allowed", "GET, HEAD"},
		{http.MethodDelete, "/api/v1/server", 405, "method_not_allowed", "GET, HEAD"},
		{http.MethodGet, "/api/v1/missing", 404, "not_found", ""},
		{http.MethodPost, "/api/v1/print", 404, "not_found", ""},
		{http.MethodGet, "/healthz/", 404, "not_found", ""},
		{http.MethodGet, "/", 404, "not_found", ""},
	}
	handler := metadataHandler("dev")
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			t.Parallel()
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(tt.method, tt.path, nil))
			if recorder.Code != tt.status {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.status)
			}
			assertJSONHeaders(t, recorder)
			if got := recorder.Header().Get("Allow"); got != tt.allow {
				t.Fatalf("Allow = %q, want %q", got, tt.allow)
			}
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != tt.code || body.Error.Message == "" {
				t.Fatalf("unexpected error response: %s", recorder.Body.String())
			}
		})
	}
}

func TestHeadHasNoBody(t *testing.T) {
	t.Parallel()
	handler := metadataHandler("dev")
	for _, tt := range []struct {
		path   string
		status int
	}{
		{"/healthz", http.StatusOK},
		{"/api/v1/server", http.StatusOK},
		{"/missing", http.StatusNotFound},
	} {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodHead, tt.path, nil))
			if recorder.Code != tt.status || recorder.Body.Len() != 0 {
				t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body.String())
			}
			assertJSONHeaders(t, recorder)
		})
	}
}

func metadataHandler(version string) http.Handler {
	return api.NewHandler(version, pairing.New("", time.Hour), nil, nil)
}

func assertJSONHeaders(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
}
