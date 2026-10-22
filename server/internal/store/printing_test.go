package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/RayFlows/printbridge/server/internal/pairing"
	"github.com/RayFlows/printbridge/server/internal/printing"
)

func TestPrintingAndSessionsPersistAndCodeRotationRevokes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	ctx := context.Background()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	const code = "test-only-pairing-code-0123456789012345"
	sessions, err := pairing.NewPersistent(code, time.Hour, db)
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Pair(code)
	if err != nil {
		t.Fatal(err)
	}
	plan := printing.Plan{ID: "plan", Owner: "owner", PageCount: 2}
	job := printing.Job{ID: "job", Owner: "owner", PlanID: plan.ID, Key: "request-key", State: "awaiting_flip", Phase: "front"}
	if err := db.SavePlan(ctx, plan, ""); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertJob(ctx, job); err == nil {
		t.Fatal("duplicate idempotency key")
	}
	db.Close()
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sessions, err = pairing.NewPersistent(code, time.Hour, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessions.Validate(session.Token); err != nil {
		t.Fatal("session lost on restart", err)
	}
	got, err := db.FindJob(ctx, "owner", "request-key")
	if err != nil || got.State != "awaiting_flip" || got.Owner != "owner" || got.Key != "request-key" {
		t.Fatal(got, err)
	}
	p, err := db.GetPlan(ctx, "plan")
	if err != nil || p.Owner != "owner" {
		t.Fatal(p, err)
	}
	if err := sessions.Revoke(session.Token); err != nil {
		t.Fatal(err)
	}
	sessions, _ = pairing.NewPersistent(code, time.Hour, db)
	if err := sessions.Validate(session.Token); !errors.Is(err, pairing.ErrUnauthorized) {
		t.Fatal(err)
	}
	session, _ = sessions.Pair(code)
	rotated, err := pairing.NewPersistent("changed-"+code, time.Hour, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := rotated.Validate(session.Token); !errors.Is(err, pairing.ErrUnauthorized) {
		t.Fatal("code rotation retained authorization")
	}
}

func TestPlanReplacementRollsBackWhenInsertionFails(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	old := printing.Plan{ID: "old", Owner: "owner"}
	newPlan := printing.Plan{ID: "new", Owner: "owner"}
	if err := db.SavePlan(ctx, old, ""); err != nil {
		t.Fatal(err)
	}
	if err := db.SavePlan(ctx, newPlan, ""); err != nil {
		t.Fatal(err)
	}
	if err := db.SavePlan(ctx, newPlan, old.ID); err == nil {
		t.Fatal("duplicate insertion accepted")
	}
	if _, err := db.GetPlan(ctx, old.ID); err != nil {
		t.Fatal("rollback lost predecessor", err)
	}
	newPlan.ID = "replacement"
	if err := db.SavePlan(ctx, newPlan, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetPlan(ctx, old.ID); !errors.Is(err, printing.ErrNotFound) {
		t.Fatal("predecessor retained", err)
	}
}
