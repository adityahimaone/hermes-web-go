// Package skillsmem reads the Hermes skills registry and memory files (the
// Skills + Memory panels). It mirrors the WebUI's read surfaces: skills list
// from SKILL.md frontmatter, per-skill content (with traversal safety), and
// MEMORY.md/USER.md/SOUL.md. Writes are NOT implemented here — those stay
// behind the proxy so the agent-side config/memory layer owns them.
package skillsmem

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxDescription = 200

// ErrInvalidSkillName is returned for traversal-shaped or empty skill names.
var ErrInvalidSkillName = errors.New("invalid skill name")

// excludedDirNames mirrors tools.skills_tool._EXCLUDED_SKILL_DIRS.
var excludedDirNames = map[string]bool{
	".git": true, "__pycache__": true, "node_modules": true, ".venv": true, "venv": true,
}

// ListSkills walks <home>/skills for SKILL.md files and returns their
// frontmatter-derived name/description, with category from the relative path
// and duplicate names collapsed (first wins).
func ListSkills(home string) ([]map[string]any, error) {
	root := filepath.Join(home, "skills")
	var skills []map[string]any
	seen := make(map[string]bool)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && excludedDirNames[d.Name()] {
			return filepath.SkipDir
		}
		if d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		meta := parseFrontmatter(string(content))
		name := meta["name"]
		if name == "" {
			name = filepath.Base(filepath.Dir(path))
		}
		if name == "" || seen[name] {
			return nil
		}
		description := meta["description"]
		if description == "" {
			description = firstBodyLine(string(content))
		}
		if len(description) > maxDescription {
			description = description[:maxDescription-3] + "..."
		}
		seen[name] = true
		rel, _ := filepath.Rel(root, filepath.Dir(path))
		skills = append(skills, map[string]any{
			"name":        name,
			"description": description,
			"category":    categoryFor(rel),
			"disabled":    false,
		})
		return nil
	})
	sort.Slice(skills, func(i, j int) bool {
		return skills[i]["name"].(string) < skills[j]["name"].(string)
	})
	return skills, nil
}

// categoryFor returns the top path segment for nested skills, empty for flat.
func categoryFor(rel string) string {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) >= 1 && parts[0] != "." {
		return parts[0]
	}
	return ""
}

// parseFrontmatter extracts the frontmatter block (--- delimited) into a
// minimal string map — enough for name/description, without YAML deps.
func parseFrontmatter(content string) map[string]string {
	out := make(map[string]string)
	lines := strings.Split(content, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return out
	}
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			break
		}
		if i := strings.Index(trimmed, ":"); i > 0 {
			key := strings.TrimSpace(trimmed[:i])
			value := strings.TrimSpace(trimmed[i+1:])
			value = strings.Trim(value, `"'`)
			out[key] = value
		}
	}
	return out
}

// firstBodyLine returns the first non-heading, non-empty line after frontmatter.
func firstBodyLine(content string) string {
	lines := strings.Split(content, "\n")
	inFrontmatter := len(lines) > 0 && strings.TrimSpace(lines[0]) == "---"
	for i, line := range lines {
		if i == 0 && inFrontmatter {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if inFrontmatter {
			if trimmed == "---" {
				inFrontmatter = false
			}
			continue
		}
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			return trimmed
		}
	}
	return ""
}

// SkillContent returns the raw SKILL.md content for the skill named `name`.
// Names are validated to be a single path segment (no traversal).
func SkillContent(home, name string) (map[string]any, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return nil, ErrInvalidSkillName
	}
	root := filepath.Join(home, "skills")
	path := filepath.Join(root, name, "SKILL.md")
	if _, err := os.Stat(path); err != nil {
		path = ""
		_ = filepath.WalkDir(root, func(candidate string, d os.DirEntry, walkErr error) error {
			if walkErr != nil || path != "" {
				return nil
			}
			if d.IsDir() && excludedDirNames[d.Name()] {
				return filepath.SkipDir
			}
			if !d.IsDir() && d.Name() == "SKILL.md" {
				content, err := os.ReadFile(candidate)
				if err == nil && parseFrontmatter(string(content))["name"] == name {
					path = candidate
				}
			}
			return nil
		})
	}
	if path == "" {
		return nil, os.ErrNotExist
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return map[string]any{"content": string(content), "name": name}, nil
}

// ReadUsage reads skill telemetry from <home>/skills/.usage.json.
func ReadUsage(home string) (map[string]any, error) {
	usage := make(map[string]map[string]any)
	raw, err := os.ReadFile(filepath.Join(home, "skills", ".usage.json"))
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
	} else {
		var decoded map[string]any
		if json.Unmarshal(raw, &decoded) == nil {
			for name, record := range decoded {
				if m, ok := record.(map[string]any); ok && m != nil {
					usage[name] = m
				}
			}
		}
	}
	total, unique := 0, 0
	for _, record := range usage {
		count := 0
		for _, key := range []string{"use_count", "view_count", "patch_count"} {
			if n, ok := record[key].(float64); ok && n > 0 {
				count += int(n)
			}
		}
		total += count
		if count > 0 {
			unique++
		}
	}
	names := make([]string, 0, len(usage))
	for name := range usage {
		names = append(names, name)
	}
	sort.Strings(names)
	return map[string]any{"usage": usage, "skill_names": names, "total_invocations": total, "unique_skills_used": unique}, nil
}

// ReadMemory reads MEMORY.md/USER.md (under memories/) and SOUL.md (home root)
// plus their mtimes, matching the WebUI /api/memory shape. Missing files
// become empty strings / nil mtimes.
func ReadMemory(home string) (map[string]any, error) {
	read := func(rel string) (string, *float64) {
		path := filepath.Join(home, rel)
		fi, err := os.Stat(path)
		if err != nil {
			return "", nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return "", nil
		}
		mt := float64(fi.ModTime().Unix())
		return string(content), &mt
	}
	memory, memoryMtime := read(filepath.Join("memories", "MEMORY.md"))
	user, userMtime := read(filepath.Join("memories", "USER.md"))
	soul, soulMtime := read("SOUL.md")
	return map[string]any{
		"memory":       memory,
		"user":         user,
		"soul":         soul,
		"memory_path":  filepath.Join(home, "memories", "MEMORY.md"),
		"user_path":    filepath.Join(home, "memories", "USER.md"),
		"soul_path":    filepath.Join(home, "SOUL.md"),
		"memory_mtime": memoryMtime,
		"user_mtime":   userMtime,
		"soul_mtime":   soulMtime,
	}, nil
}
