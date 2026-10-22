package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/RayFlows/printbridge/server/internal/accounts"
)

const userColumns = "id,app_id,name,name_prompted,banned,created_at,last_seen_at"

func scanUser(row interface{ Scan(...any) error }) (accounts.User, error) {
	var u accounts.User
	var created, seen int64
	err := row.Scan(&u.ID, &u.AppID, &u.Name, &u.NamePrompted, &u.Banned, &created, &seen)
	if errors.Is(err, sql.ErrNoRows) {
		return u, accounts.ErrNotFound
	}
	u.CreatedAt, u.LastSeenAt = time.UnixMilli(created).UTC(), time.UnixMilli(seen).UTC()
	return u, err
}

func (s *SQLite) LoginUser(ctx context.Context, appID, openID string, now time.Time) (accounts.User, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return accounts.User{}, err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO users(id,app_id,open_id,created_at,last_seen_at)
		VALUES(?,?,?,?,?) ON CONFLICT(app_id,open_id) DO UPDATE SET last_seen_at=excluded.last_seen_at`,
		hex.EncodeToString(id[:]), appID, openID, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return accounts.User{}, err
	}
	return scanUser(s.db.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE app_id=? AND open_id=?", appID, openID))
}

func (s *SQLite) GetUser(ctx context.Context, id string) (accounts.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE id=?", id))
}

func (s *SQLite) ListUsers(ctx context.Context) ([]accounts.User, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+userColumns+" FROM users ORDER BY last_seen_at DESC,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []accounts.User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, u)
	}
	return result, rows.Err()
}

func (s *SQLite) UpdateName(ctx context.Context, id, name string) (accounts.User, error) {
	if _, err := s.db.ExecContext(ctx, "UPDATE users SET name=?,name_prompted=1 WHERE id=?", name, id); err != nil {
		return accounts.User{}, err
	}
	return s.GetUser(ctx, id)
}

func (s *SQLite) SetBanned(ctx context.Context, id string, banned bool) (accounts.User, error) {
	if _, err := s.db.ExecContext(ctx, "UPDATE users SET banned=? WHERE id=?", banned, id); err != nil {
		return accounts.User{}, err
	}
	return s.GetUser(ctx, id)
}

func (s *SQLite) AccountSettings(ctx context.Context) (accounts.Settings, error) {
	var settings accounts.Settings
	err := s.db.QueryRowContext(ctx, "SELECT printing_enabled FROM account_settings WHERE id=1").Scan(&settings.PrintingEnabled)
	return settings, err
}

func (s *SQLite) SaveAccountSettings(ctx context.Context, settings accounts.Settings) error {
	_, err := s.db.ExecContext(ctx, "UPDATE account_settings SET printing_enabled=? WHERE id=1", settings.PrintingEnabled)
	return err
}
