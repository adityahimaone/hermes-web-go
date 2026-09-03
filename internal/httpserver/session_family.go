package httpserver

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"hermes-web-go/internal/store"
)

// maxDraftText / maxDraftFiles mirror Python's Stage-326 draft caps.
const (
	maxDraftText  = 50_000
	maxDraftFiles = 50
)

// maxPinnedSessions mirrors Python's default pinned_sessions_limit.
const maxPinnedSessions = 3

// SessionFamilyRouter mounts the Family-1 session lifecycle routes that are
// pure DB projections: status, usage, pin, archive, move, toolsets, draft,
// truncate, clear. reg (may be nil) supplies live agent_running for /status.
func SessionFamilyRouter(r chi.Router, db *sql.DB, reg streamRegistry) {
	// GET /api/session/status
	r.Get("/api/session/status", func(w http.ResponseWriter, req *http.Request) {
		sid := req.URL.Query().Get("session_id")
		if sid == "" {
			writeError(w, http.StatusBadRequest, "Missing session_id")
			return
		}
		row, err := store.GetSession(db, sid)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusNotFound, "Session not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to load session")
			return
		}
		// Python session_status(): agent_running is per-session (active_stream_id
		// set); the Go projection has no stream column, so we approximate with
		// the global registry liveness signal. active_stream_id stays nil until
		// the store learns a per-session stream id.
		profile := "default"
		writeJSON(w, map[string]any{
			"session_id":       row.ID,
			"title":            row.Title,
			"model":            row.Model,
			"profile":          profile,
			"hermes_home":      "",
			"workspace":        row.Workspace,
			"personality":      nil,
			"message_count":    messageCountsOnly(row.Messages),
			"created_at":       row.CreatedAt,
			"updated_at":       row.UpdatedAt,
			"agent_running":    reg != nil && reg.Len() > 0,
			"active_stream_id": nil,
			"input_tokens":     0,
			"output_tokens":    0,
			"total_tokens":     0,
			"estimated_cost":   0,
		})
	})

	// GET /api/session/usage
	r.Get("/api/session/usage", func(w http.ResponseWriter, req *http.Request) {
		sid := req.URL.Query().Get("session_id")
		if sid == "" {
			writeError(w, http.StatusBadRequest, "Missing session_id")
			return
		}
		row, err := store.GetSession(db, sid)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusNotFound, "Session not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to load session")
			return
		}
		// Python reports per-session token counters. The Go projection does not
		// import them (legacy JSON rows only carry the message array), so the
		// counters are zero until the importer learns the token fields.
		writeJSON(w, map[string]any{
			"input_tokens":   0,
			"output_tokens":  0,
			"total_tokens":   0,
			"estimated_cost": 0,
			"model":          row.Model,
		})
	})

	// POST /api/session/pin
	r.Post("/api/session/pin", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			SessionID string `json:"session_id"`
			Pinned    *bool  `json:"pinned"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.SessionID == "" {
			writeError(w, http.StatusBadRequest, "session_id is required")
			return
		}
		if _, err := store.GetSession(db, body.SessionID); err != nil {
			writeError(w, http.StatusNotFound, "Session not found")
			return
		}
		pinned := true
		if body.Pinned != nil {
			pinned = *body.Pinned
		}
		if pinned {
			count, err := store.CountPinnedSessions(db)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to count pinned sessions")
				return
			}
			if count >= maxPinnedSessions {
				writeError(w, http.StatusBadRequest, "Up to "+strconv.Itoa(maxPinnedSessions)+" sessions can be pinned. Unpin one before pinning another.")
				return
			}
		}
		v := 0
		if pinned {
			v = 1
		}
		if err := store.SetSessionFlag(db, body.SessionID, "pinned", v); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to pin session")
			return
		}
		row, _ := store.GetSession(db, body.SessionID)
		writeJSON(w, map[string]any{"ok": true, "session": sessionCompact(row)})
	})

	// POST /api/session/archive
	r.Post("/api/session/archive", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			SessionID string `json:"session_id"`
			Archived  *bool  `json:"archived"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.SessionID == "" {
			writeError(w, http.StatusBadRequest, "session_id is required")
			return
		}
		if _, err := store.GetSession(db, body.SessionID); err != nil {
			writeError(w, http.StatusNotFound, "Session not found")
			return
		}
		archived := true
		if body.Archived != nil {
			archived = *body.Archived
		}
		v := 0
		if archived {
			v = 1
		}
		if err := store.SetSessionFlag(db, body.SessionID, "archived", v); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to archive session")
			return
		}
		row, _ := store.GetSession(db, body.SessionID)
		writeJSON(w, map[string]any{"ok": true, "session": sessionCompact(row)})
	})

	// POST /api/session/move
	r.Post("/api/session/move", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			SessionID string `json:"session_id"`
			ProjectID string `json:"project_id"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.SessionID == "" {
			writeError(w, http.StatusBadRequest, "session_id is required")
			return
		}
		if _, err := store.GetSession(db, body.SessionID); err != nil {
			writeError(w, http.StatusNotFound, "Session not found")
			return
		}
		// Move to no project is a valid unassign; a non-empty target must exist.
		if body.ProjectID != "" {
			exists, err := store.ProjectExists(db, body.ProjectID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to check project")
				return
			}
			if !exists {
				writeError(w, http.StatusNotFound, "Project not found")
				return
			}
		}
		if err := store.SetSessionProject(db, body.SessionID, body.ProjectID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to move session")
			return
		}
		row, _ := store.GetSession(db, body.SessionID)
		writeJSON(w, map[string]any{"ok": true, "session": sessionCompact(row)})
	})

	// POST /api/session/toolsets
	r.Post("/api/session/toolsets", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			SessionID string          `json:"session_id"`
			Toolsets  json.RawMessage `json:"toolsets"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.SessionID == "" {
			writeError(w, http.StatusBadRequest, "session_id is required")
			return
		}
		if _, err := store.GetSession(db, body.SessionID); err != nil {
			writeError(w, http.StatusNotFound, "Session not found")
			return
		}
		// toolsets: null clears the override; otherwise must be a non-empty
		// array of non-empty strings (mirrors _validate_session_toolsets_shape).
		if len(body.Toolsets) > 0 && string(body.Toolsets) != "null" {
			var arr []string
			if err := json.Unmarshal(body.Toolsets, &arr); err != nil || len(arr) == 0 {
				writeError(w, http.StatusBadRequest, "toolsets must be a non-empty list of strings or null")
				return
			}
			for _, t := range arr {
				if t == "" {
					writeError(w, http.StatusBadRequest, "toolsets must be a non-empty list of strings or null")
					return
				}
			}
		}
		var raw []byte
		if len(body.Toolsets) > 0 && string(body.Toolsets) != "null" {
			raw = body.Toolsets
		}
		if err := store.SetSessionToolsets(db, body.SessionID, raw); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to set toolsets")
			return
		}
		row, _ := store.GetSession(db, body.SessionID)
		writeJSON(w, map[string]any{"ok": true, "enabled_toolsets": jsonOrNull(row.EnabledToolsets)})
	})

	// GET /api/session/draft
	r.Get("/api/session/draft", func(w http.ResponseWriter, req *http.Request) {
		sid := req.URL.Query().Get("session_id")
		if sid == "" {
			writeError(w, http.StatusBadRequest, "session_id is required")
			return
		}
		row, err := store.GetSession(db, sid)
		if err != nil {
			writeError(w, http.StatusNotFound, "Session not found")
			return
		}
		writeJSON(w, map[string]any{"draft": jsonOrEmptyObject(row.ComposerDraft)})
	})

	// POST /api/session/draft
	r.Post("/api/session/draft", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			SessionID string          `json:"session_id"`
			Text      *string         `json:"text"`
			Files     json.RawMessage `json:"files"`
		}
		if err := json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&body); err != nil || body.SessionID == "" {
			writeError(w, http.StatusBadRequest, "session_id is required")
			return
		}
		row, err := store.GetSession(db, body.SessionID)
		if err != nil {
			writeError(w, http.StatusNotFound, "Session not found")
			return
		}
		current := jsonOrEmptyObject(row.ComposerDraft)
		next := map[string]any{}
		if m, ok := current.(map[string]any); ok {
			for k, v := range m {
				next[k] = v
			}
		}
		unchanged := true
		if body.Text != nil {
			text := *body.Text
			if len(text) > maxDraftText {
				text = text[:maxDraftText]
			}
			if prev, _ := next["text"].(string); prev != text {
				next["text"] = text
				unchanged = false
			}
		}
		if len(body.Files) > 0 && string(body.Files) != "null" {
			var files []any
			if json.Unmarshal(body.Files, &files) == nil {
				if len(files) > maxDraftFiles {
					files = files[:maxDraftFiles]
				}
				prev := []any{}
				if p, ok := next["files"].([]any); ok {
					prev = p
				}
				if !jsonEqual(prev, files) {
					next["files"] = files
					unchanged = false
				}
			}
		}
		if unchanged {
			writeJSON(w, map[string]any{"ok": true, "draft": next, "unchanged": true})
			return
		}
		raw, _ := json.Marshal(next)
		if err := store.SetSessionDraft(db, body.SessionID, raw); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save draft")
			return
		}
		writeJSON(w, map[string]any{"ok": true, "draft": next})
	})

	// POST /api/session/truncate
	r.Post("/api/session/truncate", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			SessionID string `json:"session_id"`
			KeepCount *int   `json:"keep_count"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.SessionID == "" {
			writeError(w, http.StatusBadRequest, "session_id is required")
			return
		}
		if body.KeepCount == nil {
			writeError(w, http.StatusBadRequest, "Missing required field(s): keep_count")
			return
		}
		if *body.KeepCount < 0 {
			writeError(w, http.StatusBadRequest, "keep_count must be non-negative")
			return
		}
		if _, err := store.GetSession(db, body.SessionID); err != nil {
			writeError(w, http.StatusNotFound, "Session not found")
			return
		}
		if err := store.TruncateMessages(db, body.SessionID, *body.KeepCount); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to truncate session")
			return
		}
		row, _ := store.GetSession(db, body.SessionID)
		writeJSON(w, map[string]any{"ok": true, "session": sessionFull(row)})
	})

	// POST /api/session/duplicate
	r.Post("/api/session/duplicate", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			SessionID string `json:"session_id"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.SessionID == "" {
			writeError(w, http.StatusBadRequest, "session_id is required")
			return
		}
		row, err := store.GetSession(db, body.SessionID)
		if err != nil {
			writeError(w, http.StatusNotFound, "Session not found")
			return
		}
		// Minimal duplicate (Opsi A): carry the fields the Go projection already
		// has — title+" (copy)", workspace, model, full messages, project_id,
		// enabled_toolsets, composer_draft. Pinned/archived reset to false,
		// created/updated stamp now (Python duplicate semantics, routes.py:15449).
		// Deliberately NOT carried: tokens, personality, context engine state,
		// compression anchors, gateway routing — the Go store has no columns for
		// them and the frontend re-derives context on the next turn.
		title := row.Title
		if title == "" {
			title = "Untitled"
		}
		now := strconv.FormatInt(time.Now().Unix(), 10)
		dupRow := store.SessionImport{
			ID:              newSessionID(),
			Title:           title + " (copy)",
			Workspace:       row.Workspace,
			Model:           row.Model,
			Messages:        row.Messages,
			CreatedAt:       now,
			UpdatedAt:       now,
			ProjectID:       row.ProjectID,
			EnabledToolsets: row.EnabledToolsets,
			ComposerDraft:   row.ComposerDraft,
		}
		if err := store.ImportSession(db, dupRow); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to duplicate session")
			return
		}
		newRow, _ := store.GetSession(db, dupRow.ID)
		writeJSON(w, map[string]any{"session": sessionFull(newRow)})
	})

	// POST /api/session/clear
	r.Post("/api/session/clear", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			SessionID string `json:"session_id"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.SessionID == "" {
			writeError(w, http.StatusBadRequest, "session_id is required")
			return
		}
		if _, err := store.GetSession(db, body.SessionID); err != nil {
			writeError(w, http.StatusNotFound, "Session not found")
			return
		}
		if err := store.ClearSession(db, body.SessionID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to clear session")
			return
		}
		row, _ := store.GetSession(db, body.SessionID)
		writeJSON(w, map[string]any{"ok": true, "session": sessionCompact(row)})
	})
}

// streamRegistry is the minimal live-stream surface /status needs.
type streamRegistry interface {
	Len() int
}

// sessionCompact mirrors Python Session.compact() minus messages: the shape
// pin/archive/move/clear return.
func sessionCompact(row store.SessionRow) map[string]any {
	s := sessionResponse(row)
	delete(s, "messages")
	return s
}

// sessionFull mirrors Python compact() | {messages}.
func sessionFull(row store.SessionRow) map[string]any {
	return sessionResponse(row)
}

// messageCountsOnly returns the total message count only (used by /status).
func messageCountsOnly(messages string) int {
	total, _ := messageCounts(messages)
	return total
}

// jsonOrNull returns the raw JSON if valid, else nil.
func jsonOrNull(raw string) any {
	if raw == "" {
		return nil
	}
	var v any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return nil
	}
	return v
}

// jsonOrEmptyObject returns the parsed draft object, or an empty map.
func jsonOrEmptyObject(raw string) any {
	if v := jsonOrNull(raw); v != nil {
		return v
	}
	return map[string]any{}
}

func jsonEqual(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	ra, _ := json.Marshal(a)
	rb, _ := json.Marshal(b)
	return string(ra) == string(rb)
}
