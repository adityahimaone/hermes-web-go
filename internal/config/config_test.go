package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	c, err := Load(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "127.0.0.1" || c.Port != 8787 || c.StaticDir != "./static" {
		t.Fatalf("defaults = %+v", c)
	}
}

func TestLoadOverrides(t *testing.T) {
	vals := map[string]string{"HERMES_WEBUI_HOST": "0.0.0.0", "HERMES_WEBUI_PORT": "9999", "HERMES_WEBUI_LEGACY_PROXY_URL": "http://127.0.0.1:8788", "HERMES_WEBUI_STATIC_DIR": "x"}
	c, err := Load(func(k string) string { return vals[k] })
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "0.0.0.0" || c.Port != 9999 || c.LegacyProxyURL != vals["HERMES_WEBUI_LEGACY_PROXY_URL"] || c.StaticDir != "x" {
		t.Fatalf("overrides = %+v", c)
	}
}

func TestInvalidConfigFails(t *testing.T) {
	for _, vals := range []map[string]string{{"HERMES_WEBUI_PORT": "abc"}, {"HERMES_WEBUI_PORT": "0"}, {"HERMES_WEBUI_PORT": "65536"}} {
		if _, err := Load(func(k string) string { return vals[k] }); err == nil {
			t.Fatalf("expected error for %+v", vals)
		}
	}
}
