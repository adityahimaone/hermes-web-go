package stream

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// WriteJournalSSE writes replayed and live journal events using stream-local IDs.
func WriteJournalSSE(ctx context.Context, w http.ResponseWriter, replay []Event, live <-chan Event, cancel func(), interval time.Duration, streamID string) bool {
	if cancel != nil {
		defer cancel()
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		return false
	}
	write := func(event Event) bool {
		data := eventJSON(event.Event)
		frame := []byte("id: " + streamID + ":" + strconv.FormatUint(event.Seq, 10) + "\nevent: " + string(event.Event.Type) + "\ndata: ")
		frame = append(frame, data...)
		frame = append(frame, '\n', '\n')
		if _, err := w.Write(frame); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	for _, event := range replay {
		if !write(event) {
			return false
		}
	}
	if live == nil {
		return true
	}
	heartbeat := time.NewTicker(interval)
	defer heartbeat.Stop()
	for {
		select {
		case event, ok := <-live:
			if !ok {
				return true
			}
			if !write(event) {
				return false
			}
		case <-heartbeat.C:
			if _, err := w.Write([]byte(": heartbeat\n\n")); err != nil {
				return false
			}
			flusher.Flush()
		case <-ctx.Done():
			return false
		}
	}
}
