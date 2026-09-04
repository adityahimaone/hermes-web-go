package httpserver

// Shell template substitution (Python parity: routes.py 12850-12940).
// static/index.html contains __TOKEN__ placeholders that Python substitutes
// on every shell request; Go must do the same or the page throws
// "ReferenceError: __MAX_UPLOAD_BYTES__ is not defined" and the frontend
// boots without upload-size/CSRF configuration.

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// webuiVersion mirrors api/updates.py _detect_webui_version(): git describe,
// falling back to the short sha, then "dev". Like Python (which computes
// WEBUI_VERSION once at import), the value is process-constant: resolved on
// first use via sync.Once against the directory of the running binary (the
// repo root in dev; a deploy checkout in prod). Resolving per request with
// cmd.Dir=dataRoot walked UP into unrelated repos (e.g. a HOME dotfiles repo)
// and let the shell banner's client/server versions drift apart.
var (
	webuiVersionOnce   sync.Once
	webuiVersionCached string
)

func webuiVersion(staticDir string) string {
	webuiVersionOnce.Do(func() {
		webuiVersionCached = detectWebUIVersion(staticDir)
	})
	return webuiVersionCached
}

func detectWebUIVersion(dir string) string {
	if v := strings.TrimSpace(os.Getenv("HERMES_WEBUI_VERSION")); v != "" {
		return v
	}
	if exe, err := os.Executable(); err == nil {
		dir = filepath.Dir(exe)
	}
	for _, args := range [][]string{
		{"git", "describe", "--tags", "--always", "--dirty"},
		{"git", "rev-parse", "--short", "HEAD"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.Output(); err == nil {
			if v := strings.TrimSpace(string(out)); v != "" {
				return v
			}
		}
	}
	return "dev"
}

// shellMaxUploadBytes mirrors api/config.py MAX_UPLOAD_BYTES (default 20 MB).
func shellMaxUploadBytes() int64 {
	mb := int64(20)
	if v := strings.TrimSpace(os.Getenv("HERMES_WEBUI_MAX_UPLOAD_MB")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			mb = n
		}
	}
	return mb * 1024 * 1024
}

// randomCSRFToken: 32 hex bytes. Python binds the token to the auth session
// cookie; the Go auth layer does not (yet) validate a CSRF header, so any
// non-empty value keeps the frontend contract (token present, JSON-encoded).
func randomCSRFToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

type shellCacheEntry struct {
	size    int64
	modNano int64
	base    string
	version string
	mu      sync.Mutex
}

var shellCache shellCacheEntry

// renderIndexShell reads index.html and substitutes the process-constant
// tokens, cached on (size, mtime) like the Python _INDEX_SHELL_CACHE.
// Per-request tokens (CSRF) are applied by the caller.
func renderIndexShell(staticDir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(staticDir, "index.html"))
	if err != nil {
		return "", err
	}
	st, _ := os.Stat(filepath.Join(staticDir, "index.html"))
	var size, nano int64
	if st != nil {
		size, nano = st.Size(), st.ModTime().UnixNano()
	}

	shellCache.mu.Lock()
	if shellCache.base != "" && shellCache.size == size && shellCache.modNano == nano {
		base := shellCache.base
		shellCache.mu.Unlock()
		return base, nil
	}
	shellCache.mu.Unlock()

	version := webuiVersion(staticDir)
	base := string(raw)
	// url.QueryEscape would double-escape the +/ used by Python's quote();
	// the version only feeds cache-busting query params, so raw is safe.
	base = strings.ReplaceAll(base, "__WEBUI_VERSION__", version)
	base = strings.ReplaceAll(base, "__MAX_UPLOAD_BYTES__", strconv.FormatInt(shellMaxUploadBytes(), 10))

	shellCache.mu.Lock()
	shellCache.size, shellCache.modNano, shellCache.base, shellCache.version = size, nano, base, version
	shellCache.mu.Unlock()
	return base, nil
}

// applyShellServe wraps an http.HandlerFunc: serves the substituted shell for
// any request that would otherwise return raw index.html.
func serveShellHTML(w http.ResponseWriter, staticDir string) {
	base, err := renderIndexShell(staticDir)
	if err != nil {
		http.Error(w, "shell unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	csrf := randomCSRFToken()
	html := strings.ReplaceAll(base, "__CSRF_TOKEN_JSON__", strconv.Quote(csrf))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(html))
}
