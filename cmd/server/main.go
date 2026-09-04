package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"hermes-web-go/internal/agentclient"
	"hermes-web-go/internal/approval"
	"hermes-web-go/internal/auth"
	"hermes-web-go/internal/config"
	"hermes-web-go/internal/data"
	"hermes-web-go/internal/httpserver"
	"hermes-web-go/internal/proxy"
	"hermes-web-go/internal/store"
)

// run builds the server from cfg, binds it, and services requests until stop
// receives a value (or closes), then performs a graceful shutdown.
func run(cfg config.Config, stop <-chan struct{}) error {
	var proxyHandler http.Handler
	if cfg.LegacyProxyURL != "" {
		ph, err := proxy.Handler(cfg.LegacyProxyURL, nil)
		if err != nil {
			return err
		}
		proxyHandler = ph
	}

	var db *sql.DB
	dbPath := cfg.DatabasePath
	if dbPath != "" {
		opened, err := store.Open(dbPath)
		if err != nil {
			log.Printf("store: open %s: %v (data routes disabled)", dbPath, err)
		} else {
			db = opened
			defer db.Close()

			// Carry Python's command_allowlist into Go's durable approval store.
			// Import is additive and fail-closed: malformed/unreadable config is
			// logged, never treated as approval.
			allowlistPath := filepath.Join(filepath.Dir(cfg.DataRoot), "config.yaml")
			if err := approval.ImportPythonAllowlist(db, allowlistPath); err != nil {
				log.Printf("approval: allowlist import: %v", err)
			}

			// Import from Hermes state.db (primary source of truth).
			stateCount, stateErr := importFromStateDB(db, cfg.StateDBPath)
			if stateErr != nil {
				log.Printf("store: state.db import: %v", stateErr)
			}

			// Fall back to legacy JSON only when state.db has no usable sessions.
			// This prevents stale JSON artifacts from polluting the live sidebar.
			dataRoot := cfg.DataRoot
			if stateErr != nil || stateCount == 0 {
				importDir := filepath.Join(dataRoot, "sessions")
				n, ierr := data.ImportSessions(db, importDir)
				if ierr != nil && !errors.Is(ierr, os.ErrNotExist) {
					log.Printf("store: json import %s: %v", importDir, ierr)
				} else if ierr == nil {
					log.Printf("store: json-imported %d sessions from %s", n, importDir)
				}
			}
			if cerr := data.ImportCatalog(db, dataRoot); cerr != nil {
				log.Printf("store: import catalog: %v", cerr)
			}
		}
	}

	srv := &http.Server{
		Addr:    cfg.Host + ":" + strconv.Itoa(cfg.Port),
		Handler: buildHandler(cfg, proxyHandler, db),
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("listening on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-stop:
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		return err
	}
	return nil
}

// buildHandler assembles the HTTP handler. When a runner base URL is
// configured (HERMES_WEBUI_RUNNER_BASE_URL), native chat routes are wired to
// the HTTP runner client; otherwise the proxy catch-all keeps serving chat
// (Phase 4 cutover keeps proxy fallback until the runner is verified live).
func buildHandler(cfg config.Config, proxyHandler http.Handler, db *sql.DB) http.Handler {
	opts := []httpserver.RouterOption{}
	if cfg.Password != "" {
		a, err := auth.New(auth.Config{
			Password: cfg.Password,
			StateDir: cfg.DataRoot,
		})
		if err != nil {
			log.Printf("auth: disabled (%v)", err)
		} else {
			opts = append(opts, httpserver.WithAuth(a))
		}
	}
	if cfg.AgentBaseURL == "" {
		// No HTTP runner configured. Prefer gRPC socket when available, but
		// always mount native chat/session-stream routes so the frontend
		// gets a proper 500/error bubble instead of 404 "page not found"
		// which it treats as "session deleted" and clears S.session to
		// empty state (looks like "kelempar ke new chat").
		socket := cfg.AgentSocket
		if socket == "" {
			socket = filepath.Join(cfg.DataRoot, "agent.sock")
		}
		httpFallback := agentclient.NewHTTPClient("", "")
		client, err := agentclient.NewBestClient(context.Background(), agentclient.TransportConfig{
			Mode:       agentclient.TransportGRPC,
			SocketPath: socket,
		}, httpFallback)
		if err == nil {
			log.Printf("agent transport: using gRPC socket %s (no runner base URL)", socket)
			approvalStore := approval.NewStoreP(approval.NewSQLitePersistence(db))
			return httpserver.NewRouterWithAgent(cfg.StaticDir, proxyHandler, db, cfg.DataRoot, client, approvalStore, opts...)
		}
		log.Printf("agent transport: gRPC socket %s unavailable (%v); chat routes use HTTP fallback (will error until agent available)", socket, err)
		approvalStore := approval.NewStoreP(approval.NewSQLitePersistence(db))
		return httpserver.NewRouterWithAgent(cfg.StaticDir, proxyHandler, db, cfg.DataRoot, httpFallback, approvalStore, opts...)
	}
	httpFallback := agentclient.NewHTTPClient(cfg.AgentBaseURL, cfg.AgentAPIKey)
	opts = append(opts, httpserver.WithCronMutator(httpFallback))
	mode := agentclient.TransportMode(cfg.AgentTransport)
	if mode == "" {
		mode = agentclient.TransportAuto
	}
	socket := cfg.AgentSocket
	if socket == "" {
		socket = filepath.Join(cfg.DataRoot, "agent.sock")
	}
	client, err := agentclient.NewBestClient(context.Background(), agentclient.TransportConfig{
		Mode: mode, SocketPath: socket,
	}, httpFallback)
	if err != nil {
		return httpserver.NewRouterWithAgent(cfg.StaticDir, proxyHandler, db, cfg.DataRoot, httpFallback, approval.NewStoreP(approval.NewSQLitePersistence(db)), opts...)
	}
	approvalStore := approval.NewStoreP(approval.NewSQLitePersistence(db))
	return httpserver.NewRouterWithAgent(cfg.StaticDir, proxyHandler, db, cfg.DataRoot, client, approvalStore, opts...)
}

// importFromStateDB opens Hermes state.db and imports sessions+messages.
// It returns the number of sessions imported.
func importFromStateDB(dst *sql.DB, path string) (int, error) {
	src, err := data.OpenStateDB(path)
	if err != nil {
		return 0, err
	}
	defer src.Close()
	n, err := data.ImportStateDB(dst, src)
	if err != nil {
		return 0, err
	}
	log.Printf("store: imported %d sessions from %s", n, path)
	return n, nil
}

func main() {
	cfg, err := config.FromEnv()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	stop := make(chan struct{})
	go func() {
		<-sig
		close(stop)
	}()

	if err := run(cfg, stop); err != nil {
		log.Fatalf("server: %v", err)
	}
}
