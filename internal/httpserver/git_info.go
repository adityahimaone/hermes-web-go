package httpserver

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"hermes-web-go/internal/store"
)

// gitTimeout mirrors api/workspace_git.py GIT_TIMEOUT.
const gitTimeout = 5 * time.Second

// gitHardenedConfig mirrors api/workspace_git.py _GIT_HARDENED_CONFIG: repo-local
// configuration must not turn read/status calls into host command execution.
var gitHardenedConfig = [][2]string{
	{"core.fsmonitor", "false"},
	{"core.sshCommand", "ssh"},
	{"core.askPass", ""},
	{"credential.helper", ""},
	{"protocol.ext.allow", "never"},
	{"core.gitProxy", ""},
	{"submodule.recurse", "false"},
	{"fetch.recurseSubmodules", "false"},
}

// gitEnvScrubKeys mirrors api/workspace_git.py _GIT_ENV_SCRUB_KEYS.
var gitEnvScrubKeys = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM",
	"GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS", "GIT_ASKPASS", "SSH_ASKPASS",
	"GIT_SSH", "GIT_SSH_COMMAND", "GIT_TERMINAL_PROMPT",
}

// gitEnvForCommand returns a scrubbed environment: repo-hostile GIT_* keys are
// dropped so a workspace .git/config (or ambient env) cannot redirect git into
// attacker-controlled helpers, matching the Python hardening.
func gitEnvForCommand() []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key := kv
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			key = kv[:idx]
		}
		if key == "" || strings.HasPrefix(key, "GIT_CONFIG_KEY_") || strings.HasPrefix(key, "GIT_CONFIG_VALUE_") {
			continue
		}
		scrubbed := false
		for _, k := range gitEnvScrubKeys {
			if key == k {
				scrubbed = true
				break
			}
		}
		if !scrubbed {
			out = append(out, kv)
		}
	}
	out = append(out, "GIT_TERMINAL_PROMPT=0")
	return out
}

// gitCommand builds a hardened `git` invocation: repo-local config is
// overridden via -c so status/rev-parse cannot trigger repo-configured
// filters, hooks, or helpers.
func gitCommand(ctx context.Context, args ...string) *exec.Cmd {
	argv := []string{"git"}
	for _, kv := range gitHardenedConfig {
		argv = append(argv, "-c", kv[0]+"="+kv[1])
	}
	argv = append(argv, args...)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = gitEnvForCommand()
	return cmd
}

// runGit runs `git <args>` in dir with a timeout; ok=false when git fails
// (non-git dir, missing git, timeout).
func runGit(dir string, timeout time.Duration, args ...string) (stdout string, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := gitCommand(ctx, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return strings.TrimSpace(string(out)), false
	}
	return strings.TrimSpace(string(out)), true
}

// gitStatus mirrors api/workspace_git.py git_status's /api/git-info shape:
// nil when the workspace is not a git repo, else a branch/dirty/modified/
// untracked/ahead/behind summary.
func gitStatus(workspace string) map[string]any {
	branchOut, ok := runGit(workspace, gitTimeout, "rev-parse", "--abbrev-ref", "HEAD")
	if !ok {
		return nil
	}
	branch := strings.TrimSpace(branchOut)
	changed, untracked := 0, 0
	statusOut, sok := runGit(workspace, gitTimeout, "status", "--porcelain")
	if sok {
		for _, line := range strings.Split(statusOut, "\n") {
			line = strings.TrimRight(line, "\r")
			if line == "" {
				continue
			}
			xy := line
			if len(xy) > 2 {
				xy = line[:2]
			}
			if strings.HasPrefix(xy, "??") {
				untracked++
				continue
			}
			changed++
		}
	}
	return map[string]any{
		"branch":    branch,
		"dirty":     changed,
		"modified":  changed,
		"untracked": untracked,
		"ahead":     0,
		"behind":    0,
		"is_git":    true,
	}
}

// gitInfoRouter adds native session-scoped workspace git info to r.
func gitInfoRouter(r chi.Router, db *sql.DB) {
	if db == nil {
		r.Get("/api/git-info", func(w http.ResponseWriter, req *http.Request) {
			writeError(w, http.StatusServiceUnavailable, "data store unavailable")
		})
		return
	}
	r.Get("/api/git-info", func(w http.ResponseWriter, req *http.Request) {
		sid := strings.TrimSpace(req.URL.Query().Get("session_id"))
		if sid == "" {
			writeError(w, http.StatusBadRequest, "session_id required")
			return
		}
		row, err := store.GetSession(db, sid)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "Session not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load session")
			return
		}
		ws := row.Workspace
		if ws == "" {
			writeError(w, http.StatusBadRequest, "session has no workspace")
			return
		}
		writeJSON(w, map[string]any{"git": gitStatus(ws)})
	})
}
