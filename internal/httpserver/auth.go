package httpserver

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	hermesauth "hermes-web-go/internal/auth"
)

// AuthRouter mounts password login/status endpoints.
func AuthRouter(r chi.Router, a *hermesauth.Auth) {
	r.Get("/api/auth/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"enabled": a.Enabled()})
	})
	r.Post("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if !a.Enabled() {
			writeJSON(w, map[string]any{"ok": true, "message": "Auth not enabled"})
			return
		}
		if !a.VerifyPassword(body.Password) {
			writeError(w, http.StatusUnauthorized, "Invalid password")
			return
		}
		cookie, err := a.CreateSession()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create session")
			return
		}
		a.SetCookie(w, cookie)
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, map[string]any{"ok": true})
	})
}
