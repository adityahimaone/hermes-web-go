package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRejectsTraversalAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := SafeResolve(root, "../../etc/passwd"); err == nil {
		t.Fatal("traversal accepted")
	}
	if _, err := SafeResolve(root, "escape/secret.txt"); err == nil {
		t.Fatal("symlink escape accepted")
	}
}

func TestListDirAndReadFile(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := ListDir(root, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name != "dir" {
		t.Fatalf("entries = %#v", entries)
	}
	got, err := ReadFile(root, "a.txt", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q", got)
	}
}
