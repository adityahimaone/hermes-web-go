package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hermes-web-go/internal/approval"
)

func TestCompressStatusIdle(t *testing.T) {
	r := NewRouterWithAgent("", nil, nil, "", fakeClientNoop{}, approval.NewStore())
	ts := httptest.NewServer(r)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/session/compress/status?session_id=abc123")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Ok        bool   `json:"ok"`
		Status    string `json:"status"`
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Ok || body.Status != "idle" || body.SessionID != "abc123" {
		t.Fatalf("body = %+v", body)
	}

	// Missing session_id → 400 (parity with Python bad()).
	resp2, err := http.Get(ts.URL + "/api/session/compress/status")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing sid status = %d, want 400", resp2.StatusCode)
	}
}

func TestClientEventLogAndRateLimit(t *testing.T) {
	r := NewRouterWithAgent("", nil, nil, "", fakeClientNoop{}, approval.NewStore())
	ts := httptest.NewServer(r)
	defer ts.Close()

	post := func() int {
		resp, err := http.Post(ts.URL+"/api/client-events/log", "application/json",
			strings.NewReader(`{"event":"page_visit"}`))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	if code := post(); code != http.StatusOK {
		t.Fatalf("first POST = %d, want 200", code)
	}
	// Per-IP limit is 10/min; the remaining 9 pass, the 11th is 429.
	for i := 0; i < 9; i++ {
		if code := post(); code != http.StatusOK {
			t.Fatalf("POST %d = %d, want 200", i+2, code)
		}
	}
	if code := post(); code != http.StatusTooManyRequests {
		t.Fatalf("over-limit POST = %d, want 429", code)
	}
}

func TestClientEventLogInvalidJSON(t *testing.T) {
	r := NewRouterWithAgent("", nil, nil, "", fakeClientNoop{}, approval.NewStore())
	ts := httptest.NewServer(r)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/client-events/log", "application/json",
		strings.NewReader("not json"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid JSON = %d, want 400", resp.StatusCode)
	}
}