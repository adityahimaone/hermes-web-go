package httpserver

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
)

// ── GET /api/health/agent ──────────────────────────────────────────────────
//
// Mirrors Python build_agent_health_payload (api/agent_health.py) + the
// /api/health/agent route in api/routes.py: tri-state `alive` derived from
// gateway.pid / gateway_state.json under HERMES_HOME, plus a redacted
// `gateway_chat` backend config status. The remote-gateway HTTP probe
// (multi-container HERMES_API_URL deployments) is not ported: the Go server
// always runs on the same host as the gateway state files.
//
// `alive` is intentionally tri-state:
//   - true:  a live PID owns the gateway, or gateway_state.json self-reports
//     "running" with an updated_at within the freshness threshold.
//   - false: gateway metadata exists but no live process / stale running.
//   - null:  no usable signal (not configured, stale clean stop).

// gatewayFreshnessThreshold mirrors GATEWAY_FRESHNESS_THRESHOLD_S (two cron
// ticks): a self-reported "running" older than this is inconclusive, not down.
const gatewayFreshnessThreshold = 120 * time.Second

// gatewayRuntimeState is the subset of gateway_state.json the payload needs.
type gatewayRuntimeState struct {
	PID          int                       `json:"pid"`
	Kind         string                    `json:"kind"`
	GatewayState string                    `json:"gateway_state"`
	UpdatedAt    string                    `json:"updated_at"`
	ActiveAgents int                       `json:"active_agents"`
	Platforms    map[string]map[string]any `json:"platforms"`
}

// processAlive reports whether pid exists (kill(pid, 0)); EPERM still means
// the process is alive but owned by another user.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// parseISO8601 parses the timestamps gateway writes
// (datetime.now(timezone.utc).isoformat()). Naive timestamps are refused,
// matching Python (a naive ts "could mean anything across containers").
func parseISO8601(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		if t.Location() != time.UTC && t.Location() == nil {
			return time.Time{}, false
		}
		return t, true
	}
	return time.Time{}, false
}

// isFreshGateway mirrors _runtime_status_is_fresh: self-reported running +
// updated_at no older than the threshold. Future timestamps within the
// threshold are accepted (clock skew); wildly-future ones are not.
func isFreshGateway(st *gatewayRuntimeState, now time.Time) bool {
	if st == nil || st.GatewayState != "running" {
		return false
	}
	t, ok := parseISO8601(st.UpdatedAt)
	if !ok {
		return false
	}
	age := now.Sub(t)
	if age < 0 {
		return -age <= gatewayFreshnessThreshold
	}
	return age <= gatewayFreshnessThreshold
}

// isStaleGateway reports state==expected with an updated_at older than the
// threshold (mirrors _runtime_status_is_stale_stopped/_stale_running).
func isStaleGateway(st *gatewayRuntimeState, expected string, now time.Time) bool {
	if st == nil || st.GatewayState != expected {
		return false
	}
	t, ok := parseISO8601(st.UpdatedAt)
	if !ok {
		return false
	}
	return now.Sub(t) > gatewayFreshnessThreshold
}

// agentHealthHandler builds the /api/health/agent payload.
func agentHealthHandler(hermesHome string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		root := hermesHome
		if root == "" {
			root = defaultHermesHome()
		}
		runtimePath := filepath.Join(root, "gateway_state.json")
		pidPath := filepath.Join(root, "gateway.pid")

		var alive any // tri-state: nil (unknown), true, false
		details := map[string]any{"state": "unknown", "reason": "gateway_not_configured"}

		var st *gatewayRuntimeState
		if raw, err := os.ReadFile(runtimePath); err == nil {
			var parsed gatewayRuntimeState
			if json.Unmarshal(raw, &parsed) == nil {
				st = &parsed
			}
		}

		if st != nil {
			runningPID := st.PID
			if runningPID <= 0 {
				// Some gateway builds write PID only into gateway.pid.
				if raw, err := os.ReadFile(pidPath); err == nil {
					var pidDoc struct {
						PID int `json:"pid"`
					}
					if json.Unmarshal(raw, &pidDoc) == nil {
						runningPID = pidDoc.PID
					}
				}
			}

			// Non-sensitive detail subset (mirrors _runtime_detail_subset).
			details = map[string]any{}
			if st.GatewayState != "" {
				details["gateway_state"] = st.GatewayState
			}
			if st.UpdatedAt != "" {
				details["updated_at"] = st.UpdatedAt
			}
			if st.ActiveAgents < 0 {
				st.ActiveAgents = 0
			}
			details["active_agents"] = st.ActiveAgents
			if st.Platforms != nil {
				details["platform_count"] = len(st.Platforms)
				states := map[string]int{}
				for _, p := range st.Platforms {
					if s, ok := p["state"].(string); ok && s != "" {
						states[s]++
					}
				}
				if len(states) > 0 {
					details["platform_states"] = states
				}
			}

			switch {
			case processAlive(runningPID):
				alive = true
				details["state"] = "alive"
			case isFreshGateway(st, now):
				alive = true
				details["state"] = "alive"
				details["reason"] = "cross_container_freshness"
			case st.GatewayState == "stopped" && isStaleGateway(st, "stopped", now):
				alive = nil
				details["state"] = "unknown"
				details["reason"] = "gateway_stale_stopped_state"
			case st.GatewayState == "running":
				// Stale running: not enough information, not an outage.
				alive = nil
				details["state"] = "unknown"
				details["reason"] = "gateway_stale_running_state"
			default:
				alive = false
				details["state"] = "down"
				details["reason"] = "gateway_not_running"
			}
		}

		writeJSON(w, map[string]any{
			"alive":        alive,
			"checked_at":   now.UTC().Format(time.RFC3339),
			"details":      details,
			"gateway_chat": gatewayChatConfigStatus(),
		})
	}
}

// gatewayChatConfigStatus mirrors gateway_chat.gateway_chat_config_status:
// redacted chat-backend mode + whether a gateway base URL / API key is set.
// Defaults mirror Python: base URL falls back to http://127.0.0.1:8642 (so
// base_url_configured reflects an EXPLICIT override, not the default).
func gatewayChatConfigStatus() map[string]any {
	mode := "legacy"
	switch strings.ToLower(strings.TrimSpace(os.Getenv("HERMES_WEBUI_CHAT_BACKEND"))) {
	case "gateway", "api_server", "api-server":
		mode = "gateway"
	}
	baseURL := strings.TrimSpace(os.Getenv("HERMES_WEBUI_GATEWAY_BASE_URL"))
	apiKey := strings.TrimSpace(os.Getenv("HERMES_WEBUI_GATEWAY_API_KEY"))
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("API_SERVER_KEY"))
	}
	return map[string]any{
		"enabled":             mode == "gateway",
		"backend":             mode,
		"base_url_configured": baseURL != "",
		"api_key_configured":  apiKey != "",
	}
}

// ── POST /api/client-events/log ────────────────────────────────────────────

var (
	clMu             sync.Mutex
	clientEventRates = map[string]clientEventRate{}
)

type clientEventRate struct {
	count int
	reset time.Time
}

// ── Router ─────────────────────────────────────────────────────────────────

// miscRouter mounts small self-contained HTTP routes: the agent/gateway
// heartbeat, client telemetry logging, and the manual compression-job status.
func miscRouter(r chi.Router, hermesHome string) {
	r.Get("/api/health/agent", agentHealthHandler(hermesHome))

	// POST /api/client-events/log — client telemetry. Mirrors Python
	// _handle_client_event_log: rate-limited per IP, sanitized to the event
	// name, logged server-side, {ok, event} returned.
	r.Post("/api/client-events/log", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Event string `json:"event"`
			Type  string `json:"type"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		clientIP := r.RemoteAddr
		if idx := strings.LastIndex(clientIP, ":"); idx >= 0 {
			clientIP = clientIP[:idx]
		}
		clMu.Lock()
		now := time.Now()
		entry, ok := clientEventRates[clientIP]
		if !ok || now.After(entry.reset) {
			entry = clientEventRate{count: 0, reset: now.Add(time.Minute)}
		}
		if entry.count >= 10 {
			clMu.Unlock()
			writeError(w, http.StatusTooManyRequests, "rate_limited")
			return
		}
		entry.count++
		clientEventRates[clientIP] = entry
		clMu.Unlock()

		name := body.Event
		if name == "" {
			name = body.Type
		}
		if name != "" {
			log.Printf("[client-event] %s", name)
		}
		writeJSON(w, map[string]any{"ok": true, "event": name})
	})

	// GET /api/session/compress/status — manual compression job status.
	// Python tracks jobs in process memory; Go has no in-flight compression
	// jobs, so every request reports idle (ponytail: wire a shared job table
	// when manual compression lands natively).
	r.Get("/api/session/compress/status", func(w http.ResponseWriter, r *http.Request) {
		sid := strings.TrimSpace(r.URL.Query().Get("session_id"))
		if sid == "" {
			writeError(w, http.StatusBadRequest, "session_id is required")
			return
		}
		writeJSON(w, map[string]any{"ok": true, "status": "idle", "session_id": sid})
	})
}
