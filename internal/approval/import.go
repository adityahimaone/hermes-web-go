package approval

import (
	"bufio"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
)

// ImportPythonAllowlist reads the legacy Python `command_allowlist` (a YAML
// list in ~/.hermes/config.yaml) into the SQLite permanent-approval store.
// Missing file / empty list are no-ops. This mirrors what tools/approval.py
// does at startup (load_permanent_allowlist), so a user's existing "always"
// approvals carry over to Go.
//
// We parse the list directly rather than pulling in a YAML dependency: the
// parser only understands a top-level `key:` followed by `- item` lines,
// which is exactly the shape config.yaml uses for command_allowlist. Unknown
// sections are skipped.
func ImportPythonAllowlist(db *sql.DB, configPath string) error {
	if db == nil || configPath == "" {
		return nil
	}
	f, err := os.Open(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	var keys []string
	inAllowlist := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		// A new top-level key ends the allowlist section; indent detection
		// keeps us from swallowing nested sections.
		if !inAllowlist {
			if strings.HasPrefix(trimmed, "command_allowlist:") {
				inAllowlist = true
			}
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			keys = append(keys, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
			continue
		}
		// Any other non-empty line at column 0 exits the section.
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && trimmed != "" {
			break
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}
	return NewSQLitePersistence(db).SavePermanent(keys)
}

// AllowlistConfigPath returns the default location of the Python allowlist
// file, relative to a Hermes home dir, so callers don't hardcode it.
func AllowlistConfigPath(hermesHome string) string {
	return filepath.Join(hermesHome, "config.yaml")
}
