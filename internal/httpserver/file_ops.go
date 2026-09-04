package httpserver

// Wave 19 — workspace file-ops family (Python ports, routes.py 25650-25940 +
// 20894-20990). All ops anchor to the session workspace via
// workspaceRootForSession + workspace.SafeResolve, mirroring data.go's
// save/create/delete handlers.
//
//   POST /api/file/rename       — rename within parent dir (new_name)
//   POST /api/file/move         — move into workspace-relative dest_dir
//   POST /api/file/create-dir   — mkdir (400 when path exists)
//   POST /api/file/path         — rel → absolute resolve (no exists check)
//   POST /api/file/reveal       — OS file-manager reveal
//   POST /api/file/open-vscode  — open in VS Code (vscode: block in config.yaml)
//   POST /api/file/office-save  — 503 (Go runtime has no OOXML editor)
//   GET  /api/folder/download   — zip stream of a workspace subtree
//
// Parity guards kept: symlinked leaf rejected before exists() (dangling
// symlink → 400, not 404), new_name charset check, move-into-itself guard,
// path resolve uses the RESOLVED root for returned relative paths.

import (
	"archive/zip"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"

	"hermes-web-go/internal/workspace"
)

// fileOpResolve resolves (session_id, path) into a workspace-anchored target.
// Returns (ctx, errStatus, errMsg); ctx == nil means respond with the error.
type fileOpCtx struct {
	root     string // session workspace root
	rootEval string // symlink-resolved root
	target   string // resolved target path
	rel      string // request rel path
}

// leafIsSymlink: no-follow lstat on the lexically-requested final component.
func (c *fileOpCtx) leafIsSymlink() bool {
	lexi := filepath.Join(c.root, filepath.FromSlash(c.rel))
	fi, err := os.Lstat(lexi)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}

func (c *fileOpCtx) relOf(abs string) string {
	// Python computes dest.relative_to(ws_root.resolve()) against the resolved
	// root — eval the abs path so a symlinked root (/tmp -> /private/tmp) does
	// not produce ../../-style relative paths.
	if ev, err := filepath.EvalSymlinks(abs); err == nil {
		abs = ev
	}
	if rel, err := filepath.Rel(c.rootEval, abs); err == nil {
		return filepath.ToSlash(rel)
	}
	rel, _ := filepath.Rel(c.rootEval, abs)
	return filepath.ToSlash(rel)
}

// targetExists uses follow-based stat (mirrors Python Path.exists()).
func (c *fileOpCtx) targetExists() bool {
	_, err := os.Stat(c.target)
	return err == nil
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

// requireFields mirrors Python require(): first missing key is the message.
func requireFields(body map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		raw, ok := body[k]
		if !ok {
			return k + " is required"
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil || s == "" {
			// Python require() only checks presence + truthiness of raw value
			var anyV any
			if err2 := json.Unmarshal(raw, &anyV); err2 != nil || anyV == nil {
				return k + " is required"
			}
			var sv string
			_ = json.Unmarshal(raw, &sv)
			if sv == "" && !isNonStringTruthy(raw) {
				return k + " is required"
			}
		}
	}
	return ""
}

func isNonStringTruthy(raw json.RawMessage) bool {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return t != ""
	case bool:
		return t
	case float64:
		return t != 0
	default:
		return true
	}
}

// ── rename ─────────────────────────────────────────────────────────────────

func fileRenameOp(db *sql.DB, body map[string]json.RawMessage) (int, map[string]any) {
	if msg := requireFields(body, "session_id", "path", "new_name"); msg != "" {
		return http.StatusBadRequest, map[string]any{"error": msg}
	}
	sid := jsonStrField(body["session_id"])
	rel := jsonStrField(body["path"])
	ctx, st, emsg := fileOpResolveDB(db, sid, rel)
	if ctx == nil {
		return st, map[string]any{"error": emsg}
	}
	if ctx.leafIsSymlink() {
		return http.StatusBadRequest, map[string]any{"error": "Cannot rename a symlinked entry"}
	}
	if !ctx.targetExists() {
		return http.StatusBadRequest, map[string]any{"error": "File not found"}
	}
	newName := strings.TrimSpace(jsonStrField(body["new_name"]))
	if newName == "" || strings.ContainsAny(newName, "/\\") || strings.Contains(newName, "..") {
		return http.StatusBadRequest, map[string]any{"error": "Invalid file name"}
	}
	dest := filepath.Join(filepath.Dir(ctx.target), newName)
	if _, err := os.Lstat(dest); err == nil {
		return http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("A file named %q already exists", newName)}
	}
	if err := os.Rename(ctx.target, dest); err != nil {
		return http.StatusBadRequest, map[string]any{"error": err.Error()}
	}
	return http.StatusOK, map[string]any{"ok": true, "old_path": rel, "new_path": ctx.relOf(dest)}
}

// ── move ───────────────────────────────────────────────────────────────────

func fileMoveOp(db *sql.DB, body map[string]json.RawMessage) (int, map[string]any) {
	if msg := requireFields(body, "session_id", "path", "dest_dir"); msg != "" {
		return http.StatusBadRequest, map[string]any{"error": msg}
	}
	sid := jsonStrField(body["session_id"])
	rel := jsonStrField(body["path"])
	ctx, st, emsg := fileOpResolveDB(db, sid, rel)
	if ctx == nil {
		return st, map[string]any{"error": emsg}
	}
	if ctx.leafIsSymlink() {
		return http.StatusBadRequest, map[string]any{"error": "Cannot move a symlinked entry"}
	}
	fi, err := os.Stat(ctx.target)
	if err != nil {
		return http.StatusBadRequest, map[string]any{"error": "File not found"}
	}
	destDirRaw := strings.TrimSpace(jsonStrField(body["dest_dir"]))
	if destDirRaw == "" {
		destDirRaw = "."
	}
	for _, seg := range strings.Split(destDirRaw, "/") {
		if seg == ".." {
			return http.StatusBadRequest, map[string]any{"error": "Invalid destination"}
		}
	}
	destParent, err := workspace.SafeResolve(ctx.root, destDirRaw)
	if err != nil {
		return http.StatusBadRequest, map[string]any{"error": "Invalid destination"}
	}
	if dfi, err := os.Stat(destParent); err != nil || !dfi.IsDir() {
		return http.StatusBadRequest, map[string]any{"error": "Destination folder not found"}
	}
	if fi.IsDir() {
		destEval, err1 := filepath.EvalSymlinks(destParent)
		srcEval, err2 := filepath.EvalSymlinks(ctx.target)
		if err1 == nil && err2 == nil {
			if destEval == srcEval || strings.HasPrefix(destEval, srcEval+string(filepath.Separator)) {
				return http.StatusBadRequest, map[string]any{"error": "Cannot move a folder into itself or its subfolder"}
			}
		}
	}
	dest := filepath.Join(destParent, filepath.Base(ctx.target))
	if srcEval, err := filepath.EvalSymlinks(ctx.target); err == nil {
		if destEval, err := filepath.EvalSymlinks(dest); err == nil && srcEval == destEval {
			return http.StatusOK, map[string]any{"ok": true, "old_path": rel, "new_path": ctx.relOf(ctx.target)}
		}
	}
	if _, err := os.Lstat(dest); err == nil {
		return http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("A file named %q already exists in that folder", filepath.Base(ctx.target))}
	}
	if err := os.Rename(ctx.target, dest); err != nil {
		return http.StatusBadRequest, map[string]any{"error": err.Error()}
	}
	return http.StatusOK, map[string]any{"ok": true, "old_path": rel, "new_path": ctx.relOf(dest)}
}

// ── create-dir ─────────────────────────────────────────────────────────────

func createDirOp(db *sql.DB, body map[string]json.RawMessage) (int, map[string]any) {
	if msg := requireFields(body, "session_id", "path"); msg != "" {
		return http.StatusBadRequest, map[string]any{"error": msg}
	}
	ctx, st, emsg := fileOpResolveDB(db, jsonStrField(body["session_id"]), jsonStrField(body["path"]))
	if ctx == nil {
		return st, map[string]any{"error": emsg}
	}
	if _, err := os.Stat(ctx.target); err == nil {
		return http.StatusBadRequest, map[string]any{"error": "Path already exists"}
	}
	if err := os.MkdirAll(ctx.target, 0o755); err != nil {
		return http.StatusBadRequest, map[string]any{"error": err.Error()}
	}
	return http.StatusOK, map[string]any{"ok": true, "path": ctx.relOf(ctx.target)}
}

// ── path resolve ───────────────────────────────────────────────────────────

func filePathOp(db *sql.DB, body map[string]json.RawMessage) (int, map[string]any) {
	if msg := requireFields(body, "session_id", "path"); msg != "" {
		return http.StatusBadRequest, map[string]any{"error": msg}
	}
	ctx, st, emsg := fileOpResolveDB(db, jsonStrField(body["session_id"]), jsonStrField(body["path"]))
	if ctx == nil {
		return st, map[string]any{"error": emsg}
	}
	return http.StatusOK, map[string]any{"ok": true, "path": ctx.target}
}

// ── shared: reveal + vscode OS dispatch ────────────────────────────────────

type vscodeCfgBlock struct {
	Command             string `yaml:"command"`
	HostPathPrefix      string `yaml:"host_path_prefix"`
	ContainerPathPrefix string `yaml:"container_path_prefix"`
}

// vscodePathTranslation mirrors the Docker host/container prefix swap.
func vscodePathTranslation(cfg vscodeCfgBlock, target string) string {
	if cfg.ContainerPathPrefix == "" || cfg.HostPathPrefix == "" {
		return target
	}
	norm := strings.TrimRight(cfg.ContainerPathPrefix, "/") + "/"
	if strings.HasPrefix(target, norm) || target == strings.TrimRight(cfg.ContainerPathPrefix, "/") {
		return cfg.HostPathPrefix + strings.TrimPrefix(target, strings.TrimRight(cfg.ContainerPathPrefix, "/"))
	}
	return target
}

func loadVSCodeConfig(hermesHome string) vscodeCfgBlock {
	var cfg struct {
		VSCode vscodeCfgBlock `yaml:"vscode"`
	}
	raw, err := os.ReadFile(filepath.Join(hermesHome, "config.yaml"))
	if err == nil {
		_ = yaml.Unmarshal(raw, &cfg)
	}
	return cfg.VSCode
}

// ── reveal ─────────────────────────────────────────────────────────────────

func fileRevealOp(db *sql.DB, hermesHome string, body map[string]json.RawMessage) (int, map[string]any) {
	if msg := requireFields(body, "session_id", "path"); msg != "" {
		return http.StatusBadRequest, map[string]any{"error": msg}
	}
	ctx, st, emsg := fileOpResolveDB(db, jsonStrField(body["session_id"]), jsonStrField(body["path"]))
	if ctx == nil {
		return st, map[string]any{"error": emsg}
	}
	if !ctx.targetExists() {
		return http.StatusNotFound, map[string]any{"error": fmt.Sprintf("File not found: %s", ctx.target)}
	}
	targetStr := vscodePathTranslation(loadVSCodeConfig(hermesHome), ctx.target)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-R", targetStr)
	case "windows":
		cmd = exec.Command("explorer.exe", "/select,"+targetStr)
	default:
		cmd = exec.Command("xdg-open", filepath.Dir(targetStr))
	}
	if err := cmd.Start(); err != nil {
		return http.StatusBadRequest, map[string]any{"error": err.Error()}
	}
	go func() { _ = cmd.Wait() }()
	return http.StatusOK, map[string]any{"ok": true, "path": jsonStrField(body["path"])}
}

// ── open in VS Code ────────────────────────────────────────────────────────

func fileOpenVSCodeOp(db *sql.DB, hermesHome string, body map[string]json.RawMessage) (int, map[string]any) {
	if msg := requireFields(body, "session_id", "path"); msg != "" {
		return http.StatusBadRequest, map[string]any{"error": msg}
	}
	ctx, st, emsg := fileOpResolveDB(db, jsonStrField(body["session_id"]), jsonStrField(body["path"]))
	if ctx == nil {
		return st, map[string]any{"error": emsg}
	}
	if !ctx.targetExists() {
		return http.StatusNotFound, map[string]any{"error": fmt.Sprintf("File not found: %s", ctx.target)}
	}
	cfg := loadVSCodeConfig(hermesHome)
	targetStr := vscodePathTranslation(cfg, ctx.target)
	cmdName := cfg.Command
	if cmdName == "" {
		cmdName = "code"
	}
	resolved, err := exec.LookPath(cmdName)
	if err != nil {
		resolved = vscodeFallbackPath()
	}
	if resolved == "" {
		return http.StatusBadRequest, map[string]any{
			"error": fmt.Sprintf("VS Code command not found: %q. Install VS Code and ensure the 'code' CLI is on PATH, or set vscode.command in config.yaml to the full path.", cmdName),
		}
	}
	cmd := exec.Command(resolved, targetStr)
	if err := cmd.Start(); err != nil {
		return http.StatusBadRequest, map[string]any{"error": err.Error()}
	}
	go func() { _ = cmd.Wait() }()
	return http.StatusOK, map[string]any{"ok": true, "path": jsonStrField(body["path"])}
}

func vscodeFallbackPath() string {
	for _, c := range []string{
		"/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code",
		"/usr/bin/code",
		"/snap/bin/code",
	} {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// ── office save (deliberate 503) ───────────────────────────────────────────

func officeSaveOp(db *sql.DB, body map[string]json.RawMessage) (int, map[string]any) {
	if msg := requireFields(body, "session_id", "path"); msg != "" {
		return http.StatusBadRequest, map[string]any{"error": msg}
	}
	ctx, st, emsg := fileOpResolveDB(db, jsonStrField(body["session_id"]), jsonStrField(body["path"]))
	if ctx == nil {
		return st, map[string]any{"error": emsg}
	}
	if ctx.leafIsSymlink() {
		return http.StatusBadRequest, map[string]any{"error": "Cannot save to a symlinked entry"}
	}
	fi, err := os.Stat(ctx.target)
	if err != nil {
		return http.StatusNotFound, map[string]any{"error": "File not found"}
	}
	if fi.IsDir() {
		return http.StatusBadRequest, map[string]any{"error": "Cannot save: path is a directory"}
	}
	ext := strings.ToLower(filepath.Ext(ctx.target))
	if ext != ".docx" && ext != ".xlsx" && ext != ".pptx" {
		return http.StatusBadRequest, map[string]any{"error": "Office save is only available for .docx, .xlsx, and .pptx files"}
	}
	// ponytail: python-docx/openpyxl/python-pptx round-trip editing is not
	// bundled in the Go runtime — ceiling: docx/xlsx/pptx edits 503; upgrade
	// when a Go OOXML editor is added or document editing stays Python-side.
	return http.StatusServiceUnavailable, map[string]any{"error": "Office document editing requires the Python WebUI backend"}
}

// ── folder download ────────────────────────────────────────────────────────

func folderZipMaxBytes() int64 {
	mb := int64(1024)
	if v := strings.TrimSpace(os.Getenv("HERMES_WEBUI_FOLDER_ZIP_MAX_MB")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			mb = n
		}
	}
	return mb * 1024 * 1024
}

func folderZipMaxFiles() int {
	n := 50000
	if v := strings.TrimSpace(os.Getenv("HERMES_WEBUI_FOLDER_ZIP_MAX_FILES")); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			n = p
		}
	}
	return n
}

var (
	errZipMaxFiles = errors.New("max_files")
	errZipMaxBytes = errors.New("max_bytes")
)

type zipEntry struct {
	path    string
	arcname string
}

// folderCollect pre-flights the walk (size/count caps) and returns files to
// archive. Symlinks and dirs escaping the resolved workspace are skipped.
func folderCollect(target, rootEval string, maxBytes int64, maxFiles int) ([]zipEntry, int64, string) {
	var files []zipEntry
	var total int64
	inside := func(eval string) bool {
		return eval == rootEval || strings.HasPrefix(eval, rootEval+string(filepath.Separator))
	}
	err := filepath.WalkDir(target, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return nil
		}
		if d.IsDir() {
			eval, e := filepath.EvalSymlinks(p)
			if e != nil || !inside(eval) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			eval, e := filepath.EvalSymlinks(p)
			if e != nil || !inside(eval) {
				return nil
			}
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		if len(files)+1 > maxFiles {
			return errZipMaxFiles
		}
		total += fi.Size()
		if total > maxBytes {
			return errZipMaxBytes
		}
		rel, _ := filepath.Rel(target, p)
		files = append(files, zipEntry{path: p, arcname: filepath.ToSlash(rel)})
		return nil
	})
	switch {
	case errors.Is(err, errZipMaxFiles):
		return files, total, "max_files"
	case errors.Is(err, errZipMaxBytes):
		return files, total, "max_bytes"
	}
	return files, total, ""
}

func folderDownloadHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		sid := q.Get("session_id")
		if sid == "" {
			writeError(w, http.StatusBadRequest, "session_id is required")
			return
		}
		rel := q.Get("path")
		root, err := workspaceRootForSession(db, sid)
		if err != nil {
			writeError(w, http.StatusNotFound, "Session not found")
			return
		}
		rootEval, err := filepath.EvalSymlinks(root)
		if err != nil {
			rootEval = root
		}
		target, err := workspace.SafeResolve(root, rel)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid path")
			return
		}
		fi, err := os.Stat(target)
		if err != nil {
			writeJSONStatus(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		if !fi.IsDir() {
			writeError(w, http.StatusBadRequest, "path must be a directory; use /api/file/raw for single files")
			return
		}
		files, _, limitHit := folderCollect(target, rootEval, folderZipMaxBytes(), folderZipMaxFiles())
		if limitHit == "max_files" {
			writeJSONStatus(w, http.StatusRequestEntityTooLarge, map[string]any{
				"error": "too many files", "limit": folderZipMaxFiles(), "configure": "HERMES_WEBUI_FOLDER_ZIP_MAX_FILES",
			})
			return
		}
		if limitHit == "max_bytes" {
			writeJSONStatus(w, http.StatusRequestEntityTooLarge, map[string]any{
				"error": "folder too large", "limit_bytes": folderZipMaxBytes(), "configure": "HERMES_WEBUI_FOLDER_ZIP_MAX_MB",
			})
			return
		}
		zipName := filepath.Base(target) + ".zip"
		if zipName == ".zip" || zipName == "/" || zipName == "." {
			zipName = "workspace.zip"
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", zipName))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Connection", "close")
		w.WriteHeader(http.StatusOK)
		zw := zip.NewWriter(w)
		for _, fe := range files {
			src, err := os.Open(fe.path)
			if err != nil {
				continue
			}
			hdr := &zip.FileHeader{Name: fe.arcname, Method: zip.Deflate}
			dst, err := zw.CreateHeader(hdr)
			if err == nil {
				_, _ = io.Copy(dst, src)
			}
			_ = src.Close()
		}
		_ = zw.Close()
	}
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonStrField(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

func fileOpResolveDB(db *sql.DB, sid, rel string) (*fileOpCtx, int, string) {
	if sid == "" || rel == "" {
		return nil, http.StatusBadRequest, "session_id and path are required"
	}
	root, err := workspaceRootForSession(db, sid)
	if err != nil {
		return nil, http.StatusNotFound, "Session not found"
	}
	rootEval, err := filepath.EvalSymlinks(root)
	if err != nil {
		rootEval = root
	}
	target, err := workspace.SafeResolve(root, rel)
	if err != nil {
		return nil, http.StatusBadRequest, "path escapes workspace"
	}
	return &fileOpCtx{root: root, rootEval: rootEval, target: target, rel: rel}, 0, ""
}

// Wave19Router mounts the wave-19 file-op family.
func Wave19Router(r chi.Router, db *sql.DB, hermesHome string) {
	post := func(pattern string, fn func(db *sql.DB, body map[string]json.RawMessage) (int, map[string]any)) {
		r.Post(pattern, func(w http.ResponseWriter, req *http.Request) {
			var body map[string]json.RawMessage
			if !decodeJSONBody(w, req, &body) {
				return
			}
			st, payload := fn(db, body)
			writeJSONStatus(w, st, payload)
		})
	}
	postWithHome := func(pattern string, fn func(db *sql.DB, hermesHome string, body map[string]json.RawMessage) (int, map[string]any)) {
		r.Post(pattern, func(w http.ResponseWriter, req *http.Request) {
			var body map[string]json.RawMessage
			if !decodeJSONBody(w, req, &body) {
				return
			}
			st, payload := fn(db, hermesHome, body)
			writeJSONStatus(w, st, payload)
		})
	}
	post("/api/file/rename", fileRenameOp)
	post("/api/file/move", fileMoveOp)
	post("/api/file/create-dir", createDirOp)
	post("/api/file/path", filePathOp)
	post("/api/file/office-save", officeSaveOp)
	postWithHome("/api/file/reveal", fileRevealOp)
	postWithHome("/api/file/open-vscode", fileOpenVSCodeOp)
	r.Get("/api/folder/download", folderDownloadHandler(db))
}
