package httpserver

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func writeHomeFile(t *testing.T, home, rel, content string) {
	t.Helper()
	p := filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func TestProfileActiveDefault(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, "config.yaml", "model:\n  default: codex\n  provider: custom\nterminal:\n  cwd: /Users/adityahimawan/dev\n")
	// last_workspace.txt is profile-scoped and wins over config.yaml
	ws := filepath.Join(home, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatalf("mkdir ws: %v", err)
	}
	writeHomeFile(t, home, "last_workspace.txt", ws+"\n")

	r := chi.NewRouter()
	ConfigRouter(r, home)
	req := httptest.NewRequest(http.MethodGet, "/api/profile/active", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		Name             string `json:"name"`
		Path             string `json:"path"`
		IsDefault        bool   `json:"is_default"`
		DefaultWorkspace string `json:"default_workspace"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Name != "default" {
		t.Errorf("name = %q, want default", out.Name)
	}
	if !out.IsDefault {
		t.Errorf("is_default = false, want true")
	}
	if out.Path != filepath.Clean(home) {
		t.Errorf("path = %q, want %q", out.Path, home)
	}
	// last_workspace.txt wins over config.yaml terminal.cwd
	if out.DefaultWorkspace != ws {
		t.Errorf("default_workspace = %q, want %q (last_workspace.txt)", out.DefaultWorkspace, ws)
	}
}

func TestProfileActiveNamed(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "profiles", "karina")
	writeHomeFile(t, home, "config.yaml", "model:\n  default: gpt-5\n  provider: openai\n")

	r := chi.NewRouter()
	ConfigRouter(r, home)
	req := httptest.NewRequest(http.MethodGet, "/api/profile/active", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	var out struct {
		Name      string `json:"name"`
		IsDefault bool   `json:"is_default"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Name != "karina" {
		t.Errorf("name = %q, want karina", out.Name)
	}
	if out.IsDefault {
		t.Errorf("is_default = true for named profile")
	}
}

func TestProfilesList(t *testing.T) {
	base := t.TempDir()
	// base home = default
	writeHomeFile(t, base, "config.yaml", "model:\n  default: codex\n  provider: custom\n")
	writeHomeFile(t, base, "skills/foo/SKILL.md", "# foo\n")
	// named profile karina
	karina := filepath.Join(base, "profiles", "karina")
	writeHomeFile(t, karina, "config.yaml", "model:\n  default: gpt-5\n  provider: openai\n")

	// active home = base (default profile)
	r := chi.NewRouter()
	ConfigRouter(r, base)
	req := httptest.NewRequest(http.MethodGet, "/api/profiles", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		Profiles []map[string]any `json:"profiles"`
		Active   string           `json:"active"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Active != "default" {
		t.Errorf("active = %q, want default", out.Active)
	}
	if len(out.Profiles) != 2 {
		t.Fatalf("len(profiles) = %d, want 2 (default + karina)", len(out.Profiles))
	}
	// row 0 default is_active
	if out.Profiles[0]["name"] != "default" || out.Profiles[0]["is_active"] != true {
		t.Errorf("row0 = %v, want default active", out.Profiles[0]["name"])
	}
	if out.Profiles[1]["name"] != "karina" || out.Profiles[1]["model"] != "gpt-5" {
		t.Errorf("row1 = %v", out.Profiles[1])
	}
	if out.Profiles[0]["skill_count"] != float64(1) {
		t.Errorf("default skill_count = %v, want 1", out.Profiles[0]["skill_count"])
	}
}

func TestProfilesListNamedActive(t *testing.T) {
	base := t.TempDir()
	writeHomeFile(t, base, "config.yaml", "model:\n  default: codex\n  provider: custom\n")
	karina := filepath.Join(base, "profiles", "karina")
	writeHomeFile(t, karina, "config.yaml", "model:\n  default: gpt-5\n  provider: openai\n")

	r := chi.NewRouter()
	ConfigRouter(r, karina)
	req := httptest.NewRequest(http.MethodGet, "/api/profiles", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	var out struct {
		Profiles []map[string]any `json:"profiles"`
		Active   string           `json:"active"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Active != "karina" {
		t.Errorf("active = %q, want karina", out.Active)
	}
	if len(out.Profiles) != 2 {
		t.Fatalf("len(profiles) = %d, want 2", len(out.Profiles))
	}
	if out.Profiles[1]["is_active"] != true {
		t.Errorf("karina is_active = %v, want true", out.Profiles[1]["is_active"])
	}
}

func TestProfilesHiddenProfile(t *testing.T) {
	base := t.TempDir()
	writeHomeFile(t, base, "config.yaml", "# empty\n")
	hidden := filepath.Join(base, "profiles", "hidden")
	writeHomeFile(t, hidden, "profile.yaml", "visible: false\n")

	r := chi.NewRouter()
	ConfigRouter(r, base)
	req := httptest.NewRequest(http.MethodGet, "/api/profiles", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	var out struct {
		Profiles []map[string]any `json:"profiles"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// hidden profile visible=false
	if len(out.Profiles) != 2 {
		t.Fatalf("len = %d, want 2", len(out.Profiles))
	}
	for _, p := range out.Profiles {
		if p["name"] == "hidden" && p["visible"] != false {
			t.Errorf("hidden visible = %v, want false", p["visible"])
		}
	}
	if !strings.Contains(rr.Body.String(), `"visible":false`) && !strings.Contains(rr.Body.String(), `"visible": false`) {
		t.Errorf("no visible:false in body")
	}
}

// TestConfigRouterNativeNoProxyFallback ensures Family-2 read routes are routed
// natively through the full server router (never proxy Teapot).
func TestConfigRouterNativeNoProxyFallback(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, "config.yaml", "model:\n  default: codex\n  provider: custom\n")

	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "webui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	proxy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, "proxy-fallback")
	})
	r := NewRouterWithData(".", proxy, db, t.TempDir(), WithHermesHome(home))
	ts := httptest.NewServer(r)
	defer ts.Close()

	for _, u := range []string{
		"/api/profile/active",
		"/api/profiles",
	} {
		resp, err := http.Get(ts.URL + u)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || strings.Contains(string(body), "proxy-fallback") {
			t.Fatalf("%s = %d %q", u, resp.StatusCode, body)
		}
	}
}
