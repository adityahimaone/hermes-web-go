package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsNative(t *testing.T) {
	if !IsNative("/health") {
		t.Fatal("/health should be native")
	}
	for _, p := range []string{"/api/sessions", "/static/index.html", "/", "/api/chat"} {
		if IsNative(p) {
			t.Fatalf("%q should not be native", p)
		}
	}
}

// backendEcho is a test backend that records the exact request it received.
type backendEcho struct {
	gotMethod string
	gotURI    string
	gotBody   string
	gotHeader http.Header
}

func (b *backendEcho) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		b.gotMethod = r.Method
		// preserve original RawQuery in FullRequestURI
		if r.URL.RawQuery != "" {
			b.gotURI = r.URL.Path + "?" + r.URL.RawQuery
		} else {
			b.gotURI = r.URL.Path
		}
		b.gotBody = string(body)
		b.gotHeader = r.Header.Clone()
		w.Header().Set("X-Upstream", "yes")
		w.WriteHeader(418)
		json.NewEncoder(w).Encode(map[string]string{"upstream": "ok"})
	})
}

func TestProxyPreservesRequestAndStatus(t *testing.T) {
	backend := &backendEcho{}
	bs := httptest.NewServer(backend.handler())
	defer bs.Close()

	h, err := Handler(bs.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(h)
	defer front.Close()

	body := `{"a":1}`
	req, _ := http.NewRequest(http.MethodPost, front.URL+"/api/sessions?x=1&y=2%0A", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Custom", "abc")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if backend.gotMethod != http.MethodPost {
		t.Fatalf("method = %q", backend.gotMethod)
	}
	if backend.gotURI != "/api/sessions?x=1&y=2%0A" {
		t.Fatalf("uri = %q", backend.gotURI)
	}
	if backend.gotBody != body {
		t.Fatalf("body = %q", backend.gotBody)
	}
	if backend.gotHeader.Get("X-Custom") != "abc" {
		t.Fatalf("x-custom = %q", backend.gotHeader.Get("X-Custom"))
	}
	if backend.gotHeader.Get("Content-Type") != "application/json" {
		t.Fatalf("content-type = %q", backend.gotHeader.Get("Content-Type"))
	}
	if resp.StatusCode != 418 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Upstream") != "yes" {
		t.Fatalf("x-upstream header not preserved")
	}
}

func TestHandlerInvalidTarget(t *testing.T) {
	if _, err := Handler("://bad", nil); err == nil {
		t.Fatal("expected error for invalid target")
	}
}

func TestProxyPreservesMultipleQueryValues(t *testing.T) {
	backend := &backendEcho{}
	bs := httptest.NewServer(backend.handler())
	defer bs.Close()
	h, _ := Handler(bs.URL, nil)
	front := httptest.NewServer(h)
	defer front.Close()
	resp, err := http.Get(front.URL + "/api/sessions?tag=a&tag=b&q=go")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if want := "/api/sessions?tag=a&tag=b&q=go"; backend.gotURI != want {
		t.Fatalf("uri = %q, want %q", backend.gotURI, want)
	}
}
