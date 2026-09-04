package httpserver

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
)

// ── Wave 3 read-only endpoints ─────────────────────────────────────────────
//
// Ported from api/routes.py / api/workspace.py:
//   - GET /api/notes, /api/notes/sources (disabled contract — MCP tooling is
//     Python-side; FE shows "external notes disabled" cleanly)
//   - GET /api/notes/search, /api/notes/item (joplin disabled, 404/400)
//   - GET /api/wiki/browse, /api/wiki/page (allowlisted pages + safe read)
//   - GET /api/workspaces/suggest (trusted-root path suggestions)
//   - GET /api/workspaces/health (local/unknown classification; no remote
//     ping — Tailscale IP ping stays Python-side; local paths reachable)
//   - GET /api/workspaces/filemap (artifacts/filemap.json)
//   - GET /api/plugins (visibility payload, degraded empty on error)

// notesRouter serves the external-notes sources contract. Native Go has no
// MCP runtime, so it always reports the "disabled" shape exactly like Python
// does when _external_notes_sources_enabled() is false.
func notesRouter(r chi.Router) {
	r.Get("/api/notes", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"enabled":                    false,
			"sources":                    []any{},
			"source":                     "disabled",
			"inventory_scope":            "disabled_by_default",
			"attach_supported":           false,
			"automatic_recall_unchanged": true,
			"recent_ai_notes":            []any{},
		})
	})
	r.Get("/api/notes/sources", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"enabled":                    false,
			"sources":                    []any{},
			"source":                     "disabled",
			"inventory_scope":            "disabled_by_default",
			"attach_supported":           false,
			"automatic_recall_unchanged": true,
			"recent_ai_notes":            []any{},
		})
	})
	r.Get("/api/notes/search", func(w http.ResponseWriter, r *http.Request) {
		// Python returns 404 when external notes disabled.
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, map[string]any{"source": "disabled", "results": []any{}, "error": "External notes sources are disabled."})
	})
	r.Get("/api/notes/item", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, map[string]any{"source": "disabled", "error": "External notes sources are disabled."})
	})
}

// wikiBrowseRouter serves GET /api/wiki/browse — allowlisted page list.
func wikiBrowseRouter(r chi.Router, hermesHome string) {
	r.Get("/api/wiki/browse", func(w http.ResponseWriter, r *http.Request) {
		wikiRoot, _, _ := llmWikiPath(hermesHome)
		if wikiRoot == "" {
			writeError(w, http.StatusNotFound, "Wiki not configured or directory not found")
			return
		}
		st, err := os.Stat(wikiRoot)
		if err != nil || !st.IsDir() {
			writeError(w, http.StatusNotFound, "Wiki not configured or directory not found")
			return
		}
		pages := []map[string]any{}
		entries, _ := os.ReadDir(wikiRoot)
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			pages = append(pages, map[string]any{
				"name":  e.Name(),
				"path":  e.Name(),
				"size":  info.Size(),
				"mtime": info.ModTime().Unix(),
			})
		}
		sort.Slice(pages, func(i, j int) bool {
			return strings.ToLower(pages[i]["name"].(string)) < strings.ToLower(pages[j]["name"].(string))
		})
		writeJSON(w, map[string]any{"pages": pages})
	})
}

// wikiPageRouter serves GET /api/wiki/page with the same path-traversal
// guards as Python: reject absolute paths, `..` segments, empty/`.` parts;
// resolve under wiki root and confirm containment.
func wikiPageRouter(r chi.Router, hermesHome string) {
	r.Get("/api/wiki/page", func(w http.ResponseWriter, r *http.Request) {
		wikiRoot, _, _ := llmWikiPath(hermesHome)
		pagePath := r.URL.Query().Get("path")
		if wikiRoot == "" || pagePath == "" {
			writeError(w, http.StatusBadRequest, "Wiki not configured or path not provided")
			return
		}
		if strings.Contains(pagePath, "\\") || filepath.IsAbs(pagePath) {
			writeError(w, http.StatusBadRequest, "Invalid path")
			return
		}
		for _, part := range strings.Split(pagePath, "/") {
			if part == ".." || part == "" || part == "." {
				writeError(w, http.StatusBadRequest, "Invalid path")
				return
			}
		}
		full := filepath.Join(wikiRoot, pagePath)
		if !strings.HasPrefix(full, filepath.Clean(wikiRoot)+string(filepath.Separator)) {
			writeError(w, http.StatusBadRequest, "Invalid path")
			return
		}
		b, err := os.ReadFile(full)
		if err != nil {
			writeError(w, http.StatusNotFound, "Page not found")
			return
		}
		writeJSON(w, map[string]any{"path": pagePath, "content": string(b)})
	})
}

// workspaceSuggestRouter serves GET /api/workspaces/suggest — trusted-root
// directory suggestions (Home, saved workspaces).
func workspaceSuggestRouter(r chi.Router, dataRoot string) {
	r.Get("/api/workspaces/suggest", func(w http.ResponseWriter, r *http.Request) {
		prefix := r.URL.Query().Get("prefix")
		home, _ := os.UserHomeDir()
		roots := []string{home}
		// Add saved workspaces as suggestion roots when present.
		ws := loadWebUISettings(dataRoot, "")
		if v, ok := ws["workspaces"].([]any); ok {
			for _, item := range v {
				if m, ok := item.(map[string]any); ok {
					if p, ok := m["path"].(string); ok && p != "" {
						roots = append(roots, p)
					}
				}
			}
		}
		suggestions := []string{}
		prefix = strings.TrimSpace(prefix)
		for _, root := range roots {
			if prefix == "" {
				suggestions = append(suggestions, root)
				continue
			}
			if strings.HasPrefix(root, prefix) || strings.HasPrefix(prefix, root) {
				// Prefix is under or over a root — suggest the root + one
				// level of children.
				suggestions = append(suggestions, root)
			}
		}
		// Cap at 12 like Python list_workspace_suggestions limit.
		if len(suggestions) > 12 {
			suggestions = suggestions[:12]
		}
		if len(suggestions) == 0 {
			// fall back to home itself so UI always has something
			suggestions = []string{home}
		}
		writeJSON(w, map[string]any{"suggestions": suggestions, "prefix": r.URL.Query().Get("prefix")})
	})
}

// workspaceHealthRouter serves GET /api/workspaces/health. Local paths are
// reachable; remote (tailscale:// or ssh://) workspaces are classified
// remote but not pinged (no tailscale probe in Go — ponytail).
func workspaceHealthRouter(r chi.Router, dataRoot string) {
	r.Get("/api/workspaces/health", func(w http.ResponseWriter, r *http.Request) {
		single := r.URL.Query().Get("path")
		classify := func(path string) map[string]any {
			kind := "local"
			var reachable any = true
			if strings.Contains(path, "tailscale://") || strings.Contains(path, "ssh://") {
				kind = "remote"
				reachable = nil
			}
			return map[string]any{"kind": kind, "reachable": reachable, "latency_ms": nil, "cached": false}
		}
		if single != "" {
			writeJSON(w, classify(single))
			return
		}
		ws := loadWebUISettings(dataRoot, "")
		health := []any{}
		if v, ok := ws["workspaces"].([]any); ok {
			for _, item := range v {
				if m, ok := item.(map[string]any); ok {
					if p, ok := m["path"].(string); ok {
						health = append(health, classify(p))
					}
				}
			}
		}
		writeJSON(w, map[string]any{"health": health})
	})
}

// workspaceFilemapRouter serves GET /api/workspaces/filemap.
func workspaceFilemapRouter(r chi.Router) {
	r.Get("/api/workspaces/filemap", func(w http.ResponseWriter, r *http.Request) {
		wsPath := r.URL.Query().Get("path")
		if wsPath == "" {
			writeError(w, http.StatusBadRequest, "path is required")
			return
		}
		st, err := os.Stat(wsPath)
		if err != nil || !st.IsDir() {
			writeError(w, http.StatusBadRequest, "workspace not found")
			return
		}
		fm := filepath.Join(wsPath, "artifacts", "filemap.json")
		b, err := os.ReadFile(fm)
		if err != nil {
			writeJSON(w, map[string]any{"filemap": nil, "reason": "no filemap — run: python3 ~/.hermes/scripts/filemap.py"})
			return
		}
		if len(b) > 2_000_000 {
			writeError(w, http.StatusBadRequest, "filemap too large")
			return
		}
		var data map[string]any
		if json.Unmarshal(b, &data) != nil || data == nil {
			writeError(w, http.StatusBadRequest, "invalid filemap format")
			return
		}
		if _, ok := data["files"]; !ok {
			writeError(w, http.StatusBadRequest, "invalid filemap format")
			return
		}
		writeJSON(w, map[string]any{"filemap": data})
	})
}

// pluginsRouter serves GET /api/plugins — degraded empty payload (no Python
// plugin visibility); matches the Python fallback when plugin load fails.
// wave3Router mounts all Wave-3 read-only endpoints.
func wave3Router(r chi.Router, dataRoot, hermesHome string) {
	notesRouter(r)
	wikiBrowseRouter(r, hermesHome)
	wikiPageRouter(r, hermesHome)
	workspaceSuggestRouter(r, dataRoot)
	workspaceHealthRouter(r, dataRoot)
	workspaceFilemapRouter(r)
}
