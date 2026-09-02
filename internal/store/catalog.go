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
// AddWorkspace registers or updates a workspace.
func AddWorkspace(db *sql.DB, path, name string) error {
	_, err := db.Exec(`INSERT INTO workspaces (path, name) VALUES (?, ?) ON CONFLICT(path) DO UPDATE SET name=excluded.name`, path, name)
	return err
}

// RemoveWorkspace removes a workspace and succeeds when it is absent.
func RemoveWorkspace(db *sql.DB, path string) error {
	_, err := db.Exec(`DELETE FROM workspaces WHERE path=?`, path)
	return err
}

// RenameWorkspace updates a registered workspace name.
func RenameWorkspace(db *sql.DB, path, name string) error {
	result, err := db.Exec(`UPDATE workspaces SET name=? WHERE path=?`, name, path)
	if err != nil { return err }
	n, _ := result.RowsAffected()
	if n == 0 { return sql.ErrNoRows }
	return nil
}

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
