// Package stream owns the SSE stream registry (01-architecture-design.md §4).
package stream

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"hermes-web-go/internal/agentclient"
)

const SSEHeartbeatInterval = 30 * time.Second

func eventJSON(ev agentclient.TurnEvent) []byte {
	switch ev.Type {
	case agentclient.EventToken:
		b, _ := json.Marshal(map[string]string{"text": ev.Text})
		return b
	case agentclient.EventTool:
		b, _ := json.Marshal(map[string]string{"name": ev.Name, "preview": ev.Preview})
		return b
	case agentclient.EventApproval, agentclient.EventDone:
		b, _ := json.Marshal(ev.Data)
		return b
	case agentclient.EventError:
		b, _ := json.Marshal(map[string]string{"message": ev.Error})
		return b
	default:
		b, _ := json.Marshal(map[string]string{"message": "unknown event type"})
		return b
	}
}

// WriteSSE drains ch and writes SSE frames to w. Channel close is the internal
// end-of-stream signal; empty token text remains valid. The request context is
// passed through WriteSSEWithContext so a client disconnect (r.Context().Done())
// terminates the writer instead of leaking it. Public entry kept for tests.
func WriteSSE(w http.ResponseWriter, ch <-chan agentclient.TurnEvent) bool {
	return WriteSSEWithContext(context.Background(), w, ch, SSEHeartbeatInterval)
}

// WriteSSEWithContext is the context-aware writer. It returns false when the
// client disconnects (ctx cancelled) or a write fails; true on clean close.
func WriteSSEWithContext(ctx context.Context, w http.ResponseWriter, ch <-chan agentclient.TurnEvent, interval time.Duration) bool {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return false
	}
	heartbeat := time.NewTicker(interval)
	defer heartbeat.Stop()
	write := func(data []byte) bool {
		if _, err := w.Write(data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return true
			}
			data := eventJSON(ev)
			frame := append([]byte("event: "+string(ev.Type)+"\ndata: "), data...)
			frame = append(frame, '\n', '\n')
			if !write(frame) {
				return false
			}
		case <-heartbeat.C:
			if !write([]byte(": heartbeat\n\n")) {
				return false
			}
		case <-ctx.Done():
			return false
		}
	}
}
