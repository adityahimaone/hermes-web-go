package httpserver

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"hermes-web-go/internal/proxy"
)

// NewRouter builds the full HTTP handler: recovery + logging middleware,
// native /health, byte-identical static serving under /static/*, and a
// catch-all that proxies every non-native route to the legacy backend.
// A nil proxyHandler leaves non-native routes as 404.
func NewRouter(staticDir string, proxyHandler http.Handler) http.Handler {
	return NewRouterWithData(staticDir, proxyHandler, nil, "")
}

// NewRouterWithData is NewRouter plus the Phase 2 read-only data routes. When
// db is nil the data routes return 503 (proxy-only mode).
func NewRouterWithData(staticDir string, proxyHandler http.Handler, db *sql.DB, dataRoot string) http.Handler {
	r := chi.NewRouter()
	r.Use(Recover)
	r.Use(Logging)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "sessions": 0})
	})

	// Phase 2 read-only data routes are native when the DB is present.
	if db != nil {
		DataRouter(r, db, dataRoot)
	}

	r.Handle("/static", http.RedirectHandler("/static/", http.StatusMovedPermanently))

	// Serve the copied frontend byte-identically. Unlike the stdlib FileServer
	// (which 301-redirects /static/index.html to /static/), the legacy Python
	// WebUI serves /static/index.html directly, so we special-case that path.
	static := http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir)))
	r.Handle("/static/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimSuffix(r.URL.Path, "/")
		if path == "/static/index.html" {
			clone := r.Clone(r.Context())
			clone.URL.Path = "/static/"
			static.ServeHTTP(w, clone)
			return
		}
		static.ServeHTTP(w, r)
	}))

	// Python parity: the app shell is served at "/" (and "/session/{id}") so
	// the base-href script in index.html resolves assets relative to the origin
	// root (e.g. "static/style.css" -> "/static/style.css"). Served only for
	// browsers/GET so the catch-all proxy keeps handling unknown API routes.
	serveShell := func(w http.ResponseWriter, r *http.Request) {
		clone := r.Clone(r.Context())
		clone.URL.Path = "/static/"
		static.ServeHTTP(w, clone)
	}
	r.Get("/", serveShell)
	r.Get("/session/{id}", serveShell)
	r.Get("/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(staticDir, "manifest.json"))
	})
	r.Get("/share/{id}", serveShell)

	r.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
		if proxy.IsNative(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		if proxyHandler == nil {
			http.NotFound(w, r)
			return
		}
		proxyHandler.ServeHTTP(w, r)
	})

	return r
}
