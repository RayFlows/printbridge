package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RayFlows/printbridge/server/internal/config"
	"github.com/RayFlows/printbridge/server/internal/pairing"
)

type syntheticLogin struct{}

func (syntheticLogin) OpenID(context.Context, string) (string, error) { return "synthetic-person", nil }

func TestConfiguredIdentityPersistsThroughApplicationRestart(t *testing.T) {
	cfg, err := config.Load(func(key string) string {
		switch key {
		case "PRINTBRIDGE_WECHAT_APP_ID":
			return "test-app"
		case "PRINTBRIDGE_WECHAT_APP_SECRET":
			return "synthetic-secret"
		case "PRINTBRIDGE_PAIRING_CODE":
			return "test-only-pairing-code-0123456789012345"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg.DataDir = t.TempDir()
	// No backend calls are expected: login, profile and versions never need CUPS.
	virtual := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("identity flow contacted a printing backend")
		w.WriteHeader(500)
	}))
	defer virtual.Close()
	cfg.CUPSURL = virtual.URL
	h, err := newHandler(cfg, "test", syntheticLogin{})
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := call("GET", "/api/v1/server", "", "")
	if !strings.Contains(w.Body.String(), `"loginMode":"wechat"`) {
		t.Fatal(w.Body.String())
	}
	w = call("POST", "/api/v1/sessions", `{"pairingCode":"test-only-pairing-code-0123456789012345","loginCode":"code-1"}`, "")
	var first pairing.Session
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil || w.Code != 201 || first.User == nil {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	w = call("POST", "/api/v1/me", `{"name":"机主"}`, first.Token)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	cfg.AdminUserID = first.User.ID
	h, err = newHandler(cfg, "test", syntheticLogin{})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	w = call("POST", "/api/v1/sessions", `{"pairingCode":"test-only-pairing-code-0123456789012345","loginCode":"code-2"}`, "")
	var second pairing.Session
	if err := json.Unmarshal(w.Body.Bytes(), &second); err != nil || w.Code != 201 || second.User == nil {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	if second.User.ID != first.User.ID || second.User.Name != "机主" || !second.User.NamePrompted || !second.User.IsAdmin {
		t.Fatal("identity or role lost on restart")
	}
	w = call("GET", "/api/v1/me", "", first.Token)
	if w.Code != 200 {
		t.Fatal("old valid session lost", w.Body.String())
	}
	w = call("GET", "/api/v1/admin/users", "", second.Token)
	if w.Code != 200 || strings.Contains(w.Body.String(), "synthetic-person") {
		t.Fatal(w.Body.String())
	}
}
