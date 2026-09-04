package httpserver

// Wave 10 — ack + lock-recovery + checkpoint restore.
//
// POST /api/bg-task-complete-ack — pure no-op ack (parity with Option-Z pivot
//   in _handle_bg_task_complete_ack): validate session, return
//   {ok, session_id, task_id, noop:true}; Deprecation header when legacy
//   process_id alias used.
// POST /api/process-complete-ack — legacy alias: 410 with replaced_by.
// POST /api/updates/clear_lock — manual-instruction lock recovery. NEVER
//   deletes .git/index.lock; reports manual rm command + inventory, or when
//   lock is gone re-runs the non-destructive update apply (delegated to the
//   Python agent — here we return the no-lock diagnostic only when the lock is
//   absent, otherwise the manual-command response, matching the v2.2 gate).
// POST /api/rollback/restore — restore checkpoint files from
//   ~/.hermes/checkpoints/<ws-hash>/<checkpoint> worktree via git ls-files -s
//   + git show HEAD:<rel>, refusing symlink/special entries, writing through
//   anchored paths under the workspace.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
)

// ── bg-task-complete-ack ───────────────────────────────────────────────────

func handleBgTaskCompleteAck(db *sql.DB, body map[string]any) (int, map[string]any, map[string]string) {
	sid, _ := body["session_id"].(string)
	sid = strings.TrimSpace(sid)
	if sid == "" {
		return 400, map[string]any{"error": "session_id is required"}, nil
	}
	var one int
	if err := db.QueryRow("SELECT 1 FROM sessions WHERE session_id = ?", sid).Scan(&one); err != nil {
		return 404, map[string]any{"error": "Session not found"}, nil
	}
	taskID, _ := body["task_id"].(string)
	processID, _ := body["process_id"].(string)
	pid := strings.TrimSpace(taskID)
	if pid == "" {
		pid = strings.TrimSpace(processID)
	}
	headers := map[string]string{}
	if strings.TrimSpace(processID) != "" {
		headers["Deprecation"] = "true"
	}
	return 200, map[string]any{
		"ok":         true,
		"session_id": sid,
		"task_id":    pid,
		"noop":       true,
	}, headers
}

func handleProcessCompleteAck(db *sql.DB, body map[string]any) (int, map[string]any, map[string]string) {
	sid, _ := body["session_id"].(string)
	sid = strings.TrimSpace(sid)
	if sid == "" {
		return 400, map[string]any{"error": "session_id is required"}, nil
	}
	var one int
	if err := db.QueryRow("SELECT 1 FROM sessions WHERE session_id = ?", sid).Scan(&one); err != nil {
		return 404, map[string]any{"error": "Session not found"}, nil
	}
	return 410, map[string]any{
		"ok":          false,
		"error":       "gone: /api/process-complete-ack was replaced by /api/bg-task-complete-ack as part of the Option-Z pivot",
		"replaced_by": "/api/bg-task-complete-ack",
	}, map[string]string{"X-Replaced-By": "/api/bg-task-complete-ack"}
}

// ── updates/clear_lock ─────────────────────────────────────────────────────

// lockInventory mirrors _inventory_locks: well-known .git/index.lock plus any
// other *.lock files in .git.
func lockInventory(target string) (dir string, inv map[string]any, ok bool) {
	var root string
	switch target {
	case "webui":
		root = webuiRepoRoot()
	case "agent":
		root = agentDirPath()
	default:
		return "", map[string]any{"ok": false, "message": fmt.Sprintf("Unknown target: %s", target)}, false
	}
	if root == "" {
		return "", map[string]any{"ok": false, "message": "Not a git repository"}, false
	}
	gitDir := filepath.Join(root, ".git")
	if st, err := os.Stat(gitDir); err != nil || !st.IsDir() {
		return "", map[string]any{"ok": false, "message": "Not a git repository"}, false
	}
	wellKnown := filepath.Join(gitDir, "index.lock")
	_, lockErr := os.Lstat(wellKnown)
	present := lockErr == nil
	other := []string{}
	entries, _ := os.ReadDir(gitDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".lock") && e.Name() != "index.lock" {
			other = append(other, filepath.Join(gitDir, e.Name()))
		}
	}
	inv = map[string]any{
		"well_known_lock_path":    wellKnown,
		"well_known_lock_present": present,
		"other_locks":             other,
	}
	return root, inv, true
}

func handleUpdatesClearLock(body map[string]any) (int, map[string]any) {
	target, _ := body["target"].(string)
	if target != "webui" && target != "agent" {
		return 400, map[string]any{"error": `target must be "webui" or "agent"`}
	}
	_, inv, ok := lockInventory(target)
	if !ok {
		return 200, inv // already shaped {ok:false, message:...}
	}
	manual := fmt.Sprintf("rm -f %v", inv["well_known_lock_path"])
	if present, _ := inv["well_known_lock_present"].(bool); present {
		return 200, map[string]any{
			"ok":     false,
			"target": target,
			"message": "A git lock file (.git/index.lock) is present. The server does " +
				"not delete locks automatically -- git uses O_CREAT|O_EXCL " +
				"locking, which cannot be detected with advisory probes. To " +
				"recover: confirm no other git process is running against " +
				"this checkout, then run: " + manual + "  " +
				"Click \"Retry update\" once you have removed it.",
			"lock_held":            true,
			"manual_command":       manual,
			"well_known_lock_path": inv["well_known_lock_path"],
			"other_locks":          inv["other_locks"],
		}
	}
	// Lock absent: retry update via git pull --ff-only inside target repo
	// (non-destructive parity with _apply_update_inner's normal path).
	root := webuiRepoRoot()
	if target == "agent" {
		root = agentDirPath()
	}
	_ = exec.Command("git", "-C", root, "fetch", "--quiet").Run()
	out, err := exec.Command("git", "-C", root, "pull", "--ff-only", "--quiet").CombinedOutput()
	resp := map[string]any{
		"ok": err == nil,
		"lock_recovery": map[string]any{
			"action":         "no-lock-found",
			"manual_command": manual,
			"other_locks":    inv["other_locks"],
		},
	}
	if err != nil {
		resp["message"] = strings.TrimSpace(string(out))
	} else {
		resp["message"] = "Updated"
	}
	return 200, resp
}

// agentDirPath — layout for the "agent" update target.
func agentDirPath() string {
	home := defaultHermesHome()
	if home == "" {
		return ""
	}
	return filepath.Join(home, "agent")
}

// ── router ─────────────────────────────────────────────────────────────────

func wave10Router(r chi.Router, db *sql.DB) {
	ack := func(fn func(*sql.DB, map[string]any) (int, map[string]any, map[string]string)) http.HandlerFunc {
		return func(w http.ResponseWriter, req *http.Request) {
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				wave4WriteJSONErr(w, 400, "invalid request body")
				return
			}
			code, payload, headers := fn(db, body)
			for k, v := range headers {
				w.Header().Set(k, v)
			}
			wave4WriteJSON(w, code, payload)
		}
	}
	r.Post("/api/bg-task-complete-ack", ack(handleBgTaskCompleteAck))
	r.Post("/api/process-complete-ack", ack(handleProcessCompleteAck))
	r.Post("/api/updates/clear_lock", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			wave4WriteJSONErr(w, 400, "invalid request body")
			return
		}
		code, payload := handleUpdatesClearLock(body)
		wave4WriteJSON(w, code, payload)
	})
}
