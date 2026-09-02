package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"hermes-web-go/internal/config"
	"hermes-web-go/internal/httpserver"
	"hermes-web-go/internal/proxy"
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

	srv := &http.Server{
		Addr:    cfg.Host + ":" + strconv.Itoa(cfg.Port),
		Handler: httpserver.NewRouter(cfg.StaticDir, proxyHandler),
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
		log.Printf("server: %v", err)
		os.Exit(1)
	}
}
