package httpserver

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	hermesauth "hermes-web-go/internal/auth"
)

// ── Wave 2 quick-win reads ─────────────────────────────────────────────────
//
// Ported from api/routes.py / api/commands.py / api/config.py:
//   - POST /api/auth/logout        (clear cookie + invalidate session)
//   - GET  /api/commands           (degraded: [] — hermes_cli registry is
//                                   Python-only; Python returns [] too when
//                                   hermes_cli is missing)
//   - GET  /api/commands/bundles   (degraded: [] — agent.skill_bundles)
//   - GET  /api/personalities      (config.yaml agent.personalities)
//   - GET  /api/prompts            (saved prompts file)
//   - POST /api/default-model      (set_hermes_default_model)
//   - GET  /api/knowledge          (knowledge read)
//   - POST /api/csp-report         (204 no-content, rate-limited)

// authLogoutRouter adds POST /api/auth/logout. Clears the auth cookie and
// invalidates the server-side session (mirrors api/auth.py clear_auth_cookie
// + invalidate_session). Trusted-auth logout URL is not ported (no OIDC).
func authLogoutRouter(r chi.Router, a *hermesauth.Auth) {
	if a == nil {
		return
	}
	r.Post("/api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(a.CookieName); err == nil && c.Value != "" {
			a.InvalidateSession(c.Value)
		}
		a.ClearCookie(w)
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, map[string]any{"ok": true})
	})
}

// commandsRouter serves GET /api/commands and /api/commands/bundles with
// the degraded empty-list contract (Python returns [] when hermes_cli /
// agent.skill_bundles are unavailable; the Go server never has them).
func commandsRouter(r chi.Router) {
	r.Get("/api/commands", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"commands": []any{}})
	})
	r.Get("/api/commands/bundles", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"bundles": []any{}})
	})
}

// personalitiesRouter serves GET /api/personalities from config.yaml
// agent.personalities (matches hermes-agent CLI behavior).
func personalitiesRouter(r chi.Router, hermesHome string) {
	r.Get("/api/personalities", func(w http.ResponseWriter, r *http.Request) {
		cfg, err := os.ReadFile(filepath.Join(hermesHome, "config.yaml"))
		if err != nil {
			writeJSON(w, map[string]any{"personalities": []any{}})
			return
		}
		// Minimal YAML scan: find agent: block, then personalities: map.
		// (ponytail: use a real YAML parser when config grows.)
		personalities := []map[string]any{}
		inAgent := false
		inPersonalities := false
		for _, line := range strings.Split(string(cfg), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			indent := len(line) - len(strings.TrimLeft(line, " "))
			if indent == 0 {
				inAgent = trimmed == "agent:"
				inPersonalities = false
				continue
			}
			if inAgent && indent == 2 && strings.HasPrefix(trimmed, "personalities:") {
				inPersonalities = true
				continue
			}
			if inPersonalities && indent >= 4 {
				// name: value or name: {description: ...}
				parts := strings.SplitN(trimmed, ":", 2)
				if len(parts) != 2 {
					continue
				}
				name := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				desc := ""
				if strings.HasPrefix(val, "{") {
					// inline map: {description: "..."}
					if d := strings.Index(val, "description"); d >= 0 {
						rest := val[d+len("description"):]
						if c := strings.Index(rest, ":"); c >= 0 {
							desc = strings.Trim(strings.TrimSpace(rest[c+1:]), `"'`)
						}
					}
				} else if val != "" {
					desc = val
					if len(desc) > 80 {
						desc = desc[:80] + "..."
					}
				}
				personalities = append(personalities, map[string]any{"name": name, "description": desc})
				continue
			}
			if inPersonalities && indent < 4 {
				inPersonalities = false
			}
		}
		writeJSON(w, map[string]any{"personalities": personalities})
	})
}

// promptsRouter serves GET /api/prompts from the saved-prompts file
// (mirrors _load_saved_prompts: reads prompts.json under the WebUI data dir).
func promptsRouter(r chi.Router, dataRoot string) {
	r.Get("/api/prompts", func(w http.ResponseWriter, r *http.Request) {
		prompts := []any{}
		paths := []string{
			filepath.Join(dataRoot, "prompts.json"),
			filepath.Join(dataRoot, "saved_prompts.json"),
		}
		for _, p := range paths {
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			var raw any
			if json.Unmarshal(b, &raw) == nil {
				if arr, ok := raw.([]any); ok {
					prompts = arr
				} else if m, ok := raw.(map[string]any); ok {
					if arr, ok := m["prompts"].([]any); ok {
						prompts = arr
					}
				}
			}
			break
		}
		writeJSON(w, map[string]any{"prompts": prompts})
	})
}

// defaultModelRouter serves POST /api/default-model. Mirrors
// set_hermes_default_model: writes the model to the WebUI settings file.
// (ponytail: full provider/advanced resolution when the wizard needs it.)
func defaultModelRouter(r chi.Router, dataRoot string) {
	r.Post("/api/default-model", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		model := strings.TrimSpace(body.Model)
		if model == "" {
			writeError(w, http.StatusBadRequest, "model is required")
			return
		}
		settingsPath := filepath.Join(dataRoot, "settings.json")
		settings := map[string]any{}
		if b, err := os.ReadFile(settingsPath); err == nil {
			_ = json.Unmarshal(b, &settings)
		}
		settings["default_model"] = model
		b, _ := json.MarshalIndent(settings, "", "  ")
		if err := os.WriteFile(settingsPath, b, 0o644); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save settings")
			return
		}
		writeJSON(w, map[string]any{"ok": true, "model": model})
	})
}

// knowledgeRouter serves GET /api/knowledge — a read-only summary of the
// knowledge base directory (mirrors _handle_knowledge_read's shape).
func knowledgeRouter(r chi.Router, hermesHome string) {
	r.Get("/api/knowledge", func(w http.ResponseWriter, r *http.Request) {
		kbDir := filepath.Join(hermesHome, "knowledge")
		entries := []map[string]any{}
		if files, err := os.ReadDir(kbDir); err == nil {
			for _, f := range files {
				info, err := f.Info()
				if err != nil {
					continue
				}
				entries = append(entries, map[string]any{
					"name":     f.Name(),
					"is_dir":   f.IsDir(),
					"size":     info.Size(),
					"modified": info.ModTime().Unix(),
				})
			}
		}
		writeJSON(w, map[string]any{"entries": entries, "path": kbDir})
	})
}

// cspReportRouter serves POST /api/csp-report — collects browser CSP
// report-only violations without auth, rate-limited per IP, returns 204.
func cspReportRouter(r chi.Router) {
	r.Post("/api/csp-report", func(w http.ResponseWriter, r *http.Request) {
		clientIP := r.RemoteAddr
		if idx := strings.LastIndex(clientIP, ":"); idx >= 0 {
			clientIP = clientIP[:idx]
		}
		clMu.Lock()
		now := time.Now()
		entry, ok := clientEventRates[clientIP]
		if !ok || now.After(entry.reset) {
			entry = clientEventRate{count: 0, reset: now.Add(time.Minute)}
		}
		if entry.count >= 10 {
			clMu.Unlock()
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		entry.count++
		clientEventRates[clientIP] = entry
		clMu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
}

// wave2Router mounts all Wave-2 quick-win endpoints in one call.
func wave2Router(r chi.Router, a *hermesauth.Auth, dataRoot, hermesHome string) {
	authLogoutRouter(r, a)
	commandsRouter(r)
	personalitiesRouter(r, hermesHome)
	promptsRouter(r, dataRoot)
	defaultModelRouter(r, dataRoot)
	knowledgeRouter(r, hermesHome)
	cspReportRouter(r)
}
