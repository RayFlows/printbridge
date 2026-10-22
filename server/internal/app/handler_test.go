package app_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenPrinting/goipp"
	"github.com/RayFlows/printbridge/server/internal/app"
	"github.com/RayFlows/printbridge/server/internal/config"
)

func TestConfiguredServiceAgainstVirtualCUPS(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	virtualCUPS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request goipp.Message
		if err := request.Decode(r.Body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if request.Code != goipp.Code(goipp.OpCupsGetPrinters) {
			t.Error("unexpected IPP operation: M1 must never submit a job")
			w.WriteHeader(400)
			return
		}
		response := goipp.NewResponse(goipp.DefaultVersion, goipp.StatusOk, request.RequestID)
		response.Printer.Add(goipp.MakeAttribute("printer-name", goipp.TagName, goipp.String("VirtualPrinter")))
		response.Printer.Add(goipp.MakeAttribute("printer-state", goipp.TagEnum, goipp.Integer(3)))
		w.Header().Set("Content-Type", goipp.ContentType)
		if err := response.Encode(w); err != nil {
			t.Error(err)
		}
	}))
	defer virtualCUPS.Close()
	cfg, err := config.Load(func(key string) string {
		switch key {
		case "PRINTBRIDGE_PAIRING_CODE":
			return "test-only-pairing-code-0123456789012345"
		case "PRINTBRIDGE_CUPS_URL":
			return virtualCUPS.URL
		case "PRINTBRIDGE_MANUAL_DUPLEX_PROFILES":
			return `{"VirtualPrinter":"rear-face-up-v1"}`
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg.DataDir = t.TempDir()
	handler, err := app.NewHandler(cfg, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	server := httptest.NewServer(handler)
	defer server.Close()
	client := &http.Client{Timeout: time.Second}
	for _, path := range []string{"/healthz", "/api/v1/server", "/api/v1/printers"} {
		response, err := client.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
	}
	if calls.Load() != 0 {
		t.Fatal("metadata or unauthorized access contacted CUPS")
	}
	response, err := client.Post(server.URL+"/api/v1/sessions", "application/json",
		strings.NewReader(`{"pairingCode":"test-only-pairing-code-0123456789012345"}`))
	if err != nil {
		t.Fatal(err)
	}
	var session struct{ Token string }
	if err := json.NewDecoder(response.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusCreated || session.Token == "" {
		t.Fatal("configured pairing did not create a session")
	}
	req, _ := http.NewRequest("GET", server.URL+"/api/v1/printers", nil)
	req.Header.Set("Authorization", "Bearer "+session.Token)
	response, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || !strings.Contains(string(data), `"id":"VirtualPrinter"`) ||
		!strings.Contains(string(data), `"sides":[]`) || !strings.Contains(string(data), `"copies":null`) ||
		!strings.Contains(string(data), `"manualDuplexProfile":"rear-face-up-v1"`) || calls.Load() != 1 {
		t.Fatalf("unexpected end-to-end response: %s", data)
	}
}
