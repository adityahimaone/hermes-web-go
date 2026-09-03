package httpserver

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"
)

func seedStateDB(t *testing.T, home string) {
	t.Helper()
	path := filepath.Join(home, "state.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open seeded state.db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE messages (session_id TEXT, role TEXT, timestamp INTEGER)`); err != nil {
		t.Fatalf("create messages: %v", err)
	}
	// s1: user + assistant = 1 round; user + user + assistant = still 1 (merged); assistant alone = 0; user alone = 0.
	seed := []struct {
		sid, role string
		ts        int64
	}{
		{"20260903_a", "user", 1},
		{"20260903_a", "assistant", 2},
		{"20260903_a", "user", 3},
		{"20260903_a", "user", 4},
		{"20260903_a", "assistant", 5},
		{"20260903_a", "assistant", 6}, // stray assistant — merged into pending since seen_user? Python: role!=user, seen_user true → sets seen_agent_after_user
		{"20260903_a", "user", 7},      // new round start
		{"20260903_a", "assistant", 8}, // complete round 2
		{"20260903_b", "user", 1},      // incomplete (no assistant) → 0 rounds
	}
	for _, r := range seed {
		if _, err := db.Exec(`INSERT INTO messages (session_id, role, timestamp) VALUES (?, ?, ?)`, r.sid, r.role, r.ts); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
}

func postRounds(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	home := t.TempDir()
	seedStateDB(t, home)
	r := chi.NewRouter()
	ConversationRoundsRouter(r, home)
	req := httptest.NewRequest(http.MethodPost, "/api/session/conversation-rounds", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}

func TestConversationRoundsCount(t *testing.T) {
	rr := postRounds(t, `{"session_id":"20260903_a"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var out struct {
		Ok         bool `json:"ok"`
		Rounds     int  `json:"rounds"`
		Threshold  int  `json:"threshold"`
		ShouldShow bool `json:"should_show"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Ok {
		t.Fatalf("ok = false")
	}
	// trace: user(1)+asst(2)=round1 closed at user(3); user(3)+user(4) merged;
	// asst(5)+asst(6) close round2; user(7)+asst(8)=round3 closed at end.
	if out.Rounds != 3 {
		t.Errorf("rounds = %d, want 3", out.Rounds)
	}
	if out.Threshold != 10 {
		t.Errorf("threshold = %d, want 10", out.Threshold)
	}
	if out.ShouldShow {
		t.Errorf("should_show = true for 2 rounds, want false")
	}
}

func TestConversationRoundsIncompleteSession(t *testing.T) {
	rr := postRounds(t, `{"session_id":"20260903_b"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var out struct {
		Rounds int `json:"rounds"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Rounds != 0 {
		t.Errorf("rounds = %d, want 0", out.Rounds)
	}
}

func TestConversationRoundsSinceFilter(t *testing.T) {
	// since=5 counts only messages with ts>5 → user(7)+assistant(8)=1 round.
	rr := postRounds(t, `{"session_id":"20260903_a","since":5}`)
	var out struct {
		Rounds int `json:"rounds"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Rounds != 1 {
		t.Errorf("rounds = %d, want 1 (since=5)", out.Rounds)
	}
}

func TestConversationRoundsMissingSessionID(t *testing.T) {
	rr := postRounds(t, `{}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestConversationRoundsNoStateDB(t *testing.T) {
	// home without state.db → rounds 0, ok true (Python parity: returns 0).
	home := t.TempDir()
	r := chi.NewRouter()
	ConversationRoundsRouter(r, home)
	req := httptest.NewRequest(http.MethodPost, "/api/session/conversation-rounds", strings.NewReader(`{"session_id":"x"}`))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	var out struct {
		Ok     bool `json:"ok"`
		Rounds int  `json:"rounds"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Ok || out.Rounds != 0 {
		t.Errorf("no state.db: ok=%v rounds=%d, want ok round 0", out.Ok, out.Rounds)
	}
}
