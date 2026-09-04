package httpserver

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestReadSessionLineageReportChain(t *testing.T) {
	home := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY, source TEXT, session_source TEXT, title TEXT, started_at REAL, parent_session_id TEXT, ended_at REAL, end_reason TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	// root a1 (ended cli_close at 150) -> a2 (started 200, continuation)
	// a3 is a non-continuation child branch of a1
	_, err = db.Exec(`INSERT INTO sessions VALUES
		('a1','webui','webui','root',100,NULL,150,'cli_close'),
		('a2','webui','webui','child',200,'a1',NULL,NULL),
		('a3','webui','webui','branch',120,'a1',NULL,NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	rep := readSessionLineageReport(home, "a2")
	if rep["found"] != true {
		t.Fatalf("expected found=true, got %v", rep["found"])
	}
	if rep["lineage_key"] != "a1" {
		t.Fatalf("expected lineage_key=a1, got %v", rep["lineage_key"])
	}
	if rep["tip_session_id"] != "a2" {
		t.Fatalf("expected tip=a2, got %v", rep["tip_session_id"])
	}
	if rep["total_segments"] != 2 {
		t.Fatalf("expected 2 segments, got %v", rep["total_segments"])
	}
	kids, _ := rep["children"].([]any)
	if len(kids) != 1 {
		t.Fatalf("expected 1 child branch, got %d", len(kids))
	}
	if rep["manual_review"] != false {
		t.Fatalf("expected manual_review=false, got %v", rep["manual_review"])
	}
}

func TestReadSessionLineageReportUnknown(t *testing.T) {
	home := t.TempDir()
	rep := readSessionLineageReport(home, "nope")
	if rep["found"] != false {
		t.Fatalf("expected found=false")
	}
}

func TestAuditSessionRecoveryEmptyDir(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "webui", "sessions")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "s1.json"), []byte(`{"messages":[1,2,3]}`), 0o644)
	rep := auditSessionRecovery(home, dir)
	if rep["status"] != "ok" {
		t.Fatalf("expected ok, got %v", rep)
	}
	summary := rep["summary"].(map[string]any)
	if summary["ok"] != 1 {
		t.Fatalf("expected 1 ok, got %v", summary)
	}
}

func TestAuditSessionRecoveryOrphanBak(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "webui", "sessions")
	os.MkdirAll(dir, 0o755)
	// orphan .bak with no live file, no state.db row → unsafe
	os.WriteFile(filepath.Join(dir, "orph.json.bak"), []byte(`{"messages":[1]}`), 0o644)
	rep := auditSessionRecovery(home, dir)
	if rep["status"] != "needs_manual_review" {
		t.Fatalf("expected needs_manual_review, got %v", rep["status"])
	}
	items := rep["items"].([]map[string]any)
	if len(items) != 1 || items[0]["kind"] != "orphan_backup_without_state_row" {
		t.Fatalf("unexpected items: %v", items)
	}
}