package httpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"hermes-web-go/internal/agentclient"
	"hermes-web-go/internal/approval"
	"hermes-web-go/internal/store"
	"hermes-web-go/internal/stream"
)

// ChatHandler wires the /api/chat* routes onto r. It needs the session store
// (db) and the agent transport (client). When client is nil the routes are
// not registered, so the catch-all proxy can keep serving them (Phase 4
// cutover keeps proxy fallback until the runner is verified live).
func ChatRouter(r chi.Router, db *sql.DB, reg *stream.Registry, client agentclient.AgentClient, st *approval.Store) {
	if db == nil || reg == nil || client == nil {
		return
	}

	r.Post("/api/chat/start", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			SessionID string `json:"session_id"`
			Message   string `json:"message"`
			Model     string `json:"model"`
			Workspace string `json:"workspace"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if body.SessionID == "" {
			writeError(w, http.StatusBadRequest, "session_id is required")
			return
		}
		msg := trimSpace(body.Message)
		if msg == "" {
			writeError(w, http.StatusBadRequest, "message is required")
			return
		}

		// Critical Rule #5: capture session id up front into a local, before
		// any async work. The turn goroutine must not close over a request var.
		sessionID := body.SessionID
		row, err := store.GetSession(db, sessionID)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "Session not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load session")
			return
		}

		workspace := body.Workspace
		if workspace == "" {
			workspace = row.Workspace
		}
		model := body.Model
		if model == "" {
			model = row.Model
		}

		// Append the user message now (persisted before the turn starts).
		if err := store.AppendMessage(db, sessionID, map[string]any{"role": "user", "content": msg}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to persist message")
			return
		}

		// Create the stream before spawning the turn so the SSE endpoint can
		// attach even if the agent returns instantly.
		streamID, ch := reg.Create()

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			defer cancel()
			defer reg.Close(streamID)
			var answer strings.Builder
			doneEmitted := false

			// finishTurn is idempotent: it persists whatever assistant text
			// accumulated and emits EXACTLY ONE done event. It runs on every
			// exit path — success, transport-emitted done, agent error, or
			// context cancellation — so a partial answer is never silently
			// dropped and the frontend never sees a duplicate completion.
			finishTurn := func(status string) {
				if doneEmitted {
					return
				}
				doneEmitted = true
				if answer.Len() > 0 {
					m := map[string]any{"role": "assistant", "content": answer.String()}
					if status != "" {
						m["status"] = status
					}
					_ = store.AppendMessage(db, sessionID, m)
				}
				// Build session payload for done event (matches Python gateway shape).
				var doneData map[string]any
				row, err := store.GetSession(db, sessionID)
				if err == nil {
					var messages []map[string]any
					if row.Messages != "" {
						_ = json.Unmarshal([]byte(row.Messages), &messages)
					}
					doneData = map[string]any{"session": map[string]any{
						"session_id":    row.ID,
						"title":         row.Title,
						"workspace":     row.Workspace,
						"model":         row.Model,
						"created_at":    row.CreatedAt,
						"updated_at":    row.UpdatedAt,
						"pinned":        row.Pinned,
						"archived":      row.Archived,
						"project_id":    row.ProjectID,
						"message_count": len(messages),
						"messages":      messages,
					}}
				}
				select {
				case ch <- agentclient.TurnEvent{Type: agentclient.EventDone, Data: doneData}:
				case <-ctx.Done():
				}
			}

			evCh, err := client.RunTurn(ctx, agentclient.TurnRequest{
				SessionID: sessionID,
				TaskID:    sessionID, // Critical Rule #3: task_id, not session_id
				Message:   msg,
				Workspace: workspace,
				Model:     model,
			})
			if err != nil {
				select {
				case ch <- agentclient.TurnEvent{Type: agentclient.EventError, Error: err.Error()}:
				case <-ctx.Done():
				}
				finishTurn("partial")
				return
			}
			// Relay events from the agent into the stream registry channel.
			for {
				select {
				case ev, ok := <-evCh:
					if !ok {
						finishTurn("")
						return
					}
					// accumulate token text before forwarding
					if ev.Type == agentclient.EventToken {
						answer.WriteString(ev.Text)
					}
					// §5.1: run.completed is informational only — done is the
					// single completion signal. Swallow it so the frontend
					// never gets a duplicate terminal event.
					if string(ev.Type) == "run.completed" {
						continue
					}
					if ev.Type == agentclient.EventApproval && st != nil {
						entry := approval.FromEvent(sessionID, ev.Data)
						st.Submit(entry)
					}
					// Normalize transport terminal events at this boundary.
					// chat.go owns persistence and emits one canonical done.
					if ev.Type == agentclient.EventError {
						select {
						case ch <- ev:
						case <-ctx.Done():
						}
						finishTurn("partial")
						return
					}
					if ev.Type == agentclient.EventDone {
						finishTurn("")
						return
					}
					select {
					case ch <- ev:
					case <-ctx.Done():
						finishTurn("partial")
						return
					}
				case <-ctx.Done():
					finishTurn("partial")
					return
				}
			}
		}()

		writeJSON(w, map[string]any{
			"stream_id":  streamID,
			"session_id": sessionID,
		})
	})

	r.Get("/api/chat/stream", func(w http.ResponseWriter, req *http.Request) {
		streamID := req.URL.Query().Get("stream_id")
		ch, ok := reg.Get(streamID)
		if !ok {
			writeError(w, http.StatusNotFound, "stream not found")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("Connection", "close")
		stream.WriteSSEWithContext(req.Context(), w, ch, stream.SSEHeartbeatInterval)
	})

	r.Get("/api/chat/stream/status", func(w http.ResponseWriter, req *http.Request) {
		streamID := req.URL.Query().Get("stream_id")
		active := reg.Active(streamID)
		writeJSON(w, map[string]any{"active": active, "stream_id": streamID, "replay_available": false})
	})

	r.Post("/api/chat", func(w http.ResponseWriter, req *http.Request) {
		// Synchronous fallback: block until the turn completes, then return
		// the final session. The FE does not use this; kept for parity.
		var body struct {
			SessionID string `json:"session_id"`
			Message   string `json:"message"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if body.SessionID == "" || trimSpace(body.Message) == "" {
			writeError(w, http.StatusBadRequest, "session_id and message are required")
			return
		}
		if _, err := store.GetSession(db, body.SessionID); err != nil {
			writeError(w, http.StatusNotFound, "Session not found")
			return
		}
		if err := store.AppendMessage(db, body.SessionID, map[string]any{"role": "user", "content": body.Message}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to persist message")
			return
		}

		evCh, err := client.RunTurn(req.Context(), agentclient.TurnRequest{
			SessionID: body.SessionID,
			TaskID:    body.SessionID,
			Message:   body.Message,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "agent call failed: "+err.Error())
			return
		}
		var answer string
		for ev := range evCh {
			if ev.Type == agentclient.EventToken {
				answer += ev.Text
			}
			if ev.Type == agentclient.EventError {
				writeError(w, http.StatusInternalServerError, "agent error: "+ev.Error)
				return
			}
		}
		row, err := store.GetSession(db, body.SessionID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load session")
			return
		}
		writeJSON(w, map[string]any{
			"answer":  answer,
			"status":  "done",
			"session": sessionResponse(row),
		})
	})

}

func trimSpace(s string) string {
	// use a local loop to avoid importing strings for one use in the hot path
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
