// Package config loads server configuration from environment variables,
// with defaults matching the legacy Python WebUI.
package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Host           string
	Port           int
	LegacyProxyURL string
	StaticDir      string
}

// Load reads configuration via getenv. An empty value falls back to the default.
func Load(getenv func(string) string) (Config, error) {
	c := Config{
		Host:           getenv("HERMES_WEBUI_HOST"),
		StaticDir:      getenv("HERMES_WEBUI_STATIC_DIR"),
		LegacyProxyURL: getenv("HERMES_WEBUI_LEGACY_PROXY_URL"),
	}
	if c.Host == "" {
		c.Host = "127.0.0.1"
	}
	if c.StaticDir == "" {
		c.StaticDir = "./static"
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
