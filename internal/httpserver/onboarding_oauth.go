package httpserver

// Wave 16 — onboarding/oauth flows + onboarding/probe + local-network gate.
//
// Ports from api/oauth.py + api/onboarding.py (Python WebUI):
//   - GET  /api/onboarding/oauth/poll   — flow status (expiry, sanitized)
//   - POST /api/onboarding/oauth/start  — codex device-code or anthropic link
//   - POST /api/onboarding/oauth/cancel — cancel pending flow
//   - POST /api/onboarding/probe        — GET {base_url}/models catalog
//
// Codex: device-code request to auth.openai.com + background token-exchange
// worker writing credentials into {hermes_home}/auth.json credential_pool.
// Anthropic: detect Claude Code credentials (~/.claude/.credentials.json or
// macOS Keychain via `security`) and link by clearing ANTHROPIC_* from .env +
// writing a credential_pool marker. Flows are in-memory with 15-min expiry
// and 300s terminal-state retention (parity).

import (
	"bytes"

	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	hermesauth "hermes-web-go/internal/auth"
)

// ── Local-network gate (routes.py _onboarding_gate_allows) ─────────────────

// onboardingGateAllowed mirrors _onboarding_request_is_local: raw socket peer
// must be loopback or private. Forwarded-header trust logic (trusted-proxy
// CIDR allowlist) is not ported — Go server sits directly exposed or behind
// an operator-configured proxy that sets auth (ponytail: add
// HERMES_WEBUI_TRUSTED_PROXY_CIDRS when a passwordless proxy deployment needs it).
func onboardingGateAllowed(auth *hermesauth.Auth, r *http.Request) bool {
	if auth != nil && auth.Enabled() {
		return true
	}
	if truthy(os.Getenv("HERMES_WEBUI_ONBOARDING_OPEN")) {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate())
}

// ── Flow store (oauth.py _OAUTH_FLOWS) ─────────────────────────────────────

const (
	oauthFlowMaxWait       = 15 * time.Minute
	anthropicPollSeconds   = 5
	oauthTerminalRetention = 300 * time.Second
)

var (
	oauthFlowsMu sync.Mutex
	oauthFlows   = map[string]*oauthFlow{}
	oauthStartMu sync.Mutex // single-flight for start
)

type oauthFlow struct {
	provider          string
	status            string // pending|success|expired|cancelled|error
	userCode          string
	deviceAuthID      string
	authorizationCode string
	codeVerifier      string
	expiresAt         time.Time
	pollInterval      int
	hermesHome        string
	createdAt         time.Time
	updatedAt         time.Time
	err               string
	workerDone        bool
}

func oauthCleanupLocked(now time.Time) {
	for fid, f := range oauthFlows {
		if f.status == "pending" && now.After(f.expiresAt) {
			f.status = "expired"
			f.updatedAt = now
		}
		if f.status != "pending" && now.Sub(f.updatedAt) > oauthTerminalRetention {
			delete(oauthFlows, fid)
		}
	}
}

func dropSensitive(f *oauthFlow) {
	f.deviceAuthID = ""
	f.authorizationCode = ""
	f.codeVerifier = ""
}

// ── Codex device flow (oauth.py constants) ─────────────────────────────────

const (
	codexIssuer          = "https://auth.openai.com"
	codexClientID        = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexUserCodeURL     = codexIssuer + "/api/accounts/deviceauth/usercode"
	codexDeviceTokenURL  = codexIssuer + "/api/accounts/deviceauth/token"
	codexTokenURL        = codexIssuer + "/oauth/token"
	codexRedirectURI     = codexIssuer + "/deviceauth/callback"
	codexVerificationURI = codexIssuer + "/codex/device"
)

func codexJSONPost(url string, payload map[string]any) (map[string]any, error) {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "hermes-webui")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("invalid response from %s", url)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return out, nil
}

func generatePKCE() (verifier, challenge string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func isDarwin() bool { return runtime.GOOS == "darwin" }

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// hermesHomeActive resolves the active profile home (HERMES_HOME or default).
func hermesHomeActive() string {
	if v := strings.TrimSpace(os.Getenv("HERMES_HOME")); v != "" {
		return v
	}
	return defaultHermesHome()
}

// ── Anthropic credential detection ─────────────────────────────────────────

// claudeCodeFileCreds reads ~/.claude/.credentials.json (file source).
func claudeCodeFileCreds() (accessToken, refreshToken string, expiresAtMs int64, ok bool) {
	path := filepath.Join(homeDir(), ".claude", ".credentials.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", 0, false
	}
	var parsed struct {
		ClaudeAiOauth struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
			ExpiresAt    int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", "", 0, false
	}
	if parsed.ClaudeAiOauth.AccessToken == "" {
		return "", "", 0, false
	}
	return parsed.ClaudeAiOauth.AccessToken, parsed.ClaudeAiOauth.RefreshToken, parsed.ClaudeAiOauth.ExpiresAt, true
}

// claudeCodeKeychainCreds reads the macOS Keychain entry via `security`.
func claudeCodeKeychainCreds() (accessToken, refreshToken string, expiresAtMs int64, ok bool) {
	if !isDarwin() {
		return "", "", 0, false
	}
	cmd := exec.Command("security", "find-generic-password", "-s", "Claude Code-credentials", "-w")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil
	cmd.Stdin = nil
	if err := cmd.Run(); err != nil {
		return "", "", 0, false
	}
	var parsed struct {
		ClaudeAiOauth struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
			ExpiresAt    int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &parsed); err != nil {
		return "", "", 0, false
	}
	if parsed.ClaudeAiOauth.AccessToken == "" {
		return "", "", 0, false
	}
	return parsed.ClaudeAiOauth.AccessToken, parsed.ClaudeAiOauth.RefreshToken, parsed.ClaudeAiOauth.ExpiresAt, true
}

func claudeTokenValid(expiresAtMs int64, accessToken string) bool {
	if expiresAtMs == 0 {
		return accessToken != ""
	}
	return time.Now().UnixMilli() < expiresAtMs-60_000
}

// readClaudeCodeCreds mirrors read_claude_code_credentials: keychain+file,
// prefer the valid one, else the later expiresAt.
func readClaudeCodeCreds() (accessToken, refreshToken string, expiresAtMs int64, ok bool) {
	kcA, kcR, kcE, kcOK := claudeCodeKeychainCreds()
	fA, fR, fE, fOK := claudeCodeFileCreds()
	switch {
	case kcOK && fOK:
		kcValid := claudeTokenValid(kcE, kcA)
		fValid := claudeTokenValid(fE, fA)
		if kcValid && !fValid {
			return kcA, kcR, kcE, true
		}
		if fValid && !kcValid {
			return fA, fR, fE, true
		}
		if kcE >= fE {
			return kcA, kcR, kcE, true
		}
		return fA, fR, fE, true
	case kcOK:
		return kcA, kcR, kcE, true
	case fOK:
		return fA, fR, fE, true
	}
	return "", "", 0, false
}

// linkAnthropicCredentials clears ANTHROPIC_* env keys and writes the
// credential_pool marker (no secrets) into auth.json.
func linkAnthropicCredentials(hermesHome string) error {
	for _, key := range []string{"ANTHROPIC_TOKEN", "ANTHROPIC_API_KEY"} {
		if err := writeEnvFileKey(hermesHome, key, ""); err != nil {
			return err
		}
		os.Unsetenv(key)
	}
	authPath := filepath.Join(hermesHome, "auth.json")
	raw, _ := os.ReadFile(authPath)
	doc := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &doc); err != nil {
			doc = map[string]any{}
		}
	}
	if doc["version"] == nil {
		doc["version"] = 1
	}
	pool, _ := doc["credential_pool"].(map[string]any)
	if pool == nil {
		pool = map[string]any{}
		doc["credential_pool"] = pool
	}
	entries, _ := pool["anthropic"].([]any)
	var entry map[string]any
	for _, e := range entries {
		if m, ok := e.(map[string]any); ok && m["source"] == "claude_code_linked" {
			entry = m
			break
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if entry == nil {
		entry = map[string]any{
			"id":         "anthropic-claude-code-" + randomHex(6),
			"auth_type":  "oauth",
			"priority":   0,
			"source":     "claude_code_linked",
			"created_at": now,
		}
		entries = append([]any{entry}, entries...)
	}
	entry["label"] = "Claude Code (linked)"
	entry["auth_type"] = "oauth"
	entry["priority"] = 0
	entry["source"] = "claude_code_linked"
	entry["updated_at"] = now
	pool["anthropic"] = entries
	doc["updated_at"] = now
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(authPath, out)
}

// ── Handlers ───────────────────────────────────────────────────────────────

func onboardingOAuthPoll(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	fid := strings.TrimSpace(r.URL.Query().Get("flow_id"))
	if fid == "" {
		writeError(w, http.StatusBadRequest, "flow_id is required")
		return
	}
	oauthFlowsMu.Lock()
	defer oauthFlowsMu.Unlock()
	oauthCleanupLocked(time.Now())
	flow := oauthFlows[fid]
	if flow == nil {
		writeError(w, http.StatusNotFound, "OAuth flow not found")
		return
	}
	writeJSON(w, oauthPublicStatus(fid, flow))
}

func oauthPublicStatus(fid string, f *oauthFlow) map[string]any {
	payload := map[string]any{
		"ok":       true,
		"provider": f.provider,
		"flow_id":  fid,
		"status":   f.status,
	}
	if f.status == "error" && f.err != "" {
		if len(f.err) > 200 {
			payload["error"] = f.err[:200]
		} else {
			payload["error"] = f.err
		}
	}
	if f.provider == "anthropic" && f.status == "error" {
		payload["error"] = "Claude Code credential linking failed. Check server logs."
	}
	return payload
}

func onboardingOAuthStart(auth *hermesauth.Auth, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !onboardingGateAllowed(auth, r) {
		writeError(w, http.StatusForbidden,
			"Onboarding OAuth is only available from local networks when auth is not enabled. To bypass this on a remote server, set HERMES_WEBUI_ONBOARDING_OPEN=1.")
		return
	}
	var body struct {
		Provider string `json:"provider"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	provider := strings.ToLower(strings.TrimSpace(body.Provider))
	switch provider {
	case "anthropic", "claude", "claude-code":
		provider = "anthropic"
	case "openai-codex", "":
		if provider == "" {
			writeError(w, http.StatusBadRequest, "provider is required")
			return
		}
		provider = "openai-codex"
	default:
		writeError(w, http.StatusBadRequest,
			"Only OpenAI Codex and Anthropic/Claude OAuth are supported in WebUI onboarding right now")
		return
	}
	oauthStartMu.Lock()
	defer oauthStartMu.Unlock()

	oauthFlowsMu.Lock()
	oauthCleanupLocked(time.Now())
	// Reuse an existing pending flow for the same provider+home.
	for fid, f := range oauthFlows {
		if f.status == "pending" && f.provider == provider {
			payload := oauthPublicStart(fid, f)
			oauthFlowsMu.Unlock()
			writeJSON(w, payload)
			return
		}
	}
	oauthFlowsMu.Unlock()

	if provider == "anthropic" {
		a, rt, exp, ok := readClaudeCodeCreds()
		now := time.Now()
		if ok && (claudeTokenValid(exp, a) || rt != "") {
			// Link immediately.
			fid := randomHex(16)
			flow := &oauthFlow{provider: "anthropic", status: "success", hermesHome: hermesHomeActive(), createdAt: now, updatedAt: now}
			if err := linkAnthropicCredentials(hermesHomeActive()); err != nil {
				flow.status = "error"
				flow.err = err.Error()
			}
			oauthFlowsMu.Lock()
			oauthFlows[fid] = flow
			oauthFlowsMu.Unlock()
			writeJSON(w, oauthPublicStart(fid, flow))
			return
		}
		// Pending flow + background worker watching for credentials.
		fid := randomHex(16)
		flow := &oauthFlow{
			provider: "anthropic", status: "pending",
			expiresAt: now.Add(oauthFlowMaxWait), pollInterval: anthropicPollSeconds,
			hermesHome: hermesHomeActive(), createdAt: now, updatedAt: now,
		}
		oauthFlowsMu.Lock()
		oauthFlows[fid] = flow
		oauthFlowsMu.Unlock()
		go oauthAnthropicWorker(fid)
		writeJSON(w, oauthPublicStart(fid, flow))
		return
	}

	// Codex device-code start.
	verifier, challenge := generatePKCE()
	device, err := codexJSONPost(codexUserCodeURL, map[string]any{
		"client_id": codexClientID, "code_challenge": challenge,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to start Codex OAuth: "+err.Error())
		return
	}
	userCode, _ := device["user_code"].(string)
	deviceAuthID, _ := device["device_auth_id"].(string)
	if userCode == "" || deviceAuthID == "" {
		writeError(w, http.StatusInternalServerError, "Failed to start Codex OAuth: Device code response missing required fields")
		return
	}
	interval := 5
	if v, ok := device["interval"].(float64); ok && int(v) > 3 {
		interval = int(v)
	}
	now := time.Now()
	fid := randomHex(16)
	flow := &oauthFlow{
		provider: "openai-codex", status: "pending",
		userCode: userCode, deviceAuthID: deviceAuthID, codeVerifier: verifier,
		expiresAt: now.Add(oauthFlowMaxWait), pollInterval: interval,
		hermesHome: hermesHomeActive(), createdAt: now, updatedAt: now,
	}
	oauthFlowsMu.Lock()
	oauthFlows[fid] = flow
	oauthFlowsMu.Unlock()
	go oauthCodexWorker(fid)
	writeJSON(w, oauthPublicStart(fid, flow))
}

func oauthPublicStart(fid string, f *oauthFlow) map[string]any {
	payload := oauthPublicStatus(fid, f)
	if f.provider == "openai-codex" {
		payload["verification_uri"] = codexVerificationURI
		payload["user_code"] = f.userCode
		payload["expires_at"] = f.expiresAt.Unix()
		payload["poll_interval_seconds"] = f.pollInterval
	} else {
		payload["poll_interval_seconds"] = f.pollInterval
		if f.status == "pending" {
			payload["action_required"] = "Claude Code credentials were not found on this server. " +
				"Please run 'claude login' or 'claude setup-token' in a terminal " +
				"on the host, then return here — this page will detect the credentials automatically."
		}
		if f.status == "success" || !f.expiresAt.IsZero() {
			payload["expires_at"] = f.expiresAt.Unix()
		}
	}
	return payload
}

func oauthAnthropicWorker(fid string) {
	for {
		oauthFlowsMu.Lock()
		f := oauthFlows[fid]
		if f == nil || f.status != "pending" || time.Now().After(f.expiresAt) {
			if f != nil && f.status == "pending" {
				f.status = "expired"
				f.updatedAt = time.Now()
			}
			oauthFlowsMu.Unlock()
			return
		}
		interval := f.pollInterval
		hermesHome := f.hermesHome
		oauthFlowsMu.Unlock()

		time.Sleep(time.Duration(maxInt(1, interval)) * time.Second)

		a, rt, exp, ok := readClaudeCodeCreds()
		if !ok || !(claudeTokenValid(exp, a) || rt != "") {
			continue
		}
		oauthFlowsMu.Lock()
		cur := oauthFlows[fid]
		if cur == nil || cur.status != "pending" {
			oauthFlowsMu.Unlock()
			return
		}
		if err := linkAnthropicCredentials(hermesHome); err != nil {
			cur.status = "error"
			cur.err = err.Error()
		} else {
			cur.status = "success"
		}
		cur.updatedAt = time.Now()
		oauthFlowsMu.Unlock()
		return
	}
}

func oauthCodexWorker(fid string) {
	for {
		oauthFlowsMu.Lock()
		f := oauthFlows[fid]
		if f == nil || f.status != "pending" {
			oauthFlowsMu.Unlock()
			return
		}
		if time.Now().After(f.expiresAt) {
			f.status = "expired"
			f.updatedAt = time.Now()
			oauthFlowsMu.Unlock()
			return
		}
		deviceAuthID, userCode, verifier := f.deviceAuthID, f.userCode, f.codeVerifier
		interval := f.pollInterval
		hermesHome := f.hermesHome
		oauthFlowsMu.Unlock()

		time.Sleep(time.Duration(maxInt(3, interval)) * time.Second)

		// Poll authorization (403/404 => not yet approved).
		authRes, err := codexJSONPost(codexDeviceTokenURL, map[string]any{
			"device_auth_id": deviceAuthID, "user_code": userCode,
		})
		if err != nil || authRes == nil || authRes["authorization_code"] == nil {
			continue
		}
		authCode, _ := authRes["authorization_code"].(string)
		if authCode == "" {
			continue
		}
		tok, err := codexJSONPost(codexTokenURL, map[string]any{
			"grant_type": "authorization_code", "code": authCode,
			"redirect_uri": codexRedirectURI, "client_id": codexClientID,
			"code_verifier": verifier,
		})
		oauthFlowsMu.Lock()
		cur := oauthFlows[fid]
		if cur == nil || cur.status != "pending" {
			oauthFlowsMu.Unlock()
			return
		}
		if err != nil {
			cur.status = "error"
			cur.err = err.Error()
			cur.updatedAt = time.Now()
			oauthFlowsMu.Unlock()
			return
		}
		if err := storeCodexCredentials(hermesHome, tok); err != nil {
			cur.status = "error"
			cur.err = err.Error()
		} else {
			cur.status = "success"
		}
		cur.updatedAt = time.Now()
		dropSensitive(cur)
		oauthFlowsMu.Unlock()
		return
	}
}

// storeCodexCredentials writes the token bundle into auth.json credential_pool
// (subset of fields persisted by api/oauth.py _persist_codex_credentials).
func storeCodexCredentials(hermesHome string, tok map[string]any) error {
	access, _ := tok["access_token"].(string)
	refresh, _ := tok["refresh_token"].(string)
	if access == "" {
		return errors.New("token response missing access_token")
	}
	authPath := filepath.Join(hermesHome, "auth.json")
	doc := map[string]any{}
	if raw, err := os.ReadFile(authPath); err == nil {
		_ = json.Unmarshal(raw, &doc)
	}
	if doc["version"] == nil {
		doc["version"] = 1
	}
	pool, _ := doc["credential_pool"].(map[string]any)
	if pool == nil {
		pool = map[string]any{}
		doc["credential_pool"] = pool
	}
	entries, _ := pool["openai-codex"].([]any)
	now := time.Now().UTC().Format(time.RFC3339)
	entry := map[string]any{
		"id":           "openai-codex-" + randomHex(6),
		"label":        "OpenAI Codex (OAuth)",
		"auth_type":    "oauth",
		"priority":     0,
		"source":       "webui_onboarding",
		"access_token": access,
		"created_at":   now,
		"updated_at":   now,
	}
	if refresh != "" {
		entry["refresh_token"] = refresh
	}
	pool["openai-codex"] = append(entries, entry)
	doc["updated_at"] = now
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(authPath, out)
}

func onboardingOAuthCancel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var body struct {
		FlowID   string `json:"flow_id"`
		Provider string `json:"provider"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	fid := strings.TrimSpace(body.FlowID)
	if fid == "" {
		writeError(w, http.StatusBadRequest, "flow_id is required")
		return
	}
	provider := strings.ToLower(strings.TrimSpace(body.Provider))
	if provider != "openai-codex" && provider != "anthropic" {
		provider = "openai-codex"
	}
	oauthFlowsMu.Lock()
	defer oauthFlowsMu.Unlock()
	oauthCleanupLocked(time.Now())
	flow := oauthFlows[fid]
	if flow == nil {
		writeJSON(w, map[string]any{"ok": true, "provider": provider, "flow_id": fid, "status": "cancelled"})
		return
	}
	if flow.status == "pending" {
		flow.status = "cancelled"
		flow.updatedAt = time.Now()
		dropSensitive(flow)
	}
	writeJSON(w, oauthPublicStatus(fid, flow))
}

// ── Probe (onboarding.py probe_provider_endpoint) ──────────────────────────

const probeMaxBytes = 256 * 1024

func onboardingProbe(auth *hermesauth.Auth, w http.ResponseWriter, r *http.Request) {
	if !onboardingGateAllowed(auth, r) {
		writeError(w, http.StatusForbidden,
			"Onboarding probe is only available from local networks when auth is not enabled. To bypass this on a remote server, set HERMES_WEBUI_ONBOARDING_OPEN=1.")
		return
	}
	var body struct {
		Provider string `json:"provider"`
		BaseURL  string `json:"base_url"`
		APIKey   string `json:"api_key"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	baseURL := normalizeBaseURL(body.BaseURL)
	if baseURL == "" {
		writeJSON(w, map[string]any{"ok": false, "error": "invalid_url", "detail": "base_url is required"})
		return
	}
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		writeJSON(w, map[string]any{"ok": false, "error": "invalid_url", "detail": "base_url must start with http:// or https://"})
		return
	}
	if u.Hostname() == "" {
		writeJSON(w, map[string]any{"ok": false, "error": "invalid_url", "detail": "base_url has no host"})
		return
	}
	probeURL := strings.TrimRight(baseURL, "/") + "/models"
	req, _ := http.NewRequest(http.MethodGet, probeURL, nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "hermes-webui-onboarding-probe")
	if key := strings.TrimSpace(body.APIKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // refuse redirects (parity)
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		writeJSON(w, probeNetError(err, u.Host))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		code := "http_5xx"
		if resp.StatusCode < 500 {
			code = "http_4xx"
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			code = "unreachable"
			writeJSON(w, map[string]any{"ok": false, "error": code, "detail": fmt.Sprintf("HTTP %d — endpoint returned a redirect (probe does not follow redirects).  Point base_url at the final URL directly.", resp.StatusCode), "status": resp.StatusCode})
			return
		}
		writeJSON(w, map[string]any{"ok": false, "error": code, "detail": fmt.Sprintf("HTTP %d", resp.StatusCode), "status": resp.StatusCode})
		return
	}
	bodyBytes := make([]byte, probeMaxBytes+1)
	n, _ := resp.Body.Read(bodyBytes)
	if n > probeMaxBytes {
		writeJSON(w, map[string]any{"ok": false, "error": "parse", "detail": "response exceeded 256 KB cap"})
		return
	}
	var payload any
	if err := json.Unmarshal(bodyBytes[:n], &payload); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": "parse", "detail": "response is not JSON"})
		return
	}
	var entries []any
	switch p := payload.(type) {
	case map[string]any:
		if list, ok := p["data"].([]any); ok {
			entries = list
		} else {
			writeJSON(w, map[string]any{"ok": false, "error": "parse", "detail": "response is not in OpenAI /models shape (expected {'data': [...]} or [...])"})
			return
		}
	case []any:
		entries = p
	default:
		writeJSON(w, map[string]any{"ok": false, "error": "parse", "detail": "response is not in OpenAI /models shape (expected {'data': [...]} or [...])"})
		return
	}
	models := []map[string]string{}
	for _, e := range entries {
		switch m := e.(type) {
		case map[string]any:
			if id, _ := m["id"].(string); id != "" {
				models = append(models, map[string]string{"id": id, "label": id})
			}
		case string:
			if s := strings.TrimSpace(m); s != "" {
				models = append(models, map[string]string{"id": s, "label": s})
			}
		}
	}
	writeJSON(w, map[string]any{"ok": true, "models": models, "status": resp.StatusCode})
}

func probeNetError(err error, hostport string) map[string]any {
	msg := err.Error()
	var dnsErr *net.DNSError
	var opErr *net.OpError
	switch {
	case errors.As(err, &dnsErr):
		return map[string]any{"ok": false, "error": "dns", "detail": "could not resolve host '" + hostport + "'"}
	case errors.As(err, &opErr) && strings.Contains(strings.ToLower(msg), "connection refused"):
		return map[string]any{"ok": false, "error": "connect_refused", "detail": "connection refused at " + hostport}
	case strings.Contains(strings.ToLower(msg), "timeout") || errors.Is(err, os.ErrDeadlineExceeded):
		return map[string]any{"ok": false, "error": "timeout", "detail": "connection timed out after 10s"}
	}
	return map[string]any{"ok": false, "error": "unreachable", "detail": truncateStr(msg, 200)}
}

func normalizeBaseURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return u
	}
	if !strings.Contains(u, "://") {
		u = "http://" + u
	}
	return strings.TrimRight(u, "/")
}

// Wave16Router mounts onboarding oauth + probe endpoints.
func Wave16Router(r chi.Router, auth *hermesauth.Auth) {
	r.Get("/api/onboarding/oauth/poll", onboardingOAuthPoll)
	r.Post("/api/onboarding/oauth/start", func(w http.ResponseWriter, req *http.Request) {
		onboardingOAuthStart(auth, w, req)
	})
	r.Post("/api/onboarding/oauth/cancel", onboardingOAuthCancel)
	r.Post("/api/onboarding/probe", func(w http.ResponseWriter, req *http.Request) {
		onboardingProbe(auth, w, req)
	})
}

func truncateStr(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
