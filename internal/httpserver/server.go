package httpserver

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"hermes-web-go/internal/agentclient"
	"hermes-web-go/internal/approval"
	"hermes-web-go/internal/auth"
	"hermes-web-go/internal/proxy"
	"hermes-web-go/internal/store"
	"hermes-web-go/internal/stream"
)

// routerOpt is a functional option for NewRouterWithAgent.
type routerOpt struct {
	hermesHome string
	auth       *auth.Auth
	cron       agentclient.CronMutator
}

// RouterOption mutates router construction options.
type RouterOption func(*routerOpt)

// WithCronMutator wires scheduler mutations to the agent gateway.
func WithCronMutator(c agentclient.CronMutator) RouterOption {
	return func(o *routerOpt) { o.cron = c }
}

// WithHermesHome pins the Hermes home directory used to resolve cron/skills
// file state. Defaults to $HOME/.hermes when unset.
func WithHermesHome(home string) RouterOption {
	return func(o *routerOpt) { o.hermesHome = home }
}

// WithAuth enables the optional password gate. When nil (default) the gate
// is off and every route is public.
func WithAuth(a *auth.Auth) RouterOption {
	return func(o *routerOpt) { o.auth = a }
}

func authMiddlewareOrNil(o routerOpt) func(http.Handler) http.Handler {
	if o.auth == nil {
		return nil
	}
	return o.auth.Middleware
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

// Health carries the state needed to answer /health.
type Health struct {
	reg       *stream.JournalRegistry
	startedAt time.Time
	db        *sql.DB
}

func NewHealth(reg *stream.JournalRegistry, startedAt time.Time) *Health {
	return &Health{reg: reg, startedAt: startedAt}
}

// WithDB wires the session store for the sessions count and deep state checks.
func (h *Health) WithDB(db *sql.DB) *Health {
	h.db = db
	return h
}

func (h *Health) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	deep := strings.EqualFold(r.URL.Query().Get("deep"), "1") ||
		strings.EqualFold(r.URL.Query().Get("deep"), "true")
	active := 0
	if h.reg != nil {
		active = h.reg.Len()
	}
	sessions := 0
	if h.db != nil {
		if n, err := store.CountSessions(h.db); err == nil {
			sessions = n
		}
	}
	payload := map[string]any{
		"status":               "ok",
		"sessions":             sessions,
		"active_streams":       active,
		"active_runs":          0,
		"runs":                 []any{},
		"last_run_finished_at": nil,
		"server_started_at":    float64(h.startedAt.UnixNano()) / 1e9,
		"uptime_seconds":       round1(time.Since(h.startedAt).Seconds()),
		"accept_loop": map[string]any{
			"requests_total":  0,
			"last_request_at": float64(0),
		},
	}
	if deep {
		checks := map[string]any{
			"streams_lock":   map[string]any{"status": "ok", "active_streams": active, "ms": float64(0)},
			"stream_runtime": map[string]any{"status": "ok", "active_streams": active, "total_subscribers": 0, "total_offline_buffered_events": 0, "streams": []any{}},
			"sessions":       map[string]any{"status": "ok", "count": sessions, "ms": float64(0)},
			"projects":       map[string]any{"status": "ok", "count": 0, "ms": float64(0)},
			"state_db":       map[string]any{"status": "ok", "ms": float64(0)},
		}
		if h.db == nil {
			checks["state_db"] = map[string]any{"status": "missing", "ms": float64(0)}
		}
		payload["checks"] = checks
	}
	w.Header().Set("Content-Type", "application/json")
	status := http.StatusOK
	if payload["status"] != "ok" {
		status = http.StatusServiceUnavailable
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// round1 rounds to one decimal place, matching Python's round(x, 1).
func round1(v float64) float64 {
	return float64(int(v*10+0.5)) / 10
}

// Router constructors below.
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
	reg := stream.NewJournalRegistry()
	sessStreams := newSessionStreamState()

	r := chi.NewRouter()
	r.Use(Recover)
	r.Use(Logging)
	if mw := authMiddlewareOrNil(o); mw != nil {
		r.Use(mw)
	}
	r.Get("/health", NewHealth(reg, time.Now()).WithDB(db).ServeHTTP)
	if db != nil {
		DataRouter(r, db, dataRoot)
		SessionFamilyRouter(r, db, reg)
	}
	ChatRouter(r, db, reg, client, st, sessStreams)
	SessionStreamRouter(r, db, reg, sessStreams)
		SessionListEventsRouter(r, dataRoot, routerHermesHome(o))
	if st != nil {
		ApprovalRouter(r, st)
	}
	CronsRouter(r, routerHermesHome(o), o.cron)
	SkillsMemRouter(r, routerHermesHome(o))
	ConversationRoundsRouter(r, routerHermesHome(o))
	ConfigRouter(r, routerHermesHome(o), dataRoot)
	LogsRouter(r, routerHermesHome(o))
	miscRouter(r, routerHermesHome(o))
	miscReadsRouter(r, db, dataRoot, routerHermesHome(o))
	wave2Router(r, o.auth, dataRoot, routerHermesHome(o))
	wave3Router(r, dataRoot, routerHermesHome(o))
		wave4Router(r, db, dataRoot, "", routerHermesHome(o))
		wave6Router(r, db)
		wave7Router(r, db, dataRoot, routerHermesHome(o))
		TerminalRouter(r, db)
		wave8Router(r, db, dataRoot, routerHermesHome(o))
	gitFamilyRouter(r, db)
	if o.auth != nil {
		AuthRouter(r, o.auth)
	}
	mountStaticAndProxy(r, staticDir, proxyHandler, false)

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
	if mw := authMiddlewareOrNil(o); mw != nil {
		r.Use(mw)
	}

	reg := stream.NewJournalRegistry()
	r.Get("/health", NewHealth(reg, time.Now()).WithDB(db).ServeHTTP)

	// Phase 2 read-only data routes are native when the DB is present.
	if db != nil {
		DataRouter(r, db, dataRoot)
		SessionFamilyRouter(r, db, reg)
	}

	CronsRouter(r, routerHermesHome(o), o.cron)
	SkillsMemRouter(r, routerHermesHome(o))
	ConversationRoundsRouter(r, routerHermesHome(o))
	ConfigRouter(r, routerHermesHome(o), dataRoot)
	LogsRouter(r, routerHermesHome(o))
	miscRouter(r, routerHermesHome(o))
	miscReadsRouter(r, db, dataRoot, routerHermesHome(o))
	wave2Router(r, o.auth, dataRoot, routerHermesHome(o))
	wave3Router(r, dataRoot, routerHermesHome(o))
		wave4Router(r, db, dataRoot, "", routerHermesHome(o))
		wave6Router(r, db)
		wave7Router(r, db, dataRoot, routerHermesHome(o))
		TerminalRouter(r, db)
		wave8Router(r, db, dataRoot, routerHermesHome(o))
	gitFamilyRouter(r, db)
	if o.auth != nil {
		AuthRouter(r, o.auth)
	}

	mountStaticAndProxy(r, staticDir, proxyHandler, true)
	return r
}

// mountStaticAndProxy attaches the static file server, app shell routes, and
// the catch-all proxy handler to r. This is shared by NewRouterWithData and
// NewRouterWithAgent so both serve the same static content. allowChatProxy
// controls whether the catch-all may still forward /api/chat* to the legacy
// backend (data-only routers have no native chat handler and must proxy).
func mountStaticAndProxy(r chi.Router, staticDir string, proxyHandler http.Handler, allowChatProxy bool) {
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
	// chi `{id}` routes don't span segments, so one `/session/*` catch-all
	// serves both the app shell and its relative asset URLs. Asset paths
	// (`/session/{id}/static/<file>` and `/session/static/<file>`) are
	// rewritten to `/static/<file>` and served from staticDir; everything
	// else serves the shell (Python parity: /session/{id}).
	sessionAssets := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asset := strings.TrimPrefix(r.URL.Path, "/session")
		// asset looks like /{id}/static/... or /static/... — collapse to
		// /static/<file> so FileServer(staticDir) resolves correctly.
		if i := strings.Index(asset, "/static/"); i >= 0 {
			asset = "/static/" + asset[i+len("/static/"):]
		}
		clone := r.Clone(r.Context())
		clone.URL.Path = asset
		static.ServeHTTP(w, clone)
	})
	r.Get("/session/*", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/session")
		if rest == "/static" || strings.HasPrefix(rest, "/static/") || strings.Contains(rest, "/static/") {
			sessionAssets.ServeHTTP(w, r)
			return
		}
		serveShell(w, r)
	})
	// /session/static/* (no id segment) — exact-path match wins over the
	// catch-all's asset sniffing.
	r.Get("/session/static/*", sessionAssets)
	r.Get("/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(staticDir, "manifest.json"))
	})
	r.Get("/share/{id}", serveShell)

	r.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
		if !allowChatProxy && (r.URL.Path == "/api/chat" || strings.HasPrefix(r.URL.Path, "/api/chat/")) {
			http.NotFound(w, r)
			return
		}
		if proxy.IsNativeMethod(r.Method, r.URL.Path) {
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
