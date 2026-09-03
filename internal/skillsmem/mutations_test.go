package skillsmem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSampleSkill(t *testing.T, home, name string) string {
	t.Helper()
	dir := filepath.Join(home, "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte("---\nname: "+name+"\n---\n# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSaveSkillRoundTrip(t *testing.T) {
	home := t.TempDir()
	path, err := SaveSkill(home, "My Cool Skill", "# content\n", "")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(filepath.Dir(path)) != "my-cool-skill" {
		t.Fatalf("dir = %q", path)
	}
	if got, _ := os.ReadFile(path); string(got) != "# content\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestSaveSkillRejectsTraversal(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{"../escape", "a/b", "..", ""} {
		if _, err := SaveSkill(home, name, "x", ""); err != ErrInvalidSkillName {
			t.Fatalf("name %q: err = %v", name, err)
		}
	}
	if _, err := SaveSkill(home, "ok", "x", "../cat"); err != ErrInvalidSkillName {
		t.Fatalf("category traversal: err = %v", err)
	}
}

func TestSaveSkillRejectsSymlinkedTarget(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "skills", "s")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// plant a symlink as SKILL.md
	outside := filepath.Join(home, "outside.md")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveSkill(home, "s", "new", ""); err != ErrSymlinkedTarget {
		t.Fatalf("err = %v", err)
	}
}

func TestDeleteSkillByDirNameAndFrontmatter(t *testing.T) {
	home := t.TempDir()
	writeSampleSkill(t, home, "alpha")
	if err := DeleteSkill(home, "alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "skills", "alpha")); !os.IsNotExist(err) {
		t.Fatalf("dir still exists: %v", err)
	}

	// frontmatter-name match: dir "x" but frontmatter name "beta"
	dir := filepath.Join(home, "skills", "x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: beta\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := DeleteSkill(home, "Beta"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("frontmatter-matched dir still exists: %v", err)
	}

	if err := DeleteSkill(home, "nope"); err != os.ErrNotExist {
		t.Fatalf("missing err = %v", err)
	}
}

func TestWriteMemoryRoundTrip(t *testing.T) {
	home := t.TempDir()
	got, err := WriteMemory(home, "memory", "# M\n")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(home, "memories", "MEMORY.md") {
		t.Fatalf("path = %q", got)
	}
	if b, _ := os.ReadFile(got); string(b) != "# M\n" {
		t.Fatalf("content = %q", b)
	}
	for _, sec := range []string{"user", "soul"} {
		if _, err := WriteMemory(home, sec, "x"); err != nil {
			t.Fatalf("section %q: %v", sec, err)
		}
	}
	if _, err := WriteMemory(home, "bogus", "x"); err != ErrInvalidSection {
		t.Fatalf("bogus section err = %v", err)
	}
}

func TestWriteMemoryRejectsSymlink(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, "memories")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(home, "outside.md")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(memDir, "MEMORY.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteMemory(home, "memory", "overwrite"); err != ErrSymlinkedTarget {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "secret" {
		t.Fatalf("outside file was clobbered: %q", b)
	}
}

func TestToggleSkillPreservesCommentsAndOrder(t *testing.T) {
	home := t.TempDir()
	cfg := "# top comment\nskills:\n  disabled:\n    - alpha\n    - beta\n  # keep this comment\n  something_else: true\nmemory:\n  memory_enabled: true\n"
	cfgPath := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ToggleSkill(home, "gamma", true); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(cfgPath)
	s := string(after)
	if !strings.Contains(s, "# top comment") || !strings.Contains(s, "# keep this comment") {
		t.Fatalf("comments lost:\n%s", s)
	}
	if !strings.Contains(s, "gamma") {
		t.Fatalf("gamma not added:\n%s", s)
	}
	if !strings.Contains(s, "something_else: true") {
		t.Fatalf("key order/content lost:\n%s", s)
	}

	// now disable alpha (already disabled) then enable it
	if err := ToggleSkill(home, "alpha", false); err != nil {
		t.Fatal(err)
	}
	after, _ = os.ReadFile(cfgPath)
	s = string(after)
	if strings.Contains(s, "- alpha") {
		t.Fatalf("alpha should be removed:\n%s", s)
	}
	if !strings.Contains(s, "gamma") {
		t.Fatalf("gamma should remain:\n%s", s)
	}
	if !strings.Contains(s, "# top comment") {
		t.Fatalf("comments lost on second toggle:\n%s", s)
	}
}

func TestToggleSkillCreatesDisabledWhenAbsent(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("model: gpt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ToggleSkill(home, "newskill", true); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(home, "config.yaml"))
	s := string(after)
	if !strings.Contains(s, "skills:") || !strings.Contains(s, "disabled:") || !strings.Contains(s, "newskill") {
		t.Fatalf("skills.disabled not created:\n%s", s)
	}
	if !strings.Contains(s, "model: gpt") {
		t.Fatalf("existing key lost:\n%s", s)
	}
}
