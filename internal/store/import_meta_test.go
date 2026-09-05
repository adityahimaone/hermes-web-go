package store

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "webui.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// A restart re-imports state.db sessions whose messages carry NO timing.
// The sidecar's existing _turnDuration/_usedModel must survive the upsert.
func TestImportSessionPreservesTurnMetaOnConflict(t *testing.T) {
	db := openTestDB(t)
	// Seed: sidecar has timing (what the live turn persisted).
	rich := `[{"role":"user","content":"hi"},{"role":"assistant","content":"Hai Adit! 👋","_turnDuration":14.3,"_usedModel":"codex"},{"role":"user","content":"malem"},{"role":"assistant","content":"Malem! 🌙","_turnDuration":9.2,"_usedModel":"codex"}]`
	if err := ImportSession(db, SessionImport{ID: "s1", Messages: rich}); err != nil {
		t.Fatal(err)
	}
	// Re-import (restart): state.db projection lacks timing.
	plain := `[{"role":"user","content":"hi"},{"role":"assistant","content":"Hai Adit! 👋"},{"role":"user","content":"malem"},{"role":"assistant","content":"Malem! 🌙"}]`
	if err := ImportSession(db, SessionImport{ID: "s1", Messages: plain}); err != nil {
		t.Fatal(err)
	}
	row, err := GetSession(db, "s1")
	if err != nil {
		t.Fatal(err)
	}
	var msgs []map[string]any
	if err := json.Unmarshal([]byte(row.Messages), &msgs); err != nil {
		t.Fatal(err)
	}
	durs := 0
	for _, m := range msgs {
		if m["role"] == "assistant" && m["_turnDuration"] != nil {
			durs++
		}
	}
	if durs != 2 {
		t.Fatalf("turn durations lost on re-import: got %d, want 2; messages=%s", durs, row.Messages)
	}
}

// Content-key match wins; only unmatched turns fall through positionally.
func TestImportSessionPreservesTurnMetaPositionalFallback(t *testing.T) {
	db := openTestDB(t)
	rich := `[{"role":"user","content":"q"},{"role":"assistant","content":"answer one","_turnDuration":7.5,"_usedModel":"codex"}]`
	if err := ImportSession(db, SessionImport{ID: "s2", Messages: rich}); err != nil {
		t.Fatal(err)
	}
	// state.db content drifted (trailing whitespace) → key miss → positional.
	plain := `[{"role":"user","content":"q"},{"role":"assistant","content":"answer one  "}]`
	if err := ImportSession(db, SessionImport{ID: "s2", Messages: plain}); err != nil {
		t.Fatal(err)
	}
	row, err := GetSession(db, "s2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(row.Messages, "_turnDuration") {
		t.Fatalf("positional fallback failed: %s", row.Messages)
	}
}

// Incoming assistant that already carries timing must never be clobbered.
func TestImportSessionKeepsIncomingTiming(t *testing.T) {
	db := openTestDB(t)
	rich := `[{"role":"assistant","content":"a","_turnDuration":9.1}]`
	if err := ImportSession(db, SessionImport{ID: "s3", Messages: rich}); err != nil {
		t.Fatal(err)
	}
	fresh := `[{"role":"assistant","content":"a","_turnDuration":12.0}]`
	if err := ImportSession(db, SessionImport{ID: "s3", Messages: fresh}); err != nil {
		t.Fatal(err)
	}
	row, err := GetSession(db, "s3")
	if err != nil {
		t.Fatal(err)
	}
	var msgs []map[string]any
	_ = json.Unmarshal([]byte(row.Messages), &msgs)
	if len(msgs) != 1 || msgs[0]["_turnDuration"].(float64) != 12.0 {
		t.Fatalf("incoming timing clobbered: %s", row.Messages)
	}
}

// No timing anywhere → import stays byte-identical (no rewrite churn).
func TestImportSessionNoMetaIsNoop(t *testing.T) {
	db := openTestDB(t)
	plain := `[{"role":"user","content":"q"},{"role":"assistant","content":"a"}]`
	if err := ImportSession(db, SessionImport{ID: "s4", Messages: plain}); err != nil {
		t.Fatal(err)
	}
	if err := ImportSession(db, SessionImport{ID: "s4", Messages: plain}); err != nil {
		t.Fatal(err)
	}
	row, err := GetSession(db, "s4")
	if err != nil {
		t.Fatal(err)
	}
	if row.Messages != plain {
		t.Fatalf("unexpected rewrite without meta: %s", row.Messages)
	}
}

// Brand-new session (no existing row) imports verbatim.
func TestImportSessionFreshInsertVerbatim(t *testing.T) {
	db := openTestDB(t)
	plain := `[{"role":"user","content":"q"}]`
	if err := ImportSession(db, SessionImport{ID: "s5", Messages: plain}); err != nil {
		t.Fatal(err)
	}
	row, err := GetSession(db, "s5")
	if err != nil {
		t.Fatal(err)
	}
	if row.Messages != plain {
		t.Fatalf("fresh insert altered: %s", row.Messages)
	}
}
