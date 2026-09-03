package httpserver

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"
)

// ConfigRouter serves Family-2 config-driven read routes. It resolves Hermes
// home from hermesHome (HERMES_HOME with $HOME/.hermes fallback), reads
// config.yaml / profile directories directly, and reads webui settings from
// dataRoot (HERMES_WEBUI_DATA_ROOT, default ~/.hermes/webui) — no agent or DB
// dependency.
func ConfigRouter(r chi.Router, hermesHome, dataRoot string) {
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

	r.Get("/api/settings", func(w http.ResponseWriter, req *http.Request) {
		home := hermesHome
		if home == "" {
			home = defaultHermesHome()
		}
		settings := loadWebUISettings(dataRoot, home)
		writeJSON(w, settings)
	})
}

// settingsDefaults mirrors api/config.py _SETTINGS_DEFAULTS. A stored value in
// settings.json overrides each; the map is the fallback for missing keys.
var settingsDefaults = map[string]any{
	"default_workspace":                   "",
	"onboarding_completed":                false,
	"send_key":                            "enter",
	"show_token_usage":                    false,
	"show_quota_chip":                     false,
	"show_conversation_outline":           false,
	"show_busy_placeholder_hint":          false,
	"hide_empty_state_suggestions":        false,
	"hide_empty_state_panel":              false,
	"new_chat_on_workspace_switch":        false,
	"virtualize_transcript":               false,
	"virtualize_transcript_optin":         false,
	"show_tps":                            false,
	"fade_text_effect":                    false,
	"show_cli_sessions":                   true,
	"show_claude_code_sessions":           true,
	"show_cron_sessions":                  false,
	"show_webhook_sessions":               false,
	"show_kanban_sessions":                false,
	"show_previous_messaging_sessions":    false,
	"sync_to_insights":                    false,
	"check_for_updates":                   true,
	"update_channel":                      "stable",
	"ignore_agent_updates":                false,
	"whats_new_summary_enabled":           false,
	"tts_enabled":                         false,
	"tts_auto_read":                       false,
	"tts_engine":                          "browser",
	"tts_voice":                           "",
	"tts_rate":                            1.0,
	"tts_pitch":                           1.0,
	"voice_mode_button":                   false,
	"voice_continuous":                    false,
	"voice_silence_ms":                    1800,
	"raw_audio_mode":                      false,
	"theme":                               "dark",
	"skin":                                "default",
	"font_size":                           "default",
	"session_jump_buttons":                false,
	"render_user_markdown":                false,
	"large_text_paste_as_attachment":      true,
	"project_quick_create_buttons":        false,
	"structured_code_default_view":        "auto",
	"structured_code_auto_tree_lines":     10,
	"session_endless_scroll":              false,
	"chat_activity_display_mode":          "compact_worklog",
	"transparent_stream_event_timestamps": true,
	"auto_scroll_follow":                  true,
	"worklog_details_expanded_default":    false,
	"hide_composer_attach":                false,
	"hide_composer_saved_prompts":         false,
	"hide_composer_mic":                   false,
	"show_titlebar_profile":               false,
	"hide_composer_voice_mode":            false,
	"hide_composer_yolo":                  false,
	"hide_composer_profile":               false,
	"hide_composer_workspace":             false,
	"hide_composer_mobile_config":         false,
	"hide_composer_model":                 false,
	"hide_composer_quota_chip":            false,
	"hide_composer_reasoning":             false,
	"hide_composer_toolsets":              false,
	"hide_composer_status":                false,
	"hide_composer_context":               false,
	"hide_composer_bg_badge":              false,
	"pinned_sessions_limit":               3,
	"inflight_state_max_sessions":         8,
	"inflight_state_max_messages":         24,
	"inflight_state_max_tool_calls":       48,
	"inflight_state_max_string_chars":     60000,
	"inflight_state_max_json_chars":       1500000,
	"hidden_tabs":                         []string{},
	"tab_order":                           []string{},
	"composer_control_order":              []string{},
	"language":                            "en",
	"sound_enabled":                       false,
	"rtl":                                 false,
	"notifications_enabled":               false,
	"show_thinking":                       true,
	"simplified_tool_calling":             true,
	"terminal_auto_expand_on_output":      false,
	"workspace_todos_tab":                 false,
	"api_redact_enabled":                  true,
	"dashboard_plugins":                   map[string]any{},
	"sidebar_density":                     "compact",
	"auto_title_refresh_every":            "0",
	"default_message_mode":                "steer",
	"password_hash":                       nil,
	"auth_disabled_acknowledged":          false,
	"provider_cost_budget":                nil,
}

// speechKeys mirrors api/config.py _SETTINGS_SPEECH_KEYS.
var speechKeys = map[string]bool{
	"tts_enabled": true, "tts_auto_read": true, "tts_engine": true, "tts_voice": true,
	"tts_rate": true, "tts_pitch": true, "voice_mode_button": true, "voice_continuous": true,
	"voice_silence_ms": true,
}

// loadWebUISettings mirrors api/config.py load_settings + the GET /api/settings
// route injections (minus auth/version/update-channel which need other modules).
func loadWebUISettings(dataRoot, home string) map[string]any {
	settings := make(map[string]any, len(settingsDefaults)+8)
	for k, v := range settingsDefaults {
		settings[k] = v
	}
	// bot_name from env or default "Hermes"
	settings["bot_name"] = "Hermes"
	if bn := os.Getenv("HERMES_WEBUI_BOT_NAME"); strings.TrimSpace(bn) != "" {
		settings["bot_name"] = bn
	}

	stored := readRawSettings(dataRoot)
	if len(stored) > 0 {
		// Legacy migration: busy_input_mode → default_message_mode
		if _, hasNew := stored["default_message_mode"]; !hasNew {
			if bim, hasOld := stored["busy_input_mode"]; hasOld {
				stored["default_message_mode"] = bim
			}
		}
		delete(stored, "busy_input_mode")
		for k, v := range stored {
			if _, ok := settingsDefaults[k]; ok && k != "password_hash" {
				settings[k] = v
			}
		}
		// show_cli_sessions grandfather: established installs without the key
		// stay OFF.
		if _, has := stored["show_cli_sessions"]; !has {
			established := false
			if ob, ok := stored["onboarding_completed"].(bool); ok && ob {
				established = true
			} else {
				for k := range stored {
					if k != "show_cli_sessions" && k != "onboarding_completed" {
						established = true
						break
					}
				}
			}
			if established {
				settings["show_cli_sessions"] = false
			}
		}
		// virtualize_transcript force-off without optin marker.
		if vt, ok := stored["virtualize_transcript"].(bool); ok && vt {
			optin, _ := stored["virtualize_transcript_optin"].(bool)
			if !optin {
				settings["virtualize_transcript"] = false
			}
		}
	}
	// Appearance normalize: stored theme/skin pair wins; else defaults.
	settings["theme"], settings["skin"] = normalizeAppearance(stored, settings)

	// Route-level injections.
	settings["persisted_speech_keys"] = persistedSpeechKeys(stored)
	delete(settings, "password_hash")

	// default_model / default_model_provider from config.yaml model.default.
	if model, provider := configModel(home); model != "" {
		settings["default_model"] = model
		if provider != "" {
			settings["default_model_provider"] = provider
		}
	}
	return settings
}

// readRawSettings loads dataRoot/settings.json into a map; {} when missing or
// malformed (Python _read_raw_settings_file parity).
func readRawSettings(dataRoot string) map[string]any {
	if dataRoot == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(dataRoot, "settings.json"))
	if err != nil {
		return nil
	}
	var stored map[string]any
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil
	}
	if stored == nil {
		return nil
	}
	return stored
}

// normalizeAppearance mirrors Python _normalize_appearance: validates theme /
// skin against allowed values, falling back to defaults for unknown values.
func normalizeAppearance(stored, settings map[string]any) (string, string) {
	themeDefault := "dark"
	skinDefault := "default"
	if d, ok := settingsDefaults["theme"].(string); ok {
		themeDefault = d
	}
	if d, ok := settingsDefaults["skin"].(string); ok {
		skinDefault = d
	}
	theme := themeDefault
	skin := skinDefault
	if _, hasTheme := stored["theme"]; hasTheme {
		if t, ok := stored["theme"].(string); ok && (t == "light" || t == "dark" || t == "system") {
			theme = t
		}
	}
	if _, hasSkin := stored["skin"]; hasSkin {
		if s, ok := stored["skin"].(string); ok && s != "" {
			skin = s
		}
	}
	return theme, skin
}

// persistedSpeechKeys mirrors api/config.py persisted_speech_settings_keys:
// sorted list of speech keys present in the stored settings file.
func persistedSpeechKeys(stored map[string]any) []string {
	if len(stored) == 0 {
		return []string{}
	}
	var keys []string
	for k := range speechKeys {
		if _, ok := stored[k]; ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
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
