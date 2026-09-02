package httpserver

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"hermes-web-go/internal/agentclient"
	"hermes-web-go/internal/approval"
	"hermes-web-go/internal/proxy"
	"hermes-web-go/internal/stream"
)

// routerOpt is a functional option for NewRouterWithAgent.
type routerOpt struct {
	hermesHome string
}

// RouterOption mutates router construction options.
type RouterOption func(*routerOpt)

// WithHermesHome pins the Hermes home directory used to resolve cron/skills
// file state. Defaults to $HOME/.hermes when unset.
func WithHermesHome(home string) RouterOption {
	return func(o *routerOpt) { o.hermesHome = home }
}

func defaultHermesHome() string {
	if home := os.Getenv("HERMES_HOME"); home != "" {
		return home
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".hermes")
}

func routerHermesHome(o routerOpt) string {
	if o.hermesHome != "" {
		return o.hermesHome
	}
	return defaultHermesHome()
}

// NewRouter builds the full HTTP handler: recovery + logging middleware,
// native /health, byte-identical static serving under /static/*, and a
// catch-all that proxies every non-native route to the legacy backend.
// A nil proxyHandler leaves non-native routes as 404.
func NewRouter(staticDir string, proxyHandler http.Handler) http.Handler {
	return NewRouterWithData(staticDir, proxyHandler, nil, "")
}

// NewRouterWithAgent adds native chat routes to the data-enabled router.
// Callers pass the agent transport explicitly so tests can use a deterministic
// fake and production can select the configured runner client.
func NewRouterWithAgent(staticDir string, proxyHandler http.Handler, db *sql.DB, dataRoot string, client agentclient.AgentClient, st *approval.Store, opts ...RouterOption) http.Handler {
	var o routerOpt
	for _, fn := range opts {
		fn(&o)
	}

	r := chi.NewRouter()
	r.Use(Recover)
	r.Use(Logging)
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "sessions": 0})
	})
	if db != nil {
		DataRouter(r, db, dataRoot)
	}
	ChatRouter(r, db, stream.NewRegistry(), client, st)
	if st != nil {
		ApprovalRouter(r, st)
	}
	CronsRouter(r, routerHermesHome(o))
	mountStaticAndProxy(r, staticDir, proxyHandler)
	return r
}

// NewRouterWithData is NewRouter plus the Phase 2 read-only data routes. When
// db is nil the data routes return 503 (proxy-only mode).
func NewRouterWithData(staticDir string, proxyHandler http.Handler, db *sql.DB, dataRoot string, opts ...RouterOption) http.Handler {
	var o routerOpt
	for _, fn := range opts {
		fn(&o)
	}
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

	CronsRouter(r, routerHermesHome(o))

	mountStaticAndProxy(r, staticDir, proxyHandler)
	return r
}

// mountStaticAndProxy attaches the static file server, app shell routes, and
// the catch-all proxy handler to r. This is shared by NewRouterWithData and
// NewRouterWithAgent so both serve the same static content.
func mountStaticAndProxy(r chi.Router, staticDir string, proxyHandler http.Handler) {
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
}
