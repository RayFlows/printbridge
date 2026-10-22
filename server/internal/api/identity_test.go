package api_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RayFlows/printbridge/server/internal/accounts"
	"github.com/RayFlows/printbridge/server/internal/api"
	"github.com/RayFlows/printbridge/server/internal/pairing"
	"github.com/RayFlows/printbridge/server/internal/printing"
	"github.com/RayFlows/printbridge/server/internal/store"
)

type identityVerifier func(context.Context, string) (string, error)

func (f identityVerifier) OpenID(ctx context.Context, code string) (string, error) {
	return f(ctx, code)
}

func TestReauthorizationKeepsCurrentTask(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	users := accounts.New(db, identityVerifier(func(_ context.Context, code string) (string, error) {
		if code == "fresh-code-1" || code == "fresh-code-2" {
			return "verified-same-person", nil
		}
		return "verified-other-person", nil
	}), "mock-app", "")
	defer db.Close()
	sessions, err := pairing.NewPersistent(code, time.Hour, db)
	if err != nil {
		t.Fatal(err)
	}
	sessions.WithUsers(users)
	prints, err := printing.New(filepath.Join(root, "printing"), db, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := api.NewHandler("test", sessions, nil, nil, prints)
	first, err := sessions.Login(context.Background(), code, "fresh-code-1")
	if err != nil {
		t.Fatal(err)
	}
	// Seed through the owner captured by the actual HTTP route.
	probe := &printService{}
	probeHandler := api.NewHandler("test", sessions, nil, nil, probe)
	call(probeHandler, "POST", "/api/v1/plans", `{"settings":{}}`, first.Token)
	job := printing.Job{ID: strings.Repeat("a", 32), Owner: probe.owner, Key: "test-request-0001",
		State: "awaiting_flip", Phase: "front", CreatedAt: time.Now().UTC(),
		Settings: printing.Settings{PrinterID: "virtual", Sides: "manual-long-edge"}}
	if err := db.InsertJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	users = accounts.New(db, identityVerifier(func(_ context.Context, code string) (string, error) {
		if code == "fresh-code-2" {
			return "verified-same-person", nil
		}
		return "verified-other-person", nil
	}), "mock-app", "")
	sessions, err = pairing.NewPersistent(code, time.Hour, db)
	if err != nil {
		t.Fatal(err)
	}
	sessions.WithUsers(users)
	second, err := sessions.Login(context.Background(), code, "fresh-code-2")
	if err != nil {
		t.Fatal(err)
	}
	prints, err = printing.New(filepath.Join(root, "printing"), db, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	h = api.NewHandler("test", sessions, nil, nil, prints)
	w := call(h, "GET", "/api/v1/jobs?active=true", "", second.Token)
	var result struct{ Jobs []printing.Job }
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(result.Jobs) != 1 {
		t.Fatalf("current task disappeared after reauthorization: status=%d jobs=%d", w.Code, len(result.Jobs))
	}
	w = call(h, "POST", "/api/v1/jobs/"+job.ID+"/continue", `{"paperConfirmed":true}`, second.Token)
	if w.Code != 200 {
		t.Fatal("same user cannot continue after reauthorization", w.Code, w.Body.String())
	}
	other, err := sessions.Login(context.Background(), code, "other-code")
	if err != nil {
		t.Fatal(err)
	}
	w = call(h, "GET", "/api/v1/jobs?active=true", "", other.Token)
	json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || len(result.Jobs) != 0 {
		t.Fatal("another user can see the task", w.Body.String())
	}
	assertError(t, call(h, "POST", "/api/v1/jobs/"+job.ID+"/continue", `{"paperConfirmed":true}`, other.Token), 404, "print_not_found")
	if first.User.ID != second.User.ID || first.Token == second.Token {
		t.Fatal("identity was tied to session token")
	}
}
