package data

import (
	"os"
	"path/filepath"
	"testing"

	"hermes-web-go/internal/store"
)

func TestImportCatalogFiles(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("workspaces.json", `[{"path":"/tmp/a","name":"A"}]`)
	write("settings.json", `{"theme":"dark","enabled":true}`)
	write("projects.json", `[{"id":"p1","name":"P","color":"blue"}]`)

	db, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := ImportCatalog(db, root); err != nil {
		t.Fatal(err)
	}
	ws, err := store.ListWorkspaces(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 1 || ws[0].Path != "/tmp/a" {
		t.Fatalf("ws=%#v", ws)
	}
	var setting string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key='theme'`).Scan(&setting); err != nil {
		t.Fatal(err)
	}
	if setting != `"dark"` {
		t.Fatalf("setting=%q", setting)
	}
	var project string
	if err := db.QueryRow(`SELECT name FROM projects WHERE id='p1'`).Scan(&project); err != nil {
		t.Fatal(err)
	}
	if project != "P" {
		t.Fatalf("project=%q", project)
	}
}
