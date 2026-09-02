// Package store owns SQLite persistence for the Phase 2 read-only data ports.
// It is imported from the legacy JSON state once on boot, then read natively.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// SessionImport is a single session row from the legacy JSON import shape.
type SessionImport struct {
	ID        string
	Title     string
	Workspace string
	Model     string
	Messages  string // JSON array of OpenAI-format messages, stored verbatim
	CreatedAt string
	UpdatedAt string
	Pinned    int
	Archived  int
	ProjectID string
}

// SessionRow is a read-model session surfaced to handlers.
type SessionRow struct {
	ID        string
	Title     string
	Workspace string
	Model     string
	Messages  string
	CreatedAt float64
	UpdatedAt float64
	Pinned    int
	Archived  int
	ProjectID string
}

// Open opens (or creates) the SQLite database and applies the schema.
func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
  session_id TEXT PRIMARY KEY,
  title TEXT NOT NULL DEFAULT '',
  workspace TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  messages TEXT NOT NULL DEFAULT '[]',
  tool_calls TEXT NOT NULL DEFAULT '[]',
  created_at REAL NOT NULL DEFAULT 0,
  updated_at REAL NOT NULL DEFAULT 0,
  pinned INTEGER NOT NULL DEFAULT 0,
  archived INTEGER NOT NULL DEFAULT 0,
  project_id TEXT
);
CREATE INDEX IF NOT EXISTS idx_sessions_updated_at ON sessions(updated_at DESC);
CREATE TABLE IF NOT EXISTS workspaces (path TEXT PRIMARY KEY, name TEXT);
CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE IF NOT EXISTS projects (id TEXT PRIMARY KEY, name TEXT, color TEXT);
`

// ImportSession inserts a JSON session into sqlite, tolerating empty optional fields.
func ImportSession(db *sql.DB, s SessionImport) error {
	if _, err := db.Exec(`
		INSERT INTO sessions (session_id, title, workspace, model, messages, tool_calls, created_at, updated_at, pinned, archived, project_id)
		VALUES (?, ?, ?, ?, ?, '[]', ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			title=excluded.title, workspace=excluded.workspace, model=excluded.model,
			messages=excluded.messages, updated_at=excluded.updated_at,
			pinned=excluded.pinned, archived=excluded.archived, project_id=excluded.project_id
	`, s.ID, s.Title, s.Workspace, s.Model, s.Messages,
		parseTime(s.CreatedAt), parseTime(s.UpdatedAt), s.Pinned, s.Archived, s.ProjectID); err != nil {
		return err
	}
	return nil
}

// GetSession returns one session row, or sql.ErrNoRows if absent.
func GetSession(db *sql.DB, id string) (SessionRow, error) {
	var r SessionRow
	err := db.QueryRow(`
		SELECT session_id, title, workspace, model, messages, created_at, updated_at, pinned, archived, project_id
		FROM sessions WHERE session_id = ?`, id).
		Scan(&r.ID, &r.Title, &r.Workspace, &r.Model, &r.Messages, &r.CreatedAt, &r.UpdatedAt, &r.Pinned, &r.Archived, &r.ProjectID)
	return r, err
}

// ListSessions returns sessions ordered by most-recent update, with pagination.
func ListSessions(db *sql.DB, limit, offset int) ([]SessionRow, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := db.Query(`
		SELECT session_id, title, workspace, model, messages, created_at, updated_at, pinned, archived, project_id
		FROM sessions ORDER BY updated_at DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionRow
	for rows.Next() {
		var r SessionRow
		if err := rows.Scan(&r.ID, &r.Title, &r.Workspace, &r.Model, &r.Messages, &r.CreatedAt, &r.UpdatedAt, &r.Pinned, &r.Archived, &r.ProjectID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountSessions returns the total session row count.
func CountSessions(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n)
	return n, err
}

// parseTime converts an RFC3339 (or numeric-epoch) string to a float epoch.
// On any parse failure it returns 0 so import never fails on a malformed value.
func parseTime(v string) float64 {
	if v == "" {
		return 0
	}
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return float64(t.UnixNano()) / 1e9
	}
	var f float64
	if _, err := fmt.Sscanf(v, "%f", &f); err == nil {
		return f
	}
	return 0
}

// MarshalMessages raw-encodes the stored messages JSON (no re-serialization).
func MarshalMessages(raw []byte) ([]byte, error) {
	return json.RawMessage(raw), nil
}
