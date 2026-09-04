package httpserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestExtensionInstallUninstallFlow(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "extensions")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	dataRoot := dir

	manifest := loadInstallManifest(dataRoot)
	installed := manifest["installed"].(map[string]any)
	installed["demo"] = map[string]any{"version": "9.9.9", "files": []any{"extension.json", "main.js", "assets/sub/x.css"}, "installed_at": "t"}
	manifest["installed"] = installed
	if err := writeInstallManifest(dataRoot, manifest); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"extension.json", "main.js", "assets/sub/x.css"} {
		p := filepath.Join(root, "demo", rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x"), 0o644)
	}
	st, payload := handleExtensionUninstall(dataRoot, map[string]any{"id": "demo"})
	if st != 200 || payload["uninstalled"] != true {
		t.Fatalf("uninstall failed: %d %v", st, payload)
	}
	if _, err := os.Stat(filepath.Join(root, "demo")); !os.IsNotExist(err) {
		t.Fatalf("ext dir still exists")
	}
	if m := loadInstallManifest(dataRoot)["installed"].(map[string]any); len(m) != 0 {
		t.Fatalf("manifest entry not removed: %v", m)
	}
	_ = bytes.MinRead
	_ = sha256.New
	_ = hex.EncodeToString
}

func TestExtensionStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "extensions"), 0o755)
	st, payload := handleExtensionSidecarConsent(dir, dir, map[string]any{"id": "demo", "approved": true, "origin": "http://127.0.0.1:9000"})
	if st != 200 {
		t.Fatalf("consent failed: %d %v", st, payload)
	}
	state := loadExtensionState(dir)
	consents := state["sidecar_proxy_consents"].(map[string]string)
	if consents["demo"] != "http://127.0.0.1:9000" {
		t.Fatalf("consent missing: %v", consents)
	}
	st, _ = handleExtensionSidecarConsent(dir, dir, map[string]any{"id": "demo", "approved": false})
	if st != 200 {
		t.Fatal("revoke failed")
	}
	if len(loadExtensionState(dir)["sidecar_proxy_consents"].(map[string]string)) != 0 {
		t.Fatal("consent not revoked")
	}
}
