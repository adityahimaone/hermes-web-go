package crons

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestListReadsJobsIncludingDisabled(t *testing.T) {
	home := t.TempDir()
	cronDir := filepath.Join(home, "cron")
	if err := os.MkdirAll(cronDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := map[string]any{"jobs": []any{
		map[string]any{"id": "active", "enabled": true},
		map[string]any{"id": "paused", "enabled": false},
	}}
	raw, _ := json.Marshal(data)
	if err := os.WriteFile(filepath.Join(cronDir, "jobs.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	jobs, err := List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || jobs[1]["id"] != "paused" {
		t.Fatalf("jobs = %#v", jobs)
	}
}

func TestOutputRejectsTraversalAndReturnsNewestLimited(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "cron", "output", "job-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"old.md", "new.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Output(home, "../job-1", 5); err == nil {
		t.Fatal("expected traversal rejection")
	}
	outputs, err := Output(home, "job-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 1 || outputs[0]["content"] == "" {
		t.Fatalf("outputs = %#v", outputs)
	}
}
