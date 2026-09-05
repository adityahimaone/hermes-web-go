package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
)

// Handler returns a reverse proxy that forwards all requests to target
// (e.g. http://127.0.0.1:8788). A non-nil transport overrides the default.
//
// When HERMES_WEBUI_LEGACY_AUTH_HEADER is set (e.g. "X-Studio-User"), every
// proxied request gets that header injected with HERMES_WEBUI_LEGACY_AUTH_USER
// (default "studio"). The legacy Python WebUI reads it as trusted-header auth
// (HERMES_WEBUI_TRUSTED_AUTH_HEADER) so its password wall doesn't 401 the
// proxy path. Never enable on an untrusted network path between Go and legacy.
func Handler(target string, transport http.RoundTripper) (http.Handler, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	rp := httputil.NewSingleHostReverseProxy(u)
	if headerName := strings.TrimSpace(os.Getenv("HERMES_WEBUI_LEGACY_AUTH_HEADER")); headerName != "" {
		user := strings.TrimSpace(os.Getenv("HERMES_WEBUI_LEGACY_AUTH_USER"))
		if user == "" {
			user = "studio"
		}
		base := transport
		if base == nil {
			base = http.DefaultTransport
		}
		rp.Transport = injectedHeaderTransport{base: base, name: headerName, value: user}
	} else if transport != nil {
		rp.Transport = transport
	}
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
	}
	return rp, nil
}

type injectedHeaderTransport struct {
	base  http.RoundTripper
	name  string
	value string
}

func (t injectedHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone (RoundTrippers must not mutate the caller's request). The header
	// is stripped from the client request first so a caller cannot spoof an
	// already-authenticated identity past the trusted-header check.
	out := req.Clone(req.Context())
	out.Header.Del(t.name)
	out.Header.Set(t.name, t.value)
	return t.base.RoundTrip(out)
}
