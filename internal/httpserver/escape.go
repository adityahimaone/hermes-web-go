package httpserver

// Wave 8 — escape family + gateway/status + workspace/upload + misc POST.
//
// escape/authorize|list|file/read|file/raw — short-lived browser-only grants
//   for a symlink that escapes the workspace; TTL 300s, session-scoped,
//   read-only, traversal-guarded. Parity with api/workspace.py.
// gateway/status + gateway/start|stop|restart — status read-only native;
//   start/stop/restart defer to the Python gateway control surface and
//   return a clear "deferred" payload (agent-coupled).
// workspace/upload — multipart file write into session workspace.
// chat/cancel — real cancel moved to ChatRouter (chat.go) with turnCancels +
// AgentClient wiring; the wave8 no-op was removed so it cannot mask it.

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// ── escape token store ─────────────────────────────────────────────────────

const escapeAuthTTL = 300 * time.Second

var escapeTokensMu sync.Mutex
var escapeTokens = map[string]escapeRecord{}

type escapeRecord struct {
	SessionID      string
	WorkspaceRoot  string
	SurfacePath    string
	ExternalRoot   string
	ExternalEntry  string
	SurfaceTarget  string
	ExpiresAt      time.Time
}

func escapePruneTokens() {
	now := time.Now()
	for tok, rec := range escapeTokens {
		if now.After(rec.ExpiresAt) {
			delete(escapeTokens, tok)
		}
	}
}

func newToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// normalizeRel — parity _normalize_workspace_rel_path: no .., no abs, posix.
func normalizeRel(rel string) (string, error) {
	raw := strings.TrimSpace(strings.ReplaceAll(rel, "\\", "/"))
	if raw == "" || raw == "." {
		return ".", nil
	}
	norm := filepath.ToSlash(filepath.Clean(raw))
	if norm == "." {
		return ".", nil
	}
	if norm == ".." || strings.HasPrefix(norm, "../") || strings.HasPrefix(norm, "/") {
		return "", fmt.Errorf("Path traversal blocked: %s", rel)
	}
	return norm, nil
}

func isBlockedSystemPath(candidate string) bool {
	// blocked: /, /private/var, common system roots, but allow per-user tmp.
	userTmp := []string{}
	if h, err := os.UserHomeDir(); err == nil {
		userTmp = append(userTmp, filepath.Join(h, "tmp"))
	}
	_ = userTmp
	blocked := []string{"/", "/private/var", "/System", "/Library", "/usr", "/bin", "/sbin", "/etc", "/var/root"}
	for _, b := range blocked {
		br, _ := filepath.EvalSymlinks(b)
		if br == "" {
			br = b
		}
		cr, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			cr = candidate
		}
		if cr == br || strings.HasPrefix(cr, br+string(os.PathSeparator)) {
			// allow user tmp carve-outs under /private/var (macOS /var/folders)
			if strings.HasPrefix(cr, "/var/folders/") || strings.HasPrefix(cr, "/private/var/folders/") {
				continue
			}
			return true
		}
	}
	return false
}

func inWorkspace(path, wsRoot string) bool {
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		p = filepath.Clean(path)
	}
	w, err := filepath.EvalSymlinks(wsRoot)
	if err != nil {
		w = filepath.Clean(wsRoot)
	}
	if p == w {
		return true
	}
	return strings.HasPrefix(p, w+string(os.PathSeparator))
}

func escapeSurfaceTarget(wsRoot, rel string) (surfacePath, target string, err error) {
	surfaceRel, err := normalizeRel(rel)
	if err != nil {
		return "", "", err
	}
	surfacePath = filepath.Join(wsRoot, filepath.FromSlash(surfaceRel))
	// symlink must exist and point outside workspace
	st, err := os.Lstat(surfacePath)
	if err != nil || st.Mode()&os.ModeSymlink == 0 {
		return "", "", fmt.Errorf("Path is not an escape-target symlink: %s", rel)
	}
	target, err = filepath.EvalSymlinks(surfacePath)
	if err != nil {
		return "", "", fmt.Errorf("Path is no longer reachable: %s", rel)
	}
	if _, err := os.Stat(target); err != nil {
		return "", "", fmt.Errorf("Path is no longer reachable: %s", rel)
	}
	if inWorkspace(target, wsRoot) {
		return "", "", fmt.Errorf("Path does not escape workspace: %s", rel)
	}
	if isBlockedSystemPath(target) {
		return "", "", fmt.Errorf("Path points to a system directory: %s", target)
	}
	return surfacePath, target, nil
}

func escapeAuthorizedRoot(target string) (root, entry string) {
	st, err := os.Stat(target)
	if err == nil && st.IsDir() {
		return target, "."
	}
	return filepath.Dir(target), filepath.Base(target)
}

func escapeAuthorize(wsRoot, sid, rel string) (int, map[string]any) {
	if sid == "" {
		return 400, map[string]any{"error": "session_id is required"}
	}
	if rel == "" {
		return 400, map[string]any{"error": "path is required"}
	}
	_, target, err := escapeSurfaceTarget(wsRoot, rel)
	if err != nil {
		return 404, map[string]any{"error": err.Error()}
	}
	token, err := newToken()
	if err != nil {
		return 500, map[string]any{"error": err.Error()}
	}
	extRoot, extEntry := escapeAuthorizedRoot(target)
	surfaceRel, _ := normalizeRel(rel)
	rec := escapeRecord{
		SessionID:     sid,
		WorkspaceRoot: wsRoot,
		SurfacePath:   surfaceRel,
		ExternalRoot:  extRoot,
		ExternalEntry: extEntry,
		SurfaceTarget: target,
		ExpiresAt:     time.Now().Add(escapeAuthTTL),
	}
	escapeTokensMu.Lock()
	escapePruneTokens()
	escapeTokens[token] = rec
	escapeTokensMu.Unlock()
	st, _ := os.Stat(target)
	return 200, map[string]any{
		"token":      token,
		"path":       surfaceRel,
		"is_dir":     st != nil && st.IsDir(),
		"expires_at": rec.ExpiresAt.Unix(),
		"expires_in": int(escapeAuthTTL.Seconds()),
		"read_only":  true,
	}
}

func escapeResolve(wsRoot, sid, token, rel string) (map[string]any, int, map[string]any) {
	escapeTokensMu.Lock()
	rec, ok := escapeTokens[token]
	escapeTokensMu.Unlock()
	if !ok || time.Now().After(rec.ExpiresAt) || rec.SessionID != sid || rec.WorkspaceRoot != wsRoot {
		return nil, 403, map[string]any{"error": "Escape authorization expired"}
	}
	surfaceRel, err := normalizeRel(rec.SurfacePath)
	if err != nil {
		return nil, 403, map[string]any{"error": "Escape authorization expired"}
	}
	reqRel, err := normalizeRel(rel)
	if err != nil {
		return nil, 404, map[string]any{"error": err.Error()}
	}
	// requested rel must be under surface path
	var externalRel string
	if reqRel == "." || reqRel == surfaceRel {
		externalRel = rec.ExternalEntry
	} else if strings.HasPrefix(reqRel, surfaceRel+"/") {
		sub := strings.TrimPrefix(reqRel, surfaceRel+"/")
		if rec.ExternalEntry == "." {
			externalRel = sub
		} else {
			externalRel = rec.ExternalEntry + "/" + sub
		}
	} else {
		return nil, 404, map[string]any{"error": fmt.Sprintf("Path traversal blocked: %s", rel)}
	}
	target := filepath.Join(rec.ExternalRoot, filepath.FromSlash(externalRel))
	// containment check
	rootResolved, _ := filepath.EvalSymlinks(rec.ExternalRoot)
	if rootResolved == "" {
		rootResolved = rec.ExternalRoot
	}
	tr, err := filepath.EvalSymlinks(target)
	if err != nil {
		return nil, 404, map[string]any{"error": "not found"}
	}
	if tr != rootResolved && !strings.HasPrefix(tr, rootResolved+string(os.PathSeparator)) {
		return nil, 404, map[string]any{"error": "not found"}
	}
	return map[string]any{
		"record":        rec,
		"target":        tr,
		"request_path":  reqRel,
		"external_root": rootResolved,
	}, 0, nil
}

// listDir entries — parity list_dir: name/type/size/mtime_ns.
func escapeListDir(wsRoot, sid, token, rel string) (int, map[string]any) {
	resolved, code, errPayload := escapeResolve(wsRoot, sid, token, rel)
	if resolved == nil {
		return code, errPayload
	}
	target := resolved["target"].(string)
	entries := []map[string]any{}
	st, err := os.Stat(target)
	if err != nil || !st.IsDir() {
		return 404, map[string]any{"error": "not found"}
	}
	items, _ := os.ReadDir(target)
	for _, it := range items {
		isDir := it.IsDir()
		var size int64
		var mtime int64
		if info, err := it.Info(); err == nil {
			size = info.Size()
			mtime = info.ModTime().UnixNano()
		}
		et := "file"
		if isDir {
			et = "dir"
		}
		entry := map[string]any{
			"name":             it.Name(),
			"type":             et,
			"size":             size,
			"mtime_ns":         mtime,
			"escape_read_only": true,
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i]["name"].(string) < entries[j]["name"].(string)
	})
	return 200, map[string]any{
		"path":         resolved["request_path"],
		"entries":      entries,
		"virtual_root": surfaceRelOf(resolved),
		"read_only":    true,
	}
}

func surfaceRelOf(resolved map[string]any) string {
	rec := resolved["record"].(escapeRecord)
	return rec.SurfacePath
}

func escapeReadFile(wsRoot, sid, token, rel string) (int, map[string]any) {
	resolved, code, errPayload := escapeResolve(wsRoot, sid, token, rel)
	if resolved == nil {
		return code, errPayload
	}
	target := resolved["target"].(string)
	st, err := os.Stat(target)
	if err != nil || st.IsDir() {
		return 404, map[string]any{"error": "Not a file: " + rel}
	}
	if st.Size() > maxFileBytes {
		return 400, map[string]any{"error": fmt.Sprintf("File too large (%d bytes, max %d)", st.Size(), maxFileBytes)}
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		return 404, map[string]any{"error": "not found"}
	}
	content := string(raw)
	return 200, map[string]any{
		"path":             resolved["request_path"],
		"content":          content,
		"size":             len(raw),
		"lines":            strings.Count(content, "\n") + 1,
		"escape_read_only": true,
	}
}

// ── gateway/status ─────────────────────────────────────────────────────────

func gatewayStatus(dataRoot, hermesHome string) map[string]any {
	settings := loadWebUISettings(dataRoot, hermesHome)
	enabled, _ := settings["show_cli_sessions"].(bool)
	return map[string]any{
		"running":           false,
		"enabled":           enabled,
		"mode":              "python",
		"note":              "gateway runs in Python agent; Go reports read-only status",
	}
}

// ── workspace/upload ───────────────────────────────────────────────────────

func workspaceUpload(w http.ResponseWriter, req *http.Request, db *sql.DB) {
	if err := req.ParseMultipartForm(64 << 20); err != nil {
		wave4WriteJSONErr(w, 400, "invalid multipart form")
		return
	}
	sid := req.FormValue("session_id")
	if sid == "" {
		wave4WriteJSONErr(w, 400, "session_id is required")
		return
	}
	var ws string
	if err := db.QueryRow("SELECT workspace FROM sessions WHERE session_id = ?", sid).Scan(&ws); err == sql.ErrNoRows {
		wave4WriteJSONErr(w, 404, "Session not found")
		return
	}
	if ws == "" || ws == "/" {
		ws, _ = os.UserHomeDir()
	}
	file, hdr, err := req.FormFile("file")
	if err != nil {
		wave4WriteJSONErr(w, 400, "No file field in request")
		return
	}
	defer file.Close()
	rel := req.FormValue("path")
	if rel == "" {
		rel = hdr.Filename
	}
	clean, nerr := normalizeRel(rel)
	if nerr != nil {
		wave4WriteJSONErr(w, 400, nerr.Error())
		return
	}
	if clean == "." {
		wave4WriteJSONErr(w, 400, "invalid path")
		return
	}
	target := filepath.Join(ws, filepath.FromSlash(clean))
	if !inWorkspace(target, ws) {
		wave4WriteJSONErr(w, 400, "path outside workspace")
		return
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		wave4WriteJSONErr(w, 500, err.Error())
		return
	}
	out, err := os.Create(target)
	if err != nil {
		wave4WriteJSONErr(w, 500, err.Error())
		return
	}
	defer out.Close()
	if _, err := io.Copy(out, file); err != nil {
		wave4WriteJSONErr(w, 500, err.Error())
		return
	}
	wave4WriteJSON(w, 200, map[string]any{"ok": true, "path": clean})
}

// ── router ──────────────────────────────────────────────────────────────────

func wave8Router(r chi.Router, db *sql.DB, dataRoot, hermesHome string) {
	r.Get("/api/escape/list", func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		sid := q.Get("session_id")
		token := q.Get("token")
		rel := q.Get("path")
		if rel == "" {
			rel = "."
		}
		if sid == "" {
			wave4WriteJSONErr(w, 400, "session_id is required")
			return
		}
		if token == "" {
			wave4WriteJSONErr(w, 400, "token is required")
			return
		}
		var ws string
		if err := db.QueryRow("SELECT workspace FROM sessions WHERE session_id = ?", sid).Scan(&ws); err != nil {
			wave4WriteJSONErr(w, 404, "Session not found")
			return
		}
		code, payload := escapeListDir(ws, sid, token, rel)
		wave4WriteJSON(w, code, payload)
	})
	r.Get("/api/escape/file/read", func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		sid := q.Get("session_id")
		token := q.Get("token")
		rel := q.Get("path")
		if sid == "" {
			wave4WriteJSONErr(w, 400, "session_id is required")
			return
		}
		if token == "" {
			wave4WriteJSONErr(w, 400, "token is required")
			return
		}
		var ws string
		if err := db.QueryRow("SELECT workspace FROM sessions WHERE session_id = ?", sid).Scan(&ws); err != nil {
			wave4WriteJSONErr(w, 404, "Session not found")
			return
		}
		code, payload := escapeReadFile(ws, sid, token, rel)
		wave4WriteJSON(w, code, payload)
	})
	r.Get("/api/escape/file/raw", func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		sid := q.Get("session_id")
		token := q.Get("token")
		rel := q.Get("path")
		if sid == "" {
			wave4WriteJSONErr(w, 400, "session_id is required")
			return
		}
		if token == "" {
			wave4WriteJSONErr(w, 400, "token is required")
			return
		}
		var ws string
		if err := db.QueryRow("SELECT workspace FROM sessions WHERE session_id = ?", sid).Scan(&ws); err != nil {
			wave4WriteJSONErr(w, 404, "Session not found")
			return
		}
		resolved, code, errPayload := escapeResolve(ws, sid, token, rel)
		if resolved == nil {
			wave4WriteJSON(w, code, errPayload)
			return
		}
		target := resolved["target"].(string)
		st, err := os.Stat(target)
		if err != nil || st.IsDir() {
			wave4WriteJSONErr(w, 404, "not found")
			return
		}
		if q.Get("download") == "1" {
			w.Header().Set("Content-Disposition", "attachment; filename="+filepath.Base(target))
		} else {
			ct := "application/octet-stream"
			switch strings.ToLower(filepath.Ext(target)) {
			case ".css":
				ct = "text/css"
			case ".js":
				ct = "application/javascript"
			case ".json":
				ct = "application/json"
			case ".png":
				ct = "image/png"
			case ".jpg", ".jpeg":
				ct = "image/jpeg"
			case ".svg":
				ct = "image/svg+xml"
			}
			w.Header().Set("Content-Type", ct)
		}
		data, err := os.ReadFile(target)
		if err != nil {
			wave4WriteJSONErr(w, 404, "not found")
			return
		}
		_, _ = w.Write(data)
	})
	r.Post("/api/escape/authorize", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			wave4WriteJSONErr(w, 400, "invalid request body")
			return
		}
		sid, _ := body["session_id"].(string)
		rel, _ := body["path"].(string)
		var ws string
		if err := db.QueryRow("SELECT workspace FROM sessions WHERE session_id = ?", sid).Scan(&ws); err != nil {
			wave4WriteJSONErr(w, 404, "Session not found")
			return
		}
		code, payload := escapeAuthorize(ws, sid, rel)
		wave4WriteJSON(w, code, payload)
	})
	r.Get("/api/gateway/status", func(w http.ResponseWriter, _ *http.Request) {
		wave4WriteJSON(w, 200, gatewayStatus(dataRoot, hermesHome))
	})
	r.Get("/api/gateway/start", func(w http.ResponseWriter, _ *http.Request) {
		wave4WriteJSON(w, 200, map[string]any{"ok": false, "status": "deferred", "message": "gateway start lives in Python agent"})
	})
	r.Post("/api/gateway/stop", func(w http.ResponseWriter, _ *http.Request) {
		wave4WriteJSON(w, 200, map[string]any{"ok": false, "status": "deferred", "message": "gateway stop lives in Python agent"})
	})
	r.Post("/api/gateway/restart", func(w http.ResponseWriter, _ *http.Request) {
		wave4WriteJSON(w, 200, map[string]any{"ok": false, "status": "deferred", "message": "gateway restart lives in Python agent"})
	})
	r.Post("/api/workspace/upload", func(w http.ResponseWriter, req *http.Request) {
		workspaceUpload(w, req, db)
	})
	// POST /api/chat/cancel moved to ChatRouter (chat.go) — this wave8 stub
	// was a no-op that masked the real cancel wiring.
}

var _ = sha256.Sum256
var _ = hex.EncodeToString