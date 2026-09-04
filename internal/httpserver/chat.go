package httpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

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
func ChatRouter(r chi.Router, db *sql.DB, reg *stream.JournalRegistry, client agentclient.AgentClient, st *approval.Store, sessStreams *sessionStreamState) {
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
		history := turnHistory(row.Messages, msg)

		// Create the stream before spawning the turn so the SSE endpoint can
		// attach even if the agent returns instantly.
		streamID, journal := reg.Create(256)
		if sessStreams != nil {
			sessStreams.Set(sessionID, streamID)
		}
		_ = store.SetSessionPending(db, sessionID, float64(time.Now().UnixNano())/1e9, streamID, msg)

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			defer cancel()
			// journal terminal via finishTurn
			var answer strings.Builder
			var reasoning strings.Builder
			doneEmitted := false
			tokenCount := 0
			// Timing capture (matches Python's _turnDuration/_turnTps/_firstTokenMs
			// so the UI renders "Processed Xs", chip, first-token latency, model)
			turnStart := float64(time.Now().UnixNano()) / 1e9
			var firstTokenAt float64
			modelUsed := model

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
				if answer.Len() > 0 || reasoning.Len() > 0 {
					m := map[string]any{"role": "assistant", "content": answer.String()}
					if reasoning.Len() > 0 {
						m["reasoning"] = reasoning.String()
					}
					if status != "" {
						m["status"] = status
					}
					now := float64(time.Now().UnixNano()) / 1e9
					m["_turnDuration"] = now - turnStart
					if firstTokenAt > 0 {
						m["_firstTokenMs"] = (firstTokenAt - turnStart) * 1000.0
					} else {
						m["_firstTokenMs"] = nil
					}
					if tokenCount > 0 && now > turnStart {
						m["_turnTps"] = float64(tokenCount) / (now - turnStart)
					}
					m["_usedModel"] = modelUsed
					_ = store.AppendMessage(db, sessionID, m)
				}
				// Clear in-flight turn marker so the session no longer reports
				// busy/pending after the run completes or fails.
				if err := store.SetSessionPending(db, sessionID, 0, "", ""); err != nil {
					log.Printf("chat: clear pending failed session=%s: %v", sessionID, err)
				}
				turnElapsed := float64(time.Now().UnixNano())/1e9 - turnStart
				// Build session payload for done event (matches Python gateway shape).
				// Messages must be the state.db-reconciled transcript (Python
				// parity via _session_payload_with_full_messages): the FE swaps
				// the live turn for this payload when done lands, so tool rows
				// ("Processed" worklog), Thinking cards, and metadata survive
				// the swap instead of disappearing until the next reload.
				var doneData map[string]any
				row, err := store.GetSession(db, sessionID)
				if err == nil {
					var messages []map[string]any
					if row.Messages != "" {
						_ = json.Unmarshal([]byte(row.Messages), &messages)
					}
					if stateRows := readStateDBMessages("", sessionID); len(stateRows) > 0 {
						messages = reconcileSessionMessages(messages, stateRows)
					}
					doneData = map[string]any{
						"session": map[string]any{
							"session_id":    row.ID,
							"title":         row.Title,
							"workspace":     row.Workspace,
							"model":         row.Model,
							"created_at":    row.CreatedAt,
							"updated_at":    row.UpdatedAt,
							"pinned":        row.Pinned,
							"archived":      row.Archived,
							"project_id":    row.ProjectID,
							"rev":           row.Rev,
							"message_count": len(messages),
							"messages":      messages,
						},
						// Python gateway sends usage on done; the FE reads
						// usage.duration_seconds to stamp the settled turn
						// duration when the message lacks _turnDuration.
						"usage": map[string]any{
							"duration_seconds": turnElapsed,
						},
					}
				}
				journal.Finish(
					agentclient.TurnEvent{Type: agentclient.EventDone, Data: doneData},
					agentclient.TurnEvent{Type: agentclient.EventTypeStreamEnd, Data: map[string]any{"session_id": sessionID}},
				)
			}

			evCh, err := client.RunTurn(ctx, agentclient.TurnRequest{
				SessionID: sessionID,
				TaskID:    sessionID, // Critical Rule #3: task_id, not session_id
				Message:   msg,
				Workspace: workspace,
				Model:     model,
				History:   history,
			})
			if err != nil {
				log.Printf("chat: run_turn setup failed session=%s: %v", sessionID, err)
				journal.Publish(agentclient.TurnEvent{Type: agentclient.EventError, Error: err.Error()})
				finishTurn("partial")
				return
			}
			log.Printf("chat: run_turn started session=%s stream_tokens_will_log", sessionID)
			// Relay events from the agent into the stream registry channel.
			for {
				select {
				case ev, ok := <-evCh:
					if !ok {
						log.Printf("chat: turn finished session=%s tokens=%d", sessionID, tokenCount)
						finishTurn("")
						return
					}
					// accumulate token/reasoning text before forwarding
					if ev.Type == agentclient.EventToken {
						answer.WriteString(ev.Text)
						tokenCount++
						if firstTokenAt == 0 {
							firstTokenAt = float64(time.Now().UnixNano()) / 1e9
						}
					} else if ev.Type == agentclient.EventReasoning {
						reasoning.WriteString(ev.Text)
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
						journal.Publish(ev)
						finishTurn("partial")
						return
					}
					if ev.Type == agentclient.EventDone {
						finishTurn("")
						return
					}
					journal.Publish(ev)
				case <-ctx.Done():
					finishTurn("partial")
					return
				}
			}
		}()

		writeJSON(w, map[string]any{
			"stream_id":          streamID,
			"session_id":         sessionID,
			"pending_started_at": float64(time.Now().UnixNano()) / 1e9,
			"turn_id":            streamID,
			"title":              row.Title,
		})
	})

	r.Get("/api/chat/stream", func(w http.ResponseWriter, req *http.Request) {
		streamID := req.URL.Query().Get("stream_id")
		journal, ok := reg.Get(streamID)
		if !ok {
			writeError(w, http.StatusNotFound, "stream not found")
			return
		}
		after := parseAfterSeq(req)
		replay, live, cancel := journal.Subscribe(after)
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("Connection", "close")
		stream.WriteJournalSSE(req.Context(), w, replay, live, cancel, stream.SSEHeartbeatInterval, streamID)
	})

	r.Get("/api/chat/stream/status", func(w http.ResponseWriter, req *http.Request) {
		streamID := req.URL.Query().Get("stream_id")
		journal, ok := reg.Get(streamID)
		active := false
		replayAvailable := false
		if ok {
			active = journal.Active()
			_, live, cancel := journal.Subscribe(0)
			_ = live
			if cancel != nil {
				cancel()
			}
			replayAvailable = len(journal.SnapshotAfter(0)) > 0
		}
		writeJSON(w, map[string]any{"active": active, "stream_id": streamID, "replay_available": replayAvailable})
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
		row, err := store.GetSession(db, body.SessionID)
		if err != nil {
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
			History:   turnHistory(row.Messages, body.Message),
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
		row, err = store.GetSession(db, body.SessionID)
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

func turnHistory(raw, current string) []map[string]any {
	var history []map[string]any
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &history)
	}
	return append(history, map[string]any{"role": "user", "content": current})
}

func parseAfterSeq(req *http.Request) uint64 {
	if v := req.URL.Query().Get("after_seq"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			return n
		}
	}
	if v := req.Header.Get("Last-Event-ID"); v != "" {
		// Go ID form is "<streamID>:<seq>".
		parts := strings.Split(v, ":")
		if len(parts) > 0 {
			last := parts[len(parts)-1]
			if n, err := strconv.ParseUint(strings.TrimSpace(last), 10, 64); err == nil {
				return n
			}
		}
	}
	return 0
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
