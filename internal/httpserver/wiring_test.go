package httpserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hermes-web-go/internal/proxy"
)

// TestProxyWiring verifies that non-native routes reach the proxy handler
// while /health stays native and static stays byte-identical.
func TestProxyWiring(t *testing.T) {
	// copied frontend directory (matches production HERMES_WEBUI_STATIC_DIR=./static
	// relative to repo root when tests run from the package dir).
	dir := filepath.Join("..", "..", "static")
	wantStatic, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}

	// backend echo
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(202)
		_, _ = io.WriteString(w, `{"proxied":`+r.URL.Path+`}`)
	}))
	defer backend.Close()

	ph, err := proxy.Handler(backend.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRouter(dir, ph)
	ts := httptest.NewServer(r)
	defer ts.Close()

	// native health
	resp, _ := http.Get(ts.URL + "/health")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !contains(string(body), `"status"`) {
		t.Fatalf("health = %d %q", resp.StatusCode, body)
	}

	// static byte-identical
	resp, _ = http.Get(ts.URL + "/static/index.html")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != string(wantStatic) {
		t.Fatalf("static mismatch: %d", resp.StatusCode)
	}

	// proxied: a non-data route still falls through to the backend
	resp, _ = http.Get(ts.URL + "/api/chat/start")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 202 || string(body) != `{"proxied":/api/chat/start}` {
		t.Fatalf("proxy = %d %q", resp.StatusCode, body)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
