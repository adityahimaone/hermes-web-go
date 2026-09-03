package httpserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hermes-web-go/internal/store"
)

func TestNativeReadOnlyDataRoutes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello\nworld"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "webui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.ImportSession(db, store.SessionImport{ID: "sid", Title: "Native", Workspace: root, Messages: `[{"role":"user","content":"hello"}]`, UpdatedAt: "2026-09-02T12:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	// If a native route accidentally falls through, this handler makes it obvious.
	proxy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, "proxy-fallback")
	})
	r := NewRouterWithData(fixtureStatic(t), proxy, db, root)
	ts := httptest.NewServer(r)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/session?session_id=sid")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"title":"Native"`) {
		t.Fatalf("session = %d %q", resp.StatusCode, body)
	}

	resp, err = http.Get(ts.URL + "/api/list?session_id=sid&path=../../etc")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || strings.Contains(string(body), "proxy-fallback") {
		t.Fatalf("traversal = %d %q", resp.StatusCode, body)
	}

	resp, err = http.Get(ts.URL + "/api/file/raw?session_id=sid&path=hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "hello\nworld" {
		t.Fatalf("raw = %d %q", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("raw content-type = %q", got)
	}
}

func TestNativeWorkspacesFromDB(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(t.TempDir(), "webui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.ImportWorkspace(db, store.WorkspaceImport{Path: "/tmp/proj", Name: "Proj"}); err != nil {
		t.Fatal(err)
	}
	proxy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	r := NewRouterWithData(fixtureStatic(t), proxy, db, root)
	ts := httptest.NewServer(r)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/workspaces")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"path":"/tmp/proj"`) {
		t.Fatalf("workspaces = %d %q", resp.StatusCode, body)
	}
}

func TestSessionsCleanup(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(t.TempDir(), "webui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Seed: one empty Untitled (cleaned), one empty with title (only zero_msg),
	// one with messages (never cleaned), and their backing files.
	seed := func(id, title, msgs string) {
		if err := store.ImportSession(db, store.SessionImport{ID: id, Title: title, Messages: msgs}); err != nil {
			t.Fatal(err)
		}
		_ = os.MkdirAll(filepath.Join(root, "sessions"), 0o755)
		_ = os.WriteFile(filepath.Join(root, "sessions", id+".json"), []byte(`{"session_id":"`+id+`"}`), 0o600)
	}
	seed("empty", "Untitled", "[]")
	seed("titled", "Work", "[]")
	seed("full", "Untitled", `[{"role":"user","content":"hi"}]`)
	seed("pinned_empty", "Untitled", "[]")
	// Pin one so pinned-empty still cleans (Python predicate is title+messages only).
	if err := store.SetSessionFlag(db, "pinned_empty", "pinned", 1); err != nil {
		t.Fatal(err)
	}

	proxy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	r := NewRouterWithData(fixtureStatic(t), proxy, db, root)
	ts := httptest.NewServer(r)
	defer ts.Close()

	// /api/sessions/cleanup removes Untitled + zero-message only.
	resp, err := http.Post(ts.URL+"/api/sessions/cleanup", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"cleaned":2`) {
		t.Fatalf("cleanup = %d %q", resp.StatusCode, body)
	}
	// empty + pinned_empty cleaned; titled (has title) and full (has msgs) stay.
	for _, id := range []string{"empty", "pinned_empty"} {
		if _, err := store.GetSession(db, id); err == nil {
			t.Fatalf("session %s should be cleaned", id)
		}
		if _, err := os.Stat(filepath.Join(root, "sessions", id+".json")); err == nil {
			t.Fatalf("backing file for %s should be removed", id)
		}
	}
	for _, id := range []string{"titled", "full"} {
		if _, err := store.GetSession(db, id); err != nil {
			t.Fatalf("session %s should remain: %v", id, err)
		}
	}

	// /api/sessions/cleanup_zero_message removes any zero-message row.
	resp, err = http.Post(ts.URL+"/api/sessions/cleanup_zero_message", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"cleaned":1`) {
		t.Fatalf("cleanup_zero = %d %q", resp.StatusCode, body)
	}
	if _, err := store.GetSession(db, "titled"); err == nil {
		t.Fatalf("titled empty should be cleaned by zero_msg")
	}
	if _, err := store.GetSession(db, "full"); err != nil {
		t.Fatalf("full session should remain: %v", err)
	}
}
