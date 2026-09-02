package approval

import (
	"os"
	"path/filepath"
	"testing"

	"hermes-web-go/internal/store"
)

func TestImportPythonAllowlist(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "webui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	config := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(config, []byte("command_allowlist:\n  - rm -rf workspace\n  - shell command via -c/-lc flag\nplugins:\n  disabled: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ImportPythonAllowlist(db, config); err != nil {
		t.Fatal(err)
	}
	keys, err := NewSQLitePersistence(db).LoadPermanent()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] != "rm -rf workspace" || keys[1] != "shell command via -c/-lc flag" {
		t.Fatalf("unexpected imported keys: %#v", keys)
	}
}

func TestImportPythonAllowlistMissingIsNoop(t *testing.T) {
	if err := ImportPythonAllowlist(nil, filepath.Join(t.TempDir(), "missing.yaml")); err != nil {
		t.Fatal(err)
	}
}
