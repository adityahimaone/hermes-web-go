package httpserver

// Wave 15 — real updates/check (branch-based git diff).
//
// GET/POST /api/updates/check — replace the `disabled` stub with the real
// branch-check port of api/updates.py._check_repo_branch: fetch origin,
// resolve upstream (or default branch), rev-list behind count, merge-base
// short SHA for the compare base, compare URL. Release-tag logic
// (_check_repo_release) is NOT ported — this repo tracks main via ff-only
// pulls (see updates/clear_lock), so branch comparison is the live behavior.
// Cache: 10-min TTL keyed on include_agent (parity with CACHE_TTL).

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const updatesCheckTTL = 10 * time.Minute

var updatesCacheMu sync.Mutex
var updatesCache = map[string]map[string]any{}

func gitOut(dir string, timeout time.Duration, args ...string) (string, bool) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err == nil
}

func normalizeRemoteURL(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return remote
	}
	if strings.HasPrefix(remote, "git@") {
		remote = strings.Replace(remote, ":", "/", 1)
		remote = strings.Replace(remote, "git@", "https://", 1)
	}
	remote = strings.TrimRight(remote, "/")
	if strings.HasSuffix(remote, ".git") {
		remote = remote[:len(remote)-4]
	}
	return strings.TrimRight(remote, "/")
}

func buildCompareURL(repoURL, currentSHA, latestSHA string) any {
	if repoURL == "" || currentSHA == "" || latestSHA == "" {
		return nil
	}
	if !strings.HasPrefix(repoURL, "https://") && !strings.HasPrefix(repoURL, "http://") {
		return nil
	}
	return repoURL + "/compare/" + currentSHA + "..." + latestSHA
}

func detectDefaultBranch(path string) string {
	out, ok := gitOut(path, 5*time.Second, "symbolic-ref", "refs/remotes/origin/HEAD")
	if ok && out != "" {
		parts := strings.Split(strings.TrimSpace(out), "/")
		return parts[len(parts)-1]
	}
	for _, b := range []string{"master", "main"} {
		if _, ok := gitOut(path, 5*time.Second, "rev-parse", "--verify", "origin/"+b); ok {
			return b
		}
	}
	return "main"
}

func isDirty(path string) bool {
	cmd := exec.Command("git", "diff-index", "--quiet", "HEAD", "--")
	cmd.Dir = path
	err := cmd.Run()
	return err != nil
}

// checkRepoBranch ports _check_repo_branch (fetch + upstream compare).
func checkRepoBranch(path, name string, fetch bool) map[string]any {
	if path == "" {
		return map[string]any{"name": name, "behind": nil, "no_git": true}
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		return map[string]any{"name": name, "behind": nil, "no_git": true}
	}
	if fetch {
		cmd := exec.Command("git", "fetch", "origin", "--quiet")
		cmd.Dir = path
		if err := cmd.Run(); err != nil {
			return map[string]any{"name": name, "behind": 0, "error": "fetch failed"}
		}
	}
	compareRef := ""
	out, ok := gitOut(path, 5*time.Second, "rev-parse", "--abbrev-ref", "@{upstream}")
	if ok && strings.TrimSpace(out) != "" {
		compareRef = strings.TrimSpace(out)
	} else {
		compareRef = "origin/" + detectDefaultBranch(path)
	}
	behindOut, ok := gitOut(path, 10*time.Second, "rev-list", "--count", "HEAD.."+compareRef)
	behind := 0
	if ok {
		s := strings.TrimSpace(behindOut)
		if s != "" {
			n := 0
			for _, c := range s {
				if c < '0' || c > '9' {
					n = -1
					break
				}
				n = n*10 + int(c-'0')
			}
			if n >= 0 {
				behind = n
			}
		}
	}
	current := any(nil)
	if mb, mbOK := gitOut(path, 5*time.Second, "merge-base", "HEAD", compareRef); mbOK && strings.TrimSpace(mb) != "" {
		if short, ok := gitOut(path, 5*time.Second, "rev-parse", "--short", strings.TrimSpace(mb)); ok && strings.TrimSpace(short) != "" {
			current = strings.TrimSpace(short)
		}
	}
	latest := ""
	if l, ok := gitOut(path, 5*time.Second, "rev-parse", "--short", compareRef); ok {
		latest = strings.TrimSpace(l)
	}
	remoteRaw, _ := gitOut(path, 5*time.Second, "remote", "get-url", "origin")
	repoURL := normalizeRemoteURL(remoteRaw)
	return map[string]any{
		"name":        name,
		"behind":      behind,
		"current_sha": current,
		"latest_sha":  latest,
		"branch":      compareRef,
		"repo_url":    repoURL,
		"compare_url": buildCompareURL(repoURL, strAny(current), latest),
	}
}

func strAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// updatesCheckPayload — cached check for both targets.
func updatesCheckPayload(force bool, includeAgent bool) map[string]any {
	key := "agent"
	if !includeAgent {
		key = "noagent"
	}
	updatesCacheMu.Lock()
	cached, ok := updatesCache[key]
	updatesCacheMu.Unlock()
	if !force && ok {
		if at, ok2 := cached["checked_at"].(time.Time); ok2 && time.Since(at) < updatesCheckTTL {
			resp := map[string]any{}
			for k, v := range cached {
				resp[k] = v
			}
			resp["cached"] = true
			return resp
		}
	}
	webui := checkRepoBranch(webuiRepoRoot(), "webui", true)
	webui["dirty"] = isDirty(webuiRepoRoot())
	agent := map[string]any{"name": "agent", "behind": 0, "ignored": true}
	if includeAgent {
		agent = checkRepoBranch(agentRepoRoot(), "agent", true)
		if agent["behind"] == nil {
			// no .git for agent — minimal parity shape
			agent = map[string]any{"name": "agent", "behind": nil, "no_git": true}
		} else {
			agent["dirty"] = isDirty(agentRepoRoot())
		}
	}
	payload := map[string]any{
		"webui":         webui,
		"agent":         agent,
		"checked_at":    time.Now(),
		"include_agent": includeAgent,
	}
	updatesCacheMu.Lock()
	updatesCache[key] = payload
	updatesCacheMu.Unlock()
	resp := map[string]any{}
	for k, v := range payload {
		resp[k] = v
	}
	return resp
}

// registerUpdatesCheckReal replaces the disabled stub handler.
func registerUpdatesCheckReal(r chi.Router, dataRoot, hermesHome string) {
	handle := func(w http.ResponseWriter, req *http.Request, forceBody bool) {
		settings := loadWebUISettings(dataRoot, hermesHome)
		enabled := true
		if v, ok := settings["check_for_updates"].(bool); ok {
			enabled = v
		}
		force := false
		if forceBody {
			force = true // POST = explicit check; keep simple
		}
		if !enabled && !force {
			writeJSON(w, map[string]any{"disabled": true})
			return
		}
		ignoreAgent := false
		if v, ok := settings["ignore_agent_updates"].(bool); ok {
			ignoreAgent = v
		}
		writeJSON(w, updatesCheckPayload(force, !ignoreAgent))
	}
	r.Get("/api/updates/check", func(w http.ResponseWriter, req *http.Request) {
		handle(w, req, false)
	})
	r.Post("/api/updates/check", func(w http.ResponseWriter, req *http.Request) {
		handle(w, req, true)
	})
}
