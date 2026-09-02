package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"hermes-web-go/internal/approval"
)

func writeSkillsMemFixture(t *testing.T, home string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, "skills", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "skills", "demo", "SKILL.md"),
		[]byte("---\nname: demo\ndescription: Demo skill\n---\n# D\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "memories"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "memories", "MEMORY.md"), []byte("mem"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "memories", "USER.md"), []byte("usr"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "SOUL.md"), []byte("soul"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSkillsListRoute(t *testing.T) {
	home := t.TempDir()
	writeSkillsMemFixture(t, home)
	r := NewRouterWithAgent("", nil, nil, "", fakeClientNoop{}, approval.NewStore(), WithHermesHome(home))
	ts := httptest.NewServer(r)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/skills")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Skills []map[string]any `json:"skills"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Skills) != 1 || body.Skills[0]["name"] != "demo" {
		t.Fatalf("skills = %#v", body.Skills)
	}
}

func TestSkillsContentRoute(t *testing.T) {
	home := t.TempDir()
	writeSkillsMemFixture(t, home)
	r := NewRouterWithAgent("", nil, nil, "", fakeClientNoop{}, approval.NewStore(), WithHermesHome(home))
	ts := httptest.NewServer(r)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/skills/content?name=demo")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["content"] != "---\nname: demo\ndescription: Demo skill\n---\n# D\n" {
		t.Fatalf("content = %#v", body)
	}
}

func TestSkillsContentRejectsTraversalRoute(t *testing.T) {
	home := t.TempDir()
	writeSkillsMemFixture(t, home)
	r := NewRouterWithAgent("", nil, nil, "", fakeClientNoop{}, approval.NewStore(), WithHermesHome(home))
	ts := httptest.NewServer(r)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/skills/content?name=..%2F..")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestMemoryRoute(t *testing.T) {
	home := t.TempDir()
	writeSkillsMemFixture(t, home)
	r := NewRouterWithAgent("", nil, nil, "", fakeClientNoop{}, approval.NewStore(), WithHermesHome(home))
	ts := httptest.NewServer(r)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/memory")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["memory"] != "mem" || body["user"] != "usr" || body["soul"] != "soul" {
		t.Fatalf("memory = %#v", body)
	}
}
