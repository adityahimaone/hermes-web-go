// Package store owns SQLite persistence for the Phase 2 read-only data ports.
// It is imported from the legacy JSON state once on boot, then read natively.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	// EnabledToolsets is the raw JSON array of the per-session toolset override.
	EnabledToolsets string
	// ComposerDraft is the raw JSON object of the per-session composer draft.
	ComposerDraft string
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
	// Rev is a monotonic per-session revision counter, incremented in the same
	// transaction as every message append. The frontend uses it to refuse
	// applying a stale session snapshot over a newer, longer visible transcript.
	Rev int64
	// EnabledToolsets is the per-session toolset override (JSON array or "").
	EnabledToolsets string
	// ComposerDraft is the per-session composer draft (JSON object or "").
	ComposerDraft string
}

// Open opens (or creates) the SQLite database and applies the schema.
func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate")
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
	// Migrate existing databases that predate the `rev`, `enabled_toolsets`,
	// and `composer_draft` columns. A prior sessions table (created without
	// them, or from the legacy importer) must gain them so reads and mutations
	// carry the full projection.
	if err := migrateRevColumn(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrateSessionColumns(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func migrateRevColumn(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='rev'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := db.Exec(`ALTER TABLE sessions ADD COLUMN rev INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	return nil
}

// migrateSessionColumns adds the Family-1 projection columns (toolset override
// and composer draft) to databases that predate them.
func migrateSessionColumns(db *sql.DB) error {
	for _, col := range []string{"enabled_toolsets", "composer_draft"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='` + col + `'`).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := db.Exec(`ALTER TABLE sessions ADD COLUMN ` + col + ` TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
		}
	}
	return nil
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
  project_id TEXT,
  rev INTEGER NOT NULL DEFAULT 0,
  enabled_toolsets TEXT NOT NULL DEFAULT '',
  composer_draft TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_sessions_updated_at ON sessions(updated_at DESC);
CREATE TABLE IF NOT EXISTS workspaces (path TEXT PRIMARY KEY, name TEXT);
CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE IF NOT EXISTS projects (id TEXT PRIMARY KEY, name TEXT, color TEXT);
`

// ImportSession inserts a JSON session into sqlite, tolerating empty optional fields.
func ImportSession(db *sql.DB, s SessionImport) error {
	if _, err := db.Exec(`
		INSERT INTO sessions (session_id, title, workspace, model, messages, tool_calls, created_at, updated_at, pinned, archived, project_id, enabled_toolsets, composer_draft)
		VALUES (?, ?, ?, ?, ?, '[]', ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			title=excluded.title, workspace=excluded.workspace, model=excluded.model,
			messages=excluded.messages, updated_at=excluded.updated_at,
			pinned=excluded.pinned, archived=excluded.archived, project_id=excluded.project_id,
			enabled_toolsets=excluded.enabled_toolsets, composer_draft=excluded.composer_draft
	`, s.ID, s.Title, s.Workspace, s.Model, s.Messages,
		parseTime(s.CreatedAt), parseTime(s.UpdatedAt), s.Pinned, s.Archived, s.ProjectID,
		s.EnabledToolsets, s.ComposerDraft); err != nil {
		return err
	}
	return nil
}

// GetSession returns one session row, or sql.ErrNoRows if absent.
func GetSession(db *sql.DB, id string) (SessionRow, error) {
	var r SessionRow
	var projectID sql.NullString
	scanErr := db.QueryRow(`
		SELECT session_id, title, workspace, model, messages, created_at, updated_at, pinned, archived, project_id, rev, enabled_toolsets, composer_draft
		FROM sessions WHERE session_id = ?`, id).
		Scan(&r.ID, &r.Title, &r.Workspace, &r.Model, &r.Messages, &r.CreatedAt, &r.UpdatedAt, &r.Pinned, &r.Archived, &projectID, &r.Rev, &r.EnabledToolsets, &r.ComposerDraft)
	if scanErr != nil {
		return r, scanErr
	}
	r.ProjectID = projectID.String
	return r, nil
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
		SELECT session_id, title, workspace, model, messages, created_at, updated_at, pinned, archived, project_id, rev, enabled_toolsets, composer_draft
		FROM sessions ORDER BY updated_at DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionRow
	for rows.Next() {
		var r SessionRow
		var projectID sql.NullString
		if err := rows.Scan(&r.ID, &r.Title, &r.Workspace, &r.Model, &r.Messages, &r.CreatedAt, &r.UpdatedAt, &r.Pinned, &r.Archived, &projectID, &r.Rev, &r.EnabledToolsets, &r.ComposerDraft); err != nil {
			return nil, err
		}
		r.ProjectID = projectID.String
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
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return f
	}
	return 0
}

// MarshalMessages raw-encodes the stored messages JSON (no re-serialization).
func MarshalMessages(raw []byte) ([]byte, error) {
	return json.RawMessage(raw), nil
}

// SessionUpdate carries optional fields to update on a session.
type SessionUpdate struct {
	Workspace *string
	Model     *string
	Pinned    *int
	Archived  *int
	ProjectID *string
}

// SetSessionToolsets persists the per-session enabled-toolsets override as a
// JSON array (or SQL NULL when clearing the override).
func SetSessionToolsets(db *sql.DB, id string, raw []byte) error {
	if raw != nil {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
	}
	_, err := db.Exec(`UPDATE sessions SET enabled_toolsets=? WHERE session_id=?`, string(raw), id)
	return err
}

// SetSessionDraft persists the composer draft object. Pass nil to clear.
func SetSessionDraft(db *sql.DB, id string, draft []byte) error {
	if draft != nil {
		var v any
		if err := json.Unmarshal(draft, &v); err != nil {
			return err
		}
	}
	_, err := db.Exec(`UPDATE sessions SET composer_draft=? WHERE session_id=?`, string(draft), id)
	return err
}

// SetSessionProject assigns a session to a project_id (move).
func SetSessionProject(db *sql.DB, id, projectID string) error {
	_, err := db.Exec(`UPDATE sessions SET project_id=? WHERE session_id=?`, projectID, id)
	return err
}

// SetSessionFlag updates a single pinned/archived boolean column from an int.
// column must be one of the two whitelisted names; anything else is rejected.
func SetSessionFlag(db *sql.DB, id, column string, v int) error {
	if column != "pinned" && column != "archived" {
		return errors.New("invalid session flag column")
	}
	_, err := db.Exec(`UPDATE sessions SET `+column+`=? WHERE session_id=?`, v, id)
	return err
}

// TruncateMessages replaces messages with the first keep entries and bumps
// rev. Negative keep is rejected by the handler; here it truncates to zero.
func TruncateMessages(db *sql.DB, id string, keep int) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw string
	if err := tx.QueryRow(`SELECT messages FROM sessions WHERE session_id=?`, id).Scan(&raw); err != nil {
		return err
	}
	var messages []map[string]any
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &messages)
	}
	if keep < len(messages) {
		messages = messages[:keep]
	}
	enc, err := json.Marshal(messages)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE sessions SET messages=?, rev=rev+1, updated_at=? WHERE session_id=?`, string(enc), time.Now().Unix(), id); err != nil {
		return err
	}
	return tx.Commit()
}

// ClearSession empties messages and resets the title to Untitled, mirroring
// Python's /clear (truncate-to-zero plus title reset via the rename helper).
func ClearSession(db *sql.DB, id string) error {
	_, err := db.Exec(`UPDATE sessions SET messages='[]', title='Untitled', rev=rev+1, updated_at=? WHERE session_id=?`, time.Now().Unix(), id)
	return err
}

// CleanupSessions deletes rows matching the cleanup predicate. When
// zeroOnly is false the row must be Untitled AND have zero messages (Python
// /api/sessions/cleanup); when true any zero-message row qualifies
// (/api/sessions/cleanup_zero_message). Returns the session IDs removed.
func CleanupSessions(db *sql.DB, zeroOnly bool) ([]string, error) {
	cond := `messages = '[]'`
	if !zeroOnly {
		cond += ` AND title = 'Untitled'`
	}
	rows, err := db.Query(`SELECT session_id FROM sessions WHERE ` + cond)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return ids, nil
	}
	// Delete matching rows.
	placeholders := strings.Repeat(",?", len(ids)-1)
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	if _, err := db.Exec(`DELETE FROM sessions WHERE session_id IN (?`+placeholders+`)`, args...); err != nil {
		return nil, err
	}
	return ids, nil
}

// CountPinnedSessions returns how many sessions are currently pinned.
func CountPinnedSessions(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE pinned=1`).Scan(&n)
	return n, err
}

// CreateSession inserts a new session row. Workspace and title default to empty.
func CreateSession(db *sql.DB, s SessionImport) error {
	_, err := db.Exec(`INSERT INTO sessions (session_id, title, workspace, model, messages, tool_calls, created_at, updated_at, pinned, archived, project_id)
		VALUES (?, ?, ?, ?, ?, '[]', ?, ?, ?, ?, ?)`,
		s.ID, s.Title, s.Workspace, s.Model, s.Messages,
		parseTime(s.CreatedAt), parseTime(s.UpdatedAt), s.Pinned, s.Archived, s.ProjectID)
	return err
}

// UpdateSession updates optional fields on a session. Only non-nil fields are applied.
func UpdateSession(db *sql.DB, id string, u SessionUpdate) error {
	_, err := db.Exec(`UPDATE sessions SET workspace=COALESCE(?,workspace), model=COALESCE(?,model), pinned=COALESCE(?,pinned), archived=COALESCE(?,archived), project_id=COALESCE(?,project_id) WHERE session_id=?`,
		u.Workspace, u.Model, u.Pinned, u.Archived, u.ProjectID, id)
	return err
}

// RenameSession updates the title of a session.
func RenameSession(db *sql.DB, id, title string) error {
	_, err := db.Exec(`UPDATE sessions SET title=? WHERE session_id=?`, title, id)
	return err
}

// DeleteSession removes a session row. Returns sql.ErrNoRows when the session does not exist.
func DeleteSession(db *sql.DB, id string) error {
	r, err := db.Exec(`DELETE FROM sessions WHERE session_id=?`, id)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// AppendMessage appends an OpenAI-format message to a session's messages array.
func AppendMessage(db *sql.DB, id string, msg map[string]any) error {
	_, err := AppendMessageWithRev(db, id, msg)
	return err
}

// AppendMessageWithRev appends a message and returns the new per-session
// revision. The immediate transaction lock covers the read-modify-write, so
// concurrent turns on one session cannot overwrite each other's messages.
func AppendMessageWithRev(db *sql.DB, id string, msg map[string]any) (int64, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var raw string
	if err := tx.QueryRow(`SELECT messages FROM sessions WHERE session_id=?`, id).Scan(&raw); err != nil {
		return 0, err
	}
	var messages []map[string]any
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &messages); err != nil {
			// Corrupt messages column: replace wholesale rather than fail the turn.
			messages = nil
		}
	}
	messages = append(messages, msg)
	encoded, err := json.Marshal(messages)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE sessions SET messages=?, rev=rev+1, updated_at=? WHERE session_id=?`, string(encoded), time.Now().Unix(), id); err != nil {
		return 0, err
	}
	var rev int64
	if err := tx.QueryRow(`SELECT rev FROM sessions WHERE session_id=?`, id).Scan(&rev); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return rev, nil
}
