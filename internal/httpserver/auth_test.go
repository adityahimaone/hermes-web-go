package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hermes-web-go/internal/approval"
	"hermes-web-go/internal/auth"
)

func testAuth(t *testing.T) *auth.Auth {
	t.Helper()
	a, err := auth.New(auth.Config{Password: "s3cret", StateDir: t.TempDir(), SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAuthLoginAndGate(t *testing.T) {
	a := testAuth(t)
	r := NewRouterWithAgent("", nil, nil, "", fakeClientNoop{}, approval.NewStore(), WithAuth(a))
	ts := httptest.NewServer(r)
	defer ts.Close()

	// No cookie -> API 401 JSON
	resp, err := http.Get(ts.URL + "/api/sessions")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth status = %d", resp.StatusCode)
	}

	// Bad password -> 401
	resp, err = http.Post(ts.URL+"/api/auth/login", "application/json", strings.NewReader(`{"password":"wrong"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("badpw status = %d", resp.StatusCode)
	}

	// Good password -> 200 + Set-Cookie
	resp, err = http.Post(ts.URL+"/api/auth/login", "application/json", strings.NewReader(`{"password":"s3cret"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	cookies := resp.Cookies()
	resp.Body.Close()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d", len(cookies))
	}

	// Cookie -> authorized
	req, _ := http.NewRequest("GET", ts.URL+"/api/workspaces", nil)
	req.AddCookie(cookies[0])
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("authd status = 401")
	}

	// Health is public
	resp, err = http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", resp.StatusCode)
	}
}

func TestAuthDisabledNoGate(t *testing.T) {
	r := NewRouterWithAgent("", nil, nil, "", fakeClientNoop{}, approval.NewStore())
	ts := httptest.NewServer(r)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/workspaces")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatal("auth should be disabled")
	}
}

func TestAuthStatusRoute(t *testing.T) {
	a := testAuth(t)
	r := NewRouterWithAgent("", nil, nil, "", fakeClientNoop{}, approval.NewStore(), WithAuth(a))
	ts := httptest.NewServer(r)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/auth/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["enabled"] != true {
		t.Fatalf("status = %#v", body)
	}
}
