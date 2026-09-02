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

func TestSaveFileRejectsSymlinkAndTraversal(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "real.txt"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := SaveFile(root, "../escape.txt", []byte("x")); err == nil {
		t.Fatal("traversal save accepted")
	}
	if err := SaveFile(root, "link.txt", []byte("x")); err == nil {
		t.Fatal("symlink save accepted")
	}
	if err := SaveFile(root, "missing.txt", []byte("x")); err == nil {
		t.Fatal("save to nonexistent file accepted")
	}
	if err := SaveFile(root, "real.txt", []byte("new")); err != nil {
		t.Fatal(err)
	}
	b, _ := ReadFile(root, "real.txt", 1024)
	if string(b) != "new" {
		t.Fatalf("content = %q", b)
	}
}

func TestCreateFileRejectsExistingAndTraversal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "exists.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CreateFile(root, "exists.txt", []byte("y")); err == nil {
		t.Fatal("create over existing accepted")
	}
	if err := CreateFile(root, "../new.txt", []byte("y")); err == nil {
		t.Fatal("create traversal accepted")
	}
	if err := CreateFile(root, "fresh.txt", []byte("y")); err != nil {
		t.Fatal(err)
	}
	b, _ := ReadFile(root, "fresh.txt", 1024)
	if string(b) != "y" {
		t.Fatalf("content = %q", b)
	}
}

func TestDeleteFileRejectsSymlinkAndTraversal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "gone.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := DeleteFile(root, "../etc/passwd"); err == nil {
		t.Fatal("delete traversal accepted")
	}
	if err := DeleteFile(root, "link"); err == nil {
		t.Fatal("delete symlink accepted")
	}
	if err := DeleteFile(root, "missing.txt"); err == nil {
		t.Fatal("delete missing accepted")
	}
	if err := DeleteFile(root, "gone.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "gone.txt")); !os.IsNotExist(err) {
		t.Fatalf("file still exists: %v", err)
	}
}
