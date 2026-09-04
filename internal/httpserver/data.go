package httpserver

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"

	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"hermes-web-go/internal/store"
	"hermes-web-go/internal/workspace"
)

// maxFileBytes mirrors the Python read cap for file contents served inline.
// Python api/config.py: MAX_FILE_BYTES = 400_000.
const maxFileBytes = 400_000

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

	r.Post("/api/session/new", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			SessionID string `json:"session_id"`
			Title     string `json:"title"`
			Workspace string `json:"workspace"`
			Model     string `json:"model"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if body.SessionID == "" {
			body.SessionID = newSessionID()
		}
		now := strconv.FormatInt(time.Now().Unix(), 10)
		if err := store.CreateSession(db, store.SessionImport{ID: body.SessionID, Title: body.Title, Workspace: body.Workspace, Model: body.Model, Messages: "[]", CreatedAt: now, UpdatedAt: now}); err != nil {
			writeError(w, http.StatusConflict, "session already exists")
			return
		}
		row, _ := store.GetSession(db, body.SessionID)
		writeJSON(w, map[string]any{"session": sessionResponse(row)})
	})

	r.Post("/api/session/rename", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			SessionID string `json:"session_id"`
			Title     string `json:"title"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.SessionID == "" {
			writeError(w, http.StatusBadRequest, "session_id and title are required")
			return
		}
		if _, err := store.GetSession(db, body.SessionID); errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "Session not found")
			return
		}
		if err := store.RenameSession(db, body.SessionID, body.Title); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to rename session")
			return
		}
		row, _ := store.GetSession(db, body.SessionID)
		writeJSON(w, map[string]any{"session": sessionResponse(row)})
	})

	r.Post("/api/session/update", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		var id string
		_ = json.Unmarshal(body["session_id"], &id)
		if id == "" {
			writeError(w, http.StatusBadRequest, "session_id is required")
			return
		}
		if _, err := store.GetSession(db, id); errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "Session not found")
			return
		}
		u := store.SessionUpdate{}
		if r := body["workspace"]; r != nil {
			var ws string
			_ = json.Unmarshal(r, &ws)
			u.Workspace = &ws
		}
		if r := body["model"]; r != nil {
			var model string
			_ = json.Unmarshal(r, &model)
			u.Model = &model
		}
		if r := body["pinned"]; r != nil {
			var pinned int
			if b := string(r); b == "true" {
				pinned = 1
			} else if b == "false" {
				pinned = 0
			} else {
				_ = json.Unmarshal(r, &pinned)
			}
			u.Pinned = &pinned
		}
		if r := body["archived"]; r != nil {
			var archived int
			if b := string(r); b == "true" {
				archived = 1
			} else if b == "false" {
				archived = 0
			} else {
				_ = json.Unmarshal(r, &archived)
			}
			u.Archived = &archived
		}
		if err := store.UpdateSession(db, id, u); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update session")
			return
		}
		row, _ := store.GetSession(db, id)
		writeJSON(w, map[string]any{"session": sessionResponse(row)})
	})

	r.Post("/api/session/delete", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			SessionID string `json:"session_id"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.SessionID == "" {
			writeError(w, http.StatusBadRequest, "session_id is required")
			return
		}
		if err := store.DeleteSession(db, body.SessionID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusInternalServerError, "failed to delete session")
			return
		}
		writeJSON(w, map[string]any{"ok": true, "session_id": body.SessionID})
	})

	r.Post("/api/workspaces/add", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Path string `json:"path"`
			Name string `json:"name"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Path == "" {
			writeError(w, http.StatusBadRequest, "path is required")
			return
		}
		info, err := os.Stat(body.Path)
		if err != nil {
			writeError(w, http.StatusBadRequest, "path does not exist")
			return
		}
		if !info.IsDir() {
			writeError(w, http.StatusBadRequest, "path is not a directory")
			return
		}
		cleaned := filepath.Clean(body.Path)
		if cleaned == string(filepath.Separator) {
			writeError(w, http.StatusBadRequest, "cannot add system root")
			return
		}
		if body.Name == "" {
			body.Name = filepath.Base(cleaned)
		}
		if err := store.AddWorkspace(db, cleaned, body.Name); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to add workspace")
			return
		}
		// Python returns the complete updated workspace list.
		writeJSON(w, map[string]any{"ok": true, "workspaces": workspaceList(db)})
	})

	r.Post("/api/workspaces/remove", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Path == "" {
			writeError(w, http.StatusBadRequest, "path is required")
			return
		}
		if err := store.RemoveWorkspace(db, filepath.Clean(body.Path)); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to remove workspace")
			return
		}
		writeJSON(w, map[string]any{"ok": true, "workspaces": workspaceList(db)})
	})

	r.Post("/api/workspaces/rename", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Path string `json:"path"`
			Name string `json:"name"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Path == "" || body.Name == "" {
			writeError(w, http.StatusBadRequest, "path and name are required")
			return
		}
		if err := store.RenameWorkspace(db, filepath.Clean(body.Path), body.Name); errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "Workspace not found")
			return
		} else if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to rename workspace")
			return
		}
		writeJSON(w, map[string]any{"ok": true, "workspaces": workspaceList(db)})
	})

	r.Post("/api/file/save", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			SessionID string `json:"session_id"`
			Path      string `json:"path"`
			Content   string `json:"content"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.SessionID == "" || body.Path == "" {
			writeError(w, http.StatusBadRequest, "session_id and path are required")
			return
		}
		root, err := workspaceRootForSession(db, body.SessionID)
		if err != nil {
			writeError(w, http.StatusNotFound, "Session not found")
			return
		}
		if err := workspace.SaveFile(root, body.Path, []byte(body.Content)); err != nil {
			if errors.Is(err, workspace.ErrOutsideRoot) {
				writeError(w, http.StatusBadRequest, "path escapes workspace")
				return
			}
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "File not found")
				return
			}
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(body.Path)))
		if err == nil {
			// Python returns {ok, path, size}; size enables the FE editor/tree
			// to refresh the byte count without a second read.
			writeJSON(w, map[string]any{"ok": true, "path": body.Path, "size": info.Size()})
			return
		}
		writeJSON(w, map[string]any{"ok": true, "path": body.Path})
	})

	r.Post("/api/file/create", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			SessionID string `json:"session_id"`
			Path      string `json:"path"`
			Content   string `json:"content"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.SessionID == "" || body.Path == "" {
			writeError(w, http.StatusBadRequest, "session_id and path are required")
			return
		}
		root, err := workspaceRootForSession(db, body.SessionID)
		if err != nil {
			writeError(w, http.StatusNotFound, "Session not found")
			return
		}
		if err := workspace.CreateFile(root, body.Path, []byte(body.Content)); err != nil {
			if errors.Is(err, workspace.ErrOutsideRoot) {
				writeError(w, http.StatusBadRequest, "path escapes workspace")
				return
			}
			if os.IsExist(err) {
				writeError(w, http.StatusConflict, "File already exists")
				return
			}
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true, "path": body.Path})
	})

	r.Post("/api/file/delete", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			SessionID string `json:"session_id"`
			Path      string `json:"path"`
			Recursive bool   `json:"recursive"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.SessionID == "" || body.Path == "" {
			writeError(w, http.StatusBadRequest, "session_id and path are required")
			return
		}
		root, err := workspaceRootForSession(db, body.SessionID)
		if err != nil {
			writeError(w, http.StatusNotFound, "Session not found")
			return
		}
		if body.Recursive {
			if err := workspace.DeleteRecursive(root, body.Path); err != nil {
				if errors.Is(err, workspace.ErrOutsideRoot) {
					writeError(w, http.StatusBadRequest, "path escapes workspace")
					return
				}
				if os.IsNotExist(err) {
					writeError(w, http.StatusNotFound, "File not found")
					return
				}
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, map[string]any{"ok": true, "path": body.Path})
			return
		}
		if err := workspace.DeleteFile(root, body.Path); err != nil {
			if errors.Is(err, workspace.ErrOutsideRoot) {
				writeError(w, http.StatusBadRequest, "path escapes workspace")
				return
			}
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "File not found")
				return
			}
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true, "path": body.Path})
	})

	r.Post("/api/upload", func(w http.ResponseWriter, req *http.Request) {
		req.Body = http.MaxBytesReader(w, req.Body, uploadMaxBytes())
		if err := req.ParseMultipartForm(uploadMaxBytes()); err != nil {
			writeError(w, http.StatusBadRequest, "invalid multipart body")
			return
		}
		sid := req.FormValue("session_id")
		if sid == "" {
			writeError(w, http.StatusBadRequest, "session_id is required")
			return
		}
		root, err := workspaceRootForSession(db, sid)
		if err != nil {
			writeError(w, http.StatusNotFound, "Session not found")
			return
		}
		file, header, err := req.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "file is required")
			return
		}
		defer file.Close()
		name := sanitizeFilename(header.Filename)
		target, err := workspace.SafeResolveNonNull(root, name)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid filename")
			return
		}
		if info, statErr := os.Lstat(target); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			writeError(w, http.StatusBadRequest, "cannot upload to symlinked entry")
			return
		}
		dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		defer dst.Close()
		n, err := io.Copy(dst, file)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to write upload")
			return
		}
		writeJSON(w, map[string]any{"filename": name, "path": name, "size": n})
	})

	r.Get("/api/session/export", func(w http.ResponseWriter, req *http.Request) {
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
		title := row.Title
		if strings.HasPrefix(title, "Reply ") {
			title = title[len("Reply "):]
		}
		w.Header().Set("Content-Disposition", "attachment; filename=\"hermes-"+row.ID+".json\"")
		w.Header().Set("Content-Type", "application/json")
		total, user := messageCounts(row.Messages)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"session_id":         row.ID,
			"title":              title,
			"workspace":          row.Workspace,
			"model":              row.Model,
			"created_at":         row.CreatedAt,
			"updated_at":         row.UpdatedAt,
			"pinned":             row.Pinned,
			"archived":           row.Archived,
			"project_id":         row.ProjectID,
			"rev":                row.Rev,
			"message_count":      total,
			"user_message_count": user,
			"messages":           json.RawMessage(row.Messages),
		})
	})

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
		title := row.Title
		if strings.HasPrefix(title, "Reply ") {
			title = title[len("Reply "):]
		}
		messages := row.Messages
		if req.URL.Query().Get("messages") == "0" {
			messages = "[]"
		}
		sess := sessionResponse(row)
		if req.URL.Query().Get("messages") == "0" {
			sess["messages"] = json.RawMessage(messages)
		}
		writeJSON(w, map[string]any{"session": sess})
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
		// Python: ?download=1 forces attachment; dangerous MIME types always
		// force attachment (XSS guard) unless ?inline=1 is an HTML preview.
		forceDownload := req.URL.Query().Get("download") == "1"
		inlinePreview := req.URL.Query().Get("inline") == "1"
		dangerous := ct == "text/html" || ct == "application/xhtml+xml" || ct == "image/svg+xml"
		htmlInlineOK := inlinePreview && ct == "text/html"
		disposition := "inline"
		if forceDownload || (dangerous && !htmlInlineOK) {
			disposition = "attachment"
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Disposition", disposition)
		if htmlInlineOK {
			// Sandboxed preview: opaque origin, cannot read cookies/storage.
			w.Header().Set("Content-Security-Policy", "sandbox allow-scripts allow-popups allow-popups-to-escape-sandbox")
		}
		_, _ = w.Write(b)
	})

	r.Get("/api/workspaces", func(w http.ResponseWriter, req *http.Request) {
		rows, err := store.ListWorkspaces(db)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list workspaces")
			return
		}
		ws := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			ws = append(ws, map[string]any{"path": row.Path, "name": row.Name})
		}
		// Python returns the persisted last_workspace (from last_workspace.txt,
		// falling back to the first list entry), plus the terminal backend flag.
		writeJSON(w, map[string]any{
			"workspaces":              ws,
			"last":                    lastWorkspace(dataRoot, ws),
			"terminal_remote_backend": terminalRemoteBackend(),
		})
	})

	// POST /api/sessions/cleanup (and _zero_message): remove empty sessions.
	// Mirrors Python _handle_sessions_cleanup: rows that are Untitled + zero
	// messages (or any zero-message row for the zero_message variant) are
	// deleted, along with their backing session files under dataRoot/sessions
	// when present (so a server restart + import cannot resurrect them).
	r.Post("/api/sessions/cleanup", func(w http.ResponseWriter, req *http.Request) {
		cleaned, err := cleanupSessions(db, dataRoot, false)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to clean sessions")
			return
		}
		writeJSON(w, map[string]any{"ok": true, "cleaned": cleaned})
	})
	r.Post("/api/sessions/cleanup_zero_message", func(w http.ResponseWriter, req *http.Request) {
		cleaned, err := cleanupSessions(db, dataRoot, true)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to clean sessions")
			return
		}
		writeJSON(w, map[string]any{"ok": true, "cleaned": cleaned})
	})
}

// cleanupSessions removes empty sessions (DB rows AND their backing session
// files under dataRoot/sessions when present) and returns the count removed.
func cleanupSessions(db *sql.DB, dataRoot string, zeroOnly bool) (int, error) {
	ids, err := store.CleanupSessions(db, zeroOnly)
	if err != nil {
		return 0, err
	}
	// Best-effort backing-file deletion for exactly the cleaned sessions. The
	// DB is the Go projection, but the legacy session files are what a later
	// import re-reads; deleting them too prevents a restarted server from
	// resurrecting cleaned sessions (Python _handle_sessions_cleanup does the
	// same unlink on the file it just judged empty).
	for _, id := range ids {
		if dataRoot != "" {
			_ = os.Remove(filepath.Join(dataRoot, "sessions", id+".json"))
		}
	}
	return len(ids), nil
}

// workspaceList returns the complete workspace list, matching the shape
// Python returns from its workspace mutations.
func workspaceList(db *sql.DB) []map[string]any {
	rows, err := store.ListWorkspaces(db)
	if err != nil {
		return []map[string]any{}
	}
	ws := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		ws = append(ws, map[string]any{"path": row.Path, "name": row.Name})
	}
	return ws
}

// lastWorkspace returns Python's get_last_workspace(): the value from the
// active profile's last_workspace.txt when it names an existing directory,
// otherwise the default workspace, otherwise an empty string.
func lastWorkspace(dataRoot string, ws []map[string]any) string {
	if dataRoot != "" {
		lw := filepath.Join(dataRoot, "last_workspace.txt")
		if b, err := os.ReadFile(lw); err == nil {
			p := strings.TrimSpace(string(b))
			if p != "" {
				if info, err := os.Stat(p); err == nil && info.IsDir() {
					return p
				}
			}
		}
	}
	if len(ws) > 0 {
		if p, ok := ws[0]["path"].(string); ok {
			return p
		}
	}
	return ""
}

// terminalRemoteBackend mirrors Python's _terminal_remote_backend_enabled():
// false unless the configured terminal backend is set to something other
// than "" or "local". Go reads the same env var the Python server uses.
func terminalRemoteBackend() bool {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("HERMES_WEBUI_TERMINAL_BACKEND")))
	return backend != "" && backend != "local"
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

func uploadMaxBytes() int64 {
	mb, err := strconv.ParseInt(os.Getenv("HERMES_WEBUI_MAX_UPLOAD_MB"), 10, 64)
	if err != nil || mb <= 0 {
		mb = 20
	}
	return mb * 1024 * 1024
}

func sanitizeFilename(name string) string {
	name = filepath.Base(name)
	var b strings.Builder
	for _, r := range name {
		if r == '_' || r == '-' || r == '.' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	name = b.String()
	if len(name) > 200 {
		name = name[:200]
	}
	if name == "" || name == "." || name == ".." {
		return "upload"
	}
	return name
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

// newSessionID returns a 12-char hex session id matching the Python server shape.
func newSessionID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// sessionResponse returns the public session shape used by mutation responses.
func sessionResponse(row store.SessionRow) map[string]any {
	title := row.Title
	if strings.HasPrefix(title, "Reply ") {
		title = title[len("Reply "):]
	}
	total, user := messageCounts(row.Messages)
	ret := map[string]any{
		"session_id":         row.ID,
		"title":              title,
		"workspace":          row.Workspace,
		"model":              row.Model,
		"created_at":         row.CreatedAt,
		"updated_at":         row.UpdatedAt,
		"pinned":             row.Pinned,
		"archived":           row.Archived,
		"project_id":         row.ProjectID,
		"rev":                row.Rev,
		"message_count":      total,
		"user_message_count": user,
		"messages":           json.RawMessage(row.Messages),
	}
	if row.PendingStartedAt > 0 {
		ret["pending_started_at"] = row.PendingStartedAt
		ret["active_stream_id"] = row.ActiveStreamID
		ret["pending_user_message"] = row.PendingUserMessage
	}
	return ret
}

func sessionListItem(row store.SessionRow) map[string]any {
	title := row.Title
	if strings.HasPrefix(title, "Reply ") {
		title = title[len("Reply "):]
	}
	total, user := messageCounts(row.Messages)
	return map[string]any{
		"session_id":         row.ID,
		"title":              title,
		"workspace":          row.Workspace,
		"model":              row.Model,
		"created_at":         row.CreatedAt,
		"updated_at":         row.UpdatedAt,
		"pinned":             row.Pinned,
		"archived":           row.Archived,
		"project_id":         row.ProjectID,
		"rev":                row.Rev,
		"message_count":      total,
		"user_message_count": user,
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

// messageCounts returns the total message count and the user-message count
// from the stored OpenAI-format messages JSON, matching Python's
// Session.compact() (message_count = len(messages),
// user_message_count = count(role == "user")).
func messageCounts(messages string) (total, user int) {
	var items []struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal([]byte(messages), &items); err != nil {
		return 0, 0
	}
	for _, item := range items {
		total++
		if item.Role == "user" {
			user++
		}
	}
	return total, user
}

func messageCount(messages string) int {
	total, _ := messageCounts(messages)
	return total
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
