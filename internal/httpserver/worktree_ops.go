package httpserver

// Wave 13 — worktree status/remove + shutdown.
//
// GET  /api/session/worktree/status  — read-only snapshot: path/exists/dirty/
//   untracked_count/ahead_behind/locks/listed (mirrors worktrees.py).
// POST /api/session/worktree/remove  — guarded removal: stream/terminal locks,
//   dirty/untracked/ahead require force; unlock + git worktree remove.
//   Worktree attrs are read from the WebUI session JSON
//   (<webui_state>/sessions/<sid>.json) — Python is the source of truth.
// POST /api/shutdown — respond then SIGINT own process (parity).

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
)

// sessionJSONWorktree reads worktree attrs from the Python-side session JSON.
func sessionJSONWorktree(sessionDir, sid string) (path, repoRoot string, exists bool) {
	raw, err := os.ReadFile(filepath.Join(sessionDir, sid+".json"))
	if err != nil {
		return "", "", false
	}
	var payload struct {
		WorktreePath     string `json:"worktree_path"`
		WorktreeRepoRoot string `json:"worktree_repo_root"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return "", "", false
	}
	if strings.TrimSpace(payload.WorktreePath) == "" {
		return "", "", false
	}
	return payload.WorktreePath, payload.WorktreeRepoRoot, true
}

func resolveRealPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func gitRun(dir string, timeout time.Duration, args ...string) (string, int) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	return string(out), code
}

func worktreeStatusPorcelain(wt string) (dirty bool, untracked int) {
	out, code := gitRun(wt, 10*time.Second, "status", "--porcelain", "--untracked-files=normal")
	if code != 0 {
		return false, 0
	}
	lines := []string{}
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	untracked = 0
	for _, l := range lines {
		if strings.HasPrefix(l, "??") {
			untracked++
		}
	}
	return len(lines) > 0, untracked
}

func worktreeAheadBehind(wt string) map[string]any {
	payload := map[string]any{
		"ahead": 0, "behind": 0, "available": false, "upstream": nil,
	}
	upOut, code := gitRun(wt, 10*time.Second, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if code != 0 {
		return payload
	}
	up := strings.TrimSpace(upOut)
	if up == "" {
		return payload
	}
	payload["upstream"] = up
	cntOut, code := gitRun(wt, 10*time.Second, "rev-list", "--left-right", "--count", "HEAD...@{u}")
	if code != 0 {
		return payload
	}
	parts := strings.Fields(strings.TrimSpace(cntOut))
	if len(parts) != 2 {
		return payload
	}
	var a, b int
	if _, err := fmt.Sscanf(parts[0], "%d", &a); err != nil {
		return payload
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &b); err != nil {
		return payload
	}
	payload["ahead"] = a
	payload["behind"] = b
	payload["available"] = true
	return payload
}

func worktreeListed(wt, repoRoot string) bool {
	cwd := wt
	if repoRoot != "" {
		if st, err := os.Stat(repoRoot); err == nil && st.IsDir() {
			cwd = repoRoot
		}
	}
	out, code := gitRun(cwd, 10*time.Second, "worktree", "list", "--porcelain")
	if code != 0 {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			p := strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
			if resolveRealPath(p) == resolveRealPath(wt) {
				return true
			}
		}
	}
	return false
}

func handleWorktreeStatus(db *sql.DB, sessionDir, sid string) (int, map[string]any) {
	sid = strings.TrimSpace(sid)
	if sid == "" {
		return 400, map[string]any{"error": "session_id is required"}
	}
	var one int
	if err := db.QueryRow("SELECT 1 FROM sessions WHERE session_id = ?", sid).Scan(&one); err != nil {
		return 404, map[string]any{"error": "Session not found"}
	}
	wt, repoRoot, ok := sessionJSONWorktree(sessionDir, sid)
	if !ok {
		return 400, map[string]any{"error": "Session is not worktree-backed"}
	}
	wt = resolveRealPath(wt)
	exists := false
	if st, err := os.Stat(wt); err == nil && st.IsDir() {
		exists = true
	}
	status := map[string]any{
		"path":            wt,
		"exists":          exists,
		"dirty":           false,
		"untracked_count": 0,
		"ahead_behind": map[string]any{
			"ahead": 0, "behind": 0, "available": false, "upstream": nil,
		},
		"locked_by_stream":   false,
		"locked_by_terminal": false,
		"listed":             worktreeListed(wt, repoRoot),
	}
	if exists {
		dirty, untracked := worktreeStatusPorcelain(wt)
		status["dirty"] = dirty
		status["untracked_count"] = untracked
		status["ahead_behind"] = worktreeAheadBehind(wt)
	}
	return 200, map[string]any{"status": status}
}

func handleWorktreeRemove(db *sql.DB, sessionDir, sid string, force bool) (int, map[string]any) {
	sid = strings.TrimSpace(sid)
	if sid == "" {
		return 400, map[string]any{"error": "session_id is required"}
	}
	var one int
	if err := db.QueryRow("SELECT 1 FROM sessions WHERE session_id = ?", sid).Scan(&one); err != nil {
		return 404, map[string]any{"error": "Session not found"}
	}
	wt, repoRoot, ok := sessionJSONWorktree(sessionDir, sid)
	if !ok {
		return 400, map[string]any{"error": "Session is not worktree-backed"}
	}
	if repoRoot == "" {
		return 400, map[string]any{"error": "Session missing worktree_repo_root"}
	}
	wt = resolveRealPath(wt)
	exists := false
	if st, err := os.Stat(wt); err == nil && st.IsDir() {
		exists = true
	}
	if !exists {
		return 200, map[string]any{
			"ok":           true,
			"removed_path": wt,
			"warnings":     []string{"Worktree directory no longer exists on disk."},
		}
	}
	warnings := []string{}
	// terminal lock: Go owns terminal sessions natively now
	termMu.Lock()
	t := terminals[sid]
	termMu.Unlock()
	if t != nil && t.isAlive() && resolveRealPath(t.Workspace) == wt {
		return 400, map[string]any{"error": "Worktree is locked by an active terminal session"}
	}
	// stream lock: Go proxy has no in-process streams; a native terminal is
	// the only local lock we can observe. Stream-locked parity deferred.
	dirty, untracked := worktreeStatusPorcelain(wt)
	aheadBehind := worktreeAheadBehind(wt)
	ahead, _ := aheadBehind["ahead"].(int)
	if dirty && !force {
		return 400, map[string]any{"error": "Worktree has uncommitted changes. Use force=true to override."}
	}
	if untracked > 0 {
		if force {
			warnings = append(warnings, fmt.Sprintf("%d untracked file(s) will be removed.", untracked))
		} else {
			return 400, map[string]any{"error": fmt.Sprintf("Worktree has %d untracked file(s). Use force=true to override.", untracked)}
		}
	}
	if ahead > 0 {
		if force {
			warnings = append(warnings, fmt.Sprintf("%d unpushed commit(s) will be removed.", ahead))
		} else {
			return 400, map[string]any{"error": fmt.Sprintf("Worktree has %d unpushed commit(s). Use force=true to override.", ahead)}
		}
	}
	gitRun(repoRoot, 5*time.Second, "worktree", "unlock", wt)
	removeArgs := []string{"worktree", "remove"}
	if force {
		removeArgs = append(removeArgs, "--force")
	}
	removeArgs = append(removeArgs, wt)
	out, code := gitRun(repoRoot, 10*time.Second, removeArgs...)
	if code != 0 {
		return 400, map[string]any{"error": fmt.Sprintf("Failed to remove worktree: %s", strings.TrimSpace(out))}
	}
	return 200, map[string]any{
		"ok":           true,
		"removed_path": wt,
		"warnings":     warnings,
	}
}

// ── router ─────────────────────────────────────────────────────────────────

// Wave13Router serves worktree status/remove + shutdown.
func Wave13Router(r chi.Router, db *sql.DB, hermesHome string) {
	sessionDir := webuiSessionsDir(hermesHome)
	r.Get("/api/session/worktree/status", func(w http.ResponseWriter, req *http.Request) {
		code, payload := handleWorktreeStatus(db, sessionDir, req.URL.Query().Get("session_id"))
		wave4WriteJSON(w, code, payload)
	})
	r.Post("/api/session/worktree/remove", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			SessionID string `json:"session_id"`
			Force     bool   `json:"force"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			wave4WriteJSONErr(w, 400, "invalid request body")
			return
		}
		code, payload := handleWorktreeRemove(db, sessionDir, body.SessionID, body.Force)
		wave4WriteJSON(w, code, payload)
	})
	r.Post("/api/shutdown", func(w http.ResponseWriter, req *http.Request) {
		wave4WriteJSON(w, 200, map[string]any{"status": "shutting_down"})
		go func() {
			time.Sleep(300 * time.Millisecond)
			_ = signalIgnoreTemp()
		}()
	})
}

func signalIgnoreTemp() error {
	p, _ := os.FindProcess(os.Getpid())
	return p.Signal(syscall.SIGINT)
}

var _ = exec.Command
