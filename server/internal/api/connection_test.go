package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RayFlows/printbridge/server/internal/api"
	"github.com/RayFlows/printbridge/server/internal/pairing"
	"github.com/RayFlows/printbridge/server/internal/printers"
)

const code = "test-only-pairing-code-0123456789012345"

type printerReaderFunc func(context.Context) ([]printers.Printer, error)

func (f printerReaderFunc) List(ctx context.Context) ([]printers.Printer, error) { return f(ctx) }

func TestPairReadRevokeFlow(t *testing.T) {
	t.Parallel()
	calls := 0
	reader := printerReaderFunc(func(context.Context) ([]printers.Printer, error) {
		calls++
		return []printers.Printer{}, nil
	})
	handler := api.NewHandler("test", pairing.New(code, time.Hour), reader, nil)
	for _, token := range []string{"", code, "invalid"} {
		response := call(handler, "GET", "/api/v1/printers", "", token)
		assertError(t, response, 401, "session_expired")
		if calls != 0 {
			t.Fatal("unauthorized access contacted CUPS")
		}
	}
	response := call(handler, "POST", "/api/v1/sessions", `{"pairingCode":"`+code+`"}`, "")
	if response.Code != 201 {
		t.Fatalf("pairing: %s", response.Body.String())
	}
	assertJSONHeaders(t, response)
	var session pairing.Session
	if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	response = call(handler, "GET", "/api/v1/printers", "", session.Token)
	if response.Code != 200 || strings.TrimSpace(response.Body.String()) != `{"printers":[]}` || calls != 1 {
		t.Fatalf("printer list: %s", response.Body.String())
	}
	response = call(handler, "HEAD", "/api/v1/printers", "", session.Token)
	if response.Code != 200 || response.Body.Len() != 0 {
		t.Fatal("HEAD printer list returned a body")
	}
	response = call(handler, "DELETE", "/api/v1/session", "", session.Token)
	if response.Code != 204 || response.Body.Len() != 0 {
		t.Fatal("session was not revoked")
	}
	assertError(t, call(handler, "GET", "/api/v1/printers", "", session.Token), 401, "session_expired")
}

func TestSharedPrinterActivityAndQueueFaultAreSeparateResponseFields(t *testing.T) {
	t.Parallel()
	sessions := pairing.New(code, time.Hour)
	session, err := sessions.Pair(code)
	if err != nil {
		t.Fatal(err)
	}
	reader := printerReaderFunc(func(context.Context) ([]printers.Printer, error) {
		return []printers.Printer{{ID: "virtual", State: "processing", StateReasons: []string{"media-empty-error"},
			Activity: &printers.Activity{State: "canceling", WaitingJobs: 2}}}, nil
	})
	response := call(api.NewHandler("test", sessions, reader, nil), "GET", "/api/v1/printers", "", session.Token)
	var body struct {
		Printers []printers.Printer `json:"printers"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != 200 || len(body.Printers) != 1 {
		t.Fatal(response.Body.String(), err)
	}
	printer := body.Printers[0]
	if printer.Activity == nil || printer.Activity.State != "canceling" || printer.Activity.WaitingJobs != 2 ||
		printer.State != "processing" || len(printer.StateReasons) != 1 || printer.StateReasons[0] != "media-empty-error" {
		t.Fatal("reservation or shared queue reason was lost in the response", printer)
	}
}

func TestPairingInputAndMethods(t *testing.T) {
	t.Parallel()
	handler := api.NewHandler("test", pairing.New(code, time.Hour), nil, nil)
	tests := []struct {
		name, body, contentType, errorCode string
		status                             int
	}{
		{"malformed", "{", "application/json", "invalid_request", 400},
		{"empty", "{}", "application/json", "invalid_request", 400},
		{"null", "null", "application/json", "invalid_request", 400},
		{"wrong type", `{"pairingCode":10}`, "application/json", "invalid_request", 400},
		{"extra field", `{"pairingCode":"x","admin":true}`, "application/json", "invalid_request", 400},
		{"extra JSON", `{"pairingCode":"x"} {}`, "application/json", "invalid_request", 400},
		{"too large", `{"pairingCode":"` + strings.Repeat("x", 5000) + `"}`, "application/json", "invalid_request", 400},
		{"wrong code", `{"pairingCode":"incorrect"}`, "application/json", "invalid_pairing_code", 401},
		{"form", "pairingCode=x", "application/x-www-form-urlencoded", "invalid_content_type", 415},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/v1/sessions", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", tt.contentType)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			assertError(t, response, tt.status, tt.errorCode)
		})
	}
	for _, entry := range []struct{ method, path, allow string }{
		{"GET", "/api/v1/sessions", "POST"},
		{"POST", "/api/v1/session", "DELETE"},
		{"POST", "/api/v1/printers", "GET, HEAD"},
	} {
		response := call(handler, entry.method, entry.path, "", "")
		assertError(t, response, 405, "method_not_allowed")
		if response.Header().Get("Allow") != entry.allow {
			t.Fatal("incorrect Allow header")
		}
	}
}

func TestDisabledRateLimitedExpiredAndCUPSFailure(t *testing.T) {
	t.Parallel()
	disabled := api.NewHandler("test", pairing.New("", time.Hour), nil, nil)
	assertError(t, call(disabled, "POST", "/api/v1/sessions", `{"pairingCode":"x"}`, ""), 503, "pairing_disabled")
	service := pairing.New(code, time.Hour)
	handler := api.NewHandler("test", service, printerReaderFunc(func(context.Context) ([]printers.Printer, error) {
		return nil, errors.New("internal path or private backend detail")
	}), nil)
	session, _ := service.Pair(code)
	response := call(handler, "GET", "/api/v1/printers", "", session.Token)
	assertError(t, response, 503, "cups_unavailable")
	if strings.Contains(response.Body.String(), "private") {
		t.Fatal("internal backend details leaked")
	}
	for i := 0; i < 9; i++ {
		call(handler, "POST", "/api/v1/sessions", `{"pairingCode":"wrong"}`, "")
	}
	response = call(handler, "POST", "/api/v1/sessions", `{"pairingCode":"wrong"}`, "")
	assertError(t, response, 429, "pairing_rate_limited")
	if response.Header().Get("Retry-After") != "60" {
		t.Fatal("missing retry guidance")
	}
	expired := pairing.New(code, -time.Second)
	oldSession, _ := expired.Pair(code)
	handler = api.NewHandler("test", expired, nil, nil)
	assertError(t, call(handler, "GET", "/api/v1/printers", "", oldSession.Token), 401, "session_expired")
}

func call(handler http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func assertError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	assertJSONHeaders(t, response)
	var body struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != status || body.Error.Code != code || body.Error.Message == "" {
		t.Fatalf("error = %d %s, want %d %s", response.Code, response.Body.String(), status, code)
	}
}
