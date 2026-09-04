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
	"time"
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

// Recent returns cron jobs whose last run completed after the `since` epoch
// (seconds), mirroring Python _handle_cron_recent. The state.db session join
// (session_id / message_count) is not available here — callers get those as
// absent keys. Each entry: {job_id, name, status, completed_at, toast_notifications}.
func Recent(home string, since float64) ([]map[string]any, error) {
	jobs, err := List(home)
	if err != nil {
		return nil, err
	}
	var completions []map[string]any
	for _, job := range jobs {
		jobID := strAny(job["id"])
		if jobID == "" {
			continue
		}
		ts, ok := parseLastRun(job["last_run_at"])
		if !ok || ts <= since {
			continue
		}
		status := strAny(job["last_status"])
		if status == "" {
			status = "unknown"
		}
		toast := true
		if job["toast_notifications"] == false {
			toast = false
		}
		completions = append(completions, map[string]any{
			"job_id":              jobID,
			"name":                strAnyDefault(job["name"], "Unknown"),
			"status":              status,
			"completed_at":        ts,
			"toast_notifications": toast,
		})
	}
	if completions == nil {
		completions = []map[string]any{}
	}
	return completions, nil
}

// History lists cron run output files (metadata only) for jobID, newest first,
// with pagination. Mirrors Python _handle_cron_history. `usage` is emitted as
// an empty map (ponytail: parse **Model:/**Tokens:/**Cost: front-matter from
// the .md head when the WebUI run-history panel needs it).
func History(home, jobID string, offset, limit int) ([]map[string]any, int, error) {
	if !jobIDRe(jobID) {
		return nil, 0, fmt.Errorf("invalid job_id")
	}
	if offset < 0 {
		offset = 0
	}
	if limit < 1 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	outDir := filepath.Join(home, "cron", "output", jobID)
	entries, err := os.ReadDir(outDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []map[string]any{}, 0, nil
		}
		return nil, 0, err
	}
	var md []string
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
	total := len(md)
	if offset >= total {
		return []map[string]any{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	out := make([]map[string]any, 0, end-offset)
	for _, name := range md[offset:end] {
		st, err := os.Stat(filepath.Join(outDir, name))
		if err != nil {
			continue
		}
		out = append(out, map[string]any{
			"filename": name,
			"size":     st.Size(),
			"modified": float64(st.ModTime().UnixNano()) / 1e9,
			"usage":    map[string]any{},
		})
	}
	return out, total, nil
}

// parseLastRun converts the jobs.json last_run_at value (RFC3339 string or
// epoch number) to epoch seconds. Returns ok=false on any parse failure.
func parseLastRun(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case string:
		if t == "" {
			return 0, false
		}
		// RFC3339 (with optional Z / timezone offset). time.Parse returns
		// a time.Time; UnixNano() gives nanoseconds since the epoch.
		parsed, err := time.Parse(time.RFC3339Nano, strings.Replace(t, "Z", "+00:00", 1))
		if err != nil {
			return 0, false
		}
		return float64(parsed.UnixNano()) / 1e9, true
	}
	return 0, false
}

func strAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func strAnyDefault(v any, def string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return def
}