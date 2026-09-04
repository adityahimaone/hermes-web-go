package httpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"hermes-web-go/internal/store"
)

// ── Wave 4: Git panel family ───────────────────────────────────────────────
//
// Ported from api/workspace_git.py + routes.py git handlers. Reuses the
// hardened git exec helper from git_info.go (scrubbed env, -c overrides,
// 5s status timeout). Mutations take a per-process lock and refuse
// destructive writes when the repo uses local filters (parity with Python
// _block_filtered_destructive_write).

const (
	gitRemoteTimeout   = 15 * time.Second
	gitDiffSizeLimit   = 500 * 1024
	gitDestructiveFlag = "HERMES_WEBUI_GIT_DESTRUCTIVE"
)

var gitMu sync.Mutex

// gitDestructiveEnabled mirrors workspace_git_destructive_enabled(): default
// OFF — mutations on repos with local filters are blocked unless the env
// flag is "1"/"true".
func gitDestructiveEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(gitDestructiveFlag)))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

// hasRepoLocalFilters detects config-local filter.*, merge.*, remote.* helper
// entries that could make a destructive write execute host commands.
func hasRepoLocalFilters(dir string) bool {
	out, ok := runGit(dir, gitTimeout, "config", "--local", "--name-only", "--get-regexp", "^(filter|merge|remote)\\.")
	if !ok {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "filter.") || strings.HasPrefix(line, "merge.") {
			return true
		}
		out2, ok2 := runGit(dir, gitTimeout, "config", "--local", "--get", "remote."+strings.TrimPrefix(line, "remote.")+".vcs")
		if ok2 && strings.Contains(out2, "git") {
			return true
		}
	}
	return false
}

// gitMutationGuard blocks destructive writes on filter-hosting repos unless
// the explicit env override is set (Python _block_filtered_destructive_write).
func gitMutationGuard(workspace string) error {
	if gitDestructiveEnabled() {
		return nil
	}
	if hasRepoLocalFilters(workspace) {
		return newGitErr("Repository uses local Git filters; operation blocked. Use the terminal or set HERMES_WEBUI_GIT_DESTRUCTIVE=1.", "filtered_destructive_blocked")
	}
	return nil
}

type gitErr struct {
	msg  string
	code string
}

func newGitErr(msg, code string) *gitErr { return &gitErr{msg: msg, code: code} }
func (e *gitErr) Error() string          { return e.msg }

// gitSessionWorkspace resolves the workspace for a session_id like Python
// _git_session_workspace: (db lookup, empty => 400, absent => 404).
func gitSessionWorkspace(db *sql.DB, w http.ResponseWriter, sid string) (string, bool) {
	sid = strings.TrimSpace(sid)
	if sid == "" {
		writeError(w, http.StatusBadRequest, "session_id required")
		return "", false
	}
	if db == nil {
		writeError(w, http.StatusServiceUnavailable, "data store unavailable")
		return "", false
	}
	row, err := store.GetSession(db, sid)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "Session not found")
		return "", false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load session")
		return "", false
	}
	if row.Workspace == "" {
		writeError(w, http.StatusBadRequest, "session has no workspace")
		return "", false
	}
	return row.Workspace, true
}

// statusTotals mirrors the Python totals dict.
type statusTotals struct {
	files, added, removed, modified, untracked, staged, unstaged int
}

// gitStatusV2 runs git status --porcelain=v2 -z --branch and returns the
// Python-shaped status payload.
func gitStatusV2(workspace string) map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := gitCommand(ctx, "status", "--porcelain=v2", "-z", "--branch", "--ignored=matching", "--untracked-files=all", "--", ".")
	cmd.Dir = workspace
	out, err := cmd.Output()
	if err != nil {
		return map[string]any{"is_git": false}
	}
	branch, upstream, ahead, behind := "", "", 0, 0
	files := []map[string]any{}
	totals := statusTotals{}
	// -z splits on NUL; branch header lines start "# ".
	for _, rec := range strings.Split(string(out), "\x00") {
		if rec == "" {
			continue
		}
		if strings.HasPrefix(rec, "# ") {
			parts := strings.SplitN(rec, " ", 3)
			if len(parts) < 3 {
				continue
			}
			switch parts[1] {
			case "branch.head":
				branch = parts[2]
				if branch == "(detached)" {
					branch = ""
				}
			case "branch.upstream":
				upstream = parts[2]
			case "branch.ab":
				for _, bit := range strings.Fields(parts[2]) {
					if strings.HasPrefix(bit, "+") {
						if n, e := strconvAtoiSafe(bit[1:]); e == nil {
							ahead = n
						}
					} else if strings.HasPrefix(bit, "-") {
						if n, e := strconvAtoiSafe(bit[1:]); e == nil {
							behind = n
						}
					}
				}
			}
			continue
		}
		if strings.HasPrefix(rec, "? ") {
			path := rec[2:]
			files = append(files, map[string]any{"path": path, "status": "??", "staged": false, "unstaged": false, "untracked": true, "ignored": false, "renamed": false})
			totals.files++
			totals.untracked++
			continue
		}
		if strings.HasPrefix(rec, "! ") {
			path := rec[2:]
			files = append(files, map[string]any{"path": path, "status": "!!", "staged": false, "unstaged": false, "untracked": false, "ignored": true, "renamed": false})
			totals.files++
			continue
		}
		if strings.HasPrefix(rec, "1 ") {
			parts := strings.Split(rec, " ")
			// 1 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <path>
			if len(parts) < 9 {
				continue
			}
			xy := parts[1]
			path := strings.Join(parts[8:], " ")
			// strip quotes if -z didn't (occurs with special chars)
			path = strings.Trim(path, `"`)
			staged := strings.Trim(xy[:1], ".") != ""
			unstaged := strings.Trim(xy[1:2], ".") != ""
			code := "M"
			if len(xy) >= 2 && xy[1] == '?' {
				code = "??"
			}
			files = append(files, map[string]any{"path": path, "status": xy, "staged": staged, "unstaged": unstaged, "untracked": false, "ignored": false, "renamed": false})
			totals.files++
			if strings.Contains(xy, "A") {
				totals.added++
			}
			if strings.Contains(xy, "D") {
				totals.removed++
			}
			if staged {
				totals.staged++
			}
			if unstaged {
				totals.unstaged++
				if xy[0] == ' ' || xy[0] == '?' {
					// leaf-level M
				}
			}
			if strings.Contains(xy, "M") || strings.Contains(xy, "A") || strings.Contains(xy, "D") {
				totals.modified++
			}
			_ = code
			continue
		}
		if strings.HasPrefix(rec, "2 ") {
			parts := strings.Split(rec, " ")
			// 2 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <X.score> <path> <origPath>
			if len(parts) < 10 {
				continue
			}
			xy := parts[1]
			path := strings.Trim(strings.Join(parts[9:], " "), `"`)
			files = append(files, map[string]any{"path": path, "status": xy, "staged": strings.Trim(xy[:1], ".") != "", "unstaged": strings.Trim(xy[1:2], ".") != "", "untracked": false, "ignored": false, "renamed": true})
			totals.files++
			if strings.Contains(xy, "R") {
				totals.modified++
			}
			if strings.Trim(xy[:1], ".") != "" {
				totals.staged++
			}
			if strings.Trim(xy[1:2], ".") != "" {
				totals.unstaged++
			}
			continue
		}
	}
	return map[string]any{
		"is_git":   true,
		"branch":   branch,
		"upstream": upstream,
		"ahead":    ahead,
		"behind":   behind,
		"totals": map[string]any{
			"files":     totals.files,
			"added":     totals.added,
			"removed":   totals.removed,
			"modified":  totals.modified,
			"untracked": totals.untracked,
			"staged":    totals.staged,
			"unstaged":  totals.unstaged,
		},
		"files":     files,
		"truncated": false,
		"noise_filtering": map[string]any{
			"filemode_only": 0,
			"crlf_only":     0,
			"active":        false,
		},
	}
}

// strconvAtoiSafe avoids importing strconv in tight loop; keep tiny.
func strconvAtoiSafe(s string) (int, error) {
	n := 0
	if s == "" {
		return 0, errors.New("empty")
	}
	neg := false
	if s[0] == '-' {
		neg = true
		s = s[1:]
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("bad digit")
		}
		n = n*10 + int(c-'0')
	}
	if neg {
		n = -n
	}
	return n, nil
}

// gitBranchesV2 returns the branches payload (local + remote refs).
func gitBranchesV2(workspace string) map[string]any {
	headName, _, _ := gitRunChecked(workspace, gitTimeout, "branch", "--show-current")
	headSha, _, _ := gitRunChecked(workspace, gitTimeout, "rev-parse", "--short", "HEAD")
	status := gitStatusV2(workspace)
	local := gitForEachRef(workspace, "refs/heads")
	remote := gitForEachRef(workspace, "refs/remotes")
	return map[string]any{
		"is_git":   true,
		"current":  strings.TrimSpace(headName),
		"detached": false,
		"head":     strings.TrimSpace(headSha),
		"local":    local,
		"remote":   remote,
		"upstream": status["upstream"],
		"ahead":    status["ahead"],
		"behind":   status["behind"],
	}
}

// gitForEachRef formats refs into the Python _for_each_ref shape.
func gitForEachRef(workspace, prefix string) []map[string]any {
	fmtStr := "%(refname)%00%(refname:short)%00%(upstream:short)%00%(objectname:short)%00%(committerdate:unix)%00%(committerdate:relative)%00%(authorname)%00%(subject)"
	out, ok, _ := gitRunChecked(workspace, gitTimeout, "for-each-ref", "--format="+fmtStr, prefix)
	if !ok {
		return []map[string]any{}
	}
	refs := []map[string]any{}
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\x00")
		for len(fields) < 8 {
			fields = append(fields, "")
		}
		name := fields[1]
		if name == "" || strings.HasSuffix(name, "/HEAD") {
			continue
		}
		item := map[string]any{
			"name": name, "sha": fields[3],
			"updated": func() int64 {
				n, e := strconvAtoiSafe(fields[4])
				if e != nil {
					return 0
				}
				return int64(n)
			}(),
			"updated_relative": fields[5], "author": fields[6], "subject": fields[7],
			"upstream": fields[2], "ahead": 0, "behind": 0,
		}
		if fields[2] != "" {
			ahead, behind := branchAheadBehind(workspace, name, fields[2])
			item["ahead"], item["behind"] = ahead, behind
		}
		refs = append(refs, item)
	}
	sort.Slice(refs, func(i, j int) bool {
		return strings.ToLower(refs[i]["name"].(string)) < strings.ToLower(refs[j]["name"].(string))
	})
	return refs
}

func branchAheadBehind(workspace, branch, upstream string) (int, int) {
	out, ok, _ := gitRunChecked(workspace, gitTimeout, "rev-list", "--left-right", "--count", branch+"..."+upstream)
	if !ok {
		return 0, 0
	}
	parts := strings.Fields(out)
	if len(parts) != 2 {
		return 0, 0
	}
	a, e1 := strconvAtoiSafe(parts[0])
	b, e2 := strconvAtoiSafe(parts[1])
	if e1 != nil || e2 != nil {
		return 0, 0
	}
	return a, b
}

// gitRunChecked is a thin wrapper returning (stdout, ok, stderr).
func gitRunChecked(dir string, timeout time.Duration, args ...string) (string, bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := gitCommand(ctx, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out)), false, string(out)
	}
	return strings.TrimSpace(string(out)), true, ""
}

func newGitErr2(_ any, msg, code string) { /* placeholder */ }

// gitDiffV2 returns the diff payload for one path.
func gitDiffV2(workspace, path, kind string) map[string]any {
	if kind != "unstaged" && kind != "staged" {
		return map[string]any{"error": "kind must be staged or unstaged", "code": "git_failed"}
	}
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--unified=3"}
	if kind == "staged" {
		args = append(args, "--cached")
	}
	args = append(args, "--", path)
	out, ok, _ := gitRunChecked(workspace, gitTimeout, args...)
	if !ok {
		return map[string]any{"error": "git diff failed", "code": "git_failed"}
	}
	binary := strings.Contains(out, "Binary files ") || strings.Contains(out, "GIT binary patch")
	tooLarge := len(out) > gitDiffSizeLimit
	if tooLarge {
		out = out[:gitDiffSizeLimit]
	}
	additions, deletions := diffStats(out)
	return map[string]any{
		"path": path, "kind": kind, "binary": binary, "too_large": tooLarge,
		"additions": additions, "deletions": deletions,
		"diff": func() string {
			if binary {
				return ""
			}
			return out
		}(),
	}
}

func diffStats(diff string) (int, int) {
	add, del := 0, 0
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			add++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			del++
		}
	}
	return add, del
}

// mutationShell runs a git mutation under the process lock with the
// destructive guard; returns payload.
func mutationShell(workspace string, fn func() (map[string]any, error)) (map[string]any, error) {
	gitMu.Lock()
	defer gitMu.Unlock()
	if err := gitMutationGuard(workspace); err != nil {
		return nil, err
	}
	return fn()
}

func gitStage(workspace string, paths []string) (map[string]any, error) {
	return mutationShell(workspace, func() (map[string]any, error) {
		args := []string{"add", "--"}
		args = append(args, paths...)
		_, ok, stderr := gitRunChecked(workspace, gitTimeout, args...)
		if !ok {
			return nil, newGitErr(strings.TrimSpace(stderr), "git_failed")
		}
		return gitStatusV2(workspace), nil
	})
}

func gitUnstage(workspace string, paths []string) (map[string]any, error) {
	return mutationShell(workspace, func() (map[string]any, error) {
		args := []string{"restore", "--staged", "--"}
		args = append(args, paths...)
		_, ok, stderr := gitRunChecked(workspace, gitTimeout, args...)
		if !ok {
			// fallback like Python: git reset HEAD --
			resetArgs := []string{"reset", "HEAD", "--"}
			resetArgs = append(resetArgs, paths...)
			if _, ok2, stderr2 := gitRunChecked(workspace, gitTimeout, resetArgs...); !ok2 {
				return nil, newGitErr(strings.TrimSpace(stderr2), "git_failed")
			}
			_ = stderr
		}
		return gitStatusV2(workspace), nil
	})
}

func gitDiscard(workspace string, paths []string, deleteUntracked bool) (map[string]any, error) {
	return mutationShell(workspace, func() (map[string]any, error) {
		if deleteUntracked {
			// git clean -fd for untracked paths (dangerous — only when flag set)
			cleanArgs := []string{"clean", "-fd", "--"}
			cleanArgs = append(cleanArgs, paths...)
			gitRunChecked(workspace, gitTimeout, cleanArgs...)
		}
		args := []string{"checkout", "--"}
		args = append(args, paths...)
		_, ok, stderr := gitRunChecked(workspace, gitTimeout, args...)
		if !ok {
			return nil, newGitErr(strings.TrimSpace(stderr), "git_failed")
		}
		return gitStatusV2(workspace), nil
	})
}

func gitCommit(workspace, message string) (map[string]any, error) {
	msg := strings.TrimSpace(message)
	if msg == "" {
		return nil, newGitErr("Commit message is required", "git_failed")
	}
	return mutationShell(workspace, func() (map[string]any, error) {
		_, ok, stderr := gitRunChecked(workspace, 10*time.Second, "commit", "-m", msg)
		if !ok {
			return nil, newGitErr(strings.TrimSpace(stderr), "git_failed")
		}
		sha, _, _ := gitRunChecked(workspace, gitTimeout, "rev-parse", "--short", "HEAD")
		return map[string]any{"ok": true, "commit": strings.TrimSpace(sha), "status": gitStatusV2(workspace)}, nil
	})
}

func gitRemote(workspace, action string) (map[string]any, error) {
	return mutationShell(workspace, func() (map[string]any, error) {
		var args []string
		switch action {
		case "fetch":
			args = []string{"fetch", "--prune", "--no-recurse-submodules"}
		case "pull":
			args = []string{"pull", "--ff-only", "--no-recurse-submodules"}
		case "push":
			args = []string{"push"}
			st := gitStatusV2(workspace)
			if st["upstream"] == "" || st["upstream"] == nil {
				if _, ok, _ := gitRunChecked(workspace, gitTimeout, "remote"); !ok {
					return nil, newGitErr("No upstream branch or origin remote is configured", "no_upstream")
				}
				branch := strings.TrimSpace(func() string { b, _, _ := gitRunChecked(workspace, gitTimeout, "branch", "--show-current"); return b }())
				if branch == "" {
					return nil, newGitErr("No upstream branch or origin remote is configured", "no_upstream")
				}
				args = append(args, "-u", "origin", branch)
			}
		}
		out, ok, stderr := gitRunChecked(workspace, gitRemoteTimeout, args...)
		if !ok {
			return nil, newGitErr(strings.TrimSpace(stderr), "git_failed")
		}
		return map[string]any{"ok": true, "message": strings.TrimSpace(out), "status": gitStatusV2(workspace)}, nil
	})
}

func gitCheckout(workspace, ref, mode string, newBranch any, track bool, dirtyMode string) (map[string]any, error) {
	return mutationShell(workspace, func() (map[string]any, error) {
		if dirtyMode != "discard" && dirtyMode != "" {
			// default block: refuse if dirty
			st := gitStatusV2(workspace)
			if t, ok := st["totals"].(map[string]any); ok {
				if n, _ := t["files"].(int); n > 0 {
					return nil, newGitErr("Working tree has uncommitted changes; use discard mode or stash-checkout", "dirty_workspace")
				}
			}
		}
		args := []string{"checkout"}
		if nb, ok := newBranch.(string); ok && nb != "" {
			args = append(args, "-b", nb)
		} else if track {
			args = append(args, "-t")
		}
		args = append(args, ref)
		_, ok, stderr := gitRunChecked(workspace, gitTimeout, args...)
		if !ok {
			return nil, newGitErr(strings.TrimSpace(stderr), "git_failed")
		}
		branch, _, _ := gitRunChecked(workspace, gitTimeout, "branch", "--show-current")
		return map[string]any{
			"ok":             true,
			"git":            gitStatusV2(workspace),
			"branches":       gitBranchesV2(workspace),
			"current_branch": strings.TrimSpace(branch),
			"message":        "checkout complete",
		}, nil
	})
}

func gitStashCheckout(workspace, ref, mode string, newBranch any, track bool) (map[string]any, error) {
	return mutationShell(workspace, func() (map[string]any, error) {
		// stash current work if dirty
		stashed := false
		st := gitStatusV2(workspace)
		if t, ok := st["totals"].(map[string]any); ok {
			if n, _ := t["files"].(int); n > 0 {
				if _, ok, stderr := gitRunChecked(workspace, gitTimeout, "stash", "push", "-m", "webui auto-stash"); ok {
					stashed = true
				} else {
					return nil, newGitErr(strings.TrimSpace(stderr), "git_failed")
				}
			}
		}
		args := []string{"checkout"}
		if nb, ok := newBranch.(string); ok && nb != "" {
			args = append(args, "-b", nb)
		} else if track {
			args = append(args, "-t")
		}
		args = append(args, ref)
		_, ok, stderr := gitRunChecked(workspace, gitTimeout, args...)
		if !ok {
			// try to restore stash on failure
			if stashed {
				gitRunChecked(workspace, gitTimeout, "stash", "pop")
			}
			return nil, newGitErr(strings.TrimSpace(stderr), "git_failed")
		}
		branch, _, _ := gitRunChecked(workspace, gitTimeout, "branch", "--show-current")
		restored := false
		restoreErr := ""
		if stashed {
			if _, ok, stderr := gitRunChecked(workspace, gitTimeout, "stash", "pop"); ok {
				restored = true
			} else {
				restoreErr = strings.TrimSpace(stderr)
			}
		}
		return map[string]any{
			"ok":             true,
			"git":            gitStatusV2(workspace),
			"branches":       gitBranchesV2(workspace),
			"current_branch": strings.TrimSpace(branch),
			"message":        "checkout complete",
			"stash_name":     "",
			"stashed":        stashed,
			"restored_stash": restored,
			"restore_failed": restoreErr != "",
			"restore_error":  restoreErr,
			"restore_stash":  restoreErr == "",
		}, nil
	})
}

// ── Router ─────────────────────────────────────────────────────────────────

// gitFamilyRouter mounts all /api/git/* endpoints.
func gitFamilyRouter(r chi.Router, db *sql.DB) {
	// GET status
	r.Get("/api/git/status", func(w http.ResponseWriter, req *http.Request) {
		ws, ok := gitSessionWorkspace(db, w, req.URL.Query().Get("session_id"))
		if !ok {
			return
		}
		writeJSON(w, map[string]any{"git": gitStatusV2(ws)})
	})
	// GET branches
	r.Get("/api/git/branches", func(w http.ResponseWriter, req *http.Request) {
		ws, ok := gitSessionWorkspace(db, w, req.URL.Query().Get("session_id"))
		if !ok {
			return
		}
		writeJSON(w, map[string]any{"branches": gitBranchesV2(ws)})
	})
	// GET diff
	r.Get("/api/git/diff", func(w http.ResponseWriter, req *http.Request) {
		ws, ok := gitSessionWorkspace(db, w, req.URL.Query().Get("session_id"))
		if !ok {
			return
		}
		path := req.URL.Query().Get("path")
		kind := req.URL.Query().Get("kind")
		if kind == "" {
			kind = "unstaged"
		}
		if path == "" {
			writeError(w, http.StatusBadRequest, "path required")
			return
		}
		writeJSON(w, gitDiffV2(ws, path, kind))
	})

	bodyHelper := func(w http.ResponseWriter, req *http.Request, fn func(workspace string, body map[string]any) (map[string]any, error)) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		sid, _ := body["session_id"].(string)
		ws, ok := gitSessionWorkspace(db, w, sid)
		if !ok {
			return
		}
		res, err := fn(ws, body)
		if err != nil {
			var ge *gitErr
			if errors.As(err, &ge) {
				writeError(w, http.StatusBadRequest, ge.msg)
				return
			}
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, res)
	}
	pathsFn := func(body map[string]any) []string {
		raw := body["paths"]
		if raw == nil && body["path"] != nil {
			raw = []any{body["path"]}
		}
		if s, ok := raw.(string); ok {
			raw = []any{s}
		}
		arr, ok := raw.([]any)
		if !ok {
			return []string{}
		}
		out := []string{}
		for _, v := range arr {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	}

	r.Post("/api/git/stage", func(w http.ResponseWriter, req *http.Request) {
		bodyHelper(w, req, func(ws string, body map[string]any) (map[string]any, error) {
			paths := pathsFn(body)
			if len(paths) == 0 {
				return nil, newGitErr("At least one path is required", "git_failed")
			}
			st, err := gitStage(ws, paths)
			if err != nil {
				return nil, err
			}
			return map[string]any{"ok": true, "git": st}, nil
		})
	})
	r.Post("/api/git/unstage", func(w http.ResponseWriter, req *http.Request) {
		bodyHelper(w, req, func(ws string, body map[string]any) (map[string]any, error) {
			paths := pathsFn(body)
			if len(paths) == 0 {
				return nil, newGitErr("At least one path is required", "git_failed")
			}
			st, err := gitUnstage(ws, paths)
			if err != nil {
				return nil, err
			}
			return map[string]any{"ok": true, "git": st}, nil
		})
	})
	r.Post("/api/git/discard", func(w http.ResponseWriter, req *http.Request) {
		bodyHelper(w, req, func(ws string, body map[string]any) (map[string]any, error) {
			paths := pathsFn(body)
			if len(paths) == 0 {
				return nil, newGitErr("At least one path is required", "git_failed")
			}
			deleteUntracked := false
			if b, _ := body["delete_untracked"].(bool); b {
				deleteUntracked = true
			}
			st, err := gitDiscard(ws, paths, deleteUntracked)
			if err != nil {
				return nil, err
			}
			return map[string]any{"ok": true, "git": st}, nil
		})
	})
	r.Post("/api/git/commit", func(w http.ResponseWriter, req *http.Request) {
		bodyHelper(w, req, func(ws string, body map[string]any) (map[string]any, error) {
			msg, _ := body["message"].(string)
			return gitCommit(ws, msg)
		})
	})
	r.Post("/api/git/commit-selected", func(w http.ResponseWriter, req *http.Request) {
		bodyHelper(w, req, func(ws string, body map[string]any) (map[string]any, error) {
			msg, _ := body["message"].(string)
			paths := pathsFn(body)
			if len(paths) == 0 {
				return nil, newGitErr("At least one path is required", "git_failed")
			}
			// stage selected first, then commit
			st, err := gitStage(ws, paths)
			if err != nil {
				return nil, err
			}
			_ = st
			return gitCommit(ws, msg)
		})
	})
	for _, action := range []string{"fetch", "pull", "push"} {
		act := action
		r.Post("/api/git/"+act, func(w http.ResponseWriter, req *http.Request) {
			bodyHelper(w, req, func(ws string, body map[string]any) (map[string]any, error) {
				return gitRemote(ws, act)
			})
		})
	}
	r.Post("/api/git/checkout", func(w http.ResponseWriter, req *http.Request) {
		bodyHelper(w, req, func(ws string, body map[string]any) (map[string]any, error) {
			ref, _ := body["ref"].(string)
			mode, _ := body["mode"].(string)
			if ref == "" {
				return nil, newGitErr("ref is required", "git_failed")
			}
			dirty := "block"
			if d, ok := body["dirty_mode"].(string); ok && d != "" {
				dirty = d
			}
			tr, _ := body["track"].(bool)
			return gitCheckout(ws, ref, mode, body["new_branch"], tr, dirty)
		})
	})
	r.Post("/api/git/stash-checkout", func(w http.ResponseWriter, req *http.Request) {
		bodyHelper(w, req, func(ws string, body map[string]any) (map[string]any, error) {
			ref, _ := body["ref"].(string)
			mode, _ := body["mode"].(string)
			if ref == "" {
				return nil, newGitErr("ref is required", "git_failed")
			}
			tr, _ := body["track"].(bool)
			return gitStashCheckout(ws, ref, mode, body["new_branch"], tr)
		})
	})
}

var _ = filepath.Join
