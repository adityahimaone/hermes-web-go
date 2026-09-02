// Package config loads server configuration from environment variables,
// with defaults matching the legacy Python WebUI.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	Host           string
	Port           int
	LegacyProxyURL string
	StaticDir      string
	DataRoot       string
	DatabasePath   string
	StateDBPath    string
	AgentTransport string // auto|http|grpc (Phase 4; only http implemented now)
	AgentSocket    string
	AgentBaseURL   string
	AgentAPIKey    string
	Password       string
}

// Load reads configuration via getenv. An empty value falls back to the default.
func Load(getenv func(string) string) (Config, error) {
	c := Config{
		Host:           getenv("HERMES_WEBUI_HOST"),
		StaticDir:      getenv("HERMES_WEBUI_STATIC_DIR"),
		LegacyProxyURL: getenv("HERMES_WEBUI_LEGACY_PROXY_URL"),
		DataRoot:       getenv("HERMES_WEBUI_DATA_ROOT"),
		DatabasePath:   getenv("HERMES_WEBUI_DATABASE_PATH"),
		StateDBPath:    getenv("HERMES_WEBUI_STATE_DB_PATH"),
		AgentTransport: getenv("HERMES_WEBUI_AGENT_TRANSPORT"),
		AgentSocket:    getenv("HERMES_WEBUI_AGENT_SOCKET"),
		AgentBaseURL:   getenv("HERMES_WEBUI_RUNNER_BASE_URL"),
		AgentAPIKey:    getenv("HERMES_WEBUI_RUNNER_API_KEY"),
		Password:       getenv("HERMES_WEBUI_PASSWORD"),
	}
	if c.Host == "" {
		c.Host = "127.0.0.1"
	}
	if c.StaticDir == "" {
		c.StaticDir = "./static"
	}
	if c.DataRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Config{}, fmt.Errorf("home directory: %w", err)
		}
		c.DataRoot = filepath.Join(home, ".hermes", "webui")
	}
	if c.DatabasePath == "" {
		c.DatabasePath = filepath.Join(c.DataRoot, "webui.db")
	}
	if c.StateDBPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Config{}, fmt.Errorf("home directory: %w", err)
		}
		c.StateDBPath = filepath.Join(home, ".hermes", "state.db")
	}
	portStr := getenv("HERMES_WEBUI_PORT")
	if portStr == "" {
		portStr = "8787"
	}
	p, err := strconv.Atoi(portStr)
	if err != nil {
		return Config{}, fmt.Errorf("HERMES_WEBUI_PORT invalid: %w", err)
	}
	if p < 1 || p > 65535 {
		return Config{}, fmt.Errorf("HERMES_WEBUI_PORT out of range: %d", p)
	}
	c.Port = p
	return c, nil
}

// FromEnv loads configuration from the process environment.
func FromEnv() (Config, error) { return Load(os.Getenv) }
