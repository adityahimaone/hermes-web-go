package httpserver

import (
	"encoding/json"
	"fmt"
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

	r.Get("/api/model/auxiliary", func(w http.ResponseWriter, req *http.Request) {
		home := hermesHome
		if home == "" {
			home = defaultHermesHome()
		}
		cfg, err := readConfigYAML(home)
		if err != nil {
			// Missing config.yaml is not fatal — empty auxiliary + main defaults.
			cfg = map[string]any{}
		}
		writeJSON(w, auxiliaryModels(cfg))
	})

	r.Post("/api/model/set", func(w http.ResponseWriter, req *http.Request) {
		home := hermesHome
		if home == "" {
			home = defaultHermesHome()
		}
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		scope := strval(body["scope"])
		if scope == "" {
			writeError(w, http.StatusBadRequest, "scope is required")
			return
		}
		if scope == "main" {
			modelID := strval(body["model"])
			provider := strval(body["provider"])
			if provider == "" || provider == "auto" {
				provider = ""
			}
			var advanced map[string]any
			if adv, ok := body["advanced"].(map[string]any); ok {
				advanced = adv
			}
			resp, err := setMainModel(home, modelID, provider, advanced)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, resp)
			return
		}
		if scope != "auxiliary" {
			writeError(w, http.StatusBadRequest, "unknown scope: "+scope)
			return
		}
		task := strval(body["task"])
		provider := strval(body["provider"])
		if provider == "" || provider == "auto" {
			provider = "auto"
		}
		model := strval(body["model"])
		var advanced map[string]any
		if adv, ok := body["advanced"].(map[string]any); ok {
			advanced = adv
		}
		resp, err := setAuxModel(home, task, provider, model, advanced)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, resp)
	})

	r.Post("/api/providers", func(w http.ResponseWriter, req *http.Request) {
		home := hermesHome
		if home == "" {
			home = defaultHermesHome()
		}
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		providerID := strings.ToLower(strings.TrimSpace(strval(body["provider"])))
		apiKeyRaw, hasKey := body["api_key"]
		var apiKey string
		if hasKey && apiKeyRaw != nil {
			apiKey = strings.TrimSpace(strval(apiKeyRaw))
		}
		if providerID == "" {
			writeError(w, http.StatusBadRequest, "provider is required")
			return
		}
		resp, err := setProviderKey(home, providerID, apiKey)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, resp)
	})

	r.Post("/api/providers/delete", func(w http.ResponseWriter, req *http.Request) {
		home := hermesHome
		if home == "" {
			home = defaultHermesHome()
		}
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		providerID := strings.ToLower(strings.TrimSpace(strval(body["provider"])))
		if providerID == "" {
			writeError(w, http.StatusBadRequest, "provider is required")
			return
		}
		resp, err := removeProviderKey(home, providerID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, resp)
	})

	r.Post("/api/profile/create", func(w http.ResponseWriter, req *http.Request) {
		home := hermesHome
		if home == "" {
			home = defaultHermesHome()
		}
		// Isolated profile mode is not detectable from the Go side cheaply —
		// Go deploy pins HERMES_HOME per profile, so creation is always allowed.
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		name := strings.TrimSpace(strval(body["name"]))
		if name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		if !profileNameRe.MatchString(name) {
			writeError(w, http.StatusBadRequest, "Invalid profile name: lowercase letters, numbers, hyphens, underscores only")
			return
		}
		cloneFrom := strval(body["clone_from"])
		if cloneFrom != "" && !profileNameRe.MatchString(cloneFrom) {
			writeError(w, http.StatusBadRequest, "Invalid clone_from name")
			return
		}
		baseURL := strval(body["base_url"])
		if baseURL != "" && !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
			writeError(w, http.StatusBadRequest, "base_url must start with http:// or https://")
			return
		}
		apiKey := strval(body["api_key"])
		defaultModel := strval(body["default_model"])
		modelProvider := strval(body["model_provider"])
		cloneConfig, _ := body["clone_config"].(bool)

		prof, err := createProfile(home, name, cloneFrom, cloneConfig, baseURL, apiKey, defaultModel, modelProvider)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true, "profile": prof})
	})

	r.Post("/api/profile/switch", func(w http.ResponseWriter, req *http.Request) {
		home := hermesHome
		if home == "" {
			home = defaultHermesHome()
		}
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		name := strings.TrimSpace(strval(body["name"]))
		if name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		if name != "default" && !profileNameRe.MatchString(name) {
			writeError(w, http.StatusBadRequest, "Invalid profile name")
			return
		}
		if !profileDirExists(home, name) {
			writeError(w, http.StatusNotFound, "Profile not found: "+name)
			return
		}
		resp := switchProfileCookie(home, name, req)
		writeJSON(w, resp)
	})

	r.Post("/api/profile/update", func(w http.ResponseWriter, req *http.Request) {
		home := hermesHome
		if home == "" {
			home = defaultHermesHome()
		}
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		name := strings.TrimSpace(strval(body["name"]))
		if name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		profDir, err := profileDir(home, name)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		defaultModel := strval(body["default_model"])
		modelProvider := strval(body["model_provider"])
		baseURL := strval(body["base_url"])
		if baseURL != "" && !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
			writeError(w, http.StatusBadRequest, "base_url must start with http:// or https://")
			return
		}
		defaultModel, modelProvider = splitWebUIProviderModel(defaultModel, modelProvider)
		if defaultModel != "" || modelProvider != "" {
			if err := writeModelDefaultsToConfig(profDir, defaultModel, modelProvider); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if baseURL != "" {
			if err := writeEndpointToConfig(profDir, baseURL); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		effectiveDefault, effectiveProvider, effectiveBase := readModelConfig(profDir)
		writeJSON(w, map[string]any{
			"ok":            true,
			"profile":       map[string]any{"name": name, "path": profDir},
			"default_model": effectiveDefault,
			"provider":      effectiveProvider,
			"base_url":      effectiveBase,
		})
	})

	r.Post("/api/profile/delete", func(w http.ResponseWriter, req *http.Request) {
		home := hermesHome
		if home == "" {
			home = defaultHermesHome()
		}
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		name := strings.TrimSpace(strval(body["name"]))
		if name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		if name == "default" {
			writeError(w, http.StatusBadRequest, "Cannot delete the default profile.")
			return
		}
		if !profileNameRe.MatchString(name) {
			writeError(w, http.StatusBadRequest, "Invalid profile name")
			return
		}
		profDir, err := profileDir(home, name)
		if err != nil {
			writeError(w, http.StatusNotFound, "Profile not found: "+name)
			return
		}
		// Active profile check — Go deploy pins HERMES_HOME, so the active
		// profile is the current home's basename; deleting it is refused.
		active := filepath.Base(home)
		if filepath.Base(filepath.Dir(home)) == "profiles" && active == name {
			writeError(w, http.StatusConflict, "Cannot delete active profile")
			return
		}
		if err := os.RemoveAll(profDir); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true, "name": name})
	})

	// ── Family-2 network-probe routes ─────────────────────────────────────
	r.Get("/api/models", func(w http.ResponseWriter, req *http.Request) {
		home := hermesHome
		if home == "" {
			home = defaultHermesHome()
		}
		freshness := strings.ToLower(strings.TrimSpace(req.URL.Query().Get("freshness")))
		if freshness == "session_visit" {
			writeJSON(w, availableModelsCatalog(home, true))
			return
		}
		if freshness != "" {
			writeError(w, http.StatusBadRequest, "unknown models freshness: "+freshness)
			return
		}
		writeJSON(w, availableModelsCatalog(home, false))
	})

	r.Get("/api/models/live", func(w http.ResponseWriter, req *http.Request) {
		home := hermesHome
		if home == "" {
			home = defaultHermesHome()
		}
		prov := strings.ToLower(strings.TrimSpace(req.URL.Query().Get("provider")))
		if prov == "" {
			modelCfg := configModelSection(home)
			prov = strings.ToLower(strings.TrimSpace(strval(modelCfg["provider"])))
			if prov == "" {
				writeJSON(w, map[string]any{"error": "no_provider", "models": []any{}})
				return
			}
		}
		writeJSON(w, liveModelsForProvider(home, prov))
	})

	r.Post("/api/models/refresh", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		providerID := strings.ToLower(strings.TrimSpace(strval(body["provider"])))
		if providerID == "" {
			writeError(w, http.StatusBadRequest, "provider is required")
			return
		}
		invalidateProviderModelsCache(providerID)
		writeJSON(w, map[string]any{"ok": true, "provider": providerID})
	})

	r.Get("/api/providers", func(w http.ResponseWriter, req *http.Request) {
		home := hermesHome
		if home == "" {
			home = defaultHermesHome()
		}
		writeJSON(w, providersList(home))
	})

	r.Post("/api/providers/self-hosted", func(w http.ResponseWriter, req *http.Request) {
		home := hermesHome
		if home == "" {
			home = defaultHermesHome()
		}
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		resp, err := applySelfHostedProviderSetup(home, body)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, resp)
	})

	r.Get("/api/provider/quota", func(w http.ResponseWriter, req *http.Request) {
		home := hermesHome
		if home == "" {
			home = defaultHermesHome()
		}
		q := req.URL.Query()
		providerID := q.Get("provider")
		refresh := strings.ToLower(strings.TrimSpace(q.Get("refresh"))) == "true"
		writeJSON(w, providerQuota(home, providerID, refresh))
	})

	r.Get("/api/provider/cost-history", func(w http.ResponseWriter, req *http.Request) {
		home := hermesHome
		if home == "" {
			home = defaultHermesHome()
		}
		q := req.URL.Query()
		providerID := q.Get("provider")
		days := 7
		if raw := strings.TrimSpace(q.Get("days")); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil {
				if n < 1 {
					n = 1
				}
				if n > 365 {
					n = 365
				}
				days = n
			}
		}
		writeJSON(w, providerCostHistory(home, providerID, days))
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

// strval returns the string value of v (or "" when not a string/number).
func strval(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		return fmt.Sprint(t)
	}
}

// auxTaskCatalog mirrors api/config.py AUXILIARY_TASK_CATALOG.
var auxTaskCatalog = []struct {
	key, label, description string
}{
	{"vision", "Vision", "image/screenshot analysis"},
	{"web_extract", "Web extract", "web page summarization"},
	{"compression", "Compression", "context summarization"},
	{"approval", "Approval", "smart command approval"},
	{"mcp", "MCP", "MCP tool reasoning"},
	{"title_generation", "Title generation", "session titles"},
	{"skills_hub", "Skills hub", "skills search/install"},
	{"curator", "Curator", "skill-usage review pass"},
	{"kanban_decomposer", "Kanban decomposer", "task decomposition"},
	{"profile_describer", "Profile describer", "profile summaries"},
	{"triage_specifier", "Triage specifier", "issue/task triage specs"},
}

var retiredAuxTaskSlots = []string{"session_search"}

// auxTaskSlots returns known auxiliary task keys.
func auxTaskSlots() []string {
	slots := make([]string, 0, len(auxTaskCatalog))
	for _, s := range auxTaskCatalog {
		slots = append(slots, s.key)
	}
	return slots
}

// auxTaskPayload mirrors api/config.py _aux_task_payload.
func auxTaskPayload(taskKey string, entry map[string]any, label, desc string) map[string]any {
	if entry == nil {
		entry = map[string]any{}
	}
	provider := strval(entry["provider"])
	if provider == "" {
		provider = "auto"
	}
	var extraBody map[string]any
	if eb, ok := entry["extra_body"].(map[string]any); ok {
		extraBody = eb
	} else {
		extraBody = map[string]any{}
	}
	return map[string]any{
		"task":             taskKey,
		"provider":         provider,
		"model":            strval(entry["model"]),
		"base_url":         strval(entry["base_url"]),
		"timeout":          nvl(entry["timeout"], ""),
		"download_timeout": nvl(entry["download_timeout"], ""),
		"max_concurrency":  nvl(entry["max_concurrency"], ""),
		"extra_body":       extraBody,
		"api_key_set":      strval(entry["api_key"]) != "",
		"label":            label,
		"description":      desc,
	}
}

// nvl returns v when non-nil, else fallback.
func nvl(v, fallback any) any {
	if v == nil {
		return fallback
	}
	return v
}

// auxiliaryModels mirrors api/config.py get_auxiliary_models.
func auxiliaryModels(cfg map[string]any) map[string]any {
	modelCfg, _ := cfg["model"].(map[string]any)
	if modelCfg == nil {
		modelCfg = map[string]any{}
	}
	mainProvider := strval(modelCfg["provider"])
	mainModel := strval(modelCfg["default"])
	if mainModel == "" {
		mainModel = strval(modelCfg["name"])
	}

	auxCfg, _ := cfg["auxiliary"].(map[string]any)
	if auxCfg == nil {
		auxCfg = map[string]any{}
	}
	tasks := make([]map[string]any, 0, len(auxTaskCatalog))
	for _, slot := range auxTaskCatalog {
		entry, _ := auxCfg[slot.key].(map[string]any)
		tasks = append(tasks, auxTaskPayload(slot.key, entry, slot.label, slot.description))
	}

	return map[string]any{
		"tasks": tasks,
		"main": map[string]any{
			"provider":           mainProvider,
			"model":              mainModel,
			"supports_fast_tier": _mainModelSupportsServiceTier(mainModel, mainProvider),
			"service_tier":       _publicMainServiceTier(modelCfg),
			"base_url":           strval(modelCfg["base_url"]),
			"timeout":            nvl(modelCfg["timeout"], ""),
			"download_timeout":   nvl(modelCfg["download_timeout"], ""),
			"max_concurrency":    nvl(modelCfg["max_concurrency"], ""),
			"extra_body":         extraBodyOrEmpty(modelCfg["extra_body"]),
			"api_key_set":        strval(modelCfg["api_key"]) != "",
		},
	}
}

func extraBodyOrEmpty(v any) map[string]any {
	if eb, ok := v.(map[string]any); ok {
		return eb
	}
	return map[string]any{}
}

func _mainModelSupportsServiceTier(modelID, provider string) bool {
	if !_isOpenAIFamilyProvider(provider) {
		return false
	}
	// WebUI compatibility fallback: OpenAI-family non-codex GPT/o-series models
	// advertise priority tier unless the model id is explicitly codex.
	lower := strings.ToLower(strings.TrimSpace(modelID))
	if lower == "" {
		return provider == "openai" || provider == "openai-api"
	}
	if strings.Contains(lower, "codex") {
		return false
	}
	if strings.HasPrefix(lower, "gpt-") || strings.HasPrefix(lower, "o1") || strings.HasPrefix(lower, "o3") || strings.HasPrefix(lower, "o4") {
		return true
	}
	return false
}

func _isOpenAIFamilyProvider(provider string) bool {
	p := strings.ToLower(strings.TrimSpace(provider))
	return p == "openai" || p == "openai-api" || p == "openai-codex"
}

func _publicMainServiceTier(modelCfg map[string]any) string {
	if !_mainModelSupportsServiceTier(strval(modelCfg["default"]), strval(modelCfg["provider"])) {
		return ""
	}
	return strval(modelCfg["service_tier"])
}

var auxSlotSet = func() map[string]bool {
	m := map[string]bool{}
	for _, s := range auxTaskSlots() {
		m[s] = true
	}
	return m
}()

// setAuxModel mirrors api/config.py set_auxiliary_model for auxiliary scope,
// persisting the assignment into <home>/config.yaml (comment-preserving
// yaml.Node round-trip, same as the skills toggle path).
func setAuxModel(home, task, provider, model string, advanced map[string]any) (map[string]any, error) {
	configPath := filepath.Join(home, "config.yaml")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config.yaml: %w", err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("failed to parse config.yaml: %w", err)
	}
	doc := root.Content[0]

	auxNode := findMapKey(doc, "auxiliary")
	if auxNode == nil || auxNode.Kind != yaml.MappingNode {
		auxNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		if findMapKey(doc, "auxiliary") == nil {
			doc.Content = append(doc.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "auxiliary"}, auxNode)
		}
	}

	if task == "__reset__" {
		for _, retired := range retiredAuxTaskSlots {
			if n := findMapKey(auxNode, retired); n != nil {
				// remove key+value pair
				for i := 0; i+1 < len(auxNode.Content); i += 2 {
					if auxNode.Content[i].Value == retired {
						auxNode.Content = append(auxNode.Content[:i], auxNode.Content[i+2:]...)
						break
					}
				}
			}
		}
		for _, slot := range auxTaskSlots() {
			slotNode := findMapKey(auxNode, slot)
			if slotNode == nil || slotNode.Kind != yaml.MappingNode {
				slotNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				auxNode.Content = append(auxNode.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: slot}, slotNode)
			}
			setMapScalar(slotNode, "provider", "auto")
			setMapScalar(slotNode, "model", "")
		}
	} else {
		if !auxSlotSet[task] {
			return nil, fmt.Errorf("unknown auxiliary task slot: %q. Valid: %v", task, auxTaskSlots())
		}
		slotNode := findMapKey(auxNode, task)
		if slotNode == nil || slotNode.Kind != yaml.MappingNode {
			slotNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			auxNode.Content = append(auxNode.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: task}, slotNode)
		}
		p := provider
		if p == "" || p == "auto" {
			p = "auto"
		}
		setMapScalar(slotNode, "provider", p)
		setMapScalar(slotNode, "model", model)
		if strings.HasPrefix(p, "custom:") || p == "custom" {
			// Resolve base_url from the selected custom provider entry.
			baseURL := ""
			if strings.HasPrefix(p, "custom:") {
				baseURL = customProviderBaseURL(doc, strings.TrimPrefix(p, "custom:"))
			}
			if baseURL != "" {
				setMapScalar(slotNode, "base_url", strings.TrimRight(baseURL, "/"))
			}
		}
		if advanced != nil {
			if err := applyAdvancedModelOptions(slotNode, advanced); err != nil {
				return nil, err
			}
		}
	}

	out, err := yaml.Marshal(&root)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config.yaml: %w", err)
	}
	if err := os.WriteFile(configPath+".tmp", out, 0o644); err != nil {
		return nil, fmt.Errorf("failed to write config.yaml: %w", err)
	}
	if err := os.Rename(configPath+".tmp", configPath); err != nil {
		return nil, fmt.Errorf("failed to replace config.yaml: %w", err)
	}
	return map[string]any{"ok": true, "task": task, "provider": provider, "model": model}, nil
}

func setMapScalar(mapping *yaml.Node, key, value string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1].Value = value
			mapping.Content[i+1].Tag = "!!str"
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
	)
}

// findMapKey returns the value node for key under a mapping node, or nil.
func findMapKey(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// customProviderBaseURL finds the base_url for a custom:<slug> provider entry
// in config.yaml's custom_providers list.
func customProviderBaseURL(doc *yaml.Node, slug string) string {
	cpNode := findMapKey(doc, "custom_providers")
	if cpNode == nil || cpNode.Kind != yaml.SequenceNode {
		return ""
	}
	for _, item := range cpNode.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		if findMapKey(item, "slug") != nil && findMapKey(item, "slug").Value == slug {
			bu := findMapKey(item, "base_url")
			if bu != nil {
				return bu.Value
			}
			return ""
		}
	}
	return ""
}

// applyAdvancedModelOptions mirrors api/config.py _apply_advanced_model_options.
func applyAdvancedModelOptions(slot *yaml.Node, advanced map[string]any) error {
	if advanced == nil {
		return nil
	}
	setOrRemove := func(key, value string, present bool) {
		if present && value != "" {
			setMapScalar(slot, key, value)
		} else if !present || value == "" {
			removeMapKey(slot, key)
		}
	}
	if raw, has := advanced["base_url"]; has {
		val := strings.TrimRight(strval(raw), "/")
		setOrRemove("base_url", val, val != "" || strval(raw) == "")
	}
	for _, field := range []string{"timeout", "download_timeout", "max_concurrency"} {
		if raw, has := advanced[field]; has {
			n, ok := coercePositiveInt(raw, field)
			if ok {
				if n == "" {
					removeMapKey(slot, field)
				} else {
					setMapScalar(slot, field, n)
				}
			}
		}
	}
	if raw, has := advanced["extra_body"]; has {
		switch eb := raw.(type) {
		case string:
			text := strings.TrimSpace(eb)
			if text == "" {
				removeMapKey(slot, "extra_body")
			} else {
				var parsed map[string]any
				if err := json.Unmarshal([]byte(text), &parsed); err != nil {
					return fmt.Errorf("extra_body must be valid JSON")
				}
				if len(parsed) > 0 {
					setMapAny(slot, "extra_body", parsed)
				} else {
					removeMapKey(slot, "extra_body")
				}
			}
		case map[string]any:
			if len(eb) > 0 {
				setMapAny(slot, "extra_body", eb)
			} else {
				removeMapKey(slot, "extra_body")
			}
		default:
			return fmt.Errorf("extra_body must be a JSON object")
		}
	}
	if raw, has := advanced["service_tier"]; has {
		val := strings.ToLower(strval(raw))
		if val == "" || val == "default" {
			removeMapKey(slot, "service_tier")
		} else if val == "priority" {
			setMapScalar(slot, "service_tier", "priority")
		} else {
			return fmt.Errorf("service_tier must be one of: default, priority")
		}
	}
	if clear, _ := advanced["api_key_clear"].(bool); clear {
		removeMapKey(slot, "api_key")
	}
	if raw, has := advanced["api_key"]; has {
		key := strval(raw)
		if key != "" {
			setMapScalar(slot, "api_key", key)
		}
	}
	return nil
}

// coercePositiveInt mirrors api/config.py _coerce_optional_positive_int.
func coercePositiveInt(value any, field string) (string, bool) {
	if value == nil {
		return "", true
	}
	var s string
	if str, ok := value.(string); ok {
		s = str
	} else if num, ok := value.(int); ok {
		s = strconv.Itoa(num)
	} else if num, ok := value.(float64); ok && num == float64(int(num)) {
		s = strconv.Itoa(int(num))
	} else {
		return "", false
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", true
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return "", false
	}
	return s, true
}

func removeMapKey(mapping *yaml.Node, key string) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return
		}
	}
}

// setMapAny sets a mapping key to a complex (non-scalar) value.
func setMapAny(mapping *yaml.Node, key string, value any) {
	out, err := yaml.Marshal(value)
	if err != nil {
		return
	}
	var valNode yaml.Node
	if err := yaml.Unmarshal(out, &valNode); err != nil {
		return
	}
	vnode := valNode.Content[0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = vnode
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		vnode,
	)
}

// setMainModel mirrors api/config.py set_hermes_default_model (subset: no
// collision guards / portal codex exception / local-server detect — see
// progress-log slice 6 known gaps). Persists model.default/provider/base_url
// into config.yaml and returns {ok, model, provider}.
func setMainModel(home, modelID, provider string, advanced map[string]any) (map[string]any, error) {
	selectedModel := strings.TrimSpace(modelID)
	if selectedModel == "" {
		return nil, fmt.Errorf("model is required")
	}
	configPath := filepath.Join(home, "config.yaml")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config.yaml: %w", err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("failed to parse config.yaml: %w", err)
	}
	doc := root.Content[0]

	modelNode := findMapKey(doc, "model")
	if modelNode == nil || modelNode.Kind != yaml.MappingNode {
		modelNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		if findMapKey(doc, "model") == nil {
			doc.Content = append(doc.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "model"}, modelNode)
		}
	}
	previousProvider := ""
	if pp := findMapKey(modelNode, "provider"); pp != nil {
		previousProvider = strings.TrimSpace(pp.Value)
	}

	resolvedModel, resolvedProvider, resolvedBaseURL := resolveMainModelProvider(doc, selectedModel, previousProvider)
	persistedModel := selectedModel
	if resolvedModel != "" {
		persistedModel = resolvedModel
	}
	persistedProvider := provider
	if persistedProvider == "" {
		persistedProvider = resolvedProvider
	}
	if persistedProvider == "" {
		persistedProvider = previousProvider
	}
	providerOverrideWon := provider != "" && provider != resolvedProvider
	if strings.EqualFold(persistedProvider, "local") {
		persistedProvider = "custom" // #1384
	}

	setMapScalar(modelNode, "default", persistedModel)
	if persistedProvider != "" {
		setMapScalar(modelNode, "provider", persistedProvider)
	}
	if resolvedBaseURL != "" && !providerOverrideWon {
		setMapScalar(modelNode, "base_url", strings.TrimRight(resolvedBaseURL, "/"))
	} else if persistedProvider != previousProvider {
		if persistedProvider == "openai" {
			setMapScalar(modelNode, "base_url", "https://api.openai.com/v1")
		} else {
			removeMapKey(modelNode, "base_url")
		}
	}
	if advanced != nil {
		if err := applyAdvancedModelOptions(modelNode, advanced); err != nil {
			return nil, err
		}
	}
	if !_mainModelSupportsServiceTier(persistedModel, persistedProvider) {
		removeMapKey(modelNode, "service_tier")
	}

	out, err := yaml.Marshal(&root)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config.yaml: %w", err)
	}
	if err := os.WriteFile(configPath+".tmp", out, 0o644); err != nil {
		return nil, fmt.Errorf("failed to write config.yaml: %w", err)
	}
	if err := os.Rename(configPath+".tmp", configPath); err != nil {
		return nil, fmt.Errorf("failed to replace config.yaml: %w", err)
	}
	persistedProviderOut := persistedProvider
	return map[string]any{"ok": true, "model": persistedModel, "provider": persistedProviderOut}, nil
}

// resolveMainModelProvider is a pragmatic subset of api/config.py
// resolve_model_provider: handles @provider:model, provider/model slash
// (openrouter + custom-name prefix + known prefix strip), and custom_providers
// name matches. Falls back to the caller's provider/previous provider.
func resolveMainModelProvider(doc *yaml.Node, modelID, previousProvider string) (model, provider, baseURL string) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return "", "", ""
	}
	modelCfg := findMapKey(doc, "model")
	configProvider := previousProvider
	configBaseURL := ""
	if modelCfg != nil && modelCfg.Kind == yaml.MappingNode {
		if pn := findMapKey(modelCfg, "provider"); pn != nil {
			configProvider = strings.TrimSpace(pn.Value)
		}
		if bn := findMapKey(modelCfg, "base_url"); bn != nil {
			configBaseURL = strings.TrimSpace(bn.Value)
		}
	}
	if configProvider == "local" {
		configProvider = "custom"
	}

	// @provider:model — explicit provider hint from the dropdown.
	if strings.HasPrefix(modelID, "@") && strings.Contains(modelID, ":") {
		modelID = modelID[1:]
		idx := strings.LastIndex(modelID, ":")
		bare := modelID[idx+1:]
		hint := modelID[:idx]
		// :free / :beta / :thinking tags — peel one segment back when the hint
		// ends in ":tokens" (#1744-ish). Fall back to split(":") when rsplit
		// doesn't look like a recognised provider.
		if strings.HasPrefix(hint, "custom:") {
			seg := strings.Split(hint, ":")
			// custom:<slug>:model:free → hint should be custom:<slug>
			if len(seg) > 2 {
				hint = seg[0] + ":" + seg[1]
			}
		}
		if hint == "" || bare == "" {
			return modelID, hint, ""
		}
		bu := providerBaseURLForHint(doc, hint)
		return bare, hint, bu
	}

	// Custom providers declared in config.yaml win over slash heuristics.
	if cps := findMapKey(doc, "custom_providers"); cps != nil && cps.Kind == yaml.SequenceNode {
		for _, entry := range cps.Content {
			if entry.Kind != yaml.MappingNode {
				continue
			}
			entryModel := ""
			if mn := findMapKey(entry, "model"); mn != nil {
				entryModel = strings.TrimSpace(mn.Value)
			}
			name := ""
			if nn := findMapKey(entry, "name"); nn != nil {
				name = strings.TrimSpace(nn.Value)
			}
			owns := entryModel == modelID
			if !owns {
				if ms := findMapKey(entry, "models"); ms != nil && ms.Kind == yaml.SequenceNode {
					for _, m := range ms.Content {
						if m.Value == modelID {
							owns = true
							break
						}
					}
				}
			}
			if owns && name != "" {
				bu := ""
				if bn := findMapKey(entry, "base_url"); bn != nil {
					bu = strings.TrimSpace(bn.Value)
				}
				return modelID, slugify(name), bu
			}
		}
	}

	// providers.<slug>.models allowlist scan.
	if provs := findMapKey(doc, "providers"); provs != nil && provs.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(provs.Content); i += 2 {
			slug := provs.Content[i].Value
			pdef := provs.Content[i+1]
			if pdef.Kind != yaml.MappingNode {
				continue
			}
			ms := findMapKey(pdef, "models")
			if ms == nil || ms.Kind != yaml.SequenceNode {
				continue
			}
			for _, m := range ms.Content {
				if m.Value == modelID {
					bu := ""
					if bn := findMapKey(pdef, "base_url"); bn != nil {
						bu = strings.TrimSpace(bn.Value)
					}
					return modelID, slug, bu
				}
			}
		}
	}

	// slash form: prefix/model
	if strings.Contains(modelID, "/") {
		prefix, bare := modelID, modelID
		if idx := strings.Index(modelID, "/"); idx > 0 {
			prefix, bare = modelID[:idx], modelID[idx+1:]
		}
		if configProvider == "openrouter" {
			return modelID, "openrouter", configBaseURL
		}
		if parser := findMapKey(doc, "custom_providers"); parser != nil && parser.Kind == yaml.SequenceNode {
			for _, entry := range parser.Content {
				if entry.Kind != yaml.MappingNode {
					continue
				}
				nn := findMapKey(entry, "name")
				if nn != nil && strings.TrimSpace(nn.Value) == prefix {
					bu := ""
					if bn := findMapKey(entry, "base_url"); bn != nil {
						bu = strings.TrimSpace(bn.Value)
					}
					return modelID, slugify(strings.TrimSpace(nn.Value)), bu
				}
			}
		}
		if configProvider != "" && prefix == configProvider {
			return bare, configProvider, configBaseURL
		}
		// unknown prefix + no base_url → route through config provider,
		// keeping the full slash id (OpenRouter-style).
		if configBaseURL == "" {
			return modelID, configProvider, ""
		}
		// base_url configured → strip prefix only for known provider namespaces.
		if providerEnvVar(configProvider) != "" || configProvider == "custom" {
			return bare, configProvider, configBaseURL
		}
		// named custom provider without a slash entry → keep full id.
		return modelID, configProvider, configBaseURL
	}

	// bare model → config provider/base_url.
	return modelID, configProvider, configBaseURL
}

// providerBaseURLForHint resolves base_url for an @provider:model hint:
// named custom providers get their own entry's base_url; openai gets the
// canonical endpoint; everything else falls back to the model's current
// config base_url.
func providerBaseURLForHint(doc *yaml.Node, hint string) string {
	if strings.HasPrefix(hint, "custom:") {
		slug := strings.TrimPrefix(hint, "custom:")
		if cps := findMapKey(doc, "custom_providers"); cps != nil && cps.Kind == yaml.SequenceNode {
			for _, entry := range cps.Content {
				if entry.Kind != yaml.MappingNode {
					continue
				}
				nn := findMapKey(entry, "name")
				sl := findMapKey(entry, "slug")
				name := ""
				if nn != nil {
					name = strings.TrimSpace(nn.Value)
				}
				matches := sl != nil && strings.TrimSpace(sl.Value) == slug
				if nn != nil {
					matches = matches || slugify(name) == slug || strings.EqualFold(name, slug) || strings.EqualFold(strings.ReplaceAll(name, " ", "-"), slug)
				}
				if matches {
					if bn := findMapKey(entry, "base_url"); bn != nil {
						return strings.TrimSpace(bn.Value)
					}
					return ""
				}
			}
		}
		return ""
	}
	if hint == "openai" {
		return "https://api.openai.com/v1"
	}
	if mc := findMapKey(doc, "model"); mc != nil && mc.Kind == yaml.MappingNode {
		if bn := findMapKey(mc, "base_url"); bn != nil {
			return strings.TrimSpace(bn.Value)
		}
	}
	return ""
}

var profileNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// profileRoot returns the profiles directory under hermesHome (or the home
// itself when the current home is already a profile).
func profileRoot(home string) string {
	if filepath.Base(filepath.Dir(home)) == "profiles" {
		return filepath.Dir(home)
	}
	return filepath.Join(home, "profiles")
}

// profileDir resolves a profile's directory (default → home root).
func profileDir(home, name string) (string, error) {
	if name == "default" {
		if filepath.Base(filepath.Dir(home)) == "profiles" {
			return filepath.Dir(home), nil
		}
		return home, nil
	}
	if !profileNameRe.MatchString(name) {
		return "", fmt.Errorf("Invalid profile name")
	}
	dir := filepath.Join(profileRoot(home), name)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("Profile '%s' does not exist.", name)
	}
	return dir, nil
}

func profileDirExists(home, name string) bool {
	if name == "default" {
		return true
	}
	_, err := os.Stat(filepath.Join(profileRoot(home), name))
	return err == nil
}

// splitWebUIProviderModel mirrors api/profiles.py _split_webui_provider_model_value:
// splits "@provider:model" values into bare model + provider.
func splitWebUIProviderModel(model, provider string) (string, string) {
	model = strings.TrimSpace(model)
	provider = strings.TrimSpace(provider)
	if model != "" && strings.HasPrefix(model, "@") && strings.Contains(model, ":") {
		rest := model[1:]
		idx := strings.LastIndex(rest, ":")
		if idx > 0 {
			bare := rest[idx+1:]
			hint := rest[:idx]
			if strings.HasPrefix(hint, "custom:") {
				seg := strings.SplitN(hint, ":", 3)
				if len(seg) > 2 {
					hint = seg[0] + ":" + seg[1]
				}
			}
			if provider == "" {
				provider = hint
			}
			model = bare
		}
	}
	return strings.TrimSpace(model), strings.TrimSpace(provider)
}

// cleanProfileConfigValue mirrors _clean_profile_config_value: single-line,
// max 512 chars.
func cleanProfileConfigValue(value, field string) (string, error) {
	cleaned := strings.TrimSpace(value)
	if cleaned == "" {
		return "", nil
	}
	if strings.ContainsAny(cleaned, "\x00\r\n") {
		return "", fmt.Errorf("%s must be a single-line value", field)
	}
	if len(cleaned) > 512 {
		return "", fmt.Errorf("%s is too long", field)
	}
	return cleaned, nil
}

// writeModelDefaultsToConfig mirrors _write_model_defaults_to_config.
func writeModelDefaultsToConfig(profileDir, defaultModel, modelProvider string) error {
	defaultModel, modelProvider = splitWebUIProviderModel(defaultModel, modelProvider)
	var err error
	defaultModel, err = cleanProfileConfigValue(defaultModel, "default_model")
	if err != nil {
		return err
	}
	modelProvider, err = cleanProfileConfigValue(modelProvider, "model_provider")
	if err != nil {
		return err
	}
	if defaultModel == "" && modelProvider == "" {
		return nil
	}
	cfg, err := readProfileConfig(profileDir)
	if err != nil {
		return err
	}
	mc, _ := cfg["model"].(map[string]any)
	if mc == nil {
		mc = map[string]any{}
	}
	if defaultModel != "" {
		mc["default"] = defaultModel
	}
	if modelProvider != "" {
		mc["provider"] = modelProvider
	}
	cfg["model"] = mc
	return writeProfileConfig(profileDir, cfg)
}

// writeEndpointToConfig mirrors _write_endpoint_to_config.
func writeEndpointToConfig(profileDir, baseURL string) error {
	cfg, err := readProfileConfig(profileDir)
	if err != nil {
		return err
	}
	mc, _ := cfg["model"].(map[string]any)
	if mc == nil {
		mc = map[string]any{}
	}
	mc["base_url"] = baseURL
	cfg["model"] = mc
	return writeProfileConfig(profileDir, cfg)
}

// readModelConfig reads model.default/provider/base_url back from config.yaml.
func readModelConfig(profileDir string) (model, provider, baseURL string) {
	cfg, err := readProfileConfig(profileDir)
	if err != nil {
		return "", "", ""
	}
	mc, _ := cfg["model"].(map[string]any)
	if mc == nil {
		return "", "", ""
	}
	return strval(mc["default"]), strval(mc["provider"]), strval(mc["base_url"])
}

// readProfileConfig loads a profile's config.yaml (missing → empty map).
func readProfileConfig(profileDir string) (map[string]any, error) {
	raw, err := os.ReadFile(filepath.Join(profileDir, "config.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	if cfg == nil {
		return map[string]any{}, nil
	}
	return cfg, nil
}

// writeProfileConfig persists map into config.yaml atomically.
func writeProfileConfig(profileDir string, cfg map[string]any) error {
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	configPath := filepath.Join(profileDir, "config.yaml")
	if err := os.WriteFile(configPath+".tmp", out, 0o644); err != nil {
		return err
	}
	return os.Rename(configPath+".tmp", configPath)
}

// createProfile mirrors api/profiles.py create_profile_api (fallback path,
// no hermes_cli): mkdir profile dir, optional clone (copy config.yaml +
// skills), seed nothing, write base_url/model defaults/api key.
func createProfile(home, name, cloneFrom string, cloneConfig bool, baseURL, apiKey, defaultModel, modelProvider string) (map[string]any, error) {
	root := profileRoot(home)
	profDir := filepath.Join(root, name)

	// clone from an existing profile (default = home root)
	if cloneFrom != "" {
		src, err := profileDir(home, cloneFrom)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(profDir, 0o755); err != nil {
			return nil, err
		}
		// copy config.yaml
		if raw, err := os.ReadFile(filepath.Join(src, "config.yaml")); err == nil {
			if err := os.WriteFile(filepath.Join(profDir, "config.yaml"), raw, 0o644); err != nil {
				return nil, err
			}
		}
		// copy skills tree when cloning config
		if cloneConfig {
			if err := copyTree(filepath.Join(src, "skills"), filepath.Join(profDir, "skills")); err != nil {
				return nil, err
			}
		}
	} else {
		if _, err := os.Stat(profDir); err == nil {
			return nil, fmt.Errorf("Profile '%s' already exists.", name)
		}
		if err := os.MkdirAll(profDir, 0o755); err != nil {
			return nil, err
		}
	}
	defaultModel, modelProvider = splitWebUIProviderModel(defaultModel, modelProvider)
	if baseURL != "" {
		if err := writeEndpointToConfig(profDir, baseURL); err != nil {
			return nil, err
		}
	}
	if defaultModel != "" || modelProvider != "" {
		if err := writeModelDefaultsToConfig(profDir, defaultModel, modelProvider); err != nil {
			return nil, err
		}
	}
	if apiKey != "" {
		envVar := providerEnvVar(modelProvider)
		if envVar == "" {
			envVar = "HERMES_API_KEY"
		}
		if err := writeEnvFile(profDir, envVar, apiKey); err != nil {
			return nil, err
		}
		if err := os.Chmod(filepath.Join(profDir, ".env"), 0o600); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"name":            name,
		"path":            profDir,
		"is_default":      false,
		"is_active":       false,
		"gateway_running": false,
	}, nil
}

// copyTree recursively copies src dir into dst.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, path[len(src):]), 0o755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		return os.WriteFile(filepath.Join(dst, path[len(src):]), raw, 0o644)
	})
}

// switchProfileCookie mirrors the response part of /api/profile/switch: the
// Go side sets the hermes_profile cookie (per-client profile is managed via
// cookie in Python; Go deploy pins HERMES_HOME so the cookie is advisory).
func switchProfileCookie(home, name string, req *http.Request) map[string]any {
	active := filepath.Base(home)
	if filepath.Base(filepath.Dir(home)) != "profiles" {
		active = "default"
	}
	return map[string]any{
		"ok":     true,
		"name":   name,
		"active": active,
	}
}

// providerEnvVar mirrors api/providers.py _PROVIDER_ENV_VAR (canonical key name).
func providerEnvVar(providerID string) string {
	m := map[string]string{
		"openrouter":   "OPENROUTER_API_KEY",
		"anthropic":    "ANTHROPIC_API_KEY",
		"openai":       "OPENAI_API_KEY",
		"google":       "GOOGLE_API_KEY",
		"gemini":       "GEMINI_API_KEY",
		"zai":          "GLM_API_KEY",
		"kimi-coding":  "KIMI_API_KEY",
		"deepseek":     "DEEPSEEK_API_KEY",
		"minimax":      "MINIMAX_API_KEY",
		"minimax-cn":   "MINIMAX_CN_API_KEY",
		"mistralai":    "MISTRAL_API_KEY",
		"x-ai":         "XAI_API_KEY",
		"xiaomi":       "XIAOMI_API_KEY",
		"neuralwatt":   "NEURALWATT_API_KEY",
		"opencode-zen": "OPENCODE_ZEN_API_KEY",
		"opencode-go":  "OPENCODE_GO_API_KEY",
		"ollama-cloud": "OLLAMA_API_KEY",
		"lmstudio":     "LM_API_KEY",
		"nvidia":       "NVIDIA_API_KEY",
	}
	return m[providerID]
}

var oauthProviders = map[string]bool{
	"copilot": true, "copilot-acp": true, "nous": true,
	"openai-codex": true, "qwen-oauth": true, "xai-oauth": true,
}

// writeEnvFile mirrors api/providers.py _write_env_file: preserve comments,
// blank lines, and key order; None value removes the key; append new keys at
// end with blank-line separator. 0644→0600 atomic (tmp+rename).
func writeEnvFile(home, envVar, value string) error {
	envPath := filepath.Join(home, ".env")
	var lines []string
	if raw, err := os.ReadFile(envPath); err == nil {
		lines = strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		if len(lines) == 1 && lines[0] == "" {
			lines = nil
		}
	}
	// Map each existing key to its line index (preserve order, in-place update).
	idx := map[string]int{}
	for i, ln := range lines {
		s := strings.TrimSpace(ln)
		if s != "" && !strings.HasPrefix(s, "#") && strings.Contains(s, "=") {
			idx[strings.TrimSpace(strings.SplitN(s, "=", 2)[0])] = i
		}
	}
	if value == "" {
		if i, ok := idx[envVar]; ok {
			lines = append(lines[:i], lines[i+1:]...)
		}
	} else {
		if i, ok := idx[envVar]; ok {
			lines[i] = envVar + "=" + value
		} else {
			if len(lines) > 0 && lines[len(lines)-1] != "" {
				lines = append(lines, "")
			}
			lines = append(lines, envVar+"="+value)
		}
	}
	content := ""
	if len(lines) > 0 {
		content = strings.Join(lines, "\n") + "\n"
	}
	tmp := envPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, envPath)
}

// setProviderKey mirrors api/providers.py set_provider_key.
func setProviderKey(home, providerID, apiKey string) (map[string]any, error) {
	if oauthProviders[providerID] {
		return nil, fmt.Errorf("'%s' uses OAuth authentication. Use `hermes model` in the terminal to configure it.", providerID)
	}
	envVar := providerEnvVar(providerID)
	if envVar == "" {
		return nil, fmt.Errorf("Cannot configure API key for '%s'. This provider does not have a known env var mapping.", providerID)
	}
	if apiKey != "" {
		if strings.ContainsAny(apiKey, "\n\r") {
			return nil, fmt.Errorf("API key must not contain newline characters.")
		}
		if len(apiKey) < 8 {
			return nil, fmt.Errorf("API key appears too short.")
		}
	}
	if err := writeEnvFile(home, envVar, apiKey); err != nil {
		return nil, fmt.Errorf("Failed to save API key: %v", err)
	}
	action := "updated"
	if apiKey == "" {
		action = "removed"
	}
	return map[string]any{
		"ok":           true,
		"provider":     providerID,
		"display_name": providerDisplayName(providerID),
		"action":       action,
	}, nil
}

// removeProviderKey mirrors api/providers.py remove_provider_key.
func removeProviderKey(home, providerID string) (map[string]any, error) {
	resp, err := setProviderKey(home, providerID, "")
	if err != nil {
		return nil, err
	}
	if resp["ok"] == true {
		if err := cleanProviderKeyFromConfig(home, providerID); err != nil {
			return nil, err
		}
	}
	return resp, nil
}

// providerDisplayName is a minimal _PROVIDER_DISPLAY stand-in: id → Title Case,
// known ones get their canonical display name.
func providerDisplayName(providerID string) string {
	known := map[string]string{
		"openrouter": "OpenRouter", "anthropic": "Anthropic", "openai": "OpenAI",
		"google": "Google", "gemini": "Gemini", "zai": "Z.ai", "deepseek": "DeepSeek",
		"x-ai": "xAI", "minimax": "MiniMax", "mistralai": "Mistral",
	}
	if name, ok := known[providerID]; ok {
		return name
	}
	return strings.Title(strings.ReplaceAll(providerID, "-", " "))
}

// cleanProviderKeyFromConfig mirrors api/providers.py _clean_provider_key_from_config:
// removes providers.<id>.api_key, model.api_key (only when active provider),
// and custom_providers[].api_key where the custom name matches the provider.
func cleanProviderKeyFromConfig(home, providerID string) error {
	configPath := filepath.Join(home, "config.yaml")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return err
	}
	doc := root.Content[0]
	changed := false

	if provs := findMapKey(doc, "providers"); provs != nil && provs.Kind == yaml.MappingNode {
		if pc := findMapKey(provs, providerID); pc != nil && pc.Kind == yaml.MappingNode {
			if findMapKey(pc, "api_key") != nil {
				removeMapKey(pc, "api_key")
				changed = true
			}
		}
	}
	if mc := findMapKey(doc, "model"); mc != nil && mc.Kind == yaml.MappingNode {
		if findMapKey(mc, "api_key") != nil {
			active := findMapKey(mc, "provider")
			if active != nil && strings.EqualFold(strings.TrimSpace(active.Value), providerID) {
				removeMapKey(mc, "api_key")
				changed = true
			}
		}
	}
	if cps := findMapKey(doc, "custom_providers"); cps != nil && cps.Kind == yaml.SequenceNode {
		for _, item := range cps.Content {
			if item.Kind != yaml.MappingNode {
				continue
			}
			nameNode := findMapKey(item, "name")
			slugNode := findMapKey(item, "slug")
			if nameNode != nil && customProviderNameMatches(providerID, nameNode.Value) ||
				slugNode != nil && strings.EqualFold(strings.TrimSpace(slugNode.Value), providerID) {
				if findMapKey(item, "api_key") != nil {
					removeMapKey(item, "api_key")
					changed = true
				}
			}
		}
	}
	if !changed {
		return nil
	}
	out, err := yaml.Marshal(&root)
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath+".tmp", out, 0o644); err != nil {
		return err
	}
	return os.Rename(configPath+".tmp", configPath)
}

// customProviderNameMatches mirrors api/providers.py _custom_provider_name_matches:
// provider_id matches when it equals the raw name, "custom:<name>", or the name's slug.
func customProviderNameMatches(providerID, rawName string) bool {
	pid := strings.ToLower(strings.TrimSpace(providerID))
	name := strings.ToLower(strings.TrimSpace(rawName))
	if pid == "" || name == "" {
		return false
	}
	slug := slugify(name)
	if pid == name || pid == "custom:"+name || (slug != "" && pid == slug) {
		return true
	}
	return false
}

// slugify converts a display name into a custom-provider slug (lowercase,
// non-alphanumeric → "-", collapse repeats, trim "-").
func slugify(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	return out
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
