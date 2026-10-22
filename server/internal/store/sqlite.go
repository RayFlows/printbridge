package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	"github.com/RayFlows/printbridge/server/internal/documents"
	_ "modernc.org/sqlite"
)

//go:embed migrations/001_documents.sql
var documentsMigration string

//go:embed migrations/002_printing.sql
var printingMigration string

//go:embed migrations/003_accounts.sql
var accountsMigration string

type SQLite struct{ db *sql.DB }

func Open(path string) (*SQLite, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	uriPath := filepath.ToSlash(absolute)
	if filepath.VolumeName(absolute) != "" && uriPath[0] != '/' {
		uriPath = "/" + uriPath
	}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: uriPath}).String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &SQLite{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLite) migrate() error {
	if _, err := s.db.Exec("PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;"); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > 3 {
		return fmt.Errorf("database schema %d is newer than supported schema 3", version)
	}
	if version == 0 {
		if _, err := tx.Exec(documentsMigration); err != nil {
			return err
		}
		if _, err := tx.Exec("PRAGMA user_version=1"); err != nil {
			return err
		}
	}
	if version < 2 {
		if _, err := tx.Exec(printingMigration); err != nil {
			return err
		}
		if _, err := tx.Exec("PRAGMA user_version=2"); err != nil {
			return err
		}
	}
	if version < 3 {
		if _, err := tx.Exec(accountsMigration); err != nil {
			return err
		}
		if _, err := tx.Exec("PRAGMA user_version=3"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLite) Close() error { return s.db.Close() }

func (s *SQLite) Insert(ctx context.Context, d documents.Document) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO documents
		(id,owner,name,bytes,page_count,created_at,expires_at) VALUES (?,?,?,?,?,?,?)`,
		d.ID, d.Owner, d.Name, d.Bytes, d.PageCount, d.CreatedAt.UnixMilli(), d.ExpiresAt.UnixMilli())
	return err
}

func (s *SQLite) Get(ctx context.Context, id string) (documents.Document, error) {
	return scan(s.db.QueryRowContext(ctx, `SELECT id,owner,name,bytes,page_count,created_at,expires_at FROM documents WHERE id=?`, id))
}

func (s *SQLite) List(ctx context.Context) ([]documents.Document, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,owner,name,bytes,page_count,created_at,expires_at FROM documents")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []documents.Document{}
	for rows.Next() {
		doc, err := scan(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, doc)
	}
	return result, rows.Err()
}

func (s *SQLite) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM documents WHERE id=?", id)
	return err
}

func scan(row interface{ Scan(...any) error }) (documents.Document, error) {
	var doc documents.Document
	var created, expires int64
	err := row.Scan(&doc.ID, &doc.Owner, &doc.Name, &doc.Bytes, &doc.PageCount, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return documents.Document{}, documents.ErrNotFound
	}
	doc.CreatedAt = time.UnixMilli(created).UTC()
	doc.ExpiresAt = time.UnixMilli(expires).UTC()
	return doc, err
}
