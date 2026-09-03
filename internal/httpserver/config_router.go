package httpserver

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"
)

// ConfigRouter serves Family-2 config-driven read routes. It resolves Hermes
// home from hermesHome (HERMES_HOME with $HOME/.hermes fallback) and reads
// config.yaml / profile directories directly — no agent or DB dependency.
func ConfigRouter(r chi.Router, hermesHome string) {
	r.Get("/api/profile/active", func(w http.ResponseWriter, req *http.Request) {
		home := hermesHome
		if home == "" {
			home = defaultHermesHome()
		}
		home = filepath.Clean(home)

		name := ""
		if parent := filepath.Base(filepath.Dir(home)); parent == "profiles" {
			name = filepath.Base(home)
		}
		isDefault := name == "" || name == "default"
		if name == "" {
			name = "default"
		}

		workspace := profileDefaultWorkspace(home)

		writeJSON(w, map[string]any{
			"name":              name,
			"path":              home,
			"is_default":        isDefault,
			"default_workspace": workspace,
		})
	})

	r.Get("/api/profiles", func(w http.ResponseWriter, req *http.Request) {
		home := hermesHome
		if home == "" {
			home = defaultHermesHome()
		}
		home = filepath.Clean(home)

		rows, err := listProfileRows(home)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list profiles")
			return
		}
		active := "default"
		if base := filepath.Base(home); filepath.Base(filepath.Dir(home)) == "profiles" {
			active = base
		}
		writeJSON(w, map[string]any{
			"profiles":            rows,
			"active":              active,
			"single_profile_mode": false,
		})
	})
}

// listProfileRows builds profile rows in Python's _build_profile_rows_fast
// shape. The base home row is always named "default"; named profiles come from
// <base>/profiles/<name> directories.
func listProfileRows(home string) ([]map[string]any, error) {
	base := home
	// If HERMES_HOME is itself a profile dir, the base is its parent's parent.
	if filepath.Base(filepath.Dir(home)) == "profiles" {
		base = filepath.Dir(filepath.Dir(home))
	}

	var rows []map[string]any

	defaultHome := base
	if _, err := os.Stat(defaultHome); err == nil {
		def := profileRow(defaultHome, "default", true)
		def["is_active"] = filepath.Clean(home) == filepath.Clean(defaultHome)
		rows = append(rows, def)
	}

	profilesRoot := filepath.Join(base, "profiles")
	entries, err := os.ReadDir(profilesRoot)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if !profileNameRE.MatchString(name) {
				continue
			}
			row := profileRow(filepath.Join(profilesRoot, name), name, false)
			row["is_active"] = filepath.Clean(home) == filepath.Clean(filepath.Join(profilesRoot, name))
			rows = append(rows, row)
		}
	}

	return rows, nil
}

var profileNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// profileRow mirrors Python _build_profile_rows_fast._row for the fields Go
// can read without the skills tree walk / gateway probe.
func profileRow(home, name string, isDefault bool) map[string]any {
	model, provider := configModel(home)
	skillCount := countEnableSkills(home)
	hasEnv := false
	if _, err := os.Stat(filepath.Join(home, ".env")); err == nil {
		hasEnv = true
	}
	return map[string]any{
		"name":            name,
		"path":            home,
		"is_default":      isDefault,
		"is_active":       false, // filled by caller
		"gateway_running": false,
		"model":           model,
		"provider":        provider,
		"has_env":         hasEnv,
		"visible":         profileVisibleFromMeta(home),
		"skill_count":     skillCount,
		"enabled_skills":  skillCount,
		"total_skills":    skillCount,
	}
}

// configModel reads model.default and model.provider from config.yaml.
func configModel(home string) (string, string) {
	cfg, err := readConfigYAML(home)
	if err != nil {
		return "", ""
	}
	modelMap, _ := cfg["model"].(map[string]any)
	if modelMap == nil {
		return "", ""
	}
	model, _ := modelMap["default"].(string)
	provider, _ := modelMap["provider"].(string)
	return model, provider
}

// profileDefaultWorkspace mirrors api/workspace.py get_profile_default_workspace:
// profile-scoped last_workspace.txt, then config.yaml workspace /
// default_workspace, then terminal.cwd.
func profileDefaultWorkspace(home string) string {
	if raw, err := os.ReadFile(filepath.Join(home, "last_workspace.txt")); err == nil {
		if p := strings.TrimSpace(string(raw)); p != "" && dirExists(p) {
			return p
		}
	}
	cfg, err := readConfigYAML(home)
	if err == nil {
		for _, key := range []string{"workspace", "default_workspace"} {
			if ws, ok := cfg[key].(string); ok && strings.TrimSpace(ws) != "" {
				return strings.TrimSpace(ws)
			}
		}
		if term, ok := cfg["terminal"].(map[string]any); ok {
			if cwd, ok := term["cwd"].(string); ok && strings.TrimSpace(cwd) != "" && strings.TrimSpace(cwd) != "." {
				return strings.TrimSpace(cwd)
			}
		}
	}
	return ""
}

// dirExists reports whether path exists and is a directory.
func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// profileVisibleFromMeta returns false only for an explicit visible: false in
// profile.yaml (mirrors Python _profile_visible_from_meta).
func profileVisibleFromMeta(home string) bool {
	raw, err := os.ReadFile(filepath.Join(home, "profile.yaml"))
	if err != nil {
		return true
	}
	var meta map[string]any
	if err := yaml.Unmarshal(raw, &meta); err != nil {
		return true
	}
	visible, ok := meta["visible"].(bool)
	return !ok || visible
}

// readConfigYAML loads <home>/config.yaml into a map.
func readConfigYAML(home string) (map[string]any, error) {
	raw, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// countEnableSkills counts <home>/skills/**/SKILL.md, mirroring Python's
// skills-tree walk without the caching/mtime machinery.
func countEnableSkills(home string) int {
	skillsDir := filepath.Join(home, "skills")
	count := 0
	_ = filepath.WalkDir(skillsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && d.Name() == "SKILL.md" {
			count++
		}
		return nil
	})
	return count
}
