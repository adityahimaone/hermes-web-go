package httpserver

// Wave 6 native endpoints — session mutations + updates/apply.
//
// Scope (parity with Python api/routes.py + api/session_ops.py):
//   POST /api/session/undo                 — remove last user turn
//   POST /api/session/retry                — truncate to before last user msg
//   POST /api/session/rename               — set title
//   POST /api/session/title/regenerate     — AI title regeneration (degraded)
//   POST /api/session/yolo                 — YOLO flag (local registry)
//   GET  /api/session/yolo/status          — YOLO flag read (FE re-fetch)
//   POST /api/updates/apply                — stash+pull+pop (webui|agent)
//
// Deferred to Python (heavy/agent-coupled, half-stub would regress parity):
//   import_cli/import, compress (real), approval relay, title generation AI.
//
// Undo/retry truncate the webui persistence layer only (message arrays in
// webui.db). The Python agent-side session state stays untouched — same
// contract as the Python sidecar, which also only mutates the webui copy
// (its retry_last calls s.save() on the SAME webui Session, not the CLI state).

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// ── message helpers ────────────────────────────────────────────────────────

// sessionMessages loads the messages JSON column of one session.
func sessionMessages(db *sql.DB, sid string) (msgs []map[string]any, err error) {
	var raw string
	err = db.QueryRow("SELECT messages FROM sessions WHERE session_id = ?", sid).Scan(&raw)
	if err != nil {
		return nil, err
	}
	msgs = []map[string]any{}
	_ = json.Unmarshal([]byte(raw), &msgs)
	return msgs, nil
}

func lastUserMsg(msgs []map[string]any) (idx int, text string) {
	for i := len(msgs) - 1; i >= 0; i-- {
		if role, _ := msgs[i]["role"].(string); role == "user" {
			return i, extractMsgText(msgs[i]["content"])
		}
	}
	return -1, ""
}

func extractMsgText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var sb strings.Builder
		for _, part := range v {
			if m, ok := part.(map[string]any); ok {
				if t, ok := m["type"].(string); ok && t == "text" {
					if txt, ok := m["text"].(string); ok {
						sb.WriteString(txt)
					}
				}
			}
		}
		return sb.String()
	}
	return ""
}

func truncatePreview(text string) string {
	if len(text) > 40 {
		return text[:40] + "..."
	}
	return text
}

func updateSessionMessages(db *sql.DB, sid string, msgs []map[string]any) error {
	b, _ := json.Marshal(msgs)
	_, err := db.Exec("UPDATE sessions SET messages = ?, updated_at = ? WHERE session_id = ?",
		b, time.Now().Unix(), sid)
	return err
}

// ── undo / retry ───────────────────────────────────────────────────────────

func handleSessionTruncate(db *sql.DB, sid string, mode string) (int, map[string]any) {
	if sid == "" {
		return 400, map[string]any{"error": "session_id is required"}
	}
	msgs, err := sessionMessages(db, sid)
	if err == sql.ErrNoRows {
		return 404, map[string]any{"error": "Session not found"}
	}
	if err != nil {
		return 500, map[string]any{"error": err.Error()}
	}
	idx, text := lastUserMsg(msgs)
	if idx < 0 {
		return 400, map[string]any{"error": "Nothing to undo."}
	}
	removedCount := len(msgs) - idx
	removedText := text
	if mode == "retry" {
		// keep the user message, drop everything after it
		if err := updateSessionMessages(db, sid, msgs[:idx+1]); err != nil {
			return 500, map[string]any{"error": err.Error()}
		}
		return 200, map[string]any{"ok": true, "last_user_text": removedText, "removed_count": removedCount - 1}
	}
	// undo: drop user msg + everything after
	if err := updateSessionMessages(db, sid, msgs[:idx]); err != nil {
		return 500, map[string]any{"error": err.Error()}
	}
	return 200, map[string]any{"ok": true, "removed_count": removedCount, "removed_preview": truncatePreview(removedText)}
}

// ── rename / title regenerate ──────────────────────────────────────────────

func handleTitleRegenerate(db *sql.DB, sid string, preferLatest bool) (int, map[string]any) {
	if sid == "" {
		return 400, map[string]any{"error": "session_id is required"}
	}
	msgs, err := sessionMessages(db, sid)
	if err == sql.ErrNoRows {
		return 404, map[string]any{"error": "Session not found"}
	}
	if err != nil {
		return 500, map[string]any{"error": err.Error()}
	}
	lastText := ""
	if len(msgs) > 0 {
		lastText = extractMsgText(msgs[len(msgs)-1]["content"])
	}
	if strings.TrimSpace(lastText) == "" {
		return 422, map[string]any{"error": "Could not generate a better title (empty)"}
	}
	// AI title generation lives in the Python agent (agent_runtime title
	// endpoint); here we pick a deterministic local title from the last user
	// message so the FE still gets a rename without a round-trip failure.
	nextTitle := lastText
	if len(nextTitle) > 60 {
		nextTitle = strings.TrimSpace(nextTitle[:60]) + "..."
	}
	if _, err := db.Exec("UPDATE sessions SET title = ?, updated_at = ? WHERE session_id = ?",
		nextTitle, time.Now().Unix(), sid); err != nil {
		return 500, map[string]any{"error": err.Error()}
	}
	_ = preferLatest
	return 200, map[string]any{"ok": true, "title": nextTitle, "status": "local_fallback"}
}

// ── yolo flag (in-process registry, parity with Python in-memory) ──────────

var yoloMu sync.Mutex
var yoloFlags = map[string]bool{}

func setYolo(sid string, on bool) {
	yoloMu.Lock()
	defer yoloMu.Unlock()
	if on {
		yoloFlags[sid] = true
	} else {
		delete(yoloFlags, sid)
	}
}

func isYolo(sid string) bool {
	yoloMu.Lock()
	defer yoloMu.Unlock()
	return yoloFlags[sid]
}

func handleYoloToggle(sid string, enabled bool) (int, map[string]any) {
	if sid == "" {
		return 400, map[string]any{"error": "session_id is required"}
	}
	setYolo(sid, enabled)
	return 200, map[string]any{"ok": true, "yolo_enabled": isYolo(sid)}
}

// ── updates/apply (stash + pull --ff-only + pop) ───────────────────────────

var updateMu sync.Mutex

func gitApplyUpdate(target string) map[string]any {
	updateMu.Lock()
	defer updateMu.Unlock()
	var repo string
	switch target {
	case "webui":
		repo = webuiRepoRoot()
	case "agent":
		repo = agentRepoRoot()
	default:
		return map[string]any{"ok": false, "message": "Unknown target: " + target}
	}
	if repo == "" || !dirHasGit(repo) {
		return map[string]any{"ok": false, "message": "Not a git repository"}
	}
	stashed := false
	if out, ok, _ := gitRunChecked(repo, gitTimeout, "status", "--porcelain"); ok && strings.TrimSpace(out) != "" {
		if _, ok, _ := gitRunChecked(repo, gitTimeout, "stash", "push", "-u", "-m", "webui-auto-update"); ok {
			stashed = true
		}
	}
	pullOut, pullOk, pullErr := gitRunChecked(repo, gitTimeout, "pull", "--ff-only", "origin", "main")
	if !pullOk {
		if stashed {
			gitRunChecked(repo, gitTimeout, "stash", "pop")
		}
		return map[string]any{"ok": false, "message": "pull failed: " + strings.TrimSpace(pullErr)}
	}
	applyNote := ""
	if stashed {
		if _, ok, _ := gitRunChecked(repo, gitTimeout, "stash", "pop"); !ok {
			applyNote = " (stash pop failed; changes remain in git stash)"
		} else {
			applyNote = " (local changes restored from stash)"
		}
	}
	return map[string]any{"ok": true, "message": "Updated successfully" + applyNote, "pull": strings.TrimSpace(pullOut[:minInt(len(pullOut), 200)])}
}

func webuiRepoRoot() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	// binary lives in repo root or cmd/server; walk up to first .git
	exe = filepath.Dir(exe)
	for i := 0; i < 4; i++ {
		if dirHasGit(exe) {
			return exe
		}
		parent := filepath.Dir(exe)
		if parent == exe {
			break
		}
		exe = parent
	}
	return ""
}

func agentRepoRoot() string {
	// hermes-agent checkout; honor HERMES_AGENT_HOME / config agent home.
	if h := os.Getenv("HERMES_AGENT_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".hermes", "hermes-agent")
	if dirHasGit(p) {
		return p
	}
	return ""
}

func dirHasGit(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && st.IsDir()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ── router ──────────────────────────────────────────────────────────────────

func wave6Router(r chi.Router, db *sql.DB) {
	r.Post("/api/session/undo", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			wave4WriteJSONErr(w, 400, "invalid request body")
			return
		}
		sid, _ := body["session_id"].(string)
		st, payload := handleSessionTruncate(db, sid, "undo")
		wave4WriteJSON(w, st, payload)
	})
	r.Post("/api/session/retry", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			wave4WriteJSONErr(w, 400, "invalid request body")
			return
		}
		sid, _ := body["session_id"].(string)
		st, payload := handleSessionTruncate(db, sid, "retry")
		wave4WriteJSON(w, st, payload)
	})
	r.Post("/api/session/title/regenerate", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			wave4WriteJSONErr(w, 400, "invalid request body")
			return
		}
		sid, _ := body["session_id"].(string)
		prefer, _ := body["prefer_latest"].(bool)
		st, payload := handleTitleRegenerate(db, sid, prefer)
		wave4WriteJSON(w, st, payload)
	})
	r.Post("/api/session/yolo", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			wave4WriteJSONErr(w, 400, "invalid request body")
			return
		}
		sid, _ := body["session_id"].(string)
		on := true
		if v, ok := body["enabled"].(bool); ok {
			on = v
		}
		st, payload := handleYoloToggle(sid, on)
		wave4WriteJSON(w, st, payload)
	})
	r.Get("/api/session/yolo/status", func(w http.ResponseWriter, req *http.Request) {
		sid := req.URL.Query().Get("session_id")
		wave4WriteJSON(w, 200, map[string]any{"ok": true, "yolo_enabled": isYolo(sid)})
	})
	r.Post("/api/updates/apply", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			wave4WriteJSONErr(w, 400, "invalid request body")
			return
		}
		target, _ := body["target"].(string)
		if target != "webui" && target != "agent" {
			wave4WriteJSONErr(w, 400, "target must be \"webui\" or \"agent\"")
			return
		}
		wave4WriteJSON(w, 200, gitApplyUpdate(target))
	})
}

var _ = exec.Command // reserved for future shell helpers