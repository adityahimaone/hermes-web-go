package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"hermes-web-go/internal/agentclient"
	"hermes-web-go/internal/store"
)

// fakeClient is a scriptable AgentClient used by chat tests.
type fakeClient struct {
	mu        sync.Mutex
	started   chan TurnRequestCapture
	events    []agentclient.TurnEvent
	cancelled []string
}

type TurnRequestCapture struct {
	SessionID string
	TaskID    string
	Message   string
}

func (f *fakeClient) RunTurn(ctx context.Context, req agentclient.TurnRequest) (<-chan agentclient.TurnEvent, error) {
	f.mu.Lock()
	f.started <- TurnRequestCapture{SessionID: req.SessionID, TaskID: req.TaskID, Message: req.Message}
	ch := make(chan agentclient.TurnEvent, len(f.events))
	for _, ev := range f.events {
		ch <- ev
	}
	close(ch)
	f.mu.Unlock()
	return ch, nil
}

func (f *fakeClient) Cancel(ctx context.Context, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, sessionID)
	return nil
}

func TestChatStartStreamsTokenAndDone(t *testing.T) {
	db := testDB(t)
	if err := store.CreateSession(db, store.SessionImport{ID: "chat1", Title: "", Workspace: "/tmp", Model: "codex", Messages: "[]"}); err != nil {
		t.Fatal(err)
	}

	fake := &fakeClient{
		started: make(chan TurnRequestCapture, 1),
		events: []agentclient.TurnEvent{
			{Type: agentclient.EventToken, Text: "hello"},
			{Type: agentclient.EventDone},
		},
	}
	r := NewRouterWithAgent("", nil, db, "", fake)
	ts := httptest.NewServer(r)
	defer ts.Close()

	// POST /api/chat/start
	body := `{"session_id":"chat1","message":"hi"}`
	resp, err := http.Post(ts.URL+"/api/chat/start", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("start status = %d", resp.StatusCode)
	}
	var startResp struct {
		StreamID  string `json:"stream_id"`
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&startResp); err != nil {
		t.Fatal(err)
	}
	if startResp.SessionID != "chat1" || startResp.StreamID == "" {
		t.Fatalf("start = %+v", startResp)
	}

	// fake client must be called with the right session
	select {
	case c := <-fake.started:
		if c.SessionID != "chat1" || c.TaskID != "chat1" || c.Message != "hi" {
			t.Fatalf("turn req = %+v", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent never called")
	}

	// GET /api/chat/stream?stream_id=...
	resp2, err := http.Get(ts.URL + "/api/chat/stream?stream_id=" + startResp.StreamID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("stream status = %d", resp2.StatusCode)
	}
	if ct := resp2.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("stream content-type = %q", ct)
	}
	raw, _ := io.ReadAll(resp2.Body)
	out := string(raw)
	if !strings.Contains(out, "event: token\n") || !strings.Contains(out, `"text":"hello"`) {
		t.Fatalf("stream body missing token: %q", out)
	}
	if !strings.Contains(out, "event: done\n") {
		t.Fatalf("stream body missing done: %q", out)
	}

	// status endpoint reports inactive after done
	resp3, _ := http.Get(ts.URL + "/api/chat/stream/status?stream_id=" + startResp.StreamID)
	raw3, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()
	var status struct {
		Active bool `json:"active"`
	}
	if err := json.Unmarshal(raw3, &status); err != nil {
		t.Fatalf("status body = %s", raw3)
	}
	if status.Active {
		t.Fatalf("stream should be inactive after done, status=%s", raw3)
	}

	// user message must be persisted
	row, err := store.GetSession(db, "chat1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(row.Messages, `"role":"user"`) || !strings.Contains(row.Messages, "hi") {
		t.Fatalf("user message not persisted: %s", row.Messages)
	}
}

func TestChatStartValidatesSession(t *testing.T) {
	db := testDB(t)
	fake := &fakeClient{started: make(chan TurnRequestCapture, 1)}
	r := NewRouterWithAgent("", nil, db, "", fake)
	ts := httptest.NewServer(r)
	defer ts.Close()

	body := `{"session_id":"missing","message":"hi"}`
	resp, err := http.Post(ts.URL+"/api/chat/start", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("missing session status = %d", resp.StatusCode)
	}
}

func TestChatStreamNotFound(t *testing.T) {
	db := testDB(t)
	fake := &fakeClient{started: make(chan TurnRequestCapture, 1)}
	r := NewRouterWithAgent("", nil, db, "", fake)
	ts := httptest.NewServer(r)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/chat/stream?stream_id=missing")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("missing stream status = %d", resp.StatusCode)
	}
}
