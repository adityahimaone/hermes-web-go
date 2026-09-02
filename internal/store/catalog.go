package store

import "database/sql"

// WorkspaceImport is a workspaces.json entry.
type WorkspaceImport struct {
	Path string
	Name string
}

// ProjectImport is a projects.json entry.
type ProjectImport struct {
	ID    string
	Name  string
	Color string
}

// ImportWorkspace upserts a workspace row.
func ImportWorkspace(db *sql.DB, w WorkspaceImport) error {
	_, err := db.Exec(`
		INSERT INTO workspaces (path, name) VALUES (?, ?)
		ON CONFLICT(path) DO UPDATE SET name=excluded.name`, w.Path, w.Name)
	return err
}

// ImportSetting upserts a settings row (value stored verbatim, typically JSON).
func ImportSetting(db *sql.DB, key, value string) error {
	_, err := db.Exec(`
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// ImportProject upserts a project row.
func ImportProject(db *sql.DB, p ProjectImport) error {
	_, err := db.Exec(`
		INSERT INTO projects (id, name, color) VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, color=excluded.color`, p.ID, p.Name, p.Color)
	return err
}

// WorkspaceRow is a read-model workspace surfaced to handlers.
type WorkspaceRow struct {
	Path string
	Name string
}

// ListWorkspaces returns all workspace rows ordered by insertion.
func ListWorkspaces(db *sql.DB) ([]WorkspaceRow, error) {
	rows, err := db.Query(`SELECT path, name FROM workspaces ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkspaceRow
	for rows.Next() {
		var w WorkspaceRow
		if err := rows.Scan(&w.Path, &w.Name); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
