package httpserver

// Waves 7b/7c — session-list SSE (native) + gateway stream (degraded native).
//
// /api/sessions/events  — Python parity: subscribe_session_events pub/sub with
//   maxsize=1 coalescing; stream emits `sessions_changed` frames whenever the
//   session list changes plus 30s keepalives. The Go side owns the same bus:
//   any mutation handler that touches sessions should call
//   publishSessionEvents(reason, sid) so connected browsers refresh.
//
// /api/sessions/gateway/stream — Python parity: gateway_watcher subscribe +
//   initial `get_cli_sessions()` snapshot. The Go server has no direct view of
//   the Python GatewayWatcher, so probe mode is implemented natively and the
//   live stream degrades to watcher-absent semantics (initial snapshot + 30s
//   keepalives). FE keeps working; full live CLI-session updates still come
//   through the always-on per-session /api/session/stream.

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// ── global session-events bus ──────────────────────────────────────────────

type sessionEventsBus struct {
	mu   sync.Mutex
	subs map[chan map[string]any]struct{}
	ver  int
}

var sessionEvents = &sessionEventsBus{subs: make(map[chan map[string]any]struct{})}

func (b *sessionEventsBus) subscribe() chan map[string]any {
	ch := make(chan map[string]any, 1)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *sessionEventsBus) unsubscribe(ch chan map[string]any) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
}

// publish bumps the version and fans out a coalesced payload.
func (b *sessionEventsBus) publish(reason, profile, sessionID string) {
	b.mu.Lock()
	b.ver++
	payload := map[string]any{
		"reason":   reason,
		"version":  b.ver,
		"type":     "sessions_changed",
	}
	if profile != "" {
		payload["profile"] = profile
	}
	if sessionID != "" {
		payload["session_id"] = sessionID
	}
	subs := make([]chan map[string]any, 0, len(b.subs))
	for ch := range b.subs {
		subs = append(subs, ch)
	}
	b.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- payload:
		default:
			// full → drop oldest, coalesce
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- payload:
			default:
			}
		}
	}
}

func publishSessionEvents(reason, sessionID string) {
	sessionEvents.publish(reason, "", sessionID)
}

// ── SSE stream helpers (shared with session_stream.go) ─────────────────────

func writeSSEFrame(w http.ResponseWriter, flusher http.Flusher, event string, data any) bool {
	payload, _ := json.Marshal(data)
	frame := "event: " + event + "\ndata: " + string(payload) + "\n\n"
	if _, err := w.Write([]byte(frame)); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

func writeSSEKeepalive(w http.ResponseWriter, flusher http.Flusher) bool {
	if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

func sseHeaders(w http.ResponseWriter) http.Flusher {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	f, ok := w.(http.Flusher)
	if !ok {
		return nil
	}
	return f
}

// ── /api/sessions/events ───────────────────────────────────────────────────

func sessionsEventsHandler(w http.ResponseWriter, req *http.Request) {
	flusher := sseHeaders(w)
	if flusher == nil {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	ch := sessionEvents.subscribe()
	defer sessionEvents.unsubscribe(ch)
	// initial snapshot — parity with Python (no immediate event, only keepalive
	// until a change lands; the FE does its own first load)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-req.Context().Done():
			return
		case payload := <-ch:
			if !writeSSEFrame(w, flusher, "sessions_changed", payload) {
				return
			}
		case <-ticker.C:
			if !writeSSEKeepalive(w, flusher) {
				return
			}
		}
	}
}

// ── /api/sessions/gateway/stream ───────────────────────────────────────────

func gatewayStreamHandler(w http.ResponseWriter, req *http.Request, settings map[string]any, dataRoot, hermesHome string) {
	probe := req.URL.Query().Get("probe")
	if probe == "1" || probe == "true" || probe == "yes" {
		showCli, _ := settings["show_cli_sessions"].(bool)
		// watcher is Python-side; probe reports the stream exists and is
		// available when the flag is on, disabled otherwise.
		wave4WriteJSON(w, 200, map[string]any{
			"watcher_running": false,
			"enabled":         showCli,
			"scope":           "gateway_stream",
			"session_stream_available": true,
			"message":         "gateway watcher lives in Python agent; probe is degraded",
		})
		return
	}
	enabled, _ := settings["show_cli_sessions"].(bool)
	if !enabled {
		wave4WriteJSON(w, 404, map[string]any{"error": "agent sessions not enabled"})
		return
	}
	flusher := sseHeaders(w)
	if flusher == nil {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	// initial snapshot: empty list (Go cannot read Python CLI sessions)
	if !writeSSEFrame(w, flusher, "sessions_changed", map[string]any{"sessions": []any{}}) {
		return
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-req.Context().Done():
			return
		case <-ticker.C:
			if !writeSSEKeepalive(w, flusher) {
				return
			}
		}
	}
}

// ── router ──────────────────────────────────────────────────────────────────

func SessionListEventsRouter(r chi.Router, dataRoot, hermesHome string) {
	r.Get("/api/sessions/events", sessionsEventsHandler)
	r.Get("/api/sessions/gateway/stream", func(w http.ResponseWriter, req *http.Request) {
		settings := loadWebUISettings(dataRoot, hermesHome)
		gatewayStreamHandler(w, req, settings, dataRoot, hermesHome)
	})
}