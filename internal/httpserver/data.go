package httpserver

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"

	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"hermes-web-go/internal/store"
	"hermes-web-go/internal/workspace"
)

// maxFileBytes mirrors the Python read cap for file contents served inline.
const maxFileBytes = 5 * 1024 * 1024

// DataRouter wires the read-only data routes onto the given router. db is the
// SQLite session store; dataRoot is the Hermes home data directory used to
// resolve workspaces.json. When db is nil the data routes return 503 so the
// server can boot without a database (proxy-only mode).
func DataRouter(r chi.Router, db *sql.DB, dataRoot string) {
	if db == nil {
		r.Get("/api/session", func(w http.ResponseWriter, req *http.Request) { dataUnavailable(w) })
		r.Get("/api/sessions", func(w http.ResponseWriter, req *http.Request) { dataUnavailable(w) })
		r.Get("/api/sessions/search", func(w http.ResponseWriter, req *http.Request) { dataUnavailable(w) })
		r.Get("/api/list", func(w http.ResponseWriter, req *http.Request) { dataUnavailable(w) })
		r.Get("/api/file", func(w http.ResponseWriter, req *http.Request) { dataUnavailable(w) })
		r.Get("/api/file/raw", func(w http.ResponseWriter, req *http.Request) { dataUnavailable(w) })
		r.Get("/api/workspaces", func(w http.ResponseWriter, req *http.Request) { dataUnavailable(w) })
		return
	}

	r.Get("/api/sessions", func(w http.ResponseWriter, req *http.Request) {
		limit, offset := pageParams(req)
		rows, err := store.ListSessions(db, limit, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list sessions")
			return
		}
		count, err := store.CountSessions(db)
		if err != nil {
			count = len(rows)
		}
		items := make([]any, 0, len(rows))
		for _, row := range rows {
			items = append(items, sessionListItem(row))
		}
		writeJSON(w, map[string]any{
			"sessions":                   items,
			"sidebar_reference_sessions": []any{},
			"cli_count":                  0,
			"archived_count":             0,
			"archived_webui_count":       0,
			"archived_cli_count":         0,
			"include_archived":           false,
			"all_profiles":               false,
			"active_profile":             "default",
			"other_profile_count":        0,
			"webui_session_count":        count,
			"cli_session_count":          0,
			"server_time":                float64(time.Now().Unix()),
			"server_tz":                  time.Now().Format("MST"),
		})
	})

	r.Get("/api/session", func(w http.ResponseWriter, req *http.Request) {
		sid := req.URL.Query().Get("session_id")
		if sid == "" {
			writeError(w, http.StatusBadRequest, "session_id is required")
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
		messages := row.Messages
		if req.URL.Query().Get("messages") == "0" {
			messages = "[]"
		}
		writeJSON(w, map[string]any{
			"session_id": row.ID,
			"title":      row.Title,
			"workspace":  row.Workspace,
			"model":      row.Model,
			"created_at": row.CreatedAt,
			"updated_at": row.UpdatedAt,
			"pinned":     row.Pinned,
			"archived":   row.Archived,
			"project_id": row.ProjectID,
			"messages":   json.RawMessage(messages),
		})
	})

	r.Get("/api/sessions/search", func(w http.ResponseWriter, req *http.Request) {
		q := strings.ToLower(strings.TrimSpace(req.URL.Query().Get("q")))
		contentSearch := req.URL.Query().Get("content") != "0"
		limit, _ := pageParams(req)
		rows, err := store.ListSessions(db, limit, 0)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to search sessions")
			return
		}
		var results []map[string]any
		for _, row := range rows {
			if q == "" {
				results = append(results, sessionSearchItem(row, "", ""))
				continue
			}
			if strings.Contains(strings.ToLower(row.Title), q) {
				results = append(results, sessionSearchItem(row, "title", ""))
				continue
			}
			if contentSearch && strings.Contains(strings.ToLower(row.Messages), q) {
				preview := searchPreview(row.Messages, q)
				results = append(results, sessionSearchItem(row, "content", preview))
			}
		}
		writeJSON(w, map[string]any{
			"sessions": results,
			"query":    q,
			"count":    len(results),
		})
	})

	r.Get("/api/list", func(w http.ResponseWriter, req *http.Request) {
		sid := req.URL.Query().Get("session_id")
		if sid == "" {
			writeError(w, http.StatusBadRequest, "session_id is required")
			return
		}
		wsRoot, err := workspaceRootForSession(db, sid)
		if err != nil {
			writeError(w, http.StatusNotFound, "Session not found")
			return
		}
		rel := req.URL.Query().Get("path")
		if rel == "" {
			rel = "."
		}
		entries, err := workspace.ListDir(wsRoot, rel)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, map[string]any{
			"entries":             entries,
			"signature":           dirSignature(entries),
			"path":                rel,
			"workspace":           wsRoot,
			"workspace_recovered": false,
		})
	})

	r.Get("/api/file", func(w http.ResponseWriter, req *http.Request) {
		sid := req.URL.Query().Get("session_id")
		rel := req.URL.Query().Get("path")
		if sid == "" || rel == "" {
			writeError(w, http.StatusBadRequest, "session_id and path are required")
			return
		}
		wsRoot, err := workspaceRootForSession(db, sid)
		if err != nil {
			writeError(w, http.StatusNotFound, "Session not found")
			return
		}
		b, err := workspace.ReadFile(wsRoot, rel, maxFileBytes)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		content := string(b)
		writeJSON(w, map[string]any{
			"path":    rel,
			"content": content,
			"size":    len(b),
			"lines":   strings.Count(content, "\n") + 1,
		})
	})

	r.Get("/api/file/raw", func(w http.ResponseWriter, req *http.Request) {
		sid := req.URL.Query().Get("session_id")
		rel := req.URL.Query().Get("path")
		if sid == "" || rel == "" {
			writeError(w, http.StatusBadRequest, "session_id and path are required")
			return
		}
		wsRoot, err := workspaceRootForSession(db, sid)
		if err != nil {
			writeError(w, http.StatusNotFound, "Session not found")
			return
		}
		b, err := workspace.ReadFile(wsRoot, rel, maxFileBytes)
		if err != nil {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		ext := strings.ToLower(filepath.Ext(rel))
		ct := mime.TypeByExtension(ext)
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Disposition", "inline")
		_, _ = w.Write(b)
	})

	r.Get("/api/workspaces", func(w http.ResponseWriter, req *http.Request) {
		ws := readWorkspaces(dataRoot)
		last := ""
		if len(ws) > 0 {
			if p, ok := ws[0]["path"].(string); ok {
				last = p
			}
		}
		writeJSON(w, map[string]any{
			"workspaces": ws,
			"last":       last,
		})
	})
}

// workspaceRootForSession resolves the session's workspace to a root path that
// file operations anchor to. It is best-effort: if the stored workspace is
// empty we use the home directory.
func workspaceRootForSession(db *sql.DB, sid string) (string, error) {
	row, err := store.GetSession(db, sid)
	if err != nil {
		return "", err
	}
	ws := row.Workspace
	if ws == "" {
		home, herr := workspace.HomeDir()
		if herr != nil {
			return "", herr
		}
		ws = home
	}
	return ws, nil
}

func pageParams(req *http.Request) (limit, offset int) {
	limit, _ = strconv.Atoi(req.URL.Query().Get("limit"))
	offset, _ = strconv.Atoi(req.URL.Query().Get("offset"))
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func sessionListItem(row store.SessionRow) map[string]any {
	return map[string]any{
		"session_id":    row.ID,
		"title":         row.Title,
		"workspace":     row.Workspace,
		"model":         row.Model,
		"created_at":    row.CreatedAt,
		"updated_at":    row.UpdatedAt,
		"pinned":        row.Pinned,
		"archived":      row.Archived,
		"project_id":    row.ProjectID,
		"message_count": messageCount(row.Messages),
	}
}

func sessionSearchItem(row store.SessionRow, matchType, preview string) map[string]any {
	item := sessionListItem(row)
	if matchType != "" {
		item["match_type"] = matchType
	}
	if preview != "" {
		item["match_preview"] = preview
	}
	return item
}

func messageCount(messages string) int {
	return strings.Count(messages, `"role"`)
}

func searchPreview(messages, q string) string {
	idx := strings.Index(strings.ToLower(messages), q)
	if idx < 0 {
		return ""
	}
	start := idx - 40
	if start < 0 {
		start = 0
	}
	end := idx + len(q) + 60
	if end > len(messages) {
		end = len(messages)
	}
	return messages[start:end]
}

// readWorkspaces loads workspace paths from workspaces.json. It returns the
// parsed list or an empty slice when the file is absent.
func readWorkspaces(dataRoot string) []map[string]any {
	raw, err := workspace.ReadWorkspacesJSON(dataRoot)
	if err != nil {
		return []map[string]any{}
	}
	var ws []map[string]any
	if err := json.Unmarshal(raw, &ws); err != nil {
		return []map[string]any{}
	}
	return ws
}

// dirSignature is a cheap stable signature over the listed entries, mirroring
// the Python dir_signature semantics (content-free).
func dirSignature(entries []workspace.Entry) string {
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "%s|%s|%s|", e.Name, e.Type, e.Path)
	}
	// Note: the Python implementation hashes this; returning the joined string
	// directly is a cheap deterministic proxy the FE only uses as a change key.
	return b.String()
}

func dataUnavailable(w http.ResponseWriter) {
	writeError(w, http.StatusServiceUnavailable, "database unavailable")
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
