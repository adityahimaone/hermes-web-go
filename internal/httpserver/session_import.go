package httpserver

// Wave 14 — session/import + session/import_cli.
//
// POST /api/session/import — create a NEW session (new id) from a JSON export
//   {messages, tool_calls?, title?, workspace?, model?, pinned?}.
// POST /api/session/import_cli — import (or refresh) a CLI/agent session from
//   the profile's state.db into the WebUI store: new WebUI-owned row keyed by
//   the CLI session id; refresh extends messages only when the stored messages
//   are a prefix of the fresh transcript (WebUI-added messages never dropped).
//
// Both publish `session_import` / `session_import_cli` list-change events.

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

func newImportedSessionID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	now := time.Now()
	return now.Format("20060102_150405") + "_" + hex.EncodeToString(b)[:6]
}

// handleSessionImport mirrors _handle_session_import (Go store shape).
func handleSessionImport(db *sql.DB, body map[string]any) (int, map[string]any) {
	if body == nil {
		return 400, map[string]any{"error": "Request body must be a JSON object"}
	}
	rawMsgs, ok := body["messages"].([]any)
	if !ok {
		return 400, map[string]any{"error": `JSON must contain a "messages" array`}
	}
	rawToolCalls, _ := body["tool_calls"].([]any)
	title, _ := body["title"].(string)
	if title == "" {
		title = "Imported session"
	}
	workspace, _ := body["workspace"].(string)
	if workspace == "" {
		workspace = defaultWorkspaceForImport(db)
	}
	model, _ := body["model"].(string)
	if model == "" {
		model = defaultModelForImport()
	}
	pinned := false
	if v, ok := body["pinned"].(bool); ok {
		pinned = v
	}
	msgsJSON, _ := json.Marshal(rawMsgs)
	toolCallsJSON, _ := json.Marshal(rawToolCalls)
	sid := newImportedSessionID()
	now := time.Now().Unix()
	_, err := db.Exec(`INSERT INTO sessions (session_id, title, workspace, model, messages, tool_calls, created_at, updated_at, pinned, archived, rev, enabled_toolsets, active_stream_id, pending_user_message)
		VALUES (?,?,?,?,?,?,?,?,?,0,0,'','','')`,
		sid, title, workspace, model, string(msgsJSON), string(toolCallsJSON), now, now, boolToInt(pinned))
	if err != nil {
		return 500, map[string]any{"error": err.Error()}
	}
	publishSessionEvents("session_import", sid)
	return 200, map[string]any{
		"ok": true,
		"session": map[string]any{
			"session_id":    sid,
			"title":         title,
			"workspace":     workspace,
			"model":         model,
			"pinned":        pinned,
			"messages":      rawMsgs,
			"tool_calls":    rawToolCalls,
			"created_at":    now,
			"updated_at":    now,
			"archived":      false,
			"message_count": len(rawMsgs),
		},
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// defaultWorkspaceForImport — last workspace or home.
func defaultWorkspaceForImport(db *sql.DB) string {
	var ws string
	if err := db.QueryRow("SELECT workspace FROM sessions ORDER BY updated_at DESC LIMIT 1").Scan(&ws); err == nil && ws != "" {
		return ws
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "/"
	}
	return home
}

func defaultModelForImport() string {
	return "unknown"
}

// stateDBTranscriptWithTS returns full message rows (role/content/ts) for a
// CLI session from the profile's state.db. Reuses stateDBTranscript.
// (Stitching across continuations is a Python nicety; import stores the
// segment transcript — deviation documented.)

// handleSessionImportCLI mirrors _handle_session_import_cli core paths.
func handleSessionImportCLI(db *sql.DB, hermesHome string, body map[string]any) (int, map[string]any) {
	sid, _ := body["session_id"].(string)
	sid = strings.TrimSpace(sid)
	if sid == "" {
		return 400, map[string]any{"error": "session_id is required"}
	}
	msgs := stateDBTranscript(hermesHome, sid)
	if len(msgs) == 0 {
		return 404, map[string]any{"error": "Session not found in CLI store"}
	}
	// Build message objects with timestamps (role, content, timestamp)
	fresh := make([]any, 0, len(msgs))
	for _, m := range msgs {
		entry := map[string]any{"role": m.Role, "content": m.Content}
		if m.Timestamp > 0 {
			entry["timestamp"] = m.Timestamp
		}
		fresh = append(fresh, entry)
	}
	// title: from first user message
	title := "CLI Session"
	for _, m := range msgs {
		if m.Role == "user" {
			if t := summarizeSnippet(m.Content, 60); t != "" {
				title = t
			}
			break
		}
	}
	// session meta from state.db
	dbh, cols, err := stateDBSessions(hermesHome)
	if err == nil {
		defer dbh.Close()
		expr := func(name string) string {
			if cols[name] {
				return "COALESCE(" + name + ", '')"
			}
			return "''"
		}
		exprN := func(name string) string {
			if cols[name] {
				return "COALESCE(" + name + ", 0)"
			}
			return "0"
		}
		var cliTitle, model, source string
		var started float64
		q := fmt.Sprintf("SELECT %s, %s, %s, %s FROM sessions WHERE id = ?",
			expr("title"), expr("model"), expr("source"), exprN("started_at"))
		if dbh.QueryRow(q, sid).Scan(&cliTitle, &model, &source, &started) == nil && cliTitle != "" {
			title = cliTitle
		}
		if model == "" {
			model = "unknown"
		}
		// existing row → refresh path
		var existingJSON string
		err := db.QueryRow("SELECT messages FROM sessions WHERE session_id = ?", sid).Scan(&existingJSON)
		if err == nil {
			var existing []any
			_ = json.Unmarshal([]byte(existingJSON), &existing)
			changed := false
			if len(fresh) > len(existing) && isPrefixMatch(existing, fresh) {
				existing = fresh
				changed = true
			}
			if changed {
				out, _ := json.Marshal(existing)
				now := time.Now().Unix()
				_, _ = db.Exec("UPDATE sessions SET messages = ?, updated_at = ? WHERE session_id = ?", string(out), now, sid)
				publishSessionEvents("session_import_cli", sid)
			}
			return 200, map[string]any{
				"session": map[string]any{
					"session_id":     sid,
					"title":          title,
					"messages":       existing,
					"is_cli_session": true,
					"read_only":      false,
				},
				"imported": false,
			}
		}
	}
	// new import
	msgsJSON, _ := json.Marshal(fresh)
	now := time.Now().Unix()
	var workspace string
	if err := db.QueryRow("SELECT workspace FROM sessions ORDER BY updated_at DESC LIMIT 1").Scan(&workspace); err != nil || workspace == "" {
		workspace = defaultWorkspaceForImport(db)
	}
	_, err = db.Exec(`INSERT INTO sessions (session_id, title, workspace, model, messages, tool_calls, created_at, updated_at, pinned, archived, rev, enabled_toolsets, active_stream_id, pending_user_message)
		VALUES (?,?,?,?,?,'[]',?,?,0,0,0,'','','')`,
		sid, title, workspace, "unknown", string(msgsJSON), now, now)
	if err != nil {
		return 500, map[string]any{"error": err.Error()}
	}
	publishSessionEvents("session_import_cli", sid)
	return 200, map[string]any{
		"session": map[string]any{
			"session_id":     sid,
			"title":          title,
			"workspace":      workspace,
			"model":          "unknown",
			"messages":       fresh,
			"is_cli_session": true,
			"read_only":      false,
		},
		"imported": true,
	}
}

// isPrefixMatch — existing is a prefix of fresh (length-aware shallow compare
// on role+content; matches _is_messages_refresh_prefix_match intent).
func isPrefixMatch(existing, fresh []any) bool {
	if len(existing) > len(fresh) {
		return false
	}
	for i, e := range existing {
		em, ok1 := e.(map[string]any)
		fm, ok2 := fresh[i].(map[string]any)
		if !ok1 || !ok2 {
			return false
		}
		if asStr(em["role"]) != asStr(fm["role"]) || extractMsgText(em["content"]) != extractMsgText(fm["content"]) {
			return false
		}
	}
	return true
}

// ── router ─────────────────────────────────────────────────────────────────

// Wave14Router serves the import family.
func Wave14Router(r chi.Router, db *sql.DB, hermesHome string) {
	r.Post("/api/session/import", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			wave4WriteJSONErr(w, 400, "invalid request body")
			return
		}
		code, payload := handleSessionImport(db, body)
		wave4WriteJSON(w, code, payload)
	})
	r.Post("/api/session/import_cli", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			wave4WriteJSONErr(w, 400, "invalid request body")
			return
		}
		code, payload := handleSessionImportCLI(db, hermesHome, body)
		wave4WriteJSON(w, code, payload)
	})
}
