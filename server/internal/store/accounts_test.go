package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/RayFlows/printbridge/server/internal/accounts"
	"github.com/RayFlows/printbridge/server/internal/pairing"
	"github.com/RayFlows/printbridge/server/internal/printing"
)

func TestAccountsPersistAcrossRestartAndAppScope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, err := db.LoginUser(ctx, "app-a", "same-openid", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := 0; i < 16; i++ {
		workers.Go(func() {
			u, err := db.LoginUser(ctx, "app-a", "same-openid", time.Now())
			if err != nil || u.ID != first.ID {
				t.Error("concurrent login changed identity", err)
			}
		})
	}
	workers.Wait()
	other, err := db.LoginUser(ctx, "app-b", "same-openid", time.Now())
	if err != nil || other.ID == first.ID {
		t.Fatal("application scope lost", err)
	}
	db.UpdateName(ctx, first.ID, "本人")
	db.SetBanned(ctx, first.ID, true)
	db.SaveAccountSettings(ctx, accounts.Settings{PrintingEnabled: false})
	db.Close()
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	u, err := db.GetUser(ctx, first.ID)
	if err != nil || !u.NamePrompted || u.Name != "本人" || !u.Banned {
		t.Fatal("account lost on restart", u, err)
	}
	settings, err := db.AccountSettings(ctx)
	if err != nil || settings.PrintingEnabled {
		t.Fatal("settings lost", err)
	}
	if _, err := db.GetUser(ctx, "missing"); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestSchemaTwoUpgradeDoesNotClaimOldTasks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema-two.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const code = "test-only-pairing-code-0123456789012345"
	sessions, _ := pairing.NewPersistent(code, time.Hour, db)
	session, _ := sessions.Pair(code)
	owner, err := sessions.Owner(ctx, session.Token)
	if err != nil {
		t.Fatal(err)
	}
	job := printing.Job{ID: "legacy-job", Owner: owner, Key: "old-key", State: "awaiting_flip"}
	if err := db.InsertJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec("DROP TABLE users; DROP TABLE account_settings; ALTER TABLE sessions DROP COLUMN user_id; PRAGMA user_version=2"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stored, err := db.GetJob(ctx, job.ID)
	if err != nil || stored.Owner != owner || stored.State != "awaiting_flip" {
		t.Fatal("migration reassigned old task", err)
	}
	sessions, err = pairing.NewPersistent(code, time.Hour, db)
	if err != nil || sessions.Validate(session.Token) != nil {
		t.Fatal("legacy compatibility lost", err)
	}
	u, err := db.LoginUser(ctx, "app", "new-person", time.Now())
	if err != nil || u.ID == stored.Owner {
		t.Fatal("new user claimed old owner", err)
	}
}
