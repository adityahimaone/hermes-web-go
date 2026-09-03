package httpserver

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func writeHomeFile(t *testing.T, home, rel, content string) {
	t.Helper()
	p := filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func TestProfileActiveDefault(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, "config.yaml", "model:\n  default: codex\n  provider: custom\nterminal:\n  cwd: /Users/adityahimawan/dev\n")
	// last_workspace.txt is profile-scoped and wins over config.yaml
	ws := filepath.Join(home, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatalf("mkdir ws: %v", err)
	}
	writeHomeFile(t, home, "last_workspace.txt", ws+"\n")

	r := chi.NewRouter()
	ConfigRouter(r, home, t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/api/profile/active", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		Name             string `json:"name"`
		Path             string `json:"path"`
		IsDefault        bool   `json:"is_default"`
		DefaultWorkspace string `json:"default_workspace"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Name != "default" {
		t.Errorf("name = %q, want default", out.Name)
	}
	if !out.IsDefault {
		t.Errorf("is_default = false, want true")
	}
	if out.Path != filepath.Clean(home) {
		t.Errorf("path = %q, want %q", out.Path, home)
	}
	// last_workspace.txt wins over config.yaml terminal.cwd
	if out.DefaultWorkspace != ws {
		t.Errorf("default_workspace = %q, want %q (last_workspace.txt)", out.DefaultWorkspace, ws)
	}
}

func TestProfileActiveNamed(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "profiles", "karina")
	writeHomeFile(t, home, "config.yaml", "model:\n  default: gpt-5\n  provider: openai\n")

	r := chi.NewRouter()
	ConfigRouter(r, home, t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/api/profile/active", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	var out struct {
		Name      string `json:"name"`
		IsDefault bool   `json:"is_default"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Name != "karina" {
		t.Errorf("name = %q, want karina", out.Name)
	}
	if out.IsDefault {
		t.Errorf("is_default = true for named profile")
	}
}

func TestProfilesList(t *testing.T) {
	base := t.TempDir()
	// base home = default
	writeHomeFile(t, base, "config.yaml", "model:\n  default: codex\n  provider: custom\n")
	writeHomeFile(t, base, "skills/foo/SKILL.md", "# foo\n")
	// named profile karina
	karina := filepath.Join(base, "profiles", "karina")
	writeHomeFile(t, karina, "config.yaml", "model:\n  default: gpt-5\n  provider: openai\n")

	// active home = base (default profile)
	r := chi.NewRouter()
	ConfigRouter(r, base, t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/api/profiles", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		Profiles []map[string]any `json:"profiles"`
		Active   string           `json:"active"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Active != "default" {
		t.Errorf("active = %q, want default", out.Active)
	}
	if len(out.Profiles) != 2 {
		t.Fatalf("len(profiles) = %d, want 2 (default + karina)", len(out.Profiles))
	}
	// row 0 default is_active
	if out.Profiles[0]["name"] != "default" || out.Profiles[0]["is_active"] != true {
		t.Errorf("row0 = %v, want default active", out.Profiles[0]["name"])
	}
	if out.Profiles[1]["name"] != "karina" || out.Profiles[1]["model"] != "gpt-5" {
		t.Errorf("row1 = %v", out.Profiles[1])
	}
	if out.Profiles[0]["skill_count"] != float64(1) {
		t.Errorf("default skill_count = %v, want 1", out.Profiles[0]["skill_count"])
	}
}

func TestProfilesListNamedActive(t *testing.T) {
	base := t.TempDir()
	writeHomeFile(t, base, "config.yaml", "model:\n  default: codex\n  provider: custom\n")
	karina := filepath.Join(base, "profiles", "karina")
	writeHomeFile(t, karina, "config.yaml", "model:\n  default: gpt-5\n  provider: openai\n")

	r := chi.NewRouter()
	ConfigRouter(r, karina, t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/api/profiles", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	var out struct {
		Profiles []map[string]any `json:"profiles"`
		Active   string           `json:"active"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Active != "karina" {
		t.Errorf("active = %q, want karina", out.Active)
	}
	if len(out.Profiles) != 2 {
		t.Fatalf("len(profiles) = %d, want 2", len(out.Profiles))
	}
	if out.Profiles[1]["is_active"] != true {
		t.Errorf("karina is_active = %v, want true", out.Profiles[1]["is_active"])
	}
}

func TestProfilesHiddenProfile(t *testing.T) {
	base := t.TempDir()
	writeHomeFile(t, base, "config.yaml", "# empty\n")
	hidden := filepath.Join(base, "profiles", "hidden")
	writeHomeFile(t, hidden, "profile.yaml", "visible: false\n")

	r := chi.NewRouter()
	ConfigRouter(r, base, t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/api/profiles", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	var out struct {
		Profiles []map[string]any `json:"profiles"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// hidden profile visible=false
	if len(out.Profiles) != 2 {
		t.Fatalf("len = %d, want 2", len(out.Profiles))
	}
	for _, p := range out.Profiles {
		if p["name"] == "hidden" && p["visible"] != false {
			t.Errorf("hidden visible = %v, want false", p["visible"])
		}
	}
	if !strings.Contains(rr.Body.String(), `"visible":false`) && !strings.Contains(rr.Body.String(), `"visible": false`) {
		t.Errorf("no visible:false in body")
	}
}

func TestSettingsDefaults(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, "config.yaml", "model:\n  default: codex\n  provider: custom\n")
	dataRoot := t.TempDir() // no settings.json

	r := chi.NewRouter()
	ConfigRouter(r, home, dataRoot)
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["theme"] != "dark" {
		t.Errorf("theme = %v, want dark", out["theme"])
	}
	if out["skin"] != "default" {
		t.Errorf("skin = %v, want default", out["skin"])
	}
	if out["send_key"] != "enter" {
		t.Errorf("send_key = %v, want enter", out["send_key"])
	}
	if out["pinned_sessions_limit"] != float64(3) {
		t.Errorf("pinned_sessions_limit = %v, want 3", out["pinned_sessions_limit"])
	}
	if out["default_model"] != "codex" {
		t.Errorf("default_model = %v, want codex (config.yaml)", out["default_model"])
	}
	if out["default_model_provider"] != "custom" {
		t.Errorf("default_model_provider = %v, want custom", out["default_model_provider"])
	}
	if _, has := out["password_hash"]; has {
		t.Errorf("password_hash leaked")
	}
	ps, ok := out["persisted_speech_keys"].([]any)
	if !ok || len(ps) != 0 {
		t.Errorf("persisted_speech_keys = %v, want empty", out["persisted_speech_keys"])
	}
}

func TestSettingsStoredMerge(t *testing.T) {
	home := t.TempDir()
	dataRoot := t.TempDir()
	writeHomeFile(t, dataRoot, "settings.json", `{"theme":"light","send_key":"ctrl+enter","pinned_sessions_limit":5}`)

	r := chi.NewRouter()
	ConfigRouter(r, home, dataRoot)
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["theme"] != "light" {
		t.Errorf("theme = %v, want light (stored)", out["theme"])
	}
	if out["send_key"] != "ctrl+enter" {
		t.Errorf("send_key = %v, want ctrl+enter", out["send_key"])
	}
	if out["pinned_sessions_limit"] != float64(5) {
		t.Errorf("pinned_sessions_limit = %v, want 5", out["pinned_sessions_limit"])
	}
	// default still there
	if out["skin"] != "default" {
		t.Errorf("skin = %v, want default", out["skin"])
	}
}

func TestSettingsShowCliSessionsGrandfather(t *testing.T) {
	home := t.TempDir()
	dataRoot := t.TempDir()
	// established install: onboarding_completed true, no show_cli_sessions
	writeHomeFile(t, dataRoot, "settings.json", `{"onboarding_completed":true,"theme":"dark"}`)

	r := chi.NewRouter()
	ConfigRouter(r, home, dataRoot)
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["show_cli_sessions"] != false {
		t.Errorf("show_cli_sessions = %v, want false (grandfathered)", out["show_cli_sessions"])
	}
}

func TestSettingsShowCliSessionsFresh(t *testing.T) {
	home := t.TempDir()
	dataRoot := t.TempDir()
	// genuinely new install: only onboarding_completed false
	writeHomeFile(t, dataRoot, "settings.json", `{"onboarding_completed":false}`)

	r := chi.NewRouter()
	ConfigRouter(r, home, dataRoot)
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["show_cli_sessions"] != true {
		t.Errorf("show_cli_sessions = %v, want true (fresh install)", out["show_cli_sessions"])
	}
}

func TestSettingsVirtualizeTranscriptForceOff(t *testing.T) {
	home := t.TempDir()
	dataRoot := t.TempDir()
	// stale pre-flip value without optin marker → forced off
	writeHomeFile(t, dataRoot, "settings.json", `{"virtualize_transcript":true,"onboarding_completed":true}`)

	r := chi.NewRouter()
	ConfigRouter(r, home, dataRoot)
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["virtualize_transcript"] != false {
		t.Errorf("virtualize_transcript = %v, want false (force-off)", out["virtualize_transcript"])
	}
}

func TestSettingsVirtualizeTranscriptOptinHonored(t *testing.T) {
	home := t.TempDir()
	dataRoot := t.TempDir()
	// explicit opt-in AFTER the flip → honored True
	writeHomeFile(t, dataRoot, "settings.json", `{"virtualize_transcript":true,"virtualize_transcript_optin":true,"onboarding_completed":true}`)

	r := chi.NewRouter()
	ConfigRouter(r, home, dataRoot)
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["virtualize_transcript"] != true {
		t.Errorf("virtualize_transcript = %v, want true (optin)", out["virtualize_transcript"])
	}
}

func TestSettingsSpeechKeysPersisted(t *testing.T) {
	home := t.TempDir()
	dataRoot := t.TempDir()
	writeHomeFile(t, dataRoot, "settings.json", `{"tts_engine":"elevenlabs","tts_voice":"amy","theme":"dark"}`)

	r := chi.NewRouter()
	ConfigRouter(r, home, dataRoot)
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ps, ok := out["persisted_speech_keys"].([]any)
	if !ok || len(ps) != 2 {
		t.Fatalf("persisted_speech_keys = %v, want [tts_engine tts_voice]", out["persisted_speech_keys"])
	}
	if ps[0] != "tts_engine" || ps[1] != "tts_voice" {
		t.Errorf("persisted_speech_keys = %v, want sorted [tts_engine tts_voice]", ps)
	}
}

// TestConfigRouterNativeNoProxyFallback ensures Family-2 routes are routed
// natively through the full server router (never proxy Teapot).
func TestConfigRouterNativeNoProxyFallback(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, "config.yaml", "model:\n  default: codex\n  provider: custom\n")

	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "webui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	proxy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, "proxy-fallback")
	})
	r := NewRouterWithData(".", proxy, db, t.TempDir(), WithHermesHome(home))
	ts := httptest.NewServer(r)
	defer ts.Close()

	for _, u := range []string{
		"/api/profile/active",
		"/api/profiles",
		"/api/settings",
	} {
		resp, err := http.Get(ts.URL + u)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || strings.Contains(string(body), "proxy-fallback") {
			t.Fatalf("%s = %d %q", u, resp.StatusCode, body)
		}
	}
}

func postSettings(t *testing.T, dataRoot, home, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	ConfigRouter(r, home, dataRoot)
	req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}

func TestSettingsSavePersists(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, "config.yaml", "model:\n  default: codex\n  provider: custom\n")
	dataRoot := t.TempDir()

	rr := postSettings(t, dataRoot, home, `{"theme":"light","send_key":"ctrl+enter","show_token_usage":true}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	// file written
	stored := readRawSettings(dataRoot)
	if stored["theme"] != "light" {
		t.Errorf("stored theme = %v, want light", stored["theme"])
	}
	if stored["send_key"] != "ctrl+enter" {
		t.Errorf("stored send_key = %v, want ctrl+enter", stored["send_key"])
	}
	v, _ := stored["show_token_usage"].(bool)
	if !v {
		t.Errorf("stored show_token_usage = %v, want true", stored["show_token_usage"])
	}
	// response has defaults + no password_hash
	if _, has := stored["password_hash"]; has {
		t.Errorf("password_hash persisted")
	}
}

func TestSettingsSaveInvalidEnumIgnored(t *testing.T) {
	home := t.TempDir()
	dataRoot := t.TempDir()
	// invalid send_key → NOT changed, stays default (stored default = enter)
	rr := postSettings(t, dataRoot, home, `{"send_key":"triple-tap","theme":"dark"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	stored := readRawSettings(dataRoot)
	if stored["send_key"] != "enter" {
		t.Errorf("stored send_key = %v, want default enter (invalid not applied)", stored["send_key"])
	}
}

func TestSettingsSaveIntRange(t *testing.T) {
	home := t.TempDir()
	dataRoot := t.TempDir()
	// 500 out of range 1..99 → NOT changed, stays default (3)
	rr := postSettings(t, dataRoot, home, `{"pinned_sessions_limit":500}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	stored := readRawSettings(dataRoot)
	if stored["pinned_sessions_limit"] != float64(3) {
		t.Errorf("pinned_sessions_limit = %v, want default 3 (invalid not applied)", stored["pinned_sessions_limit"])
	}
	// valid value persists
	rr = postSettings(t, dataRoot, home, `{"pinned_sessions_limit":4}`)
	stored = readRawSettings(dataRoot)
	if v, ok := toInt64(stored["pinned_sessions_limit"]); !ok || v != 4 {
		t.Errorf("pinned_sessions_limit = %v, want 4", stored["pinned_sessions_limit"])
	}
}

func TestSettingsSaveLegacyThemeMap(t *testing.T) {
	home := t.TempDir()
	dataRoot := t.TempDir()
	rr := postSettings(t, dataRoot, home, `{"theme":"slate"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	stored := readRawSettings(dataRoot)
	if stored["theme"] != "dark" {
		t.Errorf("theme = %v, want dark (legacy slate)", stored["theme"])
	}
	if stored["skin"] != "slate" {
		t.Errorf("skin = %v, want slate", stored["skin"])
	}
}

func TestSettingsSaveSpeechKeysPersist(t *testing.T) {
	home := t.TempDir()
	dataRoot := t.TempDir()
	rr := postSettings(t, dataRoot, home, `{"tts_engine":"elevenlabs","tts_voice":"amy"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	stored := readRawSettings(dataRoot)
	if stored["tts_engine"] != "elevenlabs" {
		t.Errorf("tts_engine not persisted")
	}
	if stored["tts_voice"] != "amy" {
		t.Errorf("tts_voice not persisted")
	}
	// non-speech unrelated save keeps speech keys
	rr = postSettings(t, dataRoot, home, `{"theme":"dark"}`)
	stored = readRawSettings(dataRoot)
	if stored["tts_engine"] == nil {
		t.Errorf("tts_engine dropped on unrelated save (persisted speech keys lost)")
	}
}

func TestSettingsSaveAuthFieldsRejected(t *testing.T) {
	home := t.TempDir()
	dataRoot := t.TempDir()
	rr := postSettings(t, dataRoot, home, `{"_set_password":"hunter2"}`)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 (auth field)", rr.Code)
	}
}

func TestSettingsSaveComposerOrderClean(t *testing.T) {
	home := t.TempDir()
	dataRoot := t.TempDir()
	rr := postSettings(t, dataRoot, home, `{"composer_control_order":["hide_composer_mic","hide_composer_mic","hide_composer_model","bogus_key"]}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	stored := readRawSettings(dataRoot)
	list, ok := stored["composer_control_order"].([]any)
	if !ok {
		t.Fatalf("composer_control_order = %v", stored["composer_control_order"])
	}
	if len(list) != 2 {
		t.Errorf("composer_control_order len = %d, want 2 (dedup + bogus dropped)", len(list))
	}
}

func auxRequest(t *testing.T, home, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	ConfigRouter(r, home, t.TempDir())
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}

func TestAuxiliaryGetDefaults(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, "config.yaml", "model:\n  default: codex\n  provider: custom\n")
	rr := auxRequest(t, home, http.MethodGet, "/api/model/auxiliary", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	tasks, ok := resp["tasks"].([]any)
	if !ok || len(tasks) != 11 {
		t.Fatalf("tasks = %v, want 11 catalog rows", resp["tasks"])
	}
	main, _ := resp["main"].(map[string]any)
	if main == nil || main["model"] != "codex" || main["provider"] != "custom" {
		t.Errorf("main = %v, want codex/custom", main)
	}
	// default first task vision/provider auto
	first, _ := tasks[0].(map[string]any)
	if first["task"] != "vision" || first["provider"] != "auto" {
		t.Errorf("first task = %v, want vision/auto", first)
	}
}

func TestAuxiliarySetPersists(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, "config.yaml", "model:\n  default: codex\n  provider: custom\n")
	rr := auxRequest(t, home, http.MethodPost, "/api/model/set", `{"scope":"auxiliary","task":"vision","provider":"openrouter","model":"x-ai/grok-4.5"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	if resp["ok"] != true || resp["task"] != "vision" || resp["provider"] != "openrouter" || resp["model"] != "x-ai/grok-4.5" {
		t.Errorf("resp = %v", resp)
	}
	// file updated
	cfg, err := readConfigYAML(home)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	aux, _ := cfg["auxiliary"].(map[string]any)
	vision, _ := aux["vision"].(map[string]any)
	if vision["provider"] != "openrouter" || vision["model"] != "x-ai/grok-4.5" {
		t.Errorf("vision = %v, want openrouter/x-ai/grok-4.5", vision)
	}
	// GET reflects it
	rr = auxRequest(t, home, http.MethodGet, "/api/model/auxiliary", "")
	var got map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	gtasks, _ := got["tasks"].([]any)
	gfirst, _ := gtasks[0].(map[string]any)
	if gfirst["provider"] != "openrouter" {
		t.Errorf("GET vision provider = %v, want openrouter", gfirst["provider"])
	}
}

func TestAuxiliarySetReset(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, "config.yaml", "model:\n  default: codex\n  provider: custom\nauxiliary:\n  vision:\n    provider: openrouter\n    model: x-ai/grok-4.5\n  session_search:\n    provider: foo\n    model: bar\n")
	rr := auxRequest(t, home, http.MethodPost, "/api/model/set", `{"scope":"auxiliary","task":"__reset__","provider":"auto","model":""}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	cfg, err := readConfigYAML(home)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	aux, _ := cfg["auxiliary"].(map[string]any)
	vision, _ := aux["vision"].(map[string]any)
	if vision["provider"] != "auto" || strval(vision["model"]) != "" {
		t.Errorf("vision after reset = %v, want auto/empty", vision)
	}
	if _, has := aux["session_search"]; has {
		t.Errorf("session_search should be removed (retired slot)")
	}
}

func TestAuxiliarySetUnknownSlot(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, "config.yaml", "model:\n  default: codex\n")
	rr := auxRequest(t, home, http.MethodPost, "/api/model/set", `{"scope":"auxiliary","task":"bogus","provider":"auto","model":""}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestAuxiliarySetMainScopeDeferred(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, "config.yaml", "model:\n  default: codex\n")
	rr := auxRequest(t, home, http.MethodPost, "/api/model/set", `{"scope":"main","model":"gpt-5","provider":"openai"}`)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rr.Code)
	}
}

func TestAuxiliarySetCustomBaseURL(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, "config.yaml", "model:\n  default: codex\n  provider: custom\ncustom_providers:\n  - slug: myproxy\n    base_url: https://proxy.example/v1\n")
	rr := auxRequest(t, home, http.MethodPost, "/api/model/set", `{"scope":"auxiliary","task":"compression","provider":"custom:myproxy","model":"anthropic/claude-sonnet-4"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	cfg, err := readConfigYAML(home)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	aux, _ := cfg["auxiliary"].(map[string]any)
	comp, _ := aux["compression"].(map[string]any)
	if comp["base_url"] != "https://proxy.example/v1" {
		t.Errorf("compression base_url = %v, want https://proxy.example/v1", comp["base_url"])
	}
}

func TestAuxiliarySetAdvanced(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, "config.yaml", "model:\n  default: codex\n")
	rr := auxRequest(t, home, http.MethodPost, "/api/model/set", `{"scope":"auxiliary","task":"mcp","provider":"openrouter","model":"x-ai/grok-4.5","advanced":{"timeout":60,"extra_body":{"max_tokens":2000},"service_tier":"priority"}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	cfg, err := readConfigYAML(home)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	aux, _ := cfg["auxiliary"].(map[string]any)
	mcp, _ := aux["mcp"].(map[string]any)
	if strval(mcp["timeout"]) != "60" {
		t.Errorf("timeout = %v, want 60", mcp["timeout"])
	}
	eb, _ := mcp["extra_body"].(map[string]any)
	if mt, ok := eb["max_tokens"].(int); !ok || mt != 2000 {
		t.Errorf("extra_body = %v, want max_tokens 2000", mcp["extra_body"])
	}
	if mcp["service_tier"] != "priority" {
		t.Errorf("service_tier = %v, want priority", mcp["service_tier"])
	}
}

func TestAuxiliarySetInvalidAdvanced(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, "config.yaml", "model:\n  default: codex\n")
	rr := auxRequest(t, home, http.MethodPost, "/api/model/set", `{"scope":"auxiliary","task":"mcp","provider":"auto","model":"","advanced":{"extra_body":"not json"}}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (invalid extra_body)", rr.Code)
	}
}
