package data

import (
	"os"
	"path/filepath"
	"testing"

	"hermes-web-go/internal/store"
)

func TestImportSessionsSkipsIndexAndImportsSessions(t *testing.T) {
	dir := t.TempDir()
	// A real session file.
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("abc.json", `{"session_id":"abc","title":"Hi","created_at":"2026-08-30T10:00:00Z","updated_at":"2026-08-30T10:01:00Z","messages":[{"role":"user","content":"hello"}]}`)
	// _index.json is a JSON array, must be skipped, not fail.
	write("_index.json", `[{"session_id":"abc"}]`)

	db, err := store.Open(filepath.Join(t.TempDir(), "webui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	n, err := ImportSessions(db, dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("imported = %d, want 1", n)
	}
	got, err := store.GetSession(db, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Hi" || got.Messages != `[{"role":"user","content":"hello"}]` {
		t.Fatalf("session = %#v", got)
	}
}

func TestImportSessionFileToleratesNonObjectRoot(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "x.json")
	if err := os.WriteFile(f, []byte(`[1,2,3]`), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "webui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := ImportSessionFile(db, f); err != nil {
		t.Fatalf("non-object root should be tolerated, got %v", err)
	}
}

func TestImportSessionFileSkipsEmptySessionID(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "y.json")
	if err := os.WriteFile(f, []byte(`{"title":"No ID"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "webui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := ImportSessionFile(db, f); err != nil {
		t.Fatalf("empty session_id should be skipped, got %v", err)
	}
	n, err := store.CountSessions(db)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected 0 sessions, got %d", n)
	}
}
