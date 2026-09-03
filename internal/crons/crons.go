// Package crons reads Hermes cron job state directly from disk.
//
// The scheduler (create/pause/resume/run/advance) is deliberately NOT
// reimplemented here (see 01-architecture-design.md §2c): it lives in the
// Hermes agent process with per-profile HERMES_HOME pinning and cross-process
// locking. This package only reads the two file-backed surfaces the WebUI
// needs — jobs.json and the output/ directory — which are safe to read
// without owning the lock.
package crons

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// jobIDRe mirrors Python's cron job_id boundary exactly.
var jobIDRe = ValidJobID

// ValidJobID checks the Python cron job_id regex boundary.
func ValidJobID(id string) bool {
	if id == "" || id == "." || id == ".." || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_' && r != '.' {
			return false
		}
	}
	return true
}

// List returns the jobs array from <home>/cron/jobs.json, disabled jobs
// included. A missing file yields an empty slice, matching the Python
// graceful-degradation contract (cron_unavailable handled by the caller).
func List(home string) ([]map[string]any, error) {
	raw, err := os.ReadFile(filepath.Join(home, "cron", "jobs.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	var doc struct {
		Jobs []map[string]any `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if doc.Jobs == nil {
		return []map[string]any{}, nil
	}
	return doc.Jobs, nil
}

// Output returns the newest <limit> markdown outputs for jobID, newest first.
// Each entry carries {filename, content}. Traversal-shaped jobIDs are rejected
// before any path resolution.
func Output(home, jobID string, limit int) ([]map[string]any, error) {
	if !jobIDRe(jobID) {
		return nil, fmt.Errorf("invalid job_id")
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	outDir := filepath.Join(home, "cron", "output", jobID)
	entries, err := os.ReadDir(outDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	md := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			md = append(md, e.Name())
		}
	}
	sort.Slice(md, func(i, j int) bool {
		si, _ := os.Stat(filepath.Join(outDir, md[i]))
		sj, _ := os.Stat(filepath.Join(outDir, md[j]))
		return si.ModTime().After(sj.ModTime())
	})
	if len(md) > limit {
		md = md[:limit]
	}
	out := make([]map[string]any, 0, len(md))
	for _, name := range md {
		content, err := os.ReadFile(filepath.Join(outDir, name))
		if err != nil {
			continue
		}
		out = append(out, map[string]any{"filename": name, "content": string(content)})
	}
	return out, nil
}
