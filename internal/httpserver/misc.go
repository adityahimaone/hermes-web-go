package httpserver

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// miscRouter mounts small self-contained HTTP routes that are cheap to serve
// natively (no agent/gateway dependency): client telemetry logging and the
// manual compression-job status readout.
func miscRouter(r chi.Router) {
	// POST /api/client-events/log — client telemetry. Mirrors Python
	// _handle_client_event_log: rate-limited per IP, sanitized to the event
	// name, logged server-side, {ok, event} returned.
	r.Post("/api/client-events/log", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Event string `json:"event"`
			Type  string `json:"type"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
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
			writeError(w, http.StatusTooManyRequests, "rate_limited")
			return
		}
		entry.count++
		clientEventRates[clientIP] = entry
		clMu.Unlock()

		name := body.Event
		if name == "" {
			name = body.Type
		}
		if name != "" {
			log.Printf("[client-event] %s", name)
		}
		writeJSON(w, map[string]any{"ok": true, "event": name})
	})

	// GET /api/session/compress/status — manual compression job status.
	// Python tracks jobs in process memory; Go has no in-flight compression
	// jobs, so every request reports idle (ponytail: wire a shared job table
	// when manual compression lands natively).
	r.Get("/api/session/compress/status", func(w http.ResponseWriter, r *http.Request) {
		sid := strings.TrimSpace(r.URL.Query().Get("session_id"))
		if sid == "" {
			writeError(w, http.StatusBadRequest, "session_id is required")
			return
		}
		writeJSON(w, map[string]any{"ok": true, "status": "idle", "session_id": sid})
	})
}

var (
	clMu             sync.Mutex
	clientEventRates = map[string]clientEventRate{}
)

type clientEventRate struct {
	count int
	reset time.Time
}
