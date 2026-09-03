package data

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"hermes-web-go/internal/store"
)

// DataHome returns the Hermes webui data directory (~/.hermes/webui).
func DataHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".hermes", "webui"), nil
}

// DBPath returns the SQLite path for a given data root.
func DBPath(dataRoot string) string {
	return filepath.Join(dataRoot, "webui.db")
}

// SessionJSON mirrors a legacy per-session JSON file. Fields absent in the real
// files are tolerated and default to empty strings.
type SessionJSON struct {
	SessionID string          `json:"session_id"`
	Title     string          `json:"title"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
	Messages  json.RawMessage `json:"messages"`
}

// WorkspacesJSON is the legacy workspaces.json list.
func WorkspacesJSON(dataRoot string) (string, error) {
	f := filepath.Join(dataRoot, "workspaces.json")
	b, err := os.ReadFile(f)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ImportSessionFile reads one wrapped session object and inserts it into the DB.
// It skips non-dict top-level JSON (e.g. the _index.json index list).
func ImportSessionFile(db *sql.DB, path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var s store.SessionImport
	// Use a raw map first so we can detect a non-object root without failing on
	// an index file (which is a JSON array).
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil // skip malformed/non-object, don't fail the whole import
	}
	copySessionJSON(&s, raw)
	if s.ID == "" {
		return nil
	}
	return store.ImportSession(db, s)
}

func copySessionJSON(s *store.SessionImport, raw map[string]json.RawMessage) {
	get := func(k string) string {
		var v string
		if r := raw[k]; r != nil {
			_ = json.Unmarshal(r, &v)
		}
		return v
	}
	s.ID = get("session_id")
	if s.ID == "" {
		return // refuse rows without an identifier; never import garbage
	}
	s.Title = get("title")
	s.Workspace = get("workspace")
	s.Model = get("model")
	s.CreatedAt = get("created_at")
	s.UpdatedAt = get("updated_at")
	if p := get("pinned"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			s.Pinned = v
		}
	}
	if a := get("archived"); a != "" {
		if v, err := strconv.Atoi(a); err == nil {
			s.Archived = v
		}
	}
	s.ProjectID = get("project_id")
	if r := raw["messages"]; r != nil {
		s.Messages = string(r)
	} else {
		s.Messages = "[]"
	}
	// Family-1 fields: embed the raw JSON (or defaults) so the projection
	// columns stay populated for /draft, /toolsets, /status consumers.
	if r := raw["enabled_toolsets"]; r != nil {
		s.EnabledToolsets = string(r)
	} else {
		s.EnabledToolsets = ""
	}
	if r := raw["composer_draft"]; r != nil {
		s.ComposerDraft = string(r)
	} else {
		s.ComposerDraft = ""
	}
}

// ImportSessions imports every *.json session file under a directory into DB,
// skipping files whose name is a non-session index (e.g. _index.json).
// It returns the number of sessions imported and any per-file error.
func ImportSessions(db *sql.DB, dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	count := 0
	var firstErr error
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		if strings.HasPrefix(name, "_") {
			continue // index/housekeeping file, not a session
		}
		if err := ImportSessionFile(db, filepath.Join(dir, name)); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		count++
	}
	return count, firstErr
}
