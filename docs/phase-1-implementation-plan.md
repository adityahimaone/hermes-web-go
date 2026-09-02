# Phase 1 — Skeleton + Proxy Implementation Plan

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标:** Go binary boots, serves static byte-identical, proxies 100% `/api/*` ke Python legacy, `/health` native. Zero behavior change.

**架构:** `cmd/server/main.go` thin entrypoint; `internal/httpserver` router + middleware; `internal/proxy` reverse-proxy + route registry; static via disk-based `http.FileServer`.

**技术栈:** Go 1.26 stdlib, `github.com/go-chi/chi/v5` router, `net/http/httputil.ReverseProxy`, testing via `httptest` (no external test framework).

---

## File structure

- `go.mod` — module `hermes-web-go`, go 1.26
- `cmd/server/main.go` — entrypoint: env/flags, boot, graceful shutdown
- `internal/config/config.go` — env loading (PORT, HOST, LEGACY_PROXY_URL, STATIC_DIR)
- `internal/httpserver/server.go` — chi router, middleware, static serving, `/health`, catch-all proxy
- `internal/httpserver/middleware.go` — JSON request logging + panic recovery
- `internal/proxy/registry.go` — `NativeRoutes` map + `IsNative`
- `internal/proxy/proxy.go` — ReverseProxy target setup, `Handler()`
- `tests/*_test.go` — Go tests alongside each package (in-package)

## Env contract (matches Python exactly)

- `HERMES_WEBUI_HOST` default `127.0.0.1`
- `HERMES_WEBUI_PORT` default `8787`
- `HERMES_WEBUI_LEGACY_PROXY_URL` (new for Go; e.g. `http://127.0.0.1:8788`) — required for proxy to activate
- `HERMES_WEBUI_STATIC_DIR` default `./static`

---

### Task 1: go.mod + config package

**文件：**
- 创建：`go.mod`
- 创建：`internal/config/config.go`
- 测试：`internal/config/config_test.go`

- [ ] **步骤 1: 编写失败的测试**

```go
package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	c := Load(map[string]string{})
	if c.Host != "127.0.0.1" { t.Fatalf("host default = %q", c.Host) }
	if c.Port != 8787 { t.Fatalf("port default = %d", c.Port) }
	if c.StaticDir != "static" { t.Fatalf("static dir = %q", c.StaticDir) }
}

func TestLoadOverrides(t *testing.T) {
	c := Load(map[string]string{
		"HERMES_WEBUI_PORT": "9999",
		"HERMES_WEBUI_LEGACY_PROXY_URL": "http://127.0.0.1:8788",
	})
	if c.Port != 9999 { t.Fatalf("port override = %d", c.Port) }
	if c.LegacyProxyURL != "http://127.0.0.1:8788" { t.Fatalf("proxy url = %q", c.LegacyProxyURL) }
}

func TestInvalidPortFails(t *testing.T) {
	if _, err := Load(map[string]string{"HERMES_WEBUI_PORT": "abc"}); err == nil {
		t.Fatal("expected error for invalid port")
	}
}
```

- [ ] **步骤 2: 运行测试验证失败**
  运行: `cd .worktrees/phase-1 && go test ./internal/config/`
  预期: FAIL — package not found

- [ ] **步骤 3: 编写最少实现**

```go
package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Host           string
	Port           int
	LegacyProxyURL string
	StaticDir      string
}

func Load(getenv func(string) string) (Config, error) {
	c := Config{
		Host:      getenv("HERMES_WEBUI_HOST"),
		StaticDir: getenv("HERMES_WEBUI_STATIC_DIR"),
	}
	if c.Host == "" { c.Host = "127.0.0.1" }
	if c.StaticDir == "" { c.StaticDir = "static" }
	portStr := getenv("HERMES_WEBUI_PORT")
	if portStr == "" { portStr = "8787" }
	p, err := strconv.Atoi(portStr)
	if err != nil {
		return Config{}, fmt.Errorf("HERMES_WEBUI_PORT invalid: %w", err)
	}
	c.Port = p
	c.LegacyProxyURL = getenv("HERMES_WEBUI_LEGACY_PROXY_URL")
	return c, nil
}

func FromEnv() (Config, error) { return Load(os.Getenv) }
```

- [ ] **步骤 4: 运行测试验证通过**
  运行: `go test ./internal/config/`
  预期: PASS

- [ ] **步骤 5: Commit**
```bash
git add go.mod internal/config
git commit -m "feat(config): env loading with defaults matching Python"
```

---

### Task 2: Proxy registry + ReverseProxy

**文件：**
- 创建：`internal/proxy/registry.go`
- 创建：`internal/proxy/proxy.go`
- 测试：`internal/proxy/proxy_test.go`

- [ ] **步骤 1: 编写失败的测试**

```go
package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsNative(t *testing.T) {
	if !IsNative("/health") { t.Fatal("/health should be native") }
	if IsNative("/api/sessions") { t.Fatal("/api/sessions should be proxied") }
}

func TestProxyForwardsAndPreserves(t *testing.T) {
	// backend echo server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/sessions" { t.Errorf("path = %q", r.URL.Path) }
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	h, err := Handler(backend.URL, nil)
	if err != nil { t.Fatal(err) }
	front := httptest.NewServer(h)
	defer front.Close()

	resp, err := http.Get(front.URL + "/api/sessions")
	if err != nil { t.Fatal(err) }
	defer resp.Body.Close()
	if resp.StatusCode != 201 { t.Fatalf("status = %d", resp.StatusCode) }
}
```

- [ ] **步骤 2: 运行测试验证失败**
  运行: `go test ./internal/proxy/`
  预期: FAIL — package not found

- [ ] **步骤 3: 编写最少实现**

```go
package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
)

var NativeRoutes = map[string]bool{
	"/health": true,
}

func IsNative(path string) bool { return NativeRoutes[path] }

func Handler(target string, transport http.RoundTripper) (http.Handler, error) {
	u, err := url.Parse(target)
	if err != nil { return nil, err }
	rp := httputil.NewSingleHostReverseProxy(u)
	if transport != nil { rp.Transport = transport }
	return rp, nil
}
```

- [ ] **步骤 4: 运行测试验证通过**
  运行: `go test ./internal/proxy/`
  预期: PASS

- [ ] **步骤 5: Commit**
```bash
git add internal/proxy
git commit -m "feat(proxy): reverse proxy with native route registry"
```

---

### Task 3: HTTPServer — router, middleware, static, health, catch-all

**文件：**
- 创建：`internal/httpserver/middleware.go`
- 创建：`internal/httpserver/server.go`
- 测试：`internal/httpserver/server_test.go`

- [ ] **步骤 1: 编写失败的测试**

```go
package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestHealthNative(t *testing.T) {
	r := NewRouter("static", nil)
	ts := httptest.NewServer(r)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/health")
	if err != nil { t.Fatal(err) }
	defer resp.Body.Close()
	if resp.StatusCode != 200 { t.Fatalf("health status = %d", resp.StatusCode) }
}

func TestStaticServed(t *testing.T) {
	r := NewRouter("static", nil)
	ts := httptest.NewServer(r)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/static/index.html")
	if err != nil { t.Fatal(err) }
	defer resp.Body.Close()
	if resp.StatusCode != 200 { t.Fatalf("static status = %d", resp.StatusCode) }
}

func TestPanicRecoveryJSON(t *testing.T) {
	r := chi.NewRouter()
	r.Use(Recover)
	r.Get("/boom", func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	ts := httptest.NewServer(r)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/boom")
	if err != nil { t.Fatal(err) }
	defer resp.Body.Close()
	if resp.StatusCode != 500 { t.Fatalf("panic status = %d", resp.StatusCode) }
	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf)
	if string(buf[:n]) != `{"error":"Internal server error"}` {
		t.Fatalf("panic body = %q", string(buf[:n]))
	}
}
```

- [ ] **步骤 2: 运行测试验证失败**
  运行: `go test ./internal/httpserver/`
  预期: FAIL — package not found

- [ ] **步骤 3: 编写最少实现**

```go
package httpserver

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic: %v\n%s", rec, debug.Stack())
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": "Internal server error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("{\"time\":%q,\"method\":%q,\"path\":%q,\"dur_ms\":%d}",
			time.Now().Format(time.RFC3339), r.Method, r.URL.Path, time.Since(start).Milliseconds())
	})
}

func NewRouter(staticDir string, proxyHandler http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(Recover)
	r.Use(Logging)
	r.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "sessions": 0})
	})
	// static
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))
	r.Handle("/static", http.RedirectHandler("/static/", http.StatusMovedPermanently))
	if proxyHandler != nil {
		r.Handle("/*", proxyHandler)
	}
	return r
}
```

- [ ] **步骤 4: 运行测试验证通过**
  运行: `go test ./internal/httpserver/`
  预期: PASS

- [ ] **步骤 5: Commit**
```bash
git add internal/httpserver
git commit -m "feat(httpserver): router, middleware, static, health"
```

---

### Task 4: cmd/server/main.go entrypoint + graceful shutdown

**文件：**
- 创建：`cmd/server/main.go`
- 测试：`cmd/server/main_test.go` (optional build smoke)

- [ ] **步骤 1: 编写失败的测试**
  运行 `go build ./...` 验证 main 缺少导致 FAIL。无需单测。

- [ ] **步骤 2: 运行测试验证失败**
  运行: `go build ./...`
  预期: FAIL — cmd/server/main.go not found

- [ ] **步骤 3: 编写最少实现**

```go
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"hermes-web-go/internal/config"
	"hermes-web-go/internal/httpserver"
	"hermes-web-go/internal/proxy"
)

func main() {
	cfg, err := config.FromEnv()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	var proxyHandler http.Handler
	if cfg.LegacyProxyURL != "" {
		ph, err := proxy.Handler(cfg.LegacyProxyURL, nil)
		if err != nil {
			log.Fatalf("proxy: %v", err)
		}
		proxyHandler = ph
	}
	handler := httpserver.NewRouter(cfg.StaticDir, proxyHandler)
	srv := &http.Server{Addr: cfg.Host + ":" + itoa(cfg.Port), Handler: handler}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		log.Printf("listening on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

func itoa(n int) string {
	if n == 0 { return "0" }
	neg := n < 0
	if neg { n = -n }
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg { i--; b[i] = '-' }
	return string(b[i:])
}
```

- [ ] **步骤 4: 运行测试验证通过**
  运行: `go build ./... && go vet ./...`
  预期: PASS

- [ ] **步骤 5: Commit**
```bash
git add cmd/server
git commit -m "feat(cmd): server entrypoint with graceful shutdown"
```

---

### Task 5: Boot integration — serve static byte-identical + health + proxy live smoke

**文件：**
- 修改：`internal/httpserver/server.go` (merge proxy + native decision)

- [ ] **步骤 1: 修改 NewRouter 让 proxy 只在非 native route 生效**

```go
// inside NewRouter, after static and before catch-all:
r.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
	if proxy.IsNative(r.URL.Path) {
		// /health already handled above; native list grows per phase
		http.NotFound(w, r)
		return
	}
	proxyHandler.ServeHTTP(w, r)
})
```

- [ ] **步骤 2: 运行测试**
  运行: `go test ./... && go vet ./...`
  预期: PASS

- [ ] **步骤 3: Live smoke — Python legacy on 8788, Go front on 8787**

```bash
HERMES_WEBUI_PORT=8788 /tmp/hermes-webui-phase0-venv/bin/python server.py &   # legacy
HERMES_WEBUI_PORT=8787 HERMES_WEBUI_LEGACY_PROXY_URL=http://127.0.0.1:8788 go run ./cmd/server &
curl -s localhost:8787/health        # native JSON
curl -s localhost:8787/static/index.html | head -3   # byte-identical static
curl -s localhost:8787/api/sessions  # proxied from Python
kill %1 %2
```

- [ ] **步骤 4: Replay Phase 0 golden fixture against Go-fronted stack**
  运行: `PYTHONPATH=tools /tmp/hermes-webui-phase0-venv/bin/python tools/phase0_lifecycle.py ... --journey testdata/phase0-safe-journey.json`
  预期: `3 exchanges match after redaction/normalization`

- [ ] **步骤 5: Commit**
```bash
git add internal/httpserver
git commit -m "feat(httpserver): proxy catch-all with native route gate"
```
