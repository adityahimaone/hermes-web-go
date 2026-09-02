package main

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"hermes-web-go/internal/config"
)

// TestGracefulShutdown verifies that run() binds, serves, and returns nil
// (clean exit) once stop is closed, and that the listener is released.
func TestGracefulShutdown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // give the port back; run() will re-bind it

	cfg := config.Config{
		Host:      "127.0.0.1",
		Port:      port,
		StaticDir: "../../static",
	}

	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- run(cfg, stop) }()

	// wait until healthy
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(base + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never became healthy: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	close(stop)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after stop")
	}

	// listener must be released
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 500*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Fatal("listener still accepting after shutdown")
	}
}

// TestRunBindError ensures run returns a bind error instead of hanging.
func TestRunBindError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	cfg := config.Config{Host: "127.0.0.1", Port: port}
	stop := make(chan struct{})
	defer close(stop)

	err = run(cfg, stop)
	if err == nil {
		t.Fatal("expected bind error")
	}
	var oe *net.OpError
	if !errors.As(err, &oe) {
		t.Fatalf("expected net.OpError, got %T: %v", err, err)
	}
}
