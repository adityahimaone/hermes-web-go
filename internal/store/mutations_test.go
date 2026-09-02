package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestSessionMutations(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "webui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := CreateSession(db, SessionImport{ID: "s1", Title: "Initial", Workspace: "/tmp", Model: "codex", Messages: "[]"}); err != nil {
		t.Fatal(err)
	}
	if err := UpdateSession(db, "s1", SessionUpdate{Workspace: strptr("/work"), Model: strptr("gpt"), Pinned: intptr(1)}); err != nil {
		t.Fatal(err)
	}
	row, err := GetSession(db, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if row.Workspace != "/work" || row.Model != "gpt" || row.Pinned != 1 {
		t.Fatalf("updated row = %#v", row)
	}
	if err := RenameSession(db, "s1", "Renamed"); err != nil {
		t.Fatal(err)
	}
	row, err = GetSession(db, "s1")
	if err != nil || row.Title != "Renamed" {
		t.Fatalf("renamed row = %#v, err=%v", row, err)
	}
	if err := DeleteSession(db, "missing"); err != sql.ErrNoRows {
		t.Fatalf("missing delete err = %v, want sql.ErrNoRows", err)
	}
	if _, err := GetSession(db, "missing"); err != sql.ErrNoRows {
		t.Fatalf("missing session = %v", err)
	}
	if err := DeleteSession(db, "s1"); err != nil {
		t.Fatal(err)
	}
}

func strptr(s string) *string { return &s }
func intptr(v int) *int       { return &v }
