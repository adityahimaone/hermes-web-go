package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
)

// Handler returns a reverse proxy that forwards all requests to target
// (e.g. http://127.0.0.1:8788). A non-nil transport overrides the default.
func Handler(target string, transport http.RoundTripper) (http.Handler, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	rp := httputil.NewSingleHostReverseProxy(u)
	if transport != nil {
		rp.Transport = transport
	}
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
	}
	return rp, nil
}
