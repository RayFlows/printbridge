package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RayFlows/printbridge/server/internal/accounts"
	"github.com/RayFlows/printbridge/server/internal/api"
	"github.com/RayFlows/printbridge/server/internal/management"
	"github.com/RayFlows/printbridge/server/internal/pairing"
	"github.com/RayFlows/printbridge/server/internal/printers"
	"github.com/RayFlows/printbridge/server/internal/printing"
	"github.com/RayFlows/printbridge/server/internal/store"
)

func accountFixture(t *testing.T, printerErrors ...error) (http.Handler, *store.SQLite, *pairing.Service, accounts.User) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	verify := identityVerifier(func(_ context.Context, code string) (string, error) {
		if code == "invalid" {
			return "", accounts.ErrLogin
		}
		if code == "offline" {
			return "", accounts.ErrUnavailable
		}
		return "verified-" + code, nil
	})
	seed := accounts.New(db, verify, "test-app", "")
	owner, err := seed.Login(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	users := accounts.New(db, verify, "test-app", owner.ID)
	sessions, err := pairing.NewPersistent(code, time.Hour, db)
	if err != nil {
		t.Fatal(err)
	}
	sessions.WithUsers(users)
	prints, err := printing.New(filepath.Join(root, "printing"), db, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	prints.WithAdmission(users.AllowPrinting)
	reader := printerReaderFunc(func(context.Context) ([]printers.Printer, error) {
		if len(printerErrors) > 0 {
			return nil, printerErrors[0]
		}
		return []printers.Printer{}, nil
	})
	manager := management.New(users, db, reader, prints)
	return api.NewManagedHandler("test", sessions, reader, nil, prints, manager), db, sessions, owner
}

func TestManagementRemainsAvailableWhenCUPSIsDown(t *testing.T) {
	h, _, _, _ := accountFixture(t, errors.New("unavailable"))
	admin := loginAccount(t, h, "owner")
	w := call(h, "GET", "/api/v1/admin/dashboard", "", admin.Token)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result management.Dashboard
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.PrinterStatusAvailable || result.Printers == nil {
		t.Fatal("unavailable printer status was not explicit", err)
	}
	if w := call(h, "GET", "/api/v1/admin/users", "", admin.Token); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := call(h, "POST", "/api/v1/admin/settings", `{"printingEnabled":false}`, admin.Token); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}

func loginAccount(t *testing.T, h http.Handler, loginCode string) pairing.Session {
	t.Helper()
	w := call(h, "POST", "/api/v1/sessions", `{"pairingCode":"`+code+`","loginCode":"`+loginCode+`"}`, "")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result pairing.Session
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.User == nil {
		t.Fatal("missing user")
	}
	return result
}

func TestOptionalNameIsPersistedAndCannotGrantAdmin(t *testing.T) {
	h, _, _, _ := accountFixture(t)
	session := loginAccount(t, h, "visitor")
	if session.User.IsAdmin || session.User.NamePrompted {
		t.Fatal("first visitor has extra privileges")
	}
	for _, body := range []string{`{}`, `{"name":null}`, `{"name":"Ray","isAdmin":true}`, `{"name":"Ray","openid":"forged"}`} {
		assertError(t, call(h, "POST", "/api/v1/me", body, session.Token), 400, "invalid_request")
	}
	for _, body := range []string{`{"name":"a\nb"}`, `{"name":"` + strings.Repeat("名", 41) + `"}`} {
		assertError(t, call(h, "POST", "/api/v1/me", body, session.Token), 400, "invalid_name")
	}
}

func TestProfileSkipEditAndNewSession(t *testing.T) {
	h, _, _, _ := accountFixture(t)
	first := loginAccount(t, h, "visitor")
	w := call(h, "POST", "/api/v1/me", `{"name":""}`, first.Token)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	second := loginAccount(t, h, "visitor")
	if first.User.ID != second.User.ID || !second.User.NamePrompted || second.User.Name != "" {
		t.Fatal("skip was only cached")
	}
	w = call(h, "POST", "/api/v1/me", `{"name":"  测试姓名  "}`, second.Token)
	var result struct{ User accounts.User }
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.User.Name != "测试姓名" {
		t.Fatal(w.Body.String(), err)
	}
	third := loginAccount(t, h, "visitor")
	if third.User.Name != "测试姓名" || !third.User.NamePrompted {
		t.Fatal("name lost on login")
	}
	if strings.Contains(w.Body.String(), "open_id") || strings.Contains(w.Body.String(), "verified-") {
		t.Fatal("external identity leak")
	}
}

func TestAccountResponsesOnlyExposeInternalUserIdentifiers(t *testing.T) {
	h, _, _, _ := accountFixture(t)
	admin := loginAccount(t, h, "owner")
	visitor := loginAccount(t, h, "visitor")
	if visitor.User.ID == "verified-visitor" || len(visitor.User.ID) != 32 {
		t.Fatal("user identifier was not internal")
	}
	for _, tc := range []struct {
		method, path, body, token string
	}{
		{"POST", "/api/v1/sessions", `{"pairingCode":"` + code + `","loginCode":"visitor"}`, ""},
		{"GET", "/api/v1/me", "", visitor.Token},
		{"POST", "/api/v1/me", `{"name":"测试姓名"}`, visitor.Token},
		{"GET", "/api/v1/admin/users", "", admin.Token},
		{"POST", "/api/v1/admin/users/" + visitor.User.ID + "/ban", `{"banned":true}`, admin.Token},
	} {
		w := call(h, tc.method, tc.path, tc.body, tc.token)
		if w.Code < 200 || w.Code >= 300 {
			t.Fatal(tc.path, w.Code, w.Body.String())
		}
		body := strings.ToLower(w.Body.String())
		for _, private := range []string{"openid", "open_id", "unionid", "session_key", "appid", "verified-", "test-app"} {
			if strings.Contains(body, private) {
				t.Fatalf("%s leaked external identity field %s", tc.path, private)
			}
		}
		if !strings.Contains(w.Body.String(), visitor.User.ID) {
			t.Fatalf("%s omitted the internal account identifier", tc.path)
		}
	}
}

func TestAdminPermissionsBanAndExistingSessions(t *testing.T) {
	h, db, sessions, owner := accountFixture(t)
	visitor := loginAccount(t, h, "visitor")
	admin := loginAccount(t, h, "owner")
	if !admin.User.IsAdmin || admin.User.ID != owner.ID {
		t.Fatal("owner provisioning failed")
	}
	for _, path := range []string{"/api/v1/admin/users", "/api/v1/admin/settings", "/api/v1/admin/dashboard"} {
		assertError(t, call(h, "GET", path, "", visitor.Token), 403, "admin_required")
		if w := call(h, "GET", path, "", admin.Token); w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	assertError(t, call(h, "POST", "/api/v1/admin/settings", `{"printingEnabled":false}`, visitor.Token), 403, "admin_required")
	assertError(t, call(h, "POST", "/api/v1/admin/jobs/missing/confirm-cancel", `{"deviceStopped":true}`, visitor.Token), 403, "admin_required")
	assertError(t, call(h, "POST", "/api/v1/admin/users/"+visitor.User.ID+"/ban", `{"banned":true}`, visitor.Token), 403, "admin_required")
	assertError(t, call(h, "POST", "/api/v1/admin/users/"+admin.User.ID+"/ban", `{"banned":true}`, admin.Token), 403, "admin_required")
	assertError(t, call(h, "POST", "/api/v1/admin/users/missing/ban", `{"banned":true}`, admin.Token), 404, "user_not_found")
	job := printing.Job{ID: strings.Repeat("c", 32), Owner: visitor.User.ID, Key: "job-request-00001", State: "awaiting_flip",
		SheetCount: 2, CreatedAt: time.Now().UTC(), Settings: printing.Settings{PrinterID: "fake", Sides: "manual-long-edge"}}
	if err := db.InsertJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	w := call(h, "POST", "/api/v1/admin/users/"+visitor.User.ID+"/ban", `{"banned":true}`, admin.Token)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, path := range []string{"/api/v1/me", "/api/v1/printers", "/api/v1/jobs?active=true", "/api/v1/jobs/" + job.ID} {
		assertError(t, call(h, "GET", path, "", visitor.Token), 403, "user_banned")
	}
	assertError(t, call(h, "POST", "/api/v1/jobs/"+job.ID+"/continue", `{"paperConfirmed":true}`, visitor.Token), 403, "user_banned")
	if err := sessions.Validate(visitor.Token); !errors.Is(err, accounts.ErrBanned) {
		t.Fatal("existing session not blocked", err)
	}
	assertError(t, call(h, "POST", "/api/v1/sessions", `{"pairingCode":"`+code+`","loginCode":"visitor"}`, ""), 403, "user_banned")
	stored, _ := db.GetJob(context.Background(), job.ID)
	if stored.State != "awaiting_flip" {
		t.Fatal("ban unexpectedly changed a job")
	}
	assertError(t, call(h, "POST", "/api/v1/admin/jobs/"+job.ID+"/cancel", "{}", visitor.Token), 403, "user_banned")
	assertError(t, call(h, "POST", "/api/v1/admin/jobs/"+job.ID+"/confirm-cancel", `{"deviceStopped":true}`, visitor.Token), 403, "user_banned")
	if w := call(h, "POST", "/api/v1/admin/jobs/"+job.ID+"/cancel", "{}", admin.Token); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	stored, _ = db.GetJob(context.Background(), job.ID)
	if stored.State != "canceled" {
		t.Fatal("administrator could not clear abandoned task")
	}
	w = call(h, "POST", "/api/v1/admin/jobs/"+job.ID+"/confirm-cancel", `{"deviceStopped":true}`, admin.Token)
	var confirmation map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &confirmation); err != nil || w.Code != 200 ||
		len(confirmation) != 2 || confirmation["id"] != job.ID || confirmation["state"] != "canceled" {
		t.Fatal("admin confirmation leaked document data or lost idempotency", w.Code, w.Body.String(), err)
	}
	w = call(h, "POST", "/api/v1/admin/users/"+visitor.User.ID+"/ban", `{"banned":false}`, admin.Token)
	if w.Code != 200 || sessions.Validate(visitor.Token) != nil {
		t.Fatal("unban failed")
	}
}

func TestIdentityRequiredAndPrintAdmission(t *testing.T) {
	h, db, _, _ := accountFixture(t)
	assertError(t, call(h, "POST", "/api/v1/sessions", `{"pairingCode":"`+code+`"}`, ""), 401, "wechat_login_required")
	assertError(t, call(h, "POST", "/api/v1/sessions", `{"pairingCode":"`+code+`","openid":"forged"}`, ""), 400, "invalid_request")
	assertError(t, call(h, "POST", "/api/v1/sessions", `{"pairingCode":"`+code+`","loginCode":"invalid"}`, ""), 401, "wechat_login_failed")
	assertError(t, call(h, "POST", "/api/v1/sessions", `{"pairingCode":"`+code+`","loginCode":"offline"}`, ""), 503, "wechat_unavailable")
	admin := loginAccount(t, h, "owner")
	visitor := loginAccount(t, h, "visitor")
	for _, body := range []string{`{}`, `{"printingEnabled":null}`, `{"printingEnabled":false,"cupsCommand":"bad"}`} {
		assertError(t, call(h, "POST", "/api/v1/admin/settings", body, admin.Token), 400, "invalid_request")
	}
	w := call(h, "POST", "/api/v1/admin/settings", `{"printingEnabled":false}`, admin.Token)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	assertError(t, call(h, "POST", "/api/v1/plans", `{"settings":{}}`, visitor.Token), 409, "printing_paused")
	assertError(t, call(h, "POST", "/api/v1/jobs", `{"planId":"none","idempotencyKey":"test-request-0001"}`, visitor.Token), 409, "printing_paused")
	settings, _ := db.AccountSettings(context.Background())
	if settings.PrintingEnabled {
		t.Fatal("printing setting was not saved")
	}
	existing := printing.Job{ID: strings.Repeat("e", 32), PlanID: strings.Repeat("f", 32),
		Owner: visitor.User.ID, Key: "existing-request-0001", State: "awaiting_flip", Phase: "front",
		CreatedAt: time.Now().UTC(), Settings: printing.Settings{PrinterID: "virtual", Sides: "manual-long-edge"}}
	if err := db.InsertJob(context.Background(), existing); err != nil {
		t.Fatal(err)
	}
	w = call(h, "POST", "/api/v1/jobs/"+existing.ID+"/continue", `{"paperConfirmed":true}`, visitor.Token)
	if w.Code != 200 {
		t.Fatal("pausing new prints blocked an existing duplex task", w.Code, w.Body.String())
	}
	w = call(h, "POST", "/api/v1/jobs", `{"planId":"`+existing.PlanID+`","idempotencyKey":"`+existing.Key+`"}`, visitor.Token)
	if w.Code != 201 {
		t.Fatal("pausing new prints broke an idempotent retry", w.Code, w.Body.String())
	}
}
