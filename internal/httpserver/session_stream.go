package httpserver

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"hermes-web-go/internal/store"
	"hermes-web-go/internal/stream"
)

// sessionStreamState tracks the live per-session stream id so
// /api/session/stream can replay server_turn_started on (re)subscribe.
// This is the Go equivalent of Python's ACTIVE_RUNS attachable lookup.
type sessionStreamState struct {
	mu sync.RWMutex
	m  map[string]string // session_id -> stream_id
}

func newSessionStreamState() *sessionStreamState {
	return &sessionStreamState{m: make(map[string]string)}
}

func (s *sessionStreamState) Set(sid, streamID string) {
	s.mu.Lock()
	s.m[sid] = streamID
	s.mu.Unlock()
}

func (s *sessionStreamState) Clear(sid, streamID string) {
	s.mu.Lock()
	if s.m[sid] == streamID {
		delete(s.m, sid)
	}
	s.mu.Unlock()
}

func (s *sessionStreamState) ActiveForSession(sid string) string {
	s.mu.RLock()
	v := s.m[sid]
	s.mu.RUnlock()
	return v
}

// SessionStreamRouter mounts the persistent per-session SSE channel.
// This is Option X: lives across turns, emits `initial` immediately,
// plus on-subscribe self-heal frames (`server_turn_started` with
// recovered:true when a turn is live, otherwise `session-updated` when
// the server is ahead of the subscriber's known_count). Without this,
// a transient SSE gap drops the live-view and the transcript stays stale
// until the next user interaction.
func SessionStreamRouter(r chi.Router, db *sql.DB, reg *stream.JournalRegistry, state *sessionStreamState) {
	r.Get("/api/session/stream", func(w http.ResponseWriter, req *http.Request) {
		sid := req.URL.Query().Get("session_id")
		if sid == "" {
			writeError(w, http.StatusBadRequest, "session_id is required")
			return
		}
		if db != nil {
			if _, err := store.GetSession(db, sid); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusNotFound, "Session not found")
					return
				}
				writeError(w, http.StatusInternalServerError, "failed to load session")
				return
			}
		}
		knownStr := req.URL.Query().Get("known_count")
		var known *int
		if knownStr != "" {
			if n, err := strconv.Atoi(knownStr); err == nil {
				known = &n
			}
		}

		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError, "streaming unsupported")
			return
		}
		writeSSE := func(event string, data any) bool {
			payload, _ := json.Marshal(data)
			frame := "event: " + event + "\ndata: " + string(payload) + "\n\n"
			if _, err := w.Write([]byte(frame)); err != nil {
				return false
			}
			flusher.Flush()
			return true
		}
		if !writeSSE("initial", map[string]any{"session_id": sid}) {
			return
		}
		// ── on-subscribe self-heal (mirrors Python routes.py:21349) ──
		activeID := ""
		if state != nil {
			activeID = state.ActiveForSession(sid)
		}
		if activeID != "" {
			// Only replay if the journal is still live (attachable).
			live := true
			if reg != nil {
				if j, ok := reg.Get(activeID); ok {
					live = j.Active()
				} else {
					live = false
				}
			}
			if live {
				if !writeSSE("server_turn_started", map[string]any{
					"session_id": sid,
					"stream_id":  activeID,
					"source":     "subscribe_recovery",
					"recovered":  true,
				}) {
					return
				}
				// A live turn may already have persisted messages past the
				// reconnecting tab's known_count (turn finishing while the
				// journal is still open). Send the count self-heal too — the
				// two signals are not mutually exclusive.
				if known != nil && db != nil {
					if row, err := store.GetSession(db, sid); err == nil {
						persisted := persistedMessageCount(row.Messages)
						if persisted != nil && *persisted > *known {
							_ = writeSSE("session-updated", map[string]any{
								"session_id":    sid,
								"message_count": *persisted,
								"known_count":   *known,
								"source":        "subscribe_recovery",
							})
						}
					}
				}
			} else if known != nil && db != nil {
				// Stale activeID that already finished — fall through to
				// the "finished during gap" branch below.
				if row, err := store.GetSession(db, sid); err == nil {
					persisted := persistedMessageCount(row.Messages)
					if persisted != nil && *persisted > *known {
						_ = writeSSE("session-updated", map[string]any{
							"session_id":    sid,
							"message_count": *persisted,
							"known_count":   *known,
							"source":        "subscribe_recovery",
						})
					}
				}
			}
		} else if known != nil && db != nil {
			if row, err := store.GetSession(db, sid); err == nil {
				persisted := persistedMessageCount(row.Messages)
				if persisted != nil && *persisted > *known {
					_ = writeSSE("session-updated", map[string]any{
						"session_id":    sid,
						"message_count": *persisted,
						"known_count":   *known,
						"source":        "subscribe_recovery",
					})
				}
			}
		}

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-req.Context().Done():
				return
			case <-ticker.C:
				if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	})
}

func persistedMessageCount(raw string) *int {
	if raw == "" {
		z := 0
		return &z
	}
	var msgs []any
	if err := json.Unmarshal([]byte(raw), &msgs); err != nil {
		return nil
	}
	n := len(msgs)
	return &n
}
