package httpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hermes-web-go/internal/store"
)

func newSkillsMemTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	home := t.TempDir()
	skillDir := filepath.Join(home, "skills", "alpha")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: alpha\n---\nhi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := "# keep\nskills:\n  disabled:\n    - alpha\nmemory:\n  memory_enabled: true\n"
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "webui.db"))
	if err != nil {
		t.Fatal(err)
	}
	proxy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, "proxy-fallback")
	})
	r := NewRouterWithData("", proxy, db, t.TempDir(), WithHermesHome(home))
	ts := httptest.NewServer(r)
	return ts, home
}

func postJSON(t *testing.T, url string, body map[string]any) (*http.Response, []byte) {
	t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestSkillsSaveDeleteNative(t *testing.T) {
	ts, home := newSkillsMemTestServer(t)
	defer ts.Close()

	resp, body := postJSON(t, ts.URL+"/api/skills/save", map[string]any{"name": "My Tool", "content": "# new\n", "category": ""})
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("save = %d %q", resp.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(home, "skills", "my-tool", "SKILL.md")); err != nil {
		t.Fatalf("save did not write file: %v", err)
	}

	// delete by case-normalized name
	resp, body = postJSON(t, ts.URL+"/api/skills/delete", map[string]any{"name": "My Tool"})
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("delete = %d %q", resp.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(home, "skills", "my-tool")); !os.IsNotExist(err) {
		t.Fatalf("delete left dir: %v", err)
	}

	// traversal rejected
	resp, body = postJSON(t, ts.URL+"/api/skills/delete", map[string]any{"name": "../../etc"})
	if resp.StatusCode != 400 {
		t.Fatalf("traversal delete = %d %q (want 400)", resp.StatusCode, body)
	}
}

func TestMemoryWriteNative(t *testing.T) {
	ts, home := newSkillsMemTestServer(t)
	defer ts.Close()

	resp, body := postJSON(t, ts.URL+"/api/memory/write", map[string]any{"section": "memory", "content": "# M\n"})
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("write = %d %q", resp.StatusCode, body)
	}
	if b, err := os.ReadFile(filepath.Join(home, "memories", "MEMORY.md")); err != nil || string(b) != "# M\n" {
		t.Fatalf("memory file = %q %v", b, err)
	}

	resp, body = postJSON(t, ts.URL+"/api/memory/write", map[string]any{"section": "bogus", "content": "x"})
	if resp.StatusCode != 400 {
		t.Fatalf("bogus section = %d %q (want 400)", resp.StatusCode, body)
	}
}

func TestSkillsToggleNativePreservesConfig(t *testing.T) {
	ts, home := newSkillsMemTestServer(t)
	defer ts.Close()

	// alpha is disabled initially; enable it (enabled=true)
	resp, body := postJSON(t, ts.URL+"/api/skills/toggle", map[string]any{"name": "alpha", "enabled": true})
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"enabled":true`) {
		t.Fatalf("toggle = %d %q", resp.StatusCode, body)
	}
	cfg, _ := os.ReadFile(filepath.Join(home, "config.yaml"))
	if strings.Contains(string(cfg), "- alpha") {
		t.Fatalf("alpha still disabled:\n%s", cfg)
	}
	if !strings.Contains(string(cfg), "# keep") {
		t.Fatalf("comment lost:\n%s", cfg)
	}

	// unknown skill → 404
	resp, body = postJSON(t, ts.URL+"/api/skills/toggle", map[string]any{"name": "nope", "enabled": true})
	if resp.StatusCode != 404 {
		t.Fatalf("unknown toggle = %d %q (want 404)", resp.StatusCode, body)
	}
}
