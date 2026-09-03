package httpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hermes-web-go/internal/agentclient"
	"hermes-web-go/internal/approval"
	"hermes-web-go/internal/store"
)

// TestChatStreamFanout ensures two concurrent SSE listeners receive identical events.
func TestChatStreamFanout(t *testing.T) {
	db := testDB(t)
	if err := store.CreateSession(db, store.SessionImport{ID: "fan1", Title: "", Workspace: "/tmp", Model: "codex", Messages: "[]"}); err != nil {
		t.Fatal(err)
	}

	fake := &fakeClient{
		started: make(chan TurnRequestCapture, 1),
		events: []agentclient.TurnEvent{
			{Type: agentclient.EventToken, Text: "one"},
			{Type: agentclient.EventToken, Text: "two"},
			{Type: agentclient.EventDone},
		},
	}
	r := NewRouterWithAgent("", nil, db, "", fake, approval.NewStore())
	ts := httptest.NewServer(r)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/chat/start", "application/json", bytes.NewBufferString(`{"session_id":"fan1","message":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var startResp struct {
		StreamID string `json:"stream_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&startResp); err != nil {
		t.Fatal(err)
	}

	first := fetchStream(t, ts.URL+"/api/chat/stream?stream_id="+startResp.StreamID)
	second := fetchStream(t, ts.URL+"/api/chat/stream?stream_id="+startResp.StreamID)

	if first != second {
		t.Fatalf("fan-out mismatch: first=%q second=%q", first, second)
	}
}

// TestChatStreamReplay ensures reconnect via Last-Event-ID replays missed events.
func TestChatStreamReplay(t *testing.T) {
	db := testDB(t)
	if err := store.CreateSession(db, store.SessionImport{ID: "replay1", Title: "", Workspace: "/tmp", Model: "codex", Messages: "[]"}); err != nil {
		t.Fatal(err)
	}

	fake := &fakeClient{
		started: make(chan TurnRequestCapture, 1),
		events: []agentclient.TurnEvent{
			{Type: agentclient.EventToken, Text: "alpha"},
			{Type: agentclient.EventToken, Text: "beta"},
			{Type: agentclient.EventDone},
		},
	}
	r := NewRouterWithAgent("", nil, db, "", fake, approval.NewStore())
	ts := httptest.NewServer(r)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/chat/start", "application/json", bytes.NewBufferString(`{"session_id":"replay1","message":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var startResp struct {
		StreamID string `json:"stream_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&startResp); err != nil {
		t.Fatal(err)
	}

	first := fetchStream(t, ts.URL+"/api/chat/stream?stream_id="+startResp.StreamID)
	if !strings.Contains(first, `"text":"alpha"`) || !strings.Contains(first, `"text":"beta"`) {
		t.Fatalf("first fetch missing events: %q", first)
	}

	// Extract first id and replay from there
	id := extractFirstID(first)
	if id == "" {
		t.Fatalf("missing id in stream: %q", first)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/chat/stream?stream_id="+startResp.StreamID, nil)
	req.Header.Set("Last-Event-ID", id)
	r2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Body.Close()
	b, _ := io.ReadAll(r2.Body)
	replayed := string(b)
	if strings.Contains(replayed, `"text":"alpha"`) {
		t.Fatalf("replay should skip first event: %q", replayed)
	}
	if !strings.Contains(replayed, `"text":"beta"`) || !strings.Contains(replayed, "event: done") {
		t.Fatalf("replay missing remaining events: %q", replayed)
	}
}

func fetchStream(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("get %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("stream status %d: %q", resp.StatusCode, b)
	}
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func extractFirstID(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "id: ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "id: "))
		}
	}
	return ""
}
