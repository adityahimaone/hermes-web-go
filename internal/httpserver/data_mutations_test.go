package httpserver

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"hermes-web-go/internal/store"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "webui.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSessionNewMutation(t *testing.T) {
	db := testDB(t)
	r := NewRouterWithData("", nil, db, "")
	ts := httptest.NewServer(r)
	defer ts.Close()

	body := `{"session_id":"n1","title":"New","workspace":"/tmp","model":"codex"}`
	resp, err := http.Post(ts.URL+"/api/session/new", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	row, err := store.GetSession(db, "n1")
	if err != nil {
		t.Fatal(err)
	}
	if row.Title != "New" || row.Workspace != "/tmp" {
		t.Fatalf("session = %#v", row)
	}
}

func TestSessionRenameMutation(t *testing.T) {
	db := testDB(t)
	if err := store.CreateSession(db, store.SessionImport{ID: "r1", Title: "Old", Messages: "[]"}); err != nil {
		t.Fatal(err)
	}
	r := NewRouterWithData("", nil, db, "")
	ts := httptest.NewServer(r)
	defer ts.Close()

	body := `{"session_id":"r1","title":"Renamed"}`
	resp, err := http.Post(ts.URL+"/api/session/rename", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	row, err := store.GetSession(db, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if row.Title != "Renamed" {
		t.Fatalf("title = %q", row.Title)
	}
}

func TestSessionDeleteNeverCreates(t *testing.T) {
	db := testDB(t)
	r := NewRouterWithData("", nil, db, "")
	ts := httptest.NewServer(r)
	defer ts.Close()

	// Delete a session that doesn't exist — must NOT create it (Rule #1).
	body := `{"session_id":"ghost"}`
	resp, err := http.Post(ts.URL+"/api/session/delete", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("delete status = %d", resp.StatusCode)
	}
	n, err := store.CountSessions(db)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("sessions = %d, want 0 (delete must never create)", n)
	}
}

func TestSessionUpdateMutation(t *testing.T) {
	db := testDB(t)
	if err := store.CreateSession(db, store.SessionImport{ID: "u1", Title: "T", Messages: "[]"}); err != nil {
		t.Fatal(err)
	}
	r := NewRouterWithData("", nil, db, "")
	ts := httptest.NewServer(r)
	defer ts.Close()

	body := `{"session_id":"u1","workspace":"/new","model":"claude"}`
	resp, err := http.Post(ts.URL+"/api/session/update", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	row, err := store.GetSession(db, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if row.Workspace != "/new" || row.Model != "claude" {
		t.Fatalf("session = %#v", row)
	}
}

func TestFileMutations(t *testing.T) {
	db := testDB(t)
	root := t.TempDir()
	if err := store.CreateSession(db, store.SessionImport{ID: "f1", Workspace: root, Messages: "[]"}); err != nil { t.Fatal(err) }
	r := NewRouterWithData("", nil, db, "")
	ts := httptest.NewServer(r)
	defer ts.Close()
	post := func(path, body string) *http.Response {
		resp, err := http.Post(ts.URL+path, "application/json", bytes.NewBufferString(body))
		if err != nil { t.Fatal(err) }
		return resp
	}
	resp := post("/api/file/create", `{"session_id":"f1","path":"new.txt","content":"one"}`)
	if resp.StatusCode != 200 { t.Fatalf("create status = %d", resp.StatusCode) }
	resp.Body.Close()
	resp = post("/api/file/save", `{"session_id":"f1","path":"new.txt","content":"two"}`)
	if resp.StatusCode != 200 { t.Fatalf("save status = %d", resp.StatusCode) }
	resp.Body.Close()
	b, err := os.ReadFile(filepath.Join(root, "new.txt"))
	if err != nil || string(b) != "two" { t.Fatalf("saved content = %q, err=%v", b, err) }
	resp = post("/api/file/delete", `{"session_id":"f1","path":"new.txt"}`)
	if resp.StatusCode != 200 { t.Fatalf("delete status = %d", resp.StatusCode) }
	resp.Body.Close()
	if _, err := os.Stat(filepath.Join(root, "new.txt")); !os.IsNotExist(err) { t.Fatalf("file still exists: %v", err) }
	resp = post("/api/file/create", `{"session_id":"f1","path":"../escape.txt","content":"x"}`)
	if resp.StatusCode == 200 { t.Fatal("traversal create accepted") }
	resp.Body.Close()
}

var _ = json.Marshal
