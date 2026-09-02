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

// TestChatTurnSurvivesStartRequest proves the turn goroutine is NOT tied to
// the /api/chat/start request context. Python parity: the turn runs as a daemon
// and the SSE stream is a subscriber — disconnect from /start must not cancel it.
func TestChatTurnSurvivesStartRequest(t *testing.T) {
	db := testDB(t)
	if err := store.CreateSession(db, store.SessionImport{ID: "survive1", Title: "", Workspace: "/tmp", Model: "codex", Messages: "[]"}); err != nil {
		t.Fatal(err)
	}

	// Hang client: RunTurn blocks until ctx is cancelled.
	hang := &hangClient{
		started:   make(chan TurnRequestCapture, 1),
		hangCh:    make(chan struct{}),
		cancelled: make(chan struct{}),
		finished:  make(chan struct{}),
	}
	r := NewRouterWithAgent("", nil, db, "", hang)
	ts := httptest.NewServer(r)
	defer ts.Close()

	// POST /api/chat/start — this returns immediately
	body := `{"session_id":"survive1","message":"hi"}`
	resp, _ := http.Post(ts.URL+"/api/chat/start", "application/json", bytes.NewBufferString(body))
	resp.Body.Close()

	// Wait for the goroutine to actually start (RunTurn was called)
	select {
	case <-hang.started:
	case <-time.After(2 * time.Second):
		t.Fatal("agent never called")
	}

	// The turn goroutine must still be alive after /start returned. Old code
	// incorrectly derived turn context from request context, cancelling here.
	select {
	case <-hang.cancelled:
		t.Fatal("turn context cancelled when /start request returned")
	case <-time.After(100 * time.Millisecond):
	}
	close(hang.hangCh)
	select {
	case <-hang.finished:
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not finish after unblock")
	}
}

// hangClient is an AgentClient that blocks in RunTurn until hangCh is closed.
type hangClient struct {
	mu        sync.Mutex
	started   chan TurnRequestCapture
	hangCh    chan struct{}
	cancelled chan struct{}
	finished  chan struct{}
}

func (h *hangClient) RunTurn(ctx context.Context, req agentclient.TurnRequest) (<-chan agentclient.TurnEvent, error) {
	h.mu.Lock()
	h.started <- TurnRequestCapture{SessionID: req.SessionID, TaskID: req.TaskID, Message: req.Message}
	h.mu.Unlock()
	// Block until told to proceed, or ctx is cancelled
	select {
	case <-h.hangCh:
	case <-ctx.Done():
		close(h.cancelled)
	}
	close(h.finished)
	ch := make(chan agentclient.TurnEvent, 1)
	ch <- agentclient.TurnEvent{Type: agentclient.EventDone}
	close(ch)
	return ch, nil
}

func (h *hangClient) Cancel(ctx context.Context, sessionID string) error {
	return nil
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

func TestChatSyncBlocksUntilAgentCompletes(t *testing.T) {
	db := testDB(t)
	if err := store.CreateSession(db, store.SessionImport{ID: "sync1", Title: "", Workspace: "/tmp", Model: "codex", Messages: "[]"}); err != nil {
		t.Fatal(err)
	}
	fake := &blockingClient{started: make(chan struct{}), release: make(chan struct{})}
	r := NewRouterWithAgent("", nil, db, "", fake)
	ts := httptest.NewServer(r)
	defer ts.Close()

	result := make(chan *http.Response, 1)
	go func() {
		resp, err := http.Post(ts.URL+"/api/chat", "application/json", strings.NewReader(`{"session_id":"sync1","message":"hi"}`))
		if err != nil {
			t.Errorf("sync request: %v", err)
			return
		}
		result <- resp
	}()
	select {
	case <-fake.started:
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not start")
	}
	select {
	case resp := <-result:
		resp.Body.Close()
		t.Fatal("sync response returned before agent completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(fake.release)
	select {
	case resp := <-result:
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("sync status = %d", resp.StatusCode)
		}
		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["answer"] != "done" {
			t.Fatalf("sync answer = %#v", body["answer"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sync response did not return")
	}
}

type blockingClient struct {
	started chan struct{}
	release chan struct{}
}

func (b *blockingClient) RunTurn(ctx context.Context, req agentclient.TurnRequest) (<-chan agentclient.TurnEvent, error) {
	close(b.started)
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	ch := make(chan agentclient.TurnEvent, 2)
	ch <- agentclient.TurnEvent{Type: agentclient.EventToken, Text: "done"}
	close(ch)
	return ch, nil
}
func (b *blockingClient) Cancel(ctx context.Context, sessionID string) error { return nil }

func TestChatConcurrentSessionsNoCrossContamination(t *testing.T) {
	db := testDB(t)
	for _, sid := range []string{"concA", "concB"} {
		if err := store.CreateSession(db, store.SessionImport{ID: sid, Title: "", Workspace: "/tmp", Model: "codex", Messages: "[]"}); err != nil {
			t.Fatal(err)
		}
	}
	// Respond deterministically per session: A->alpha, B->bravo.
	perSess := map[string][]agentclient.TurnEvent{
		"concA": {{Type: agentclient.EventToken, Text: "alpha"}, {Type: agentclient.EventDone}},
		"concB": {{Type: agentclient.EventToken, Text: "bravo"}, {Type: agentclient.EventDone}},
	}
	fake := &mapClient{perSess: perSess, started: make(chan TurnRequestCapture, 4)}
	r := NewRouterWithAgent("", nil, db, "", fake)
	ts := httptest.NewServer(r)
	defer ts.Close()

	// Fire both starts concurrently.
	var wg sync.WaitGroup
	streams := make(map[string]string, 2)
	var mu sync.Mutex
	for _, sid := range []string{"concA", "concB"} {
		wg.Add(1)
		go func(sid string) {
			defer wg.Done()
			resp, err := http.Post(ts.URL+"/api/chat/start", "application/json", bytes.NewBufferString(`{"session_id":"`+sid+`","message":"m"}`))
			if err != nil {
				t.Errorf("start %s: %v", sid, err)
				return
			}
			defer resp.Body.Close()
			var startResp struct {
				StreamID  string `json:"stream_id"`
				SessionID string `json:"session_id"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&startResp); err != nil {
				t.Errorf("decode %s: %v", sid, err)
				return
			}
			mu.Lock()
			streams[sid] = startResp.StreamID
			mu.Unlock()
		}(sid)
	}
	wg.Wait()
	if len(streams) != 2 {
		t.Fatalf("expected 2 streams, got %d", len(streams))
	}

	// Drain each stream and assert its token text matches only its session.
	for _, sid := range []string{"concA", "concB"} {
		sid := sid
		wg.Add(1)
		go func() {
			defer wg.Done()
			var streamID string
			mu.Lock()
			streamID = streams[sid]
			mu.Unlock()
			resp, err := http.Get(ts.URL + "/api/chat/stream?stream_id=" + streamID)
			if err != nil {
				t.Errorf("stream %s: %v", sid, err)
				return
			}
			defer resp.Body.Close()
			out, _ := io.ReadAll(resp.Body)
			want := map[string]string{"concA": "alpha", "concB": "bravo"}[sid]
			if !strings.Contains(string(out), `"text":"`+want+`"`) {
				t.Errorf("session %s stream=%q missing text %q", sid, string(out), want)
			}
		}()
	}
	wg.Wait()
}

// mapClient returns a fixed event sequence per session id.
type mapClient struct {
	perSess map[string][]agentclient.TurnEvent
	started chan TurnRequestCapture
}

func (m *mapClient) RunTurn(ctx context.Context, req agentclient.TurnRequest) (<-chan agentclient.TurnEvent, error) {
	select {
	case m.started <- TurnRequestCapture{SessionID: req.SessionID, TaskID: req.TaskID, Message: req.Message}:
	default:
	}
	ch := make(chan agentclient.TurnEvent, len(m.perSess[req.SessionID]))
	for _, ev := range m.perSess[req.SessionID] {
		ch <- ev
	}
	close(ch)
	return ch, nil
}
func (m *mapClient) Cancel(ctx context.Context, sessionID string) error { return nil }

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
