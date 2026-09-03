package store

import (
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
