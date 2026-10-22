package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/RayFlows/printbridge/server/internal/documents"
)

func TestMigrateReopenReadAndDelete(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "database with spaces.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := documents.Document{ID: "0123456789abcdef0123456789abcdef", Owner: "hashed-owner", Name: "report.pdf",
		Bytes: 200, PageCount: 16, CreatedAt: time.Unix(1000, 0).UTC(), ExpiresAt: time.Unix(2000, 0).UTC()}
	if err := db.Insert(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := db.Get(context.Background(), doc.ID)
	if err != nil || got != doc {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	all, err := db.List(context.Background())
	if err != nil || len(all) != 1 {
		t.Fatal(all, err)
	}
	if err := db.Delete(context.Background(), doc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Get(context.Background(), doc.ID); !errors.Is(err, documents.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestFutureSchemaIsRejectedWithoutResetting(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "future.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec("PRAGMA user_version=4"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Open(path); err == nil {
		t.Fatal("future schema accepted")
	}
}

func TestSchemaOneUpgradeKeepsDocuments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := documents.Document{ID: "retained", Owner: "owner", Name: "old.pdf", Bytes: 20, PageCount: 1, CreatedAt: time.Now().UTC().Truncate(time.Millisecond), ExpiresAt: time.Now().Add(time.Hour).UTC().Truncate(time.Millisecond)}
	if err := db.Insert(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec("DROP TABLE sessions; DROP TABLE print_plans; DROP TABLE print_jobs; DROP TABLE users; DROP TABLE account_settings; PRAGMA user_version=1"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := db.Get(context.Background(), doc.ID)
	if err != nil || got != doc {
		t.Fatal("upgrade changed document", got, err)
	}
	var version int
	if err := db.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 3 {
		t.Fatal(version, err)
	}
}
