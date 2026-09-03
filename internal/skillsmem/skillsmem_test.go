package skillsmem

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListSkillsParsesFrontmatter(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "skills", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: demo\ndescription: Demo skill\n---\n# Demo\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	skills, err := ListSkills(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0]["name"] != "demo" || skills[0]["description"] != "Demo skill" {
		t.Fatalf("skills = %#v", skills)
	}
}

func TestSkillContentRejectsTraversal(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "skills", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: demo\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SkillContent(home, "../demo"); err == nil {
		t.Fatal("expected invalid skill name")
	}
	got, err := SkillContent(home, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if got["content"] != "---\nname: demo\n---\nbody" {
		t.Fatalf("content = %#v", got)
	}
}

func TestSkillContentFindsNestedByFrontmatterName(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "skills", "category", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: demo\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := SkillContent(home, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if got["content"] != "---\nname: demo\n---\nbody" {
		t.Fatalf("content = %#v", got)
	}
}

func TestReadMemoryReturnsAllFiles(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "memories"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, value := range map[string]string{"memories/MEMORY.md": "m", "memories/USER.md": "u", "SOUL.md": "s"} {
		if err := os.WriteFile(filepath.Join(home, path), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ReadMemory(home)
	if err != nil {
		t.Fatal(err)
	}
	if got["memory"] != "m" || got["user"] != "u" || got["soul"] != "s" {
		t.Fatalf("memory = %#v", got)
	}
}

func TestReadUsage(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"demo":{"use_count":2,"view_count":1,"patch_count":0,"last_used":"today"},"broken":"ignored"}`
	if err := os.WriteFile(filepath.Join(home, "skills", ".usage.json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadUsage(home)
	if err != nil {
		t.Fatal(err)
	}
	usage := got["usage"].(map[string]map[string]any)
	if usage["demo"]["last_used"] != "today" {
		t.Fatalf("usage = %#v", usage)
	}
	if got["total_invocations"] != 3 || got["unique_skills_used"] != 1 {
		t.Fatalf("totals = %#v", got)
	}
}
