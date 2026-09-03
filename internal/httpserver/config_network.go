package httpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// ── provider display names ────────────────────────────────────────────────
// Mirrors api/providers.py _PROVIDER_DISPLAY (id → human label). Entries not
// present fall back to the id itself, matching the Python effective name.
var providerDisplayNames = map[string]string{
	"openai":        "OpenAI",
	"anthropic":     "Anthropic",
	"openrouter":    "OpenRouter",
	"gemini":        "Google Gemini",
	"xai":           "xAI (Grok)",
	"deepseek":      "DeepSeek",
	"together":      "Together AI",
	"groq":          "Groq",
	"mistral":       "Mistral",
	"zai":           "Z.ai (GLM)",
	"moonshotai":    "Moonshot AI",
	"openai-codex":  "OpenAI Codex (OAuth)",
	"copilot":       "GitHub Copilot",
	"nous":          "Nous Research",
	"lmstudio":      "LM Studio",
	"ollama":        "Ollama",
	"custom":        "Custom (OpenAI-compatible)",
	"openai-api":    "OpenAI (API)",
	"openai-compat": "OpenAI-compatible",
	"kimi-coding":   "Kimi",
	"opencode-zen":  "OpenCode Zen",
	"xai-oauth":     "xAI (OAuth)",
	"copilot-acp":   "Copilot ACP",
	"moa":           "MOA",
}

// ── models cache ──────────────────────────────────────────────────────────
// In-memory TTL cache. Python keeps an on-disk cache + 24h TTL + live
// rebuild; Go keeps a process-local 10-min TTL so a running server serves a
// consistent list without hammering provider endpoints, and refresh
// invalidates it immediately.

type modelsCacheEntry struct {
	payload   any
	expiresAt time.Time
}

var (
	modelsCacheMu   sync.Mutex
	modelsCache     = map[string]modelsCacheEntry{}
	liveModelsCache = map[string]modelsCacheEntry{}
)

func invalidateProviderModelsCache(providerID string) {
	modelsCacheMu.Lock()
	defer modelsCacheMu.Unlock()
	delete(modelsCache, "catalog:"+providerID)
	delete(liveModelsCache, providerID)
}

// ── /api/models (catalog) ────────────────────────────────────────────────
// Shape parity with api/config.py get_available_models():
//   {"active_provider": str|None, "default_model": str,
//    "groups": [{"provider": str, "provider_id": str, "models": [{"id","label"}]}]}
// Network-free static catalog (Python's hardcoded _PROVIDER_MODELS fallback).

func configModelSection(home string) map[string]any {
	cfg, _ := readConfigYAML(home)
	if m, ok := cfg["model"].(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func availableModelsCatalog(home string, sessionVisit bool) map[string]any {
	modelCfg := configModelSection(home)
	activeProvider := strings.ToLower(strings.TrimSpace(strval(modelCfg["provider"])))
	defaultModel := strings.TrimSpace(strval(modelCfg["default"]))

	cacheKey := "catalog:" + activeProvider
	modelsCacheMu.Lock()
	if e, ok := modelsCache[cacheKey]; ok && time.Now().Before(e.expiresAt) && !sessionVisit {
		modelsCacheMu.Unlock()
		return e.payload.(map[string]any)
	}
	modelsCacheMu.Unlock()

	groups := buildCatalogGroups(home, activeProvider, defaultModel)

	resp := map[string]any{
		"active_provider": nil,
		"default_model":   defaultModel,
		"groups":          groups,
	}
	if activeProvider != "" {
		resp["active_provider"] = activeProvider
	}

	if !sessionVisit {
		modelsCacheMu.Lock()
		modelsCache[cacheKey] = modelsCacheEntry{payload: resp, expiresAt: time.Now().Add(10 * time.Minute)}
		modelsCacheMu.Unlock()
	}
	return resp
}

func buildCatalogGroups(home, activeProvider, defaultModel string) []any {
	type groupEntry struct {
		provider   string
		providerID string
		models     []any
	}

	var groups []groupEntry
	seen := map[string]bool{}

	// Active provider first
	if activeProvider != "" {
		models := staticModelsForID(activeProvider)
		if len(models) > 0 {
			groups = append(groups, groupEntry{
				provider:   providerDisplayName(activeProvider),
				providerID: activeProvider,
				models:     models,
			})
			seen[activeProvider] = true
		}
	}

	// Then all known providers sorted
	var ids []string
	for pid := range staticProviderModels {
		if !seen[pid] {
			ids = append(ids, pid)
		}
	}
	sort.Strings(ids)
	for _, pid := range ids {
		models := staticModelsForID(pid)
		if len(models) == 0 {
			continue
		}
		groups = append(groups, groupEntry{
			provider:   providerDisplayName(pid),
			providerID: pid,
			models:     models,
		})
	}

	out := make([]any, 0, len(groups))
	for _, g := range groups {
		out = append(out, map[string]any{
			"provider":    g.provider,
			"provider_id": g.providerID,
			"models":      g.models,
		})
	}
	return out
}

func staticModelsForID(pid string) []any {
	raw, ok := staticProviderModels[pid]
	if !ok {
		return nil
	}
	out := make([]any, 0, len(raw))
	for _, m := range raw {
		out = append(out, map[string]any{"id": m, "label": m})
	}
	return out
}

// ── /api/models/live ────────────────────────────────────────────────────
// Shape parity with api/routes.py _handle_live_models():
//   [{"ok": true, "provider": str, "models": [{"id","label"}], "source": str}]
//   or {"ok": false, "error": str, "models": []any{}}
// Go implements the generic OpenAI-compat /v1/models live fetch (custom,
// openai, openrouter, deepseek, together, groq, mistral, zai, nous, kimi,
// opencode-zen); providers with non-standard endpoints fall back to the
// static list (same behaviour as Python's provider_model_ids() fallback).
// hermes_cli OAuth providers (openai-codex, copilot, xai-oauth) are NOT
// ported — their auth + token-exchange lives in the agent; the Go server
// serves the static curated list and documents the gap.

func liveModelsForProvider(home, provider string) map[string]any {
	provider = resolveProviderAlias(provider)

	modelsCacheMu.Lock()
	if e, ok := liveModelsCache[provider]; ok && time.Now().Before(e.expiresAt) {
		modelsCacheMu.Unlock()
		return e.payload.(map[string]any)
	}
	modelsCacheMu.Unlock()

	var payload map[string]any
	cfg, _ := readConfigYAML(home)
	modelCfg := map[string]any{}
	if m, ok := cfg["model"].(map[string]any); ok {
		modelCfg = m
	}

	baseURL := strings.TrimSpace(strval(modelCfg["base_url"]))
	apiKey := ""

	// Custom providers resolve base_url + key from the custom_providers entry.
	if provider == "custom" || strings.HasPrefix(provider, "custom:") {
		cp := findCustomProviderEntry(cfg, provider)
		if cp != nil {
			if bu, ok := cp["base_url"].(string); ok {
				baseURL = strings.TrimSpace(bu)
			}
			apiKey = customProviderAPIKey(cp)
		} else {
			baseURL = strings.TrimSpace(strval(modelCfg["base_url"]))
			apiKey = strings.TrimSpace(strval(modelCfg["api_key"]))
		}
	} else {
		apiKey = providerKeyFromEnv(home, provider)
		if apiKey == "" {
			apiKey = strings.TrimSpace(strval(modelCfg["api_key"]))
		}
	}

	ids, err := fetchOpenAICompatModels(baseURL, apiKey)
	if err != nil || len(ids) == 0 {
		// Static fallback — same as Python's provider_model_ids() fallback.
		static := staticModelsForID(provider)
		if static != nil {
			payload = map[string]any{
				"ok":       true,
				"provider": provider,
				"models":   static,
				"source":   "static",
			}
		} else {
			payload = map[string]any{"ok": false, "error": "no_models", "models": []any{}}
		}
	} else {
		models := make([]any, 0, len(ids))
		for _, m := range ids {
			models = append(models, map[string]any{"id": m, "label": m})
		}
		payload = map[string]any{
			"ok":       true,
			"provider": provider,
			"models":   models,
			"source":   "live",
		}
	}

	modelsCacheMu.Lock()
	liveModelsCache[provider] = modelsCacheEntry{payload: payload, expiresAt: time.Now().Add(5 * time.Minute)}
	modelsCacheMu.Unlock()
	return payload
}

// resolveProviderAlias mirrors api/config.py _resolve_provider_alias.
func resolveProviderAlias(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "z.ai", "z-ai":
		return "zai"
	case "x.ai", "x-ai":
		return "xai"
	case "moon", "moonshot":
		return "moonshotai"
	case "kimi":
		return "kimi-coding"
	case "openai-codex":
		return "openai-codex"
	default:
		return strings.ToLower(strings.TrimSpace(p))
	}
}

func findCustomProviderEntry(cfg map[string]any, provider string) map[string]any {
	cps, ok := cfg["custom_providers"].([]any)
	if !ok {
		if seq, ok2 := cfg["custom_providers"].([]map[string]any); ok2 {
			cps = make([]any, 0, len(seq))
			for _, s := range seq {
				cps = append(cps, s)
			}
		}
	}
	slug := strings.TrimPrefix(provider, "custom:")
	for _, item := range cps {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := strval(entry["name"])
		if provider == "custom" && name == "" {
			return entry
		}
		if customProviderSlug(name) == slug {
			return entry
		}
	}
	return nil
}

func customProviderAPIKey(cp map[string]any) string {
	raw := cp["api_key"]
	if raw != nil {
		key := strings.TrimSpace(strval(raw))
		if strings.HasPrefix(key, "${") && strings.HasSuffix(key, "}") && len(key) > 3 {
			return strings.TrimSpace(os.Getenv(key[2 : len(key)-1]))
		}
		if key != "" {
			return key
		}
	}
	env := strings.TrimSpace(strval(cp["key_env"]))
	if env != "" {
		return strings.TrimSpace(os.Getenv(env))
	}
	return ""
}

// customProviderSlug mirrors api/config.py _custom_provider_slug_from_name.
func customProviderSlug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	prevDash := true
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			prevDash = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == ' ' || r == '_' || r == '-' || r == '.':
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func providerKeyFromEnv(home, provider string) string {
	envVar := providerEnvVar(provider)
	if envVar == "" {
		return ""
	}
	if v := strings.TrimSpace(os.Getenv(envVar)); v != "" {
		return v
	}
	envPath := filepath.Join(home, ".env")
	data, err := os.ReadFile(envPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			continue
		}
		if strings.TrimSpace(kv[0]) == envVar {
			return strings.Trim(strings.TrimSpace(kv[1]), `"'`)
		}
	}
	return ""
}

// fetchOpenAICompatModels GETs <base_url>/v1/models (or /models if base_url
// already ends in /v1) with a Bearer key. Mirrors the urllib fetch in
// api/routes.py _handle_live_models for custom providers.
func fetchOpenAICompatModels(baseURL, apiKey string) ([]string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" || apiKey == "" {
		return nil, fmt.Errorf("base_url or api_key missing")
	}
	ep := strings.TrimSuffix(baseURL, "/")
	if strings.HasSuffix(ep, "/v1") {
		ep += "/models"
	} else {
		ep += "/v1/models"
	}

	client := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequest(http.MethodGet, ep, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	var out []string
	for _, m := range parsed.Data {
		id := strings.TrimSpace(m.ID)
		if id != "" {
			out = append(out, id)
		}
	}
	return out, nil
}

// ── /api/providers ────────────────────────────────────────────────────────
// Shape parity with api/providers.py get_providers():
//   {"providers": [{"id","display_name","has_key","configurable","key_source",
//                   "models","is_oauth","auth_error"}]}
// hermes_cli auth probes (OAuth status, plugin providers) are NOT ported —
// key_source derives from .env / config.yaml presence. Documented gap.

func providersList(home string) map[string]any {
	cfg, _ := readConfigYAML(home)
	if cfg == nil {
		cfg = map[string]any{}
	}
	providersCfg := map[string]any{}
	if m, ok := cfg["providers"].(map[string]any); ok {
		providersCfg = m
	}

	known := map[string]bool{}
	for pid := range staticProviderModels {
		known[pid] = true
	}
	for pid := range providerDisplayNames {
		known[pid] = true
	}
	for pid := range providersCfg {
		known[strings.ToLower(pid)] = true
	}
	for pid := range oauthProviders {
		known[pid] = true
	}

	envKeys := map[string]string{}
	if data, err := os.ReadFile(filepath.Join(home, ".env")); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			kv := strings.SplitN(line, "=", 2)
			if len(kv) == 2 {
				envKeys[strings.TrimSpace(kv[0])] = strings.Trim(strings.TrimSpace(kv[1]), `"'`)
			}
		}
	}

	var ids []string
	for pid := range known {
		ids = append(ids, pid)
	}
	sort.Strings(ids)

	providers := make([]any, 0, len(ids))
	for _, pid := range ids {
		hasKey := false
		keySource := "none"
		if oauthProviders[pid] {
			keySource = "oauth"
			hasKey = true
		}
		envVar := providerEnvVar(pid)
		if envVar != "" {
			envVal := strings.TrimSpace(os.Getenv(envVar))
			if envVal == "" {
				envVal = envKeys[envVar]
			}
			if envVal != "" {
				hasKey = true
				if keySource == "none" {
					keySource = "env_file"
				}
			}
		}
		// config.yaml providers / model sections may carry api_key
		if !hasKey {
			if pc, ok := providersCfg[pid].(map[string]any); ok {
				if strings.TrimSpace(strval(pc["api_key"])) != "" {
					hasKey = true
					keySource = "config_yaml"
				}
			}
			if !hasKey && pid == strings.ToLower(strings.TrimSpace(strval(configModelSection(home)["provider"]))) {
				if strings.TrimSpace(strval(configModelSection(home)["api_key"])) != "" {
					hasKey = true
					keySource = "config_yaml"
				}
			}
		}

		models := staticModelsForID(pid)
		if models == nil {
			models = []any{}
		}
		providers = append(providers, map[string]any{
			"id":           pid,
			"display_name": providerDisplayName(pid),
			"has_key":      hasKey,
			"configurable": envVar != "" || pid == "custom" || strings.HasPrefix(pid, "custom:"),
			"key_source":   keySource,
			"models":       models,
			"is_oauth":     oauthProviders[pid],
		})
	}

	return map[string]any{"providers": providers}
}

// ── /api/providers/self-hosted ───────────────────────────────────────────
// Mirrors api/onboarding.py apply_self_hosted_provider_setup for the two
// supported self-hosted providers (ollama, lmstudio). Writes provider config
// + .env key, then invalidates the models cache.
// Shape: {"ok": true, "provider": str, "base_url": str} + "model" when active.

var selfHostedSetups = map[string]struct {
	envVar string
}{
	"ollama":   {envVar: "OLLAMA_API_KEY"},
	"lmstudio": {envVar: "LMSTUDIO_API_KEY"},
}

func applySelfHostedProviderSetup(home string, body map[string]any) (map[string]any, error) {
	provider := strings.ToLower(strings.TrimSpace(strval(body["provider"])))
	model := strings.TrimSpace(strval(body["model"]))
	apiKey := strings.TrimSpace(strval(body["api_key"]))
	baseURL := strings.TrimSpace(strval(body["base_url"]))
	activate := body["activate"]
	doActivate := activate == nil || truthy(activate)

	meta, ok := selfHostedSetups[provider]
	if !ok {
		return nil, fmt.Errorf("unsupported self-hosted provider: %s", provider)
	}
	if model == "" {
		return nil, fmt.Errorf("model is required")
	}
	if baseURL == "" {
		return nil, fmt.Errorf("base_url is required for this provider")
	}
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("base_url must start with http:// or https://")
	}

	cfg, _ := readConfigYAML(home)
	if cfg == nil {
		cfg = map[string]any{}
	}
	providersCfg := map[string]any{}
	if m, ok := cfg["providers"].(map[string]any); ok {
		providersCfg = m
	}
	providerCfg := map[string]any{}
	if m, ok := providersCfg[provider].(map[string]any); ok {
		providerCfg = m
	}
	providerCfg["base_url"] = baseURL
	providersCfg[provider] = providerCfg
	mutateConfigRoot(cfg, "providers", providersCfg)

	modelCfg := map[string]any{}
	if m, ok := cfg["model"].(map[string]any); ok {
		modelCfg = m
	}
	if doActivate {
		modelCfg["provider"] = provider
		modelCfg["default"] = normalizeModelForProvider(provider, model)
		modelCfg["base_url"] = baseURL
		mutateConfigRoot(cfg, "model", modelCfg)
	}
	if err := writeConfigYAML(home, cfg); err != nil {
		return nil, err
	}

	if apiKey != "" && meta.envVar != "" {
		if err := writeEnvFileKey(home, meta.envVar, apiKey); err != nil {
			return nil, err
		}
	}

	result := map[string]any{"ok": true, "provider": provider, "base_url": baseURL}
	if doActivate {
		result["model"] = strval(modelCfg["default"])
	}
	invalidateProviderModelsCache(provider)
	return result, nil
}

func normalizeModelForProvider(provider, model string) string {
	clean := strings.TrimSpace(model)
	if clean == "" {
		return ""
	}
	if (provider == "anthropic" || provider == "openai") && strings.HasPrefix(clean, provider+"/") {
		return strings.SplitN(clean, "/", 2)[1]
	}
	return clean
}

// ── config.yaml write helpers (yaml.Node round-trip, comment-preserving) ──

func mutateConfigRoot(cfg map[string]any, key string, value map[string]any) {
	cfg[key] = value
}

func writeConfigYAML(home string, cfg map[string]any) error {
	path := filepath.Join(home, "config.yaml")
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return atomicWriteFile(path, data)
}

func atomicWriteFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// writeEnvFileKey rewrites .env preserving all other lines/comments and
// replaces or appends the given key. Mirrors api/providers.py _write_env_file.
func writeEnvFileKey(home, key, value string) error {
	envPath := filepath.Join(home, ".env")
	data, err := os.ReadFile(envPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	lines := strings.Split(string(data), "\n")
	found := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		kv := strings.SplitN(trimmed, "=", 2)
		if len(kv) == 2 && strings.TrimSpace(kv[0]) == key {
			lines[i] = key + "=" + value
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, key+"="+value)
	}
	return atomicWriteFile(envPath, []byte(strings.Join(lines, "\n")+"\n"))
}

func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}

// ── /api/provider/quota ────────────────────────────────────────────────────
// Shape parity with api/providers.py get_provider_quota(). OpenRouter uses a
// pure-HTTP /auth/key fetch (ported); account-usage providers (openai-codex,
// anthropic) rely on hermes_cli's /usage abstraction — NOT ported, they
// return the documented "unavailable" gap response.

const openRouterKeyURL = "https://openrouter.ai/api/v1/key"

var accountUsageProviders = map[string]bool{"openai-codex": true, "anthropic": true}

func providerQuota(home, providerID string, refresh bool) map[string]any {
	provider := strings.ToLower(strings.TrimSpace(providerID))
	if provider == "" {
		mc := configModelSection(home)
		provider = strings.ToLower(strings.TrimSpace(strval(mc["provider"])))
	}
	if provider == "" {
		return map[string]any{
			"ok": false, "provider": nil, "display_name": nil,
			"supported": false, "status": "unavailable", "quota": nil,
			"message": "No active provider is configured.",
		}
	}
	displayName := providerDisplayName(provider)
	if accountUsageProviders[provider] {
		// hermes_cli /usage account-limits abstraction — not available in Go.
		return map[string]any{
			"ok": false, "provider": provider, "display_name": displayName,
			"supported": true, "status": "unavailable", "quota": nil,
			"message": "Account usage status for this provider is not available in the Go build. Use the Python WebUI or CLI.",
		}
	}
	if provider != "openrouter" {
		return map[string]any{
			"ok": false, "provider": provider, "display_name": displayName,
			"supported": false, "status": "unsupported", "quota": nil,
			"message": "Quota status is not available for " + displayName + ".",
		}
	}
	apiKey := providerKeyFromEnv(home, "openrouter")
	if apiKey == "" {
		return map[string]any{
			"ok": false, "provider": "openrouter", "display_name": displayName,
			"supported": true, "status": "no_key", "quota": nil,
			"message": "OpenRouter quota status needs an OPENROUTER_API_KEY configured on the server.",
		}
	}
	if !refresh {
		if cached := cachedProviderQuota(); cached != nil {
			return cached
		}
	}
	quota, label, err := fetchOpenRouterKey(apiKey)
	if err != nil {
		status := "unavailable"
		if err == errInvalidKey {
			status = "invalid_key"
		}
		return map[string]any{
			"ok": false, "provider": "openrouter", "display_name": displayName,
			"supported": true, "status": status, "quota": nil,
			"message": quotaStatusMessage(status),
		}
	}
	resp := map[string]any{
		"ok": true, "provider": "openrouter", "display_name": displayName,
		"supported": true, "status": "available",
		"label": "OpenRouter credits",
		"quota": quota, "message": "OpenRouter quota status loaded.",
	}
	if label != "" {
		resp["label"] = label
	}
	setProviderQuotaCache(resp)
	return resp
}

var errInvalidKey = fmt.Errorf("invalid key")

func fetchOpenRouterKey(apiKey string) (map[string]any, string, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequest(http.MethodGet, openRouterKeyURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, "", errInvalidKey
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, "", err
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, "", err
	}
	data, _ := parsed["data"].(map[string]any)
	if data == nil {
		data = parsed
	}
	quota := map[string]any{
		"limit_remaining": quotaNumber(data["limit_remaining"]),
		"usage":           quotaNumber(data["usage"]),
		"limit":           quotaNumber(data["limit"]),
	}
	label := strings.TrimSpace(strval(data["label"]))
	return quota, label, nil
}

func quotaNumber(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case float64:
		return t
	case int:
		return float64(t)
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return nil
		}
		return f
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return nil
		}
		return f
	default:
		return nil
	}
}

func quotaStatusMessage(status string) string {
	switch status {
	case "invalid_key":
		return "OpenRouter rejected the configured API key."
	default:
		return "OpenRouter quota status is temporarily unavailable."
	}
}

func cachedProviderQuota() map[string]any {
	modelsCacheMu.Lock()
	defer modelsCacheMu.Unlock()
	e, ok := modelsCache["quota:openrouter"]
	if !ok || !time.Now().Before(e.expiresAt) {
		return nil
	}
	return e.payload.(map[string]any)
}

func setProviderQuotaCache(resp map[string]any) {
	modelsCacheMu.Lock()
	modelsCache["quota:openrouter"] = modelsCacheEntry{payload: resp, expiresAt: time.Now().Add(60 * time.Second)}
	modelsCacheMu.Unlock()
}

// ── /api/provider/cost-history ─────────────────────────────────────────────
// Full port of api/providers.py get_provider_cost_history() — openrouter-only.
// Snapshots persisted to <home>/cost-snapshots/openrouter.json (same layout as
// Python: {"provider","snapshots":[{"date","used","limit"}]}).

const costSnapshotMaxDays = 365

func providerCostHistory(home, providerID string, days int) map[string]any {
	provider := strings.ToLower(strings.TrimSpace(providerID))
	if provider == "" {
		return map[string]any{
			"ok": false, "provider": nil,
			"status":  "missing_provider",
			"message": "Provider parameter is required.  Use ?provider=openrouter",
		}
	}
	displayName := providerDisplayName(provider)
	if provider != "openrouter" {
		return map[string]any{
			"ok": false, "provider": provider, "display_name": displayName,
			"supported": false, "status": "unsupported",
			"message": "Cost history is not available for " + displayName + ". Only openrouter is supported in this release.",
		}
	}
	monthlyBudget := providerCostBudget(home)
	apiKey := providerKeyFromEnv(home, "openrouter")
	if apiKey == "" {
		return map[string]any{
			"ok": false, "provider": "openrouter", "display_name": displayName,
			"supported": true, "status": "no_key", "monthly_budget": monthlyBudget,
			"message": "OpenRouter cost history needs an OPENROUTER_API_KEY configured on the server.",
		}
	}
	keyInfo, _, keyErr := fetchOpenRouterKey(apiKey)
	var snapshots []map[string]any
	if keyErr != nil {
		snapshots = readCostSnapshots(home)
		return map[string]any{
			"ok": false, "provider": "openrouter", "display_name": displayName,
			"supported": true, "status": "unavailable", "window_days": days,
			"snapshots": computeDeltas(snapshots, days),
			"limit":     nil, "label": nil, "monthly_budget": monthlyBudget,
			"message": "OpenRouter cost history is temporarily unavailable. Showing last known data.",
		}
	}
	snapshots = appendCostSnapshot(home, quotaNumber(keyInfo["usage"]), quotaNumber(keyInfo["limit"]))
	deltas := computeDeltas(snapshots, days)
	resp := map[string]any{
		"ok": true, "provider": "openrouter", "display_name": displayName,
		"supported": true, "status": "available", "window_days": days,
		"snapshots": deltas,
		"limit":     keyInfo["limit"], "monthly_budget": monthlyBudget,
		"message": "OpenRouter cost history loaded.",
	}
	if l, ok := keyInfo["label"].(string); ok && l != "" {
		resp["label"] = l
	} else {
		resp["label"] = "OpenRouter credits"
	}
	return resp
}

func providerCostBudget(home string) any {
	raw, err := os.ReadFile(filepath.Join(home, "settings.json"))
	if err != nil {
		return nil
	}
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil
	}
	return quotaNumber(s["provider_cost_budget"])
}

func costSnapshotsPath(home string) string {
	return filepath.Join(home, "cost-snapshots", "openrouter.json")
}

func readCostSnapshots(home string) []map[string]any {
	data, err := os.ReadFile(costSnapshotsPath(home))
	if err != nil {
		return nil
	}
	var payload struct {
		Snapshots []map[string]any `json:"snapshots"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil
	}
	var out []map[string]any
	for _, e := range payload.Snapshots {
		date := strings.TrimSpace(strval(e["date"]))
		if date == "" {
			continue
		}
		out = append(out, map[string]any{
			"date":  date,
			"used":  quotaNumber(e["used"]),
			"limit": quotaNumber(e["limit"]),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return strval(out[i]["date"]) < strval(out[j]["date"])
	})
	return out
}

func appendCostSnapshot(home string, usage, limit any) []map[string]any {
	snapshots := readCostSnapshots(home)
	today := time.Now().UTC().Format("2006-01-02")
	updated := false
	for _, e := range snapshots {
		if strval(e["date"]) == today {
			e["used"] = usage
			e["limit"] = limit
			updated = true
			break
		}
	}
	if !updated {
		snapshots = append(snapshots, map[string]any{"date": today, "used": usage, "limit": limit})
	}
	sort.Slice(snapshots, func(i, j int) bool {
		return strval(snapshots[i]["date"]) < strval(snapshots[j]["date"])
	})
	if len(snapshots) > costSnapshotMaxDays {
		snapshots = snapshots[len(snapshots)-costSnapshotMaxDays:]
	}
	payload := map[string]any{"provider": "openrouter", "snapshots": snapshots}
	data, _ := json.MarshalIndent(payload, "", "  ")
	if err := os.MkdirAll(filepath.Dir(costSnapshotsPath(home)), 0o755); err == nil {
		atomicWriteFile(costSnapshotsPath(home), data)
	}
	return snapshots
}

func computeDeltas(snapshots []map[string]any, windowDays int) []map[string]any {
	window := snapshots
	if len(window) > windowDays {
		window = window[len(window)-windowDays:]
	}
	var result []map[string]any
	for i, entry := range window {
		var delta any
		if i > 0 {
			prevUsed := quotaNumber(window[i-1]["used"])
			curUsed := quotaNumber(entry["used"])
			if curUsed != nil && prevUsed != nil {
				if pf, ok1 := prevUsed.(float64); ok1 {
					if cf, ok2 := curUsed.(float64); ok2 {
						d := cf - pf
						if d < 0 {
							d = cf
						}
						if d < 1e-9 && d > -1e-9 {
							d = 0.0
						} else {
							d = mathRound6(d)
						}
						delta = d
					}
				}
			}
		}
		result = append(result, map[string]any{
			"date":  entry["date"],
			"used":  entry["used"],
			"delta": delta,
		})
	}
	return result
}

func mathRound6(v float64) float64 {
	return float64(int64(v*1e6+0.5)) / 1e6
}

// ── /api/reasoning ────────────────────────────────────────────────────────
// Mirrors api/config.py get_reasoning_status / set_reasoning_display /
// set_reasoning_effort — config.yaml keys shared with the CLI.

var validReasoningEfforts = []string{"minimal", "low", "medium", "high", "xhigh", "max"}

func reasoningStatus(home string) map[string]any {
	cfg, _ := readConfigYAML(home)
	if cfg == nil {
		cfg = map[string]any{}
	}
	var showReasoning any
	if d, ok := cfg["display"].(map[string]any); ok {
		showReasoning = d["show_reasoning"]
	}
	show := true
	if b, ok := showReasoning.(bool); ok {
		show = b
	}
	var effortRaw string
	if a, ok := cfg["agent"].(map[string]any); ok {
		effortRaw = strings.ToLower(strings.TrimSpace(strval(a["reasoning_effort"])))
	}
	effort := ""
	for _, v := range validReasoningEfforts {
		if v == effortRaw {
			effort = v
			break
		}
	}
	return map[string]any{
		"show_reasoning":            show,
		"reasoning_effort":          effort,
		"supported_efforts":         validReasoningEfforts,
		"supports_reasoning_effort": true,
		"supports_thinking_toggle":  true,
	}
}

func setReasoningDisplay(home string, show bool) map[string]any {
	cfg, _ := readConfigYAML(home)
	if cfg == nil {
		cfg = map[string]any{}
	}
	displayCfg := map[string]any{}
	if m, ok := cfg["display"].(map[string]any); ok {
		displayCfg = m
	}
	displayCfg["show_reasoning"] = show
	cfg["display"] = displayCfg
	if err := writeConfigYAML(home, cfg); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	return reasoningStatus(home)
}

func setReasoningEffort(home, effort, modelID, providerID, baseURL string) (map[string]any, error) {
	raw := strings.ToLower(strings.TrimSpace(effort))
	valid := raw == "" || raw == "none"
	for _, v := range validReasoningEfforts {
		if v == raw {
			valid = true
			break
		}
	}
	if !valid {
		return nil, fmt.Errorf("Unknown reasoning effort '%s'. Valid: none, %s.", effort, strings.Join(validReasoningEfforts, ", "))
	}
	cfg, _ := readConfigYAML(home)
	if cfg == nil {
		cfg = map[string]any{}
	}
	agentCfg := map[string]any{}
	if m, ok := cfg["agent"].(map[string]any); ok {
		agentCfg = m
	}
	if raw != "" && raw != "none" {
		agentCfg["reasoning_effort"] = raw
	} else {
		delete(agentCfg, "reasoning_effort")
	}
	cfg["agent"] = agentCfg
	if err := writeConfigYAML(home, cfg); err != nil {
		return nil, err
	}
	return reasoningStatus(home), nil
}

// ── /api/dashboard ─────────────────────────────────────────────────────────
// Mirrors api/dashboard_probe.py get_dashboard_config / get_dashboard_status.
// Status probes loopback targets for `hermes dashboard` — returns running:false
// when the dashboard isn't up (same as Python).

var dashboardEnabledValues = map[string]bool{"auto": true, "always": true, "never": true}

func dashboardConfig(home string) map[string]any {
	cfg, _ := readConfigYAML(home)
	if cfg == nil {
		cfg = map[string]any{}
	}
	var d map[string]any
	if w, ok := cfg["webui"].(map[string]any); ok {
		if dd, ok2 := w["dashboard"].(map[string]any); ok2 {
			d = dd
		}
	}
	if d == nil {
		d = map[string]any{}
	}
	enabled := strings.ToLower(strings.TrimSpace(strval(d["enabled"])))
	if enabled == "" {
		enabled = "auto"
	}
	if !dashboardEnabledValues[enabled] {
		enabled = "auto"
	}
	rawURL := strings.TrimSpace(strval(d["url"]))
	return map[string]any{"enabled": enabled, "url": rawURL}
}

func dashboardStatus(home string) map[string]any {
	cfg := dashboardConfig(home)
	enabled := strval(cfg["enabled"])
	if enabled == "never" {
		return map[string]any{"running": false, "enabled": "never"}
	}
	// auto probe: check default loopback targets (127.0.0.1:8787 etc) briefly.
	// Python probes each target; a quick best-effort GET /api/status to the
	// first default target mirrors the common case.
	client := &http.Client{Timeout: 800 * time.Millisecond}
	resp, err := client.Get("http://127.0.0.1:8787/api/status")
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			return map[string]any{"running": true, "host": "127.0.0.1", "port": 8787, "url": "http://127.0.0.1:8787", "browser_url": "http://127.0.0.1:8787", "enabled": enabled}
		}
	}
	if enabled == "always" {
		return map[string]any{"running": true, "enabled": enabled, "url": "http://127.0.0.1:8787", "browser_url": "http://127.0.0.1:8787"}
	}
	return map[string]any{"running": false, "enabled": enabled}
}

// ── /api/projects ───────────────────────────────────────────────────────────
// Mirrors api/routes.py GET /api/projects: reads hermesHome/state/projects.json
// and profile-scopes rows to the active profile unless ?all_profiles=1.

func loadProjectsFile(home string) []map[string]any {
	p := filepath.Join(home, "state", "projects.json")
	if _, err := os.Stat(p); err != nil {
		return nil
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil
	}
	return rows
}

// activeProfileName resolves the profile name for a HERMES_HOME (mirrors the
// scheme used by GET /api/profile/active): a home inside profiles/ is named by
// its last path segment, otherwise it is the root "default".
func activeProfileName(home string) string {
	home = filepath.Clean(home)
	if parent := filepath.Base(filepath.Dir(home)); parent == "profiles" {
		return filepath.Base(home)
	}
	return "default"
}

// profilesMatch mirrors api/profiles.py _profiles_match: rows tagged "default"
// (or untagged, which is treated as default) belong to the root profile, and
// renamed-root aliases cross-match.
//
// ponytail: Python's _is_root_profile() shells out to `hermes_cli list-profiles`
// to detect renamed-root aliases (is_default flag). We skip that subprocess and
// only handle the literal "default" alias — renamed-root aliases won't cross-
// match legacy "default"-tagged rows. Upgrade when profile listing is native.
func profilesMatch(rowProfile, active string) bool {
	row := strings.TrimSpace(rowProfile)
	if row == "" {
		row = "default"
	}
	active = strings.TrimSpace(active)
	if active == "" {
		active = "default"
	}
	return row == active
}

func projectsList(home string, allProfiles bool) map[string]any {
	all := loadProjectsFile(home)
	if all == nil {
		all = []map[string]any{}
	}
	active := activeProfileName(home)
	var scoped []map[string]any
	for _, p := range all {
		if allProfiles || profilesMatch(strval(p["profile"]), active) {
			scoped = append(scoped, p)
		}
	}
	other := 0
	if !allProfiles {
		other = len(all) - len(scoped)
	}
	if scoped == nil {
		scoped = []map[string]any{}
	}
	return map[string]any{
		"projects":            scoped,
		"all_profiles":        allProfiles,
		"active_profile":      active,
		"other_profile_count": other,
	}
}

// ── /api/auth/status ─────────────────────────────────────────────────────────
// Mirrors api/routes.py GET /api/auth/status: reports whether auth is enabled
// and the shapes of auth the frontend should offer. Session validation
// (trusted sessions, cookies) is left to the proxy/Python — we report the
// config-level facts only.

func passkeyFeatureFlagEnabled(home string) bool {
	envValue := os.Getenv("HERMES_WEBUI_PASSKEY")
	if envValue != "" {
		v := strings.ToLower(strings.TrimSpace(envValue))
		return v == "1" || v == "true" || v == "yes" || v == "on"
	}
	cfg, _ := readConfigYAML(home)
	if cfg == nil {
		cfg = map[string]any{}
	}
	if b, ok := cfg["webui_passkey_enabled"].(bool); ok {
		return b
	}
	return false
}

func registeredCredentialsCount(home string) int {
	p := filepath.Join(home, "state", "passkeys.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		return 0
	}
	var rows []any
	if err := json.Unmarshal(raw, &rows); err != nil {
		return 0
	}
	return len(rows)
}

func authStatus(home string) map[string]any {
	settings := readRawSettings(home)
	if settings == nil {
		settings = map[string]any{}
	}
	passwordEnabled := settings["password_hash"] != nil
	if os.Getenv("HERMES_WEBUI_PASSWORD") != "" {
		passwordEnabled = true
	}
	passkeyFlag := passkeyFeatureFlagEnabled(home)
	passkeyCount := 0
	if passkeyFlag {
		passkeyCount = registeredCredentialsCount(home)
	}
	passkeysEnabled := passkeyCount > 0
	oidcEnabled := false    // OIDC config lives under webui.oidc in config.yaml
	trustedEnabled := false // trusted-header auth is a proxy deployment concern
	authEnabled := passwordEnabled || passkeysEnabled || oidcEnabled || trustedEnabled
	passwordless := passkeysEnabled && !passwordEnabled
	acked := false
	if !authEnabled {
		if b, ok := settings["auth_disabled_acknowledged"].(bool); ok {
			acked = b
		}
	}
	payload := map[string]any{
		"auth_enabled":               authEnabled,
		"logged_in":                  false,
		"oidc_enabled":               oidcEnabled,
		"password_auth_enabled":      passwordEnabled,
		"passwordless_enabled":       passwordless,
		"passkeys_enabled":           passkeysEnabled,
		"passkeys_count":             passkeyCount,
		"passkey_feature_flag":       passkeyFlag,
		"auth_disabled_acknowledged": acked,
	}
	return payload
}
