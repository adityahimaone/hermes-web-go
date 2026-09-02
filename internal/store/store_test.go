package store

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCreatesSchemaAndImportsSession(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "webui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := ImportSession(db, SessionImport{
		ID: "abc", Title: "Hello", Workspace: "/tmp", Model: "gpt", Messages: `[{"role":"user","content":"hi"}]`,
		CreatedAt: "2026-08-30T10:02:42.363396Z", UpdatedAt: "2026-08-30T10:02:46.982569Z",
	}); err != nil {
		t.Fatal(err)
	}
	var title string
	if err := db.QueryRow(`SELECT title FROM sessions WHERE session_id = ?`, "abc").Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Hello" {
		t.Fatalf("title = %q", title)
	}
	var updated float64
	if err := db.QueryRow(`SELECT updated_at FROM sessions WHERE session_id = ?`, "abc").Scan(&updated); err != nil {
		t.Fatal(err)
	}
	// 2026-08-30T10:02:46.982569Z in unix seconds
	if !strings.HasPrefix(fmt.Sprintf("%.3f", updated), "1788084166") {
		t.Fatalf("updated_at not parsed as full RFC3339 epoch: %v", updated)
	}
}

func TestListSessionsPaginationStableOrdering(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "webui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Real epoch timestamps so ORDER BY updated_at DESC is deterministic.
	for _, s := range []SessionImport{
		{ID: "a", Messages: "[]", UpdatedAt: "2026-08-30T10:00:00Z"},
		{ID: "b", Messages: "[]", UpdatedAt: "2026-08-30T11:00:00Z"},
		{ID: "c", Messages: "[]", UpdatedAt: "2026-08-30T12:00:00Z"},
	} {
		if err := ImportSession(db, s); err != nil {
			t.Fatal(err)
		}
	}
	// page 1: 1 item, most recently updated = c
	rows, err := ListSessions(db, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "c" {
		t.Fatalf("page1 = %#v, want [c]", rows)
	}
	// page 2: offset 1 -> b
	rows, err = ListSessions(db, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "b" {
		t.Fatalf("page2 = %#v, want [b]", rows)
	}
}

func TestImportToleratesMalformedTimestamp(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "webui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Bad timestamp must not fail import; updated_at coerces to 0.
	if err := ImportSession(db, SessionImport{ID: "x", Messages: "[]", UpdatedAt: "garbage"}); err != nil {
		t.Fatalf("import should tolerate malformed ts: %v", err)
	}
	got, err := GetSession(db, "x")
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedAt != 0 {
		t.Fatalf("updated_at = %v, want 0", got.UpdatedAt)
	}
}
