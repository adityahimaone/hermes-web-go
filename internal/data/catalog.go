package data

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"

	"hermes-web-go/internal/store"
)

// ImportCatalog imports the legacy workspaces.json, settings.json and
// projects.json files into SQLite. Missing files are tolerated; malformed
// files are skipped rather than aborting the import.
func ImportCatalog(db *sql.DB, root string) error {
	// workspaces.json: array of {"path","name"}
	if b, err := os.ReadFile(filepath.Join(root, "workspaces.json")); err == nil {
		var ws []map[string]string
		if json.Unmarshal(b, &ws) == nil {
			for _, w := range ws {
				if w["path"] == "" {
					continue
				}
				if err := store.ImportWorkspace(db, store.WorkspaceImport{Path: w["path"], Name: w["name"]}); err != nil {
					return err
				}
			}
		}
	}
	// settings.json: map[String]JSON
	if b, err := os.ReadFile(filepath.Join(root, "settings.json")); err == nil {
		var settings map[string]json.RawMessage
		if json.Unmarshal(b, &settings) == nil {
			for k, v := range settings {
				if err := store.ImportSetting(db, k, string(v)); err != nil {
					return err
				}
			}
		}
	}
	// projects.json: array of {"id","name","color"}
	if b, err := os.ReadFile(filepath.Join(root, "projects.json")); err == nil {
		var projects []map[string]string
		if json.Unmarshal(b, &projects) == nil {
			for _, p := range projects {
				if p["id"] == "" {
					continue
				}
				if err := store.ImportProject(db, store.ProjectImport{ID: p["id"], Name: p["name"], Color: p["color"]}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
