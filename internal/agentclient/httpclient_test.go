package agentclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// httptest shim that emulates the Hermes runner `/v1/runs` API
// (the same shape Python's HttpRunnerClient speaks).
func newRunnerShim(t *testing.T, onEvents func(w http.ResponseWriter)) (string, chan map[string]any) {
	t.Helper()
	var mu sync.Mutex
	started := make(chan map[string]any, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/runs":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad json", 400)
				return
			}
			started <- body
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"run_id":"run123","session_id":"sess","stream_id":"stream123","status":"started"}`))
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/runs/run123/events"):
			w.Header().Set("Content-Type", "text/event-stream")
			if onEvents != nil {
				onEvents(w)
			}
		case r.Method == "POST" && r.URL.Path == "/v1/runs/run123/cancel":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL, started
}

func TestHTTPClientRunTurn(t *testing.T) {
	onEvents := func(w http.ResponseWriter) {
		f := w.(http.Flusher)
		_, _ = w.Write([]byte("event: token\ndata: {\"text\":\"hello\"}\n\n"))
		_, _ = w.Write([]byte("event: done\ndata: {}\n\n"))
		f.Flush()
	}
	base, started := newRunnerShim(t, onEvents)
	c := NewHTTPClient(base, "")
	ctx := context.Background()
	ch, err := c.RunTurn(ctx, TurnRequest{
		SessionID: "sess", TaskID: "task", Message: "hi", Workspace: "/work",
	})
	if err != nil {
		t.Fatal(err)
	}
	// start body must carry task id semantics + message + workspace
	body := <-started
	if body["session_id"] != "sess" || body["message"] != "hi" || body["workspace"] != "/work" {
		t.Fatalf("start body = %+v", body)
	}

	var events []TurnEvent
	for ev := range ch {
		events = append(events, ev)
	}
	if len(events) != 2 || events[0].Type != EventToken || events[0].Text != "hello" {
		t.Fatalf("events = %+v", events)
	}
	if events[1].Type != EventDone {
		t.Fatalf("expected done, got %+v", events[1])
	}
}

func TestHTTPClientCancel(t *testing.T) {
	onEvents := func(w http.ResponseWriter) {
		select {} // never close; cancel path is what we test
	}
	base, _ := newRunnerShim(t, onEvents)
	c := NewHTTPClient(base, "")
	if err := c.Cancel(context.Background(), "run123"); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPClientStartError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"no runner"}`, 503)
	}))
	t.Cleanup(ts.Close)
	c := NewHTTPClient(ts.URL, "")
	_, err := c.RunTurn(context.Background(), TurnRequest{TaskID: "t", Message: "m"})
	if err == nil {
		t.Fatal("expected start error")
	}
}
