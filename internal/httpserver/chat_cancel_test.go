package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"hermes-web-go/internal/agentclient"
	"hermes-web-go/internal/approval"
	"hermes-web-go/internal/store"
)

// slowClient keeps the events channel open until Cancel is called: it models
// an in-flight turn the relay goroutine is still draining. fakeClient is
// embedded by pointer so its sync.Mutex is shared, not copied.
type slowClient struct {
	*fakeClient
	events  chan agentclient.TurnEvent
	request bool
}

func (s *slowClient) RunTurn(ctx context.Context, req agentclient.TurnRequest) (<-chan agentclient.TurnEvent, error) {
	s.request = true
	return s.events, nil
}

// TestChatCancelAbortsInFlightTurn: POST /api/chat/cancel with a live
// stream_id must abort the relay goroutine, persist the partial answer via
// finishTurn, clear pending, call AgentClient.Cancel, and return
// {ok:true, cancelled:true, session_id:...}.
func TestChatCancelAbortsInFlightTurn(t *testing.T) {
	db := testDB(t)
	if err := store.CreateSession(db, store.SessionImport{ID: "cancel1", Title: "", Workspace: "/tmp", Model: "codex", Messages: "[]"}); err != nil {
		t.Fatal(err)
	}

	events := make(chan agentclient.TurnEvent)
	fake := &fakeClient{started: make(chan TurnRequestCapture, 1)}
	slow := &slowClient{fakeClient: fake, events: events}

	r := NewRouterWithAgent("", nil, db, "", slow, approval.NewStore())
	ts := httptest.NewServer(r)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/chat/start", "application/json", bytes.NewBufferString(`{"session_id":"cancel1","message":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	var startResp struct {
		StreamID string `json:"stream_id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&startResp)
	resp.Body.Close()
	if startResp.StreamID == "" {
		t.Fatal("no stream_id from /api/chat/start")
	}

	// let the relay goroutine subscribe
	time.Sleep(50 * time.Millisecond)

	cresp, err := http.Post(ts.URL+"/api/chat/cancel?stream_id="+startResp.StreamID, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	var cres struct {
		OK        bool   `json:"ok"`
		Cancelled bool   `json:"cancelled"`
		SessionID string `json:"session_id"`
	}
	_ = json.NewDecoder(cresp.Body).Decode(&cres)
	cresp.Body.Close()
	if !cres.OK || !cres.Cancelled || cres.SessionID != "cancel1" {
		t.Fatalf("cancel response = %+v", cres)
	}
	if len(slow.cancelled) != 1 || slow.cancelled[0] != "cancel1" {
		t.Fatalf("AgentClient.Cancel calls = %v", slow.cancelled)
	}

	// relay goroutine must be gone: ctx cancel closes the journal. Give it a
	// moment, then assert pending cleared (finishTurn ran).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		row, err := store.GetSession(db, "cancel1")
		if err == nil && row.PendingStartedAt == 0 {
			return // success
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("pending was not cleared after cancel — finishTurn did not run")
}

// TestChatStart409WhileTurnActive: starting a second turn on a session with a
// live pending turn must return 409 with the active stream id (Python parity).
func TestChatStart409WhileTurnActive(t *testing.T) {
	db := testDB(t)
	if err := store.CreateSession(db, store.SessionImport{ID: "conflict1", Title: "", Workspace: "/tmp", Model: "codex", Messages: "[]"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionPending(db, "conflict1", float64(time.Now().Unix()), "stream-live", "earlier message"); err != nil {
		t.Fatal(err)
	}

	fake := &fakeClient{started: make(chan TurnRequestCapture, 1)}
	r := NewRouterWithAgent("", nil, db, "", fake, approval.NewStore())
	ts := httptest.NewServer(r)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/chat/start", "application/json", bytes.NewBufferString(`{"session_id":"conflict1","message":"second"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["stream_id"] != "stream-live" {
		t.Fatalf("409 body = %v", body)
	}
}

// TestChatStartReclaimsStalePending: a pending_started_at older than
// pendingStaleAfter is a crashed turn (restart mid-stream) and must NOT 409 —
// the new start simply reclaims it.
func TestChatStartReclaimsStalePending(t *testing.T) {
	db := testDB(t)
	if err := store.CreateSession(db, store.SessionImport{ID: "stale1", Title: "", Workspace: "/tmp", Model: "codex", Messages: "[]"}); err != nil {
		t.Fatal(err)
	}
	old := float64(time.Now().Add(-time.Hour).Unix())
	if err := store.SetSessionPending(db, "stale1", old, "stream-old", "orphan"); err != nil {
		t.Fatal(err)
	}

	fake := &fakeClient{
		started: make(chan TurnRequestCapture, 1),
		events: []agentclient.TurnEvent{
			{Type: agentclient.EventToken, Text: "ok"},
			{Type: agentclient.EventDone},
		},
	}
	r := NewRouterWithAgent("", nil, db, "", fake, approval.NewStore())
	ts := httptest.NewServer(r)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/chat/start", "application/json", bytes.NewBufferString(`{"session_id":"stale1","message":"fresh"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (stale pending must be reclaimed)", resp.StatusCode)
	}
}
