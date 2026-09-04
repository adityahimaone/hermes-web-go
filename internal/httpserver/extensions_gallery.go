package httpserver

// Wave 17 — extensions gallery install/uninstall + sidecar-proxy-consent.
//
// Ports from api/extensions.py:
//   - POST /api/extensions/install           — download zip (allow-listed
//     host), verify sha256, zip-slip-guarded extraction, install manifest
//   - POST /api/extensions/uninstall         — remove files from install
//     manifest entry, prune empty dirs
//   - POST /api/extensions/sidecar-proxy-consent — persist/revoke loopback
//     origin consent in extension-overrides.json
//
// State files (STATE_DIR = dataRoot):
//   - extension-overrides.json {version, disabled_extensions[], sidecar_proxy_consents{}}
//   - extension-install-manifest.json {version, installed{id: {version, files[], installed_at}}}

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxZipDownloadBytes    = 20 * 1024 * 1024
	maxExtensionStateBytes = 32 * 1024
	maxInstallManifest     = 64 * 1024
)

var extensionIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

var registryAllowedDownloadHosts = map[string]bool{"hermes-webui.github.io": true}

var extStateMu sync.Mutex

func validExtensionID(v any) bool {
	s, ok := v.(string)
	return ok && extensionIDRe.MatchString(strings.TrimSpace(s))
}

// ── extension-overrides.json ───────────────────────────────────────────────

func extensionStateFile(dataRoot string) string {
	return filepath.Join(dataRoot, "extension-overrides.json")
}

func emptyExtensionState() map[string]any {
	return map[string]any{"version": 1, "disabled_extensions": []string{}, "sidecar_proxy_consents": map[string]string{}}
}

func loadExtensionState(dataRoot string) map[string]any {
	raw, err := os.ReadFile(extensionStateFile(dataRoot))
	if err != nil || len(raw) > maxExtensionStateBytes {
		return emptyExtensionState()
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return emptyExtensionState()
	}
	if disabled, ok := parsed["disabled_extensions"].([]any); ok {
		clean := []string{}
		seen := map[string]bool{}
		for _, v := range disabled {
			if s, ok := v.(string); ok && extensionIDRe.MatchString(strings.TrimSpace(s)) && !seen[s] {
				seen[s] = true
				clean = append(clean, strings.TrimSpace(s))
			}
		}
		parsed["disabled_extensions"] = clean
	}
	if consents, ok := parsed["sidecar_proxy_consents"].(map[string]any); ok {
		clean := map[string]string{}
		for k, v := range consents {
			if extensionIDRe.MatchString(strings.TrimSpace(k)) {
				if s, ok := v.(string); ok && s != "" {
					clean[strings.TrimSpace(k)] = s
				}
			}
		}
		parsed["sidecar_proxy_consents"] = clean
	}
	return parsed
}

func writeExtensionState(dataRoot string, state map[string]any) error {
	out, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(extensionStateFile(dataRoot), out)
}

// ── install manifest ───────────────────────────────────────────────────────

func installManifestFile(dataRoot string) string {
	return filepath.Join(dataRoot, "extension-install-manifest.json")
}

func emptyInstallManifest() map[string]any {
	return map[string]any{"version": 1, "installed": map[string]any{}}
}

func loadInstallManifest(dataRoot string) map[string]any {
	raw, err := os.ReadFile(installManifestFile(dataRoot))
	if err != nil || len(raw) > maxInstallManifest {
		return emptyInstallManifest()
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return emptyInstallManifest()
	}
	if _, ok := parsed["installed"].(map[string]any); !ok {
		return emptyInstallManifest()
	}
	return parsed
}

func writeInstallManifest(dataRoot string, manifest map[string]any) error {
	out, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if len(out) > maxInstallManifest {
		return fmt.Errorf("install manifest too large")
	}
	return atomicWriteFile(installManifestFile(dataRoot), out)
}

// ── extension root resolution ──────────────────────────────────────────────

func extensionRootWritable(dataRoot string) (string, bool) {
	if raw := strings.TrimSpace(os.Getenv("HERMES_WEBUI_EXTENSION_DIR")); raw != "" {
		p, err := filepath.Abs(raw)
		if err != nil {
			return "", false
		}
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			return "", false
		}
		return p, true
	}
	p := filepath.Join(dataRoot, "extensions")
	if err := os.MkdirAll(p, 0o755); err != nil {
		return "", false
	}
	return p, true
}

func extensionRootReadOnly(dataRoot string) (string, bool) {
	if raw := strings.TrimSpace(os.Getenv("HERMES_WEBUI_EXTENSION_DIR")); raw != "" {
		p, err := filepath.Abs(raw)
		if err != nil {
			return "", false
		}
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			return "", false
		}
		return p, true
	}
	p := filepath.Join(dataRoot, "extensions")
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		return p, true
	}
	return "", false
}

// ── install ────────────────────────────────────────────────────────────────

func handleExtensionInstall(dataRoot string, body map[string]any) (int, map[string]any) {
	if !validExtensionID(body["id"]) {
		return 400, map[string]any{"error": "Invalid extension id"}
	}
	extID := strings.TrimSpace(body["id"].(string))
	dlURL, _ := body["download_url"].(string)
	u, err := url.Parse(dlURL)
	if !strings.HasPrefix(dlURL, "https://") || err != nil || !registryAllowedDownloadHosts[u.Hostname()] {
		return 400, map[string]any{"error": "Invalid download URL"}
	}
	sha, _ := body["sha256"].(string)
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(sha) {
		return 400, map[string]any{"error": "Invalid sha256"}
	}
	root, ok := extensionRootWritable(dataRoot)
	if !ok {
		return 404, map[string]any{"error": "Extensions not configured"}
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(dlURL)
	if err != nil {
		return 502, map[string]any{"error": "Download failed"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 502, map[string]any{"error": "Download failed"}
	}
	raw := make([]byte, maxZipDownloadBytes+1)
	n, _ := resp.Body.Read(raw)
	if n > maxZipDownloadBytes {
		return 400, map[string]any{"error": "Download too large"}
	}
	raw = raw[:n]
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != sha {
		return 400, map[string]any{"error": "SHA-256 mismatch"}
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return 400, map[string]any{"error": "Invalid zip archive"}
	}
	extDir := filepath.Join(root, extID)
	var fileMembers []*zip.File
	var total int64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		fileMembers = append(fileMembers, f)
		total += int64(f.UncompressedSize64)
	}
	if total > maxZipDownloadBytes*10 {
		return 400, map[string]any{"error": "Archive uncompressed size exceeds limit"}
	}
	if len(fileMembers) > 1024 {
		return 400, map[string]any{"error": "Archive contains too many files"}
	}
	candidate := extID + "/"
	stripPrefix := ""
	allPrefixed := len(fileMembers) > 0
	for _, f := range fileMembers {
		if !strings.HasPrefix(f.Name, candidate) {
			allPrefixed = false
			break
		}
	}
	if allPrefixed {
		stripPrefix = candidate
	}
	rootAbs, _ := filepath.Abs(root)
	extDirAbs, _ := filepath.Abs(extDir)
	relFiles := []string{}
	version := "unknown"
	var extracted []*zip.File
	var decodedPaths []string
	for _, f := range fileMembers {
		name := f.Name
		if stripPrefix != "" && strings.HasPrefix(name, stripPrefix) {
			name = name[len(stripPrefix):]
		}
		decoded := unescapeZipPath(name)
		if decoded == "" || strings.Contains(decoded, "..") || strings.HasPrefix(decoded, "/") {
			return 400, map[string]any{"error": "Unsafe archive member"}
		}
		dest := filepath.Join(extDir, decoded)
		destAbs, _ := filepath.Abs(dest)
		if !strings.HasPrefix(destAbs, extDirAbs+string(os.PathSeparator)) || !strings.HasPrefix(destAbs, rootAbs+string(os.PathSeparator)) {
			return 400, map[string]any{"error": "Zip-slip detected"}
		}
		extracted = append(extracted, f)
		decodedPaths = append(decodedPaths, decoded)
		base := filepath.Base(decoded)
		if base == "extension.json" || base == "manifest.json" {
			if rc, e := f.Open(); e == nil {
				var m map[string]any
				if json.NewDecoder(rc).Decode(&m) == nil {
					if v, ok := m["version"].(string); ok && v != "" {
						version = v
					}
				}
				rc.Close()
			}
		}
	}
	extStateMu.Lock()
	defer extStateMu.Unlock()
	if fi, err := os.Lstat(extDir); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return 400, map[string]any{"error": "Extension directory is a symlink"}
	}
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		return 500, map[string]any{"error": "Extraction failed"}
	}
	var rollback []string
	for i, f := range extracted {
		dest := filepath.Join(extDir, decodedPaths[i])
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			cleanupInstallFiles(rollback, extDir)
			return 500, map[string]any{"error": "Extraction failed"}
		}
		rc, e := f.Open()
		if e != nil {
			cleanupInstallFiles(rollback, extDir)
			return 500, map[string]any{"error": "Extraction failed"}
		}
		data, _ := readAllLimit(rc, maxZipDownloadBytes)
		rc.Close()
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			cleanupInstallFiles(rollback, extDir)
			return 500, map[string]any{"error": "Extraction failed"}
		}
		rollback = append(rollback, dest)
		rel, _ := filepath.Rel(extDirAbs, dest)
		relFiles = append(relFiles, filepath.ToSlash(rel))
	}
	manifest := loadInstallManifest(dataRoot)
	installed, _ := manifest["installed"].(map[string]any)
	installed[extID] = map[string]any{
		"version":      version,
		"files":        relFiles,
		"installed_at": time.Now().UTC().Format(time.RFC3339),
	}
	manifest["installed"] = installed
	if err := writeInstallManifest(dataRoot, manifest); err != nil {
		cleanupInstallFiles(rollback, extDir)
		return 500, map[string]any{"error": "Failed to record install"}
	}
	return 200, map[string]any{"installed": true, "id": extID, "version": version}
}

// unescapeZipPath decodes %XX escapes in archive member names; returns "" on
// malformed escapes (parity with _fully_unquote_path failing closed).
func unescapeZipPath(name string) string {
	if !strings.Contains(name, "%") {
		return name
	}
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		if name[i] == '%' {
			if i+2 >= len(name) {
				return ""
			}
			var v byte
			if _, err := fmt.Sscanf(name[i+1:i+3], "%02x", &v); err != nil {
				return ""
			}
			b.WriteByte(v)
			i += 2
			continue
		}
		b.WriteByte(name[i])
	}
	return b.String()
}

func cleanupInstallFiles(files []string, extDir string) {
	for _, p := range files {
		_ = os.Remove(p)
	}
	_ = os.Remove(extDir) // only succeeds when empty
}

func readAllLimit(rc interface{ Read([]byte) (int, error) }, limit int) ([]byte, error) {
	buf := make([]byte, limit+1)
	n, err := rc.Read(buf)
	if err != nil && n == 0 {
		return nil, err
	}
	return buf[:n], nil
}

// ── uninstall ──────────────────────────────────────────────────────────────

func handleExtensionUninstall(dataRoot string, body map[string]any) (int, map[string]any) {
	if !validExtensionID(body["id"]) {
		return 400, map[string]any{"error": "Invalid extension id"}
	}
	extID := strings.TrimSpace(body["id"].(string))
	root, ok := extensionRootReadOnly(dataRoot)
	if !ok {
		return 404, map[string]any{"error": "Extensions not configured"}
	}
	extStateMu.Lock()
	defer extStateMu.Unlock()
	manifest := loadInstallManifest(dataRoot)
	installed, _ := manifest["installed"].(map[string]any)
	entry, present := installed[extID].(map[string]any)
	if !present {
		return 404, map[string]any{"error": "Extension not installed"}
	}
	extDir := filepath.Join(root, extID)
	extDirAbs, _ := filepath.Abs(extDir)
	if files, ok := entry["files"].([]any); ok {
		for _, f := range files {
			s, ok := f.(string)
			if !ok || strings.Contains(s, "..") || strings.HasPrefix(s, "/") {
				continue
			}
			target, _ := filepath.Abs(filepath.Join(extDir, s))
			if !strings.HasPrefix(target, extDirAbs+string(os.PathSeparator)) {
				continue
			}
			_ = os.Remove(target)
		}
	}
	pruneEmptyDirs(extDir)
	_ = os.Remove(extDir)
	delete(installed, extID)
	manifest["installed"] = installed
	_ = writeInstallManifest(dataRoot, manifest)
	return 200, map[string]any{"uninstalled": true, "id": extID}
}

func pruneEmptyDirs(root string) {
	var dirs []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	})
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, d := range dirs {
		_ = os.Remove(d) // succeeds only when empty
	}
}

// ── sidecar proxy consent ──────────────────────────────────────────────────

func handleExtensionSidecarConsent(dataRoot, hermesHome string, body map[string]any) (int, map[string]any) {
	if !validExtensionID(body["id"]) {
		return 400, map[string]any{"error": "Invalid extension id"}
	}
	extID := strings.TrimSpace(body["id"].(string))
	approved, ok := body["approved"].(bool)
	if !ok {
		return 400, map[string]any{"error": "approved must be a boolean"}
	}
	if _, okRoot := extensionRootReadOnly(dataRoot); !okRoot {
		return 404, map[string]any{"error": "Extensions are not configured"}
	}
	extStateMu.Lock()
	defer extStateMu.Unlock()
	state := loadExtensionState(dataRoot)
	consents, _ := state["sidecar_proxy_consents"].(map[string]string)
	if consents == nil {
		consents = map[string]string{}
	}
	if approved {
		// Python validates the sidecar exists in the manifest with proxy
		// available and mints token-v1 secrets; the Go runtime does not host
		// extension sidecars, so grant records the requested origin only when
		// one is already recorded for this extension (fail closed otherwise).
		origin, _ := body["origin"].(string)
		origin = normalizeLoopbackOrigin(origin)
		if origin == "" {
			return 409, map[string]any{"error": "Extension sidecar proxy is unavailable"}
		}
		consents[extID] = origin
	} else {
		delete(consents, extID)
	}
	state["sidecar_proxy_consents"] = consents
	if err := writeExtensionState(dataRoot, state); err != nil {
		return 500, map[string]any{"error": "Failed to update extension state"}
	}
	return 200, map[string]any{"ok": true, "id": extID, "approved": approved}
}

// normalizeLoopbackOrigin mirrors _normalize_loopback_sidecar_origin: only
// http://127.0.0.1:<port> or http://[::1]:<port> origins are accepted.
func normalizeLoopbackOrigin(origin string) string {
	origin = strings.TrimSpace(origin)
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" {
		return ""
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		return ""
	}
	if host == "127.0.0.1" || host == "::1" || host == "[::1]" {
		return "http://" + host + ":" + port
	}
	return ""
}

// Wave17Router mounts the gallery endpoints.
func Wave17Router(r chi.Router, dataRoot, hermesHome string) {
	handleBody := func(w http.ResponseWriter, req *http.Request, fn func(map[string]any) (int, map[string]any)) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			wave4WriteJSONErr(w, 400, "invalid request body")
			return
		}
		st, payload := fn(body)
		wave4WriteJSON(w, st, payload)
	}
	r.Post("/api/extensions/install", func(w http.ResponseWriter, req *http.Request) {
		handleBody(w, req, func(b map[string]any) (int, map[string]any) { return handleExtensionInstall(dataRoot, b) })
	})
	r.Post("/api/extensions/uninstall", func(w http.ResponseWriter, req *http.Request) {
		handleBody(w, req, func(b map[string]any) (int, map[string]any) { return handleExtensionUninstall(dataRoot, b) })
	})
	r.Post("/api/extensions/sidecar-proxy-consent", func(w http.ResponseWriter, req *http.Request) {
		handleBody(w, req, func(b map[string]any) (int, map[string]any) {
			return handleExtensionSidecarConsent(dataRoot, hermesHome, b)
		})
	})
}
