package store

import (
	"path/filepath"
	"testing"
)

func TestWorkspaceSettingsProjectsRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "webui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := ImportWorkspace(db, WorkspaceImport{Path: "/tmp/project", Name: "Project"}); err != nil {
		t.Fatal(err)
	}
	if err := ImportSetting(db, "theme", `"dark"`); err != nil {
		t.Fatal(err)
	}
	if err := ImportProject(db, ProjectImport{ID: "p1", Name: "Project", Color: "#fff"}); err != nil {
		t.Fatal(err)
	}

	workspaces, err := ListWorkspaces(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspaces) != 1 || workspaces[0].Path != "/tmp/project" || workspaces[0].Name != "Project" {
		t.Fatalf("workspaces = %#v", workspaces)
	}
	var setting string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key = 'theme'`).Scan(&setting); err != nil {
		t.Fatal(err)
	}
	if setting != `"dark"` {
		t.Fatalf("setting = %q", setting)
	}
	var projectName string
	if err := db.QueryRow(`SELECT name FROM projects WHERE id = 'p1'`).Scan(&projectName); err != nil {
		t.Fatal(err)
	}
	if projectName != "Project" {
		t.Fatalf("project = %q", projectName)
	}
}
