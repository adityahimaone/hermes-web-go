package httpserver

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"hermes-web-go/internal/store"
)

// ── Wave 1 misc reads ─────────────────────────────────────────────────────
//
// Small parity endpoints ported from api/routes.py / api/upload.py:
//   - GET /api/transcribe/capability  (api/upload.py handle_transcribe_capability)
//   - GET /api/wiki/status            (routes.py _build_llm_wiki_status)
//   - GET /api/insights               (routes.py _handle_insights)
//   - GET /api/updates/check          (routes.py _handle_updates_check)
//   - GET /api/onboarding/status      (api/onboarding.py get_onboarding_status)

// transcribeCapabilityRouter mirrors the STT detection contract without
// probing binaries: no native transcription provider ships with the Go
// server, so `available` is always false. (ponytail: probe whisper/STT
// configs when transcription lands natively.)
func transcribeCapabilityRouter(r chi.Router) {
	r.Get("/api/transcribe/capability", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "available": false, "provider": "none"})
	})
}

// llmWikiPath resolves the LLM wiki location the same way Python
// _llm_wiki_resolve_path does: WIKI_PATH env, then config.yaml wiki.path,
// then ~/.hermes/wiki. Returns (path, source, configured).
func llmWikiPath(hermesHome string) (string, string, bool) {
	if p := strings.TrimSpace(os.Getenv("WIKI_PATH")); p != "" {
		return p, "env", true
	}
	if cfg, err := os.ReadFile(filepath.Join(hermesHome, "config.yaml")); err == nil {
		for _, line := range strings.Split(string(cfg), "\n") {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, "path:") && strings.Contains(line, "wiki") {
				if v := strings.TrimSpace(strings.TrimPrefix(t, "path:")); v != "" {
					return strings.Trim(v, `"'`), "config", true
				}
			}
		}
	}
	p := filepath.Join(hermesHome, "wiki")
	return p, "default", false
}

// wikiStatusRouter serves GET /api/wiki/status with the private-safe metadata
// subset of _build_llm_wiki_status (counts + timestamps, never page bodies).
func wikiStatusRouter(r chi.Router, hermesHome string) {
	r.Get("/api/wiki/status", func(w http.ResponseWriter, r *http.Request) {
		wikiPath, pathSource, pathConfigured := llmWikiPath(hermesHome)
		base := map[string]any{
			"available":        false,
			"enabled":          false,
			"status":           "missing",
			"entry_count":      0,
			"page_count":       0,
			"raw_source_count": 0,
			"last_updated":     nil,
			"last_writer":      "ai-agent",
			"path_configured":  pathConfigured,
			"path_source":      pathSource,
			"docs_url":         "https://hermes-agent.nousresearch.com/docs/wiki",
		}
		st, err := os.Stat(wikiPath)
		if err != nil {
			writeJSON(w, base)
			return
		}
		if !st.IsDir() {
			base["status"] = "not_directory"
			writeJSON(w, base)
			return
		}
		pageCount := 0
		var latest int64
		entries, _ := os.ReadDir(wikiPath)
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			pageCount++
			if m := info.ModTime().Unix(); m > latest {
				latest = m
			}
		}
		rawCount := 0
		rawEntries, _ := os.ReadDir(filepath.Join(wikiPath, "raw"))
		for _, e := range rawEntries {
			if !e.IsDir() {
				rawCount++
			}
		}
		status := "empty"
		if pageCount > 0 {
			status = "ready"
		}
		base["available"] = true
		base["enabled"] = true
		base["status"] = status
		base["entry_count"] = pageCount
		base["page_count"] = pageCount
		base["raw_source_count"] = rawCount
		if latest > 0 {
			base["last_updated"] = latest
		}
		writeJSON(w, base)
	})
}

// insightsRouter serves GET /api/insights: usage analytics aggregated from
// the SQLite session store (the Go-native equivalent of Python walking
// sessions/_index.json). Same top-level shape: period_days, totals, models,
// daily_tokens, activity_by_day/hour.
func insightsRouter(r chi.Router, db *sql.DB) {
	r.Get("/api/insights", func(w http.ResponseWriter, r *http.Request) {
		days := 30
		if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && d >= 1 && d <= 365 {
			days = d
		}
		if db == nil {
			writeError(w, http.StatusServiceUnavailable, "data store unavailable")
			return
		}
		rows, err := store.ListSessions(db, 100000, 0)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list sessions")
			return
		}
		type modelAgg struct {
			sessions  int
			inTokens  int64
			outTokens int64
			cacheRead int64
			cost      float64
		}
		totalIn, totalOut, totalCache, totalMsg := int64(0), int64(0), int64(0), int64(0)
		totalCost := 0.0
		totalSessions := 0
		models := map[string]*modelAgg{}
		daily := map[string]*modelAgg{}
		dow := map[string]int{}
		hod := map[string]int{}
		cutoff := time.Now().AddDate(0, 0, -(days - 1)).Unix()
		for _, s := range rows {
			if s.UpdatedAt < float64(cutoff) {
				continue
			}
			totalSessions++
			model := s.Model
			if model == "" {
				model = "unknown"
			}
			m := models[model]
			if m == nil {
				m = &modelAgg{}
				models[model] = m
			}
			m.sessions++
			// Usage lives in each message's _usage block; walk the JSON once.
			var msgs []struct {
				Usage *struct {
					InputTokens  int64   `json:"input_tokens"`
					OutputTokens int64   `json:"output_tokens"`
					CacheRead    int64   `json:"cache_read_tokens"`
					Cost         float64 `json:"estimated_cost"`
				} `json:"_usage"`
			}
			if s.Messages != "" && json.Unmarshal([]byte(s.Messages), &msgs) == nil {
				for _, msg := range msgs {
					if msg.Usage == nil {
						continue
					}
					m.inTokens += msg.Usage.InputTokens
					m.outTokens += msg.Usage.OutputTokens
					m.cacheRead += msg.Usage.CacheRead
					m.cost += msg.Usage.Cost
				}
			}
			totalIn += m.inTokens
			totalOut += m.outTokens
			totalCache += m.cacheRead
			totalCost += m.cost
			totalMsg += 1
			dayKey := time.Unix(int64(s.UpdatedAt), 0).Format("2006-01-02")
			d := daily[dayKey]
			if d == nil {
				d = &modelAgg{}
				daily[dayKey] = d
			}
			d.inTokens += m.inTokens
			d.outTokens += m.outTokens
			d.cost += m.cost
			dow[time.Unix(int64(s.UpdatedAt), 0).Format("Monday")]++
			hod[strconv.Itoa(time.Unix(int64(s.UpdatedAt), 0).Hour())]++
		}
		modelsOut := map[string]any{}
		for name, m := range models {
			modelsOut[name] = map[string]any{
				"sessions":          m.sessions,
				"input_tokens":      m.inTokens,
				"output_tokens":     m.outTokens,
				"cache_read_tokens": m.cacheRead,
				"cost":              m.cost,
				"total_tokens":      m.inTokens + m.outTokens,
			}
		}
		dailyOut := map[string]any{}
		for day, d := range daily {
			dailyOut[day] = map[string]any{
				"input_tokens":  d.inTokens,
				"output_tokens": d.outTokens,
				"total_tokens":  d.inTokens + d.outTokens,
				"cost":          d.cost,
			}
		}
		hitPct := 0.0
		if denom := totalIn + totalCache; denom > 0 {
			hitPct = float64(totalCache) / float64(denom) * 100
		}
		writeJSON(w, map[string]any{
			"period_days":             days,
			"total_sessions":          totalSessions,
			"total_messages":          totalMsg,
			"total_input_tokens":      totalIn,
			"total_output_tokens":     totalOut,
			"total_cache_read_tokens": totalCache,
			"total_cache_hit_percent": hitPct,
			"total_tokens":            totalIn + totalOut,
			"total_cost":              totalCost,
			"models":                  modelsOut,
			"daily_tokens":            dailyOut,
			"activity_by_day":         dow,
			"activity_by_hour":        hod,
		})
	})
}

// updatesCheckRouter serves GET /api/updates/check: settings flag only. The
// native Go server never self-updates, so `disabled` mirrors the Python
// behavior when check_for_updates is off (ponytail: port api/updates.py
// git-diff cache when update management moves native).
func updatesCheckRouter(r chi.Router, dataRoot, hermesHome string) {
	r.Get("/api/updates/check", func(w http.ResponseWriter, r *http.Request) {
		settings := loadWebUISettings(dataRoot, hermesHome)
		enabled := false
		if v, ok := settings["check_for_updates"].(bool); ok {
			enabled = v
		}
		if !enabled {
			writeJSON(w, map[string]any{"disabled": true})
			return
		}
		writeJSON(w, map[string]any{"disabled": true, "up_to_date": true})
	})
}

// onboardingStatusRouter serves GET /api/onboarding/status with the same
// top-level keys as Python get_onboarding_status. Setup catalog is the
// abbreviated form (id/label/done) — the FE wizard only reads those.
func onboardingStatusRouter(r chi.Router, dataRoot, hermesHome string) {
	r.Get("/api/onboarding/status", func(w http.ResponseWriter, r *http.Request) {
		settings := loadWebUISettings(dataRoot, hermesHome)
		str := func(k string) string {
			v, _ := settings[k].(string)
			return v
		}
		completed := false
		if v, ok := settings["onboarding_completed"].(bool); ok {
			completed = v
		}
		if os.Getenv("HERMES_WEBUI_SKIP_ONBOARDING") == "1" {
			completed = true
		}
		configExists := false
		if _, err := os.Stat(filepath.Join(hermesHome, "config.yaml")); err == nil {
			configExists = true
		}
		system := map[string]any{
			"hermes_found":  configExists,
			"imports_ok":    true,
			"config_path":   filepath.Join(hermesHome, "config.yaml"),
			"config_exists": configExists,
			"chat_ready":    configExists,
		}
		workspaces := map[string]any{"items": []any{}, "last": nil}
		if ws := str("last_workspace"); ws != "" {
			workspaces["last"] = ws
		}
		model := str("default_model")
		writeJSON(w, map[string]any{
			"completed": completed,
			"settings": map[string]any{
				"default_model":     model,
				"default_workspace": str("default_workspace"),
				"password_enabled":  settings["password_enabled"],
				"bot_name":          settings["bot_name"],
			},
			"system":     system,
			"workspaces": workspaces,
			"models":     availableModelsCatalog(hermesHome, false),
		})
	})
}

// gitInfoRouterMount mounts the /api/git-info handler built in git_info.go
// with the server's DB handle.
func gitInfoRouterMount(r chi.Router, db *sql.DB) {
	gitInfoRouter(r, db)
}

// miscReadsRouter mounts all Wave-1 native read handlers (transcribe, wiki,
// insights, updates, onboarding, git-info) in one call for both router
// constructors.
func miscReadsRouter(r chi.Router, db *sql.DB, dataRoot, hermesHome string) {
	transcribeCapabilityRouter(r)
	wikiStatusRouter(r, hermesHome)
	insightsRouter(r, db)
	updatesCheckRouter(r, dataRoot, hermesHome)
	onboardingStatusRouter(r, dataRoot, hermesHome)
	gitInfoRouterMount(r, db)
}
