package httpserver

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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

	r.Post("/api/settings", func(w http.ResponseWriter, req *http.Request) {
		home := hermesHome
		if home == "" {
			home = defaultHermesHome()
		}
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		// Auth-bound settings (password / passwordless / passkey) stay on the
		// Python side until the auth module is ported. Refuse loudly rather
		// than half-apply.
		for _, k := range []string{"_set_password", "_clear_password", "_passwordless", "_current_password"} {
			if _, has := body[k]; has {
				writeError(w, http.StatusNotImplemented, k+" requires the legacy auth backend; proxy still owns password settings")
				return
			}
		}
		saved, err := saveWebUISettings(dataRoot, home, body)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save settings")
			return
		}
		writeJSON(w, saved)
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

// ── Settings save (POST /api/settings) ─────────────────────────────────────

// settingsEnumValues mirrors api/config.py _SETTINGS_ENUM_VALUES.
var settingsEnumValues = map[string]map[string]bool{
	"send_key":                     {"enter": true, "ctrl+enter": true, "shift+enter": true, "ctrl+shift+enter": true},
	"sidebar_density":              {"compact": true, "detailed": true},
	"update_channel":               {"stable": true, "experimental": true},
	"font_size":                    {"small": true, "default": true, "large": true, "xlarge": true},
	"auto_title_refresh_every":     {"0": true, "5": true, "10": true, "20": true},
	"default_message_mode":         {"queue": true, "interrupt": true, "steer": true},
	"chat_activity_display_mode":   {"compact_worklog": true, "transparent_stream": true, "hide_all_activity": true},
	"structured_code_default_view": {"auto": true, "on": true, "off": true},
}

// settingsIntRanges mirrors api/config.py _SETTINGS_INT_RANGES.
var settingsIntRanges = map[string][2]int64{
	"pinned_sessions_limit":           {1, 99},
	"inflight_state_max_sessions":     {1, 25},
	"inflight_state_max_messages":     {1, 100},
	"inflight_state_max_tool_calls":   {1, 200},
	"inflight_state_max_string_chars": {1000, 500000},
	"inflight_state_max_json_chars":   {100000, 4000000},
	"structured_code_auto_tree_lines": {1, 1000},
	"voice_silence_ms":                {200, 60000},
}

// settingsFloatRanges mirrors api/config.py _SETTINGS_FLOAT_RANGES.
var settingsFloatRanges = map[string][2]float64{
	"tts_rate":  {0.5, 2.0},
	"tts_pitch": {0.0, 2.0},
}

// settingsBoolKeys mirrors api/config.py _SETTINGS_BOOL_KEYS.
var settingsBoolKeys = map[string]bool{
	"onboarding_completed": true, "show_token_usage": true, "show_quota_chip": true,
	"show_conversation_outline": true, "show_busy_placeholder_hint": true,
	"hide_empty_state_suggestions": true, "hide_empty_state_panel": true,
	"new_chat_on_workspace_switch": true, "virtualize_transcript": true,
	"virtualize_transcript_optin": true, "show_tps": true, "fade_text_effect": true,
	"show_cli_sessions": true, "show_claude_code_sessions": true,
	"show_cron_sessions": true, "show_webhook_sessions": true,
	"show_kanban_sessions": true, "show_previous_messaging_sessions": true,
	"sync_to_insights": true, "check_for_updates": true, "ignore_agent_updates": true,
	"whats_new_summary_enabled": true, "tts_enabled": true, "tts_auto_read": true,
	"voice_mode_button": true, "voice_continuous": true, "raw_audio_mode": true,
	"session_jump_buttons": true, "render_user_markdown": true,
	"large_text_paste_as_attachment": true, "project_quick_create_buttons": true,
	"session_endless_scroll": true, "transparent_stream_event_timestamps": true,
	"auto_scroll_follow": true, "worklog_details_expanded_default": true,
	"hide_composer_attach": true, "hide_composer_saved_prompts": true,
	"hide_composer_mic": true, "show_titlebar_profile": true,
	"hide_composer_voice_mode": true, "hide_composer_yolo": true,
	"hide_composer_profile": true, "hide_composer_workspace": true,
	"hide_composer_mobile_config": true, "hide_composer_model": true,
	"hide_composer_quota_chip": true, "hide_composer_reasoning": true,
	"hide_composer_toolsets": true, "hide_composer_status": true,
	"hide_composer_context": true, "hide_composer_bg_badge": true,
	"sound_enabled": true, "rtl": true, "notifications_enabled": true,
	"show_thinking": true, "simplified_tool_calling": true,
	"terminal_auto_expand_on_output": true, "workspace_todos_tab": true,
	"api_redact_enabled": true, "auth_disabled_acknowledged": true,
}

// settingsLegacyThemeMap mirrors api/config.py _SETTINGS_LEGACY_THEME_MAP.
var settingsLegacyThemeMap = map[string][2]string{
	"slate":     {"dark", "slate"},
	"solarized": {"dark", "poseidon"},
	"monokai":   {"dark", "sisyphus"},
	"nord":      {"dark", "slate"},
	"oled":      {"dark", "default"},
}

var settingsThemeValues = map[string]bool{"light": true, "dark": true, "system": true}

var settingsSkinValues = map[string]bool{
	"default": true, "ares": true, "mono": true, "graphite": true, "slate": true,
	"poseidon": true, "sisyphus": true, "charizard": true, "sienna": true,
	"catppuccin": true, "nous": true, "geist-contrast": true, "zeus": true,
	"verdigris": true, "neon-soft": true, "neon-paint": true,
}

var settingsLangRE = regexp.MustCompile(`^[a-zA-Z]{2,10}(-[a-zA-Z0-9]{2,8})?$`)
var settingsTTSEngineRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// saveWebUISettings mirrors api/config.py save_settings minus auth fields.
// Returns the merged settings payload (like the Python response) and writes
// settings.json atomically.
func saveWebUISettings(dataRoot, home string, body map[string]any) (map[string]any, error) {
	stored := readRawSettings(dataRoot)
	if stored == nil {
		stored = map[string]any{}
	}
	// persisted_speech_keys snapshot BEFORE merge (like Python).
	prevSpeechKeys := persistedSpeechKeys(stored)
	appliedSpeechKeys := map[string]bool{}

	current := loadWebUISettings(dataRoot, home) // merged defaults + stored

	// Legacy renames (like Python save_settings).
	if _, hasNew := body["default_message_mode"]; !hasNew {
		if bim, hasOld := body["busy_input_mode"]; hasOld {
			body["default_message_mode"] = bim
		}
	}
	delete(body, "busy_input_mode")
	delete(body, "simplified_tool_calling")
	if _, hasNew := body["worklog_details_expanded_default"]; !hasNew {
		if adf, hasOld := body["activity_feed_expanded_default"]; hasOld {
			body["worklog_details_expanded_default"] = adf
		}
	}
	delete(body, "activity_feed_expanded_default")

	// dashboard_plugins deep-merge.
	if plugins, ok := body["dashboard_plugins"].(map[string]any); ok {
		cur := map[string]any{}
		if c, ok := current["dashboard_plugins"].(map[string]any); ok {
			cur = c
		}
		for k, v := range plugins {
			cur[k] = boolVal(v)
		}
		current["dashboard_plugins"] = cur
	}

	pendingTheme := asString(current["theme"])
	pendingSkin := asString(current["skin"])
	themeExplicit := false
	skinExplicit := false

	for k, v := range body {
		if k == "dashboard_plugins" {
			continue
		}
		if !isAllowedSettingsKey(k) {
			continue
		}
		switch k {
		case "theme":
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				pendingTheme = s
				themeExplicit = true
			}
			continue
		case "skin":
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				pendingSkin = s
				skinExplicit = true
			}
			continue
		}
		if vals, ok := settingsEnumValues[k]; ok {
			if s, ok := v.(string); !ok || !vals[s] {
				continue
			}
		}
		if rng, ok := settingsIntRanges[k]; ok {
			n, ok := toInt64(v)
			if !ok || n < rng[0] || n > rng[1] {
				continue
			}
			v = n
		}
		if rng, ok := settingsFloatRanges[k]; ok {
			f, ok := toFloat64(v)
			if !ok || f < rng[0] || f > rng[1] {
				continue
			}
			v = f
		}
		if k == "tts_engine" {
			s, ok := v.(string)
			if !ok || !settingsTTSEngineRE.MatchString(strings.TrimSpace(s)) {
				continue
			}
			v = strings.TrimSpace(s)
		}
		if k == "tts_voice" {
			s, ok := v.(string)
			if !ok || len(s) > 200 || strings.ContainsRune(s, 0) {
				continue
			}
		}
		if k == "language" {
			s, ok := v.(string)
			if !ok || !settingsLangRE.MatchString(s) {
				continue
			}
		}
		if k == "hidden_tabs" || k == "tab_order" || k == "composer_control_order" {
			cleaned, ok := cleanOrderList(k, v)
			if !ok {
				continue
			}
			v = cleaned
		}
		if k == "provider_cost_budget" {
			if v == nil || asString(v) == "" {
				current[k] = nil
				continue
			}
			budget, ok := toFloat64(v)
			if !ok || budget <= 0 || budget >= 1e9 {
				continue
			}
			current[k] = budget
			continue
		}
		if settingsBoolKeys[k] {
			v = boolVal(v)
		}
		current[k] = v
		if speechKeys[k] {
			appliedSpeechKeys[k] = true
		}
	}

	// Theme/skin pair normalize (Python parity incl legacy map).
	if themeExplicit && !skinExplicit {
		raw := strings.ToLower(strings.TrimSpace(pendingTheme))
		if !settingsThemeValues[raw] {
			pendingSkin = ""
		}
	}
	theme, skin := normalizeAppearancePair(pendingTheme, pendingSkin)
	current["theme"] = theme
	current["skin"] = skin

	// default_workspace resolve (env var / home fallback).
	current["default_workspace"] = resolveDefaultWorkspace(home)
	if model, provider := configModel(home); model != "" {
		current["default_model"] = model
		if provider != "" {
			current["default_model_provider"] = provider
		}
	}

	// Effective persisted speech keys = previous ∪ applied.
	effective := map[string]bool{}
	for _, k := range prevSpeechKeys {
		effective[k] = true
	}
	for k := range appliedSpeechKeys {
		effective[k] = true
	}
	persisted := settingsPayloadForWrite(current, effective)
	if err := atomicWriteSettings(dataRoot, persisted); err != nil {
		return nil, err
	}

	saved := current
	saved["persisted_speech_keys"] = persistedSpeechKeys(readRawSettings(dataRoot))
	delete(saved, "password_hash")
	return saved, nil
}

// isAllowedSettingsKey mirrors Python _SETTINGS_ALLOWED_KEYS (defaults minus
// password_hash / default_model / simplified_tool_calling).
func isAllowedSettingsKey(k string) bool {
	if k == "password_hash" || k == "default_model" || k == "simplified_tool_calling" {
		return false
	}
	_, ok := settingsDefaults[k]
	return ok
}

// cleanOrderList mirrors Python list-valued ordering settings validation:
// strip, dedupe preserving first order, exclude chat/settings tabs, only known
// composer keys (hide_composer_*).
func cleanOrderList(k string, v any) ([]string, bool) {
	list, ok := v.([]any)
	if !ok {
		return nil, false
	}
	seen := map[string]bool{}
	var cleaned []string
	for _, item := range list {
		s, ok := item.(string)
		if !ok {
			continue
		}
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		if (k == "hidden_tabs" || k == "tab_order") && (s == "chat" || s == "settings") {
			continue
		}
		if k == "composer_control_order" && !strings.HasPrefix(s, "hide_composer_") {
			continue
		}
		cleaned = append(cleaned, s)
		seen[s] = true
	}
	return cleaned, true
}

// settingsPayloadForWrite mirrors _settings_payload_for_write: drops
// default_model + persisted_speech_keys, keeps speech keys only when persisted.
func settingsPayloadForWrite(settings map[string]any, persistedSpeech map[string]bool) map[string]any {
	out := map[string]any{}
	for k, v := range settings {
		if k == "default_model" || k == "default_model_provider" || k == "persisted_speech_keys" {
			continue
		}
		if speechKeys[k] && !persistedSpeech[k] {
			continue
		}
		out[k] = v
	}
	return out
}

// atomicWriteSettings writes settings.json atomically (tmp + fsync + rename),
// preserving the existing file mode (Python _atomic_write_settings_text parity).
func atomicWriteSettings(dataRoot string, payload map[string]any) error {
	if dataRoot == "" {
		return nil
	}
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dataRoot, "settings.json")
	mode := os.FileMode(0o666)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dataRoot, ".settings.*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// normalizeAppearancePair mirrors Python _normalize_appearance incl legacy map.
func normalizeAppearancePair(theme, skin string) (string, string) {
	rawTheme := strings.ToLower(strings.TrimSpace(theme))
	rawSkin := strings.ToLower(strings.TrimSpace(skin))
	if legacy, ok := settingsLegacyThemeMap[rawTheme]; ok {
		if rawSkin != "" && settingsSkinValues[rawSkin] {
			return legacy[0], rawSkin
		}
		return legacy[0], legacy[1]
	}
	if settingsThemeValues[rawTheme] {
		if rawSkin != "" && settingsSkinValues[rawSkin] {
			return rawTheme, rawSkin
		}
		return rawTheme, "default"
	}
	return "dark", "default"
}

// resolveDefaultWorkspace mirrors api/config.py resolve_default_workspace
// discovery order (env var → ~/workspace → ~/work → create).
func resolveDefaultWorkspace(home string) string {
	if env := os.Getenv("HERMES_WEBUI_DEFAULT_WORKSPACE"); strings.TrimSpace(env) != "" {
		return strings.TrimSpace(env)
	}
	u, err := os.UserHomeDir()
	base := ""
	if err == nil {
		base = u
	} else {
		base = home
	}
	for _, p := range []string{filepath.Join(base, "workspace"), filepath.Join(base, "work")} {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
	}
	ws := filepath.Join(base, "workspace")
	_ = os.MkdirAll(ws, 0o755)
	return ws
}

func boolVal(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		s := strings.ToLower(strings.TrimSpace(t))
		return s == "true" || s == "1" || s == "on" || s == "yes"
	case float64:
		return t != 0
	case int64:
		return t != 0
	case int:
		return t != 0
	default:
		return false
	}
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func toInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		return int64(t), true
	case int64:
		return t, true
	case int:
		return int64(t), true
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return n, err == nil
	default:
		return 0, false
	}
}

func toFloat64(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int64:
		return float64(t), true
	case int:
		return float64(t), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f, err == nil
	default:
		return 0, false
	}
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
