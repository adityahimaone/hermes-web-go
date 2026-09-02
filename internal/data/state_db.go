package data

import (
	"database/sql"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"hermes-web-go/internal/store"
)

// workspaceTagRE matches the authoritative [Workspace::v1: /abs/path] marker
// Hermes stamps on user messages.
var workspaceTagRE = regexp.MustCompile(`\[Workspace::v1:\s+([^\]]+)\]`)

// ImportStateDB imports sessions+messages from a Hermes state.db into the
// webui SQLite database. The state.db is opened read-only. Workspace is taken
// from the latest [Workspace::v1: ...] user-message tag, falling back to cwd.
func ImportStateDB(dst *sql.DB, src *sql.DB) (int, error) {
	cols := stateDBSessionCols(src)
	if !cols["id"] || !cols["started_at"] {
		return 0, nil // not a Hermes state.db sessions table
	}

	hasMsg := false
	if err := src.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='messages'`).Scan(new(string)); err == nil {
		hasMsg = true
	}

	var wsRE *regexp.Regexp
	if cols["cwd"] {
		wsRE = workspaceTagRE
	}

	rows, err := src.Query(stateDBSessionSelect(cols))
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	imported := 0
	var firstErr error
	for rows.Next() {
		var sid string
		var title, model, cwd, source sql.NullString
		var archived, pinned, msgCount sql.NullInt64
		var startedAt, lastActivity sql.NullFloat64
		fields := []any{&sid}
		if cols["title"] {
			fields = append(fields, &title)
		}
		if cols["source"] {
			fields = append(fields, &source)
		}
		if cols["model"] {
			fields = append(fields, &model)
		}
		if cols["started_at"] {
			fields = append(fields, &startedAt)
		}
		if cols["last_activity_at"] {
			fields = append(fields, &lastActivity)
		}
		if cols["cwd"] {
			fields = append(fields, &cwd)
		}
		if cols["archived"] {
			fields = append(fields, &archived)
		}
		if cols["pinned"] {
			fields = append(fields, &pinned)
		}
		if cols["message_count"] {
			fields = append(fields, &msgCount)
		}
		if err := rows.Scan(fields...); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if sid == "" {
			continue
		}

		workspace := cwd.String
		if wsRE != nil {
			if w := latestWorkspaceTag(src, sid, wsRE); w != "" {
				workspace = w
			}
		}

		messages, err := stateDBMessages(src, sid, hasMsg)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := store.ImportSession(dst, store.SessionImport{
			ID:        sid,
			Title:     title.String,
			Workspace: workspace,
			Model:     model.String,
			Messages:  messages,
			CreatedAt: epochF(startedAt.Float64),
			UpdatedAt: epochF(lastActivity.Float64),
			Pinned:    int(pinned.Int64),
			Archived:  int(archived.Int64),
		}); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		imported++
	}
	return imported, firstErr
}

// stateDBSessionCols returns which sessions columns exist in src.
func stateDBSessionCols(src *sql.DB) map[string]bool {
	out := map[string]bool{}
	rows, err := src.Query(`PRAGMA table_info(sessions)`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var cid, name, typ string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err == nil {
			out[name] = true
		}
	}
	return out
}

// stateDBSessionSelect builds the SELECT list from available columns.
func stateDBSessionSelect(cols map[string]bool) string {
	sel := []string{"id"}
	for _, c := range []string{"title", "source", "model", "started_at", "last_activity_at", "cwd", "archived", "pinned", "message_count"} {
		if cols[c] {
			sel = append(sel, c)
		}
	}
	return "SELECT " + strings.Join(sel, ", ") + " FROM sessions"
}

// latestWorkspaceTag scans user messages (api_content preferred) for the last
// [Workspace::v1: ...] marker and returns it.
func latestWorkspaceTag(src *sql.DB, sid string, re *regexp.Regexp) string {
	rows, err := src.Query(`SELECT COALESCE(api_content, content) FROM messages WHERE session_id=? AND role='user' AND (api_content IS NOT NULL OR content IS NOT NULL) ORDER BY timestamp ASC`, sid)
	if err != nil {
		return ""
	}
	defer rows.Close()
	last := ""
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			continue
		}
		if m := re.FindStringSubmatch(text); m != nil {
			last = m[1]
		}
	}
	return last
}

// stateDBMessages returns the session messages as a JSON array of
// {role, content, timestamp} objects.
func stateDBMessages(src *sql.DB, sid string, hasMsg bool) (string, error) {
	if !hasMsg {
		return "[]", nil
	}
	rows, err := src.Query(`SELECT role, COALESCE(api_content, content), timestamp FROM messages WHERE session_id=? ORDER BY timestamp ASC`, sid)
	if err != nil {
		return "[]", err
	}
	defer rows.Close()
	type msg struct {
		Role      string  `json:"role"`
		Content   string  `json:"content"`
		Timestamp float64 `json:"timestamp,omitempty"`
	}
	var out []msg
	for rows.Next() {
		var m msg
		var ts sql.NullFloat64
		if err := rows.Scan(&m.Role, &m.Content, &ts); err != nil {
			continue
		}
		m.Timestamp = ts.Float64
		out = append(out, m)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "[]", err
	}
	return string(b), nil
}

// epochF renders an epoch float as a plain string without scientific notation.
func epochF(f float64) string {
	if f == 0 {
		return ""
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}
