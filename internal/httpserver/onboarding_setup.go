package httpserver

// Wave 18 — onboarding/setup (apply_onboarding_setup port).
//
// POST /api/onboarding/setup — write provider/model selection into
// {hermes_home}/config.yaml (comment-preserving yaml.Node round-trip) and the
// api_key into .env (writeEnvFileKey). Provider matrix is a condensed port of
// _SUPPORTED_PROVIDER_SETUPS (env_var, base_url requirements, key_optional).
// SKIP_ONBOARDING=1 short-circuits to flag+status (no writes), confirm_overwrite
// guards an existing config.yaml, unsupported providers just mark complete.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"
)

type providerSetupMeta struct {
	Label           string
	EnvVar          string
	DefaultModel    string
	DefaultBaseURL  string
	RequiresBaseURL bool
	KeyOptional     bool
}

var supportedProviderSetups = map[string]providerSetupMeta{
	"openrouter": {Label: "OpenRouter", EnvVar: "OPENROUTER_API_KEY", DefaultModel: "anthropic/claude-sonnet-4.6"},
	"anthropic":  {Label: "Anthropic", EnvVar: "ANTHROPIC_API_KEY", DefaultModel: "claude-sonnet-4.6"},
	"openai":     {Label: "OpenAI", EnvVar: "OPENAI_API_KEY", DefaultModel: "gpt-4o", DefaultBaseURL: "https://api.openai.com/v1"},
	"ollama":     {Label: "Ollama", EnvVar: "OLLAMA_API_KEY", DefaultModel: "qwen3:32b", DefaultBaseURL: "http://localhost:11434/v1", RequiresBaseURL: true, KeyOptional: true},
	"lmstudio":   {Label: "LM Studio", EnvVar: "LM_API_KEY", DefaultModel: "gpt-4o-mini", DefaultBaseURL: "http://localhost:1234/v1", RequiresBaseURL: true, KeyOptional: true},
	"custom":     {Label: "Custom OpenAI-compatible", EnvVar: "OPENAI_API_KEY", DefaultModel: "gpt-4o-mini", RequiresBaseURL: true, KeyOptional: true},
	"gemini":     {Label: "Google Gemini", EnvVar: "GOOGLE_API_KEY", DefaultModel: "gemini-3.1-pro-preview", DefaultBaseURL: "https://generativelanguage.googleapis.com/v1beta/openai"},
	"deepseek":   {Label: "DeepSeek", EnvVar: "DEEPSEEK_API_KEY", DefaultModel: "deepseek-v4-flash", DefaultBaseURL: "https://api.deepseek.com"},
	"xiaomi":     {Label: "Xiaomi MiMo", EnvVar: "XIAOMI_API_KEY", DefaultModel: "mimo-v2.5-pro", DefaultBaseURL: "https://api.xiaomimimo.com/v1"},
	"zai":        {Label: "Z.AI / GLM", EnvVar: "GLM_API_KEY", DefaultModel: "glm-5.1", DefaultBaseURL: "https://open.bigmodel.cn/api/paas/v4"},
	"nvidia":     {Label: "NVIDIA NIM", EnvVar: "NVIDIA_API_KEY", DefaultModel: "nvidia/llama-3.3-nemotron-super-49b-v1.5", DefaultBaseURL: "https://integrate.api.nvidia.com/v1"},
	"mistralai":  {Label: "Mistral", EnvVar: "MISTRAL_API_KEY", DefaultModel: "mistral-large-latest", DefaultBaseURL: "https://api.mistral.ai/v1"},
	"x-ai":       {Label: "xAI (Grok)", EnvVar: "XAI_API_KEY", DefaultModel: "grok-4.20", DefaultBaseURL: "https://api.x.ai/v1"},
}

// providerAPIKeyPresent mirrors _provider_api_key_present (.env canonical +
// aliases + config model.api_key + providers.<provider>.api_key).
func providerAPIKeyPresent(provider string, cfg map[string]any, envValues map[string]string) bool {
	meta, ok := supportedProviderSetups[provider]
	if !ok {
		return false
	}
	if envValues[meta.EnvVar] != "" {
		return true
	}
	if provider == "lmstudio" && envValues["LMSTUDIO_API_KEY"] != "" {
		return true
	}
	if modelCfg, ok := cfg["model"].(map[string]any); ok {
		if k, _ := modelCfg["api_key"].(string); strings.TrimSpace(k) != "" {
			return true
		}
	}
	providersCfg, _ := cfg["providers"].(map[string]any)
	if providersCfg != nil {
		if pc, ok := providersCfg[provider].(map[string]any); ok {
			if k, _ := pc["api_key"].(string); strings.TrimSpace(k) != "" {
				return true
			}
		}
	}
	return false
}

func loadEnvValues(hermesHome string) map[string]string {
	raw, err := os.ReadFile(filepath.Join(hermesHome, ".env"))
	values := map[string]string{}
	if err != nil {
		return values
	}
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		kv := strings.SplitN(trimmed, "=", 2)
		if len(kv) == 2 {
			values[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
		}
	}
	return values
}

// setModelConfig writes cfg["model"] via a yaml.Node round-trip preserving
// comments, mirroring setAuxModel's approach.
func setModelConfig(home string, mutate func(model map[string]any) map[string]any) error {
	configPath := filepath.Join(home, "config.yaml")
	raw, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var root yaml.Node
	if len(raw) > 0 {
		if err := yaml.Unmarshal(raw, &root); err != nil {
			return fmt.Errorf("failed to parse config.yaml: %w", err)
		}
	}
	if root.Kind == 0 || len(root.Content) == 0 {
		root = yaml.Node{Kind: yaml.DocumentNode}
		doc := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = []*yaml.Node{doc}
	}
	doc := root.Content[0]

	// Extract the current model mapping into a generic map for mutation.
	var modelCfg map[string]any
	if n := findMapKey(doc, "model"); n != nil && n.Kind == yaml.MappingNode {
		rawModel, _ := yaml.Marshal(n)
		_ = yaml.Unmarshal(rawModel, &modelCfg)
	} else {
		modelCfg = map[string]any{}
	}
	modelCfg = mutate(modelCfg)

	if n := findMapKey(doc, "model"); n != nil {
		// replace value node in place
		var fresh yaml.Node
		data, _ := yaml.Marshal(modelCfg)
		if err := yaml.Unmarshal(data, &fresh); err == nil && len(fresh.Content) > 0 {
			*n = *fresh.Content[0]
		}
	} else {
		var fresh yaml.Node
		data, _ := yaml.Marshal(modelCfg)
		if err := yaml.Unmarshal(data, &fresh); err == nil && len(fresh.Content) > 0 {
			doc.Content = append(doc.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "model"},
				fresh.Content[0])
		}
	}
	out, err := yaml.Marshal(&root)
	if err != nil {
		return err
	}
	return atomicWriteFile(configPath, out)
}

func handleOnboardingSetup(dataRoot, hermesHome string, body map[string]any) (int, map[string]any) {
	settings := loadWebUISettings(dataRoot, hermesHome)
	skip := os.Getenv("HERMES_WEBUI_SKIP_ONBOARDING")
	if skip == "1" || skip == "true" || skip == "yes" {
		settings["onboarding_completed"] = true
		_, _ = saveWebUISettings(dataRoot, hermesHome, settings)
		return 200, map[string]any{"completed": true, "skipped": true}
	}

	provider, _ := body["provider"].(string)
	provider = strings.ToLower(strings.TrimSpace(provider))
	model, _ := body["model"].(string)
	apiKey, _ := body["api_key"].(string)
	apiKey = strings.TrimSpace(apiKey)
	baseURLRaw, _ := body["base_url"].(string)
	baseURL := strings.TrimRight(strings.TrimSpace(baseURLRaw), "/")

	meta, supported := supportedProviderSetups[provider]
	if !supported {
		// Unsupported providers are already CLI-configured: just mark complete.
		settings["onboarding_completed"] = true
		_, _ = saveWebUISettings(dataRoot, hermesHome, settings)
		return 200, map[string]any{"completed": true, "unsupported_provider": provider}
	}
	if strings.TrimSpace(model) == "" {
		return 400, map[string]any{"error": "model is required"}
	}
	if meta.RequiresBaseURL && baseURL == "" {
		return 400, map[string]any{"error": "base_url is required for " + meta.Label}
	}
	if baseURL != "" {
		if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
			return 400, map[string]any{"error": "base_url must start with http:// or https://"}
		}
	}

	configPath := filepath.Join(hermesHome, "config.yaml")
	if _, err := os.Stat(configPath); err == nil && body["confirm_overwrite"] != true {
		return 200, map[string]any{
			"error":            "config_exists",
			"message":          "Hermes is already configured (config.yaml exists). Pass confirm_overwrite=true to overwrite it.",
			"requires_confirm": true,
		}
	}

	// Key requirement: key_optional providers skip; others need a key present
	// (new input or already in .env/config).
	envValues := loadEnvValues(hermesHome)
	cfg := map[string]any{}
	if raw, err := os.ReadFile(configPath); err == nil {
		_ = yaml.Unmarshal(raw, &cfg)
	}
	if apiKey == "" && !meta.KeyOptional && !providerAPIKeyPresent(provider, cfg, envValues) {
		return 400, map[string]any{"error": meta.EnvVar + " is required"}
	}

	if err := setModelConfig(hermesHome, func(modelCfg map[string]any) map[string]any {
		modelCfg["provider"] = provider
		modelCfg["default"] = normalizeModelForProvider(provider, model)
		switch {
		case meta.RequiresBaseURL:
			modelCfg["base_url"] = baseURL
		case meta.DefaultBaseURL != "":
			modelCfg["base_url"] = meta.DefaultBaseURL
		default:
			delete(modelCfg, "base_url")
		}
		return modelCfg
	}); err != nil {
		return 500, map[string]any{"error": err.Error()}
	}

	if apiKey != "" {
		if err := writeEnvFileKey(hermesHome, meta.EnvVar, apiKey); err != nil {
			return 500, map[string]any{"error": "failed to write .env: " + err.Error()}
		}
		os.Setenv(meta.EnvVar, apiKey)
	}

	settings["onboarding_completed"] = true
	_, _ = saveWebUISettings(dataRoot, hermesHome, settings)
	return 200, map[string]any{"ok": true, "completed": true, "provider": provider, "model": normalizeModelForProvider(provider, model)}
}

// Wave18Router mounts POST /api/onboarding/setup.
func Wave18Router(r chi.Router, dataRoot, hermesHome string) {
	r.Post("/api/onboarding/setup", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			wave4WriteJSONErr(w, 400, "invalid request body")
			return
		}
		st, payload := handleOnboardingSetup(dataRoot, hermesHome, body)
		wave4WriteJSON(w, st, payload)
	})
}
