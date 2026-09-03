package store

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestAppendMessage(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "webui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := CreateSession(db, SessionImport{ID: "m1", Messages: "[]"}); err != nil {
		t.Fatal(err)
	}
	if err := AppendMessage(db, "m1", map[string]any{"role": "user", "content": "hello"}); err != nil {
		t.Fatal(err)
	}
	row, err := GetSession(db, "m1")
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(row.Messages), &got); err != nil {
		t.Fatalf("messages not json: %s", row.Messages)
	}
	if len(got) != 1 || got[0]["role"] != "user" || got[0]["content"] != "hello" {
		t.Fatalf("messages = %s", row.Messages)
	}
}

func TestAppendMessageRevIsMonotonicAndAtomic(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "webui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := CreateSession(db, SessionImport{ID: "rev", Messages: "[]"}); err != nil {
		t.Fatal(err)
	}
	rev1, err := AppendMessageWithRev(db, "rev", map[string]any{"role": "user", "content": "hi"})
	if err != nil || rev1 != 1 {
		t.Fatalf("first append rev=%d err=%v, want rev=1", rev1, err)
	}
	rev2, err := AppendMessageWithRev(db, "rev", map[string]any{"role": "assistant", "content": "hello"})
	if err != nil || rev2 != 2 {
		t.Fatalf("second append rev=%d err=%v, want rev=2", rev2, err)
	}
	row, err := GetSession(db, "rev")
	if err != nil {
		t.Fatal(err)
	}
	if row.Rev != rev2 {
		t.Fatalf("GetSession rev=%d, want %d", row.Rev, rev2)
	}
}

func TestAppendMessageConcurrentSameSessionPreservesBothMessages(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "webui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := CreateSession(db, SessionImport{ID: "same", Messages: "[]"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	go func() {
		_, err := AppendMessageWithRev(db, "same", map[string]any{"role": "user", "content": "one"})
		done <- err
	}()
	go func() {
		_, err := AppendMessageWithRev(db, "same", map[string]any{"role": "user", "content": "two"})
		done <- err
	}()
	for range 2 {
		if err := <-done; err != nil {
			t.Fatalf("concurrent same-session append: %v", err)
		}
	}
	row, err := GetSession(db, "same")
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(row.Messages), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("messages len=%d, want 2: %s", len(got), row.Messages)
	}
	if row.Rev != 2 {
		t.Fatalf("rev=%d, want 2", row.Rev)
	}
}

func TestAppendMessageConcurrentSessionsNoBusy(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "webui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, sid := range []string{"a", "b"} {
		if err := CreateSession(db, SessionImport{ID: sid, Messages: "[]"}); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 2)
	go func() { done <- AppendMessage(db, "a", map[string]any{"role": "user", "content": "hi a"}) }()
	go func() { done <- AppendMessage(db, "b", map[string]any{"role": "user", "content": "hi b"}) }()
	for range 2 {
		if err := <-done; err != nil {
			t.Fatalf("concurrent AppendMessage: %v", err)
		}
	}
}

// TestMigrateRevColumnOnLegacySchema verifies a sessions table created before
// the `rev` column gained it on Open, and that a legacy import row keeps rev=0
// until the first append bumps it to 1. This is the migration path a deployed
// DB (created without rev) actually takes.
func TestMigrateRevColumnOnLegacySchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webui.db")
	// Create a legacy DB with the pre-rev schema (no migration helper).
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS sessions (
		session_id TEXT PRIMARY KEY,
		title TEXT NOT NULL DEFAULT '',
		workspace TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		messages TEXT NOT NULL DEFAULT '[]',
		created_at REAL NOT NULL DEFAULT 0,
		updated_at REAL NOT NULL DEFAULT 0,
		pinned INTEGER NOT NULL DEFAULT 0,
		archived INTEGER NOT NULL DEFAULT 0,
		project_id TEXT
	)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (session_id, messages, project_id) VALUES ('legacy', '[]', '')`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	// Now open through the real Open(), which runs the migration.
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	row, err := GetSession(reopened, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if row.Rev != 0 {
		t.Fatalf("legacy row should start rev=0, got %d", row.Rev)
	}
	rev, err := AppendMessageWithRev(reopened, "legacy", map[string]any{"role": "user", "content": "hi"})
	if err != nil || rev != 1 {
		t.Fatalf("append after migration rev=%d err=%v, want 1", rev, err)
	}
	if row, err := GetSession(reopened, "legacy"); err != nil || row.Rev != 1 {
		t.Fatalf("after append rev=%d err=%v, want 1", row.Rev, err)
	}
}
