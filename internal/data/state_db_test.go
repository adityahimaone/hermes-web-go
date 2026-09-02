package data

import (
	"database/sql"
	"path/filepath"
	"testing"

	"hermes-web-go/internal/store"
	_ "modernc.org/sqlite"
)

func TestImportStateDBSessionsAndMessages(t *testing.T) {
	src, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	_, err = src.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY, source TEXT, model TEXT, started_at REAL, last_activity_at REAL, title TEXT, cwd TEXT, archived INTEGER, pinned INTEGER, project_id TEXT, message_count INTEGER); CREATE TABLE messages (id INTEGER PRIMARY KEY, session_id TEXT, role TEXT, content TEXT, api_content TEXT, timestamp REAL, active INTEGER); INSERT INTO sessions VALUES ('s1','webui','codex',10,20,'State title',NULL,0,1,'p1',2); INSERT INTO messages VALUES (1,'s1','user','hello','[Workspace::v1: /tmp/workspace]\nhello',11,1); INSERT INTO messages VALUES (2,'s1','assistant','hi',NULL,12,1);`)
	if err != nil {
		t.Fatal(err)
	}

	db, err := store.Open(filepath.Join(t.TempDir(), "webui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if n, err := ImportStateDB(db, src); err != nil || n != 1 {
		t.Fatalf("ImportStateDB() = %d, %v; want 1, nil", n, err)
	}
	got, err := store.GetSession(db, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Workspace != "/tmp/workspace" || got.Title != "State title" || got.Messages == "[]" || got.Pinned != 1 {
		t.Fatalf("imported session = %#v", got)
	}
}
