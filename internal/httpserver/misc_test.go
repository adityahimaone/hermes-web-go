package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// writeGatewayState writes a gateway_state.json fixture into home.
func writeGatewayState(t *testing.T, home string, payload map[string]any) {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "gateway_state.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func agentHealthBody(t *testing.T, home string) map[string]any {
	t.Helper()
	r := NewRouterWithAgent("", nil, nil, "", fakeClientNoop{}, approval.NewStore(), WithHermesHome(home))
	ts := httptest.NewServer(r)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/health/agent")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestAgentHealthAliveWhenPIDOwnerDeadButFresh(t *testing.T) {
	home := t.TempDir()
	// PID 999999 should be dead in CI; freshness fallback must still report alive.
	writeGatewayState(t, home, map[string]any{
		"pid":           999999,
		"gateway_state": "running",
		"updated_at":    time.Now().UTC().Format(time.RFC3339),
		"active_agents": 2,
		"platforms":     map[string]any{},
	})
	body := agentHealthBody(t, home)
	if body["alive"] != true {
		t.Fatalf("alive = %v, want true (freshness fallback)", body["alive"])
	}
	det, _ := body["details"].(map[string]any)
	if det == nil || det["state"] != "alive" {
		t.Fatalf("details = %v, want state=alive", body["details"])
	}
	if body["gateway_chat"] == nil {
		t.Fatal("gateway_chat missing")
	}
}

func TestAgentHealthDownWhenStaleRunning(t *testing.T) {
	home := t.TempDir()
	// Stale running (old updated_at), PID dead → inconclusive (alive null).
	writeGatewayState(t, home, map[string]any{
		"pid":           999999,
		"gateway_state": "running",
		"updated_at":    time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339),
	})
	body := agentHealthBody(t, home)
	if v, ok := body["alive"]; ok && v != nil {
		t.Fatalf("alive = %v, want null (stale running)", v)
	}
	det, _ := body["details"].(map[string]any)
	if det["reason"] != "gateway_stale_running_state" {
		t.Fatalf("reason = %v, want gateway_stale_running_state", det["reason"])
	}
}

func TestAgentHealthUnknownWhenNotConfigured(t *testing.T) {
	home := t.TempDir() // no gateway_state.json at all
	body := agentHealthBody(t, home)
	if v, ok := body["alive"]; ok && v != nil {
		t.Fatalf("alive = %v, want null (not configured)", v)
	}
	det, _ := body["details"].(map[string]any)
	if det["reason"] != "gateway_not_configured" {
		t.Fatalf("reason = %v, want gateway_not_configured", det["reason"])
	}
}