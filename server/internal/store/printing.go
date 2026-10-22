package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/RayFlows/printbridge/server/internal/pairing"
	"github.com/RayFlows/printbridge/server/internal/printing"
)

func (s *SQLite) SavePlan(ctx context.Context, p printing.Plan, replaces string) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "INSERT INTO print_plans(id,owner,payload) VALUES(?,?,?)", p.ID, p.Owner, string(data)); err != nil {
		return err
	}
	if replaces != "" {
		if _, err := tx.ExecContext(ctx, "DELETE FROM print_plans WHERE id=? AND owner=?", replaces, p.Owner); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *SQLite) GetPlan(ctx context.Context, id string) (printing.Plan, error) {
	var p printing.Plan
	var owner, data string
	err := s.db.QueryRowContext(ctx, "SELECT owner,payload FROM print_plans WHERE id=?", id).Scan(&owner, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return p, printing.ErrNotFound
	}
	if err != nil {
		return p, err
	}
	err = json.Unmarshal([]byte(data), &p)
	p.Owner = owner
	return p, err
}
func (s *SQLite) ListPlans(ctx context.Context) ([]printing.Plan, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT owner,payload FROM print_plans")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []printing.Plan{}
	for rows.Next() {
		var owner, data string
		if err := rows.Scan(&owner, &data); err != nil {
			return nil, err
		}
		var plan printing.Plan
		if err := json.Unmarshal([]byte(data), &plan); err != nil {
			return nil, err
		}
		plan.Owner = owner
		result = append(result, plan)
	}
	return result, rows.Err()
}
func (s *SQLite) DeletePlan(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM print_plans WHERE id=?", id)
	return err
}
func (s *SQLite) InsertJob(ctx context.Context, j printing.Job) error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO print_jobs(id,owner,request_key,payload) VALUES(?,?,?,?)", j.ID, j.Owner, j.Key, string(data))
	return err
}
func (s *SQLite) SaveJob(ctx context.Context, j printing.Job) error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, "UPDATE print_jobs SET payload=? WHERE id=?", string(data), j.ID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count != 1 {
		return printing.ErrNotFound
	}
	return err
}
func scanJob(row interface{ Scan(...any) error }) (printing.Job, error) {
	var j printing.Job
	var owner, key, data string
	err := row.Scan(&owner, &key, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return j, printing.ErrNotFound
	}
	if err != nil {
		return j, err
	}
	err = json.Unmarshal([]byte(data), &j)
	j.Owner = owner
	j.Key = key
	return j, err
}
func (s *SQLite) GetJob(ctx context.Context, id string) (printing.Job, error) {
	return scanJob(s.db.QueryRowContext(ctx, "SELECT owner,request_key,payload FROM print_jobs WHERE id=?", id))
}
func (s *SQLite) FindJob(ctx context.Context, owner, key string) (printing.Job, error) {
	return scanJob(s.db.QueryRowContext(ctx, "SELECT owner,request_key,payload FROM print_jobs WHERE owner=? AND request_key=?", owner, key))
}
func (s *SQLite) ListJobs(ctx context.Context) ([]printing.Job, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT owner,request_key,payload FROM print_jobs")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []printing.Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}
func (s *SQLite) DeleteJob(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM print_jobs WHERE id=?", id)
	return err
}

func (s *SQLite) LoadSessions(code [32]byte, now time.Time) (map[[32]byte]pairing.Record, error) {
	if _, err := s.db.Exec("DELETE FROM sessions WHERE code_hash<>? OR expires_at<=?", code[:], now.UnixMilli()); err != nil {
		return nil, err
	}
	rows, err := s.db.Query("SELECT token_hash,expires_at,user_id FROM sessions")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[[32]byte]pairing.Record{}
	for rows.Next() {
		var hash []byte
		var expires int64
		var userID string
		if err := rows.Scan(&hash, &expires, &userID); err != nil {
			return nil, err
		}
		if len(hash) != 32 {
			return nil, errors.New("invalid session hash")
		}
		var key [32]byte
		copy(key[:], hash)
		result[key] = pairing.Record{ExpiresAt: time.UnixMilli(expires).UTC(), UserID: userID}
	}
	return result, rows.Err()
}
func (s *SQLite) SaveSession(hash, code [32]byte, record pairing.Record) error {
	_, err := s.db.Exec("INSERT INTO sessions(token_hash,code_hash,expires_at,user_id) VALUES(?,?,?,?)", hash[:], code[:], record.ExpiresAt.UnixMilli(), record.UserID)
	return err
}
func (s *SQLite) DeleteSession(hash [32]byte) error {
	_, err := s.db.Exec("DELETE FROM sessions WHERE token_hash=?", hash[:])
	return err
}
