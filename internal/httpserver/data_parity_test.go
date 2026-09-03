package httpserver

import (
	"testing"

	"hermes-web-go/internal/store"
)

func TestSessionListItemMatchesPythonSidebarSemantics(t *testing.T) {
	row := store.SessionRow{ID: "s1", Title: "Reply SMOKE_OK", Messages: `[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"}]`}
	item := sessionListItem(row)
	if item["title"] != "SMOKE_OK" {
		t.Fatalf("title = %v, want SMOKE_OK", item["title"])
	}
	// Python compact(): message_count = len(messages) (all roles),
	// user_message_count = count(role == "user").
	if item["message_count"] != 2 {
		t.Fatalf("message_count = %v, want 2 all-message count", item["message_count"])
	}
	if item["user_message_count"] != 1 {
		t.Fatalf("user_message_count = %v, want 1 user message", item["user_message_count"])
	}
}
