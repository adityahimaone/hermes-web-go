# Hermes Web Go

Go port of the Hermes WebUI backend. Same vanilla-JS frontend (`static/`), same
SQLite state, same SSE contract — but the HTTP layer is now Go. Python only
remains for what legitimately is Python: the Hermes agent / tool loop.

```
Browser  ──SSE──▶  Go :8787  ──gRPC/UDS──▶  Python gateway shim  ──▶  LLM
                  (this repo)    agent.sock      (~/.hermes/hermes-agent)
                      │
                      └──HTTP fallback──▶  9router / HERMES_API_URL (127.0.0.1:8642)
                      │
                      └──proxy fallback──▶ Python WebUI :8788 (legacy routes not yet migrated)
```

## Table of Contents

- [Status](#status)
- [Prerequisites](#prerequisites)
- [Quick Start](#quick-start)
- [Configuration](#configuration)
- [Agent Transport (gRPC over Unix Socket)](#agent-transport-grpc-over-unix-socket)
- [Chat Flow (end-to-end)](#chat-flow-end-to-end)
- [SSE Event Contract](#sse-event-contract)
- [Session Store & Reconciliation](#session-store--reconciliation)
- [Static Assets](#static-assets)
- [Running Modes](#running-modes)
- [ctl.sh & switch-backend.sh](#ctlsh--switch-backendsh)
- [Development](#development)
- [Testing & Parity](#testing--parity)
- [Deployment](#deployment)
- [Troubleshooting](#troubleshooting)
- [Docs Index](#docs-index)

## Status

Migration is incremental (strangler fig). Go is the public entry point on
`:8787`; routes not yet native are reverse-proxied to the legacy Python WebUI
on `:8788`. See `docs/10-endpoint-migration-inventory.md` for the live count
(`python3 scripts/endpoint_inventory.py`).

- Native Go: ~192 `/api/*` routes (chat, session CRUD, files, git, crons, skills, memory, auth, etc.)
- Proxied (legacy Python): ~41 routes (OIDC/passkey, some extensions/plugins, share/media boundaries)
- Go-only additions: 4 (e.g. `GET /api/share/{token}` chi dynamic route)

No route is removed from the proxy until its Go handler passes parity tests.

## Prerequisites

- Go 1.26+ (`go version`)
- Python 3.11+ with the Hermes venv at `~/.hermes/hermes-agent/venv` (provides `grpc`, `httpx`, `agent_grpc` shim deps)
- `modernc.org/sqlite` is pure Go — no CGO / no `libsqlite3` needed
- macOS LaunchAgents for the two Python daemons (gateway + gRPC shim), or run them manually

## Quick Start

```bash
git clone <this-repo> && cd hermes-web-go

# 1. Build
go build -o hermes-web-go ./cmd/server

# 2. Ensure the two Python daemons are running
launchctl list | grep -E "ai.hermes.(gateway|agent-grpc-shim)"
# expected:  ai.hermes.gateway  and  ai.hermes.agent-grpc-shim
# if missing:
launchctl kickstart -k gui/$(id -u)/ai.hermes.gateway
launchctl kickstart -k gui/$(id -u)/ai.hermes.agent-grpc-shim
# verify socket exists:
ls -l ~/.hermes/webui/agent.sock
# verify shim answers Ping:
python3 -c "import asyncio,grpc,sys; sys.path.insert(0,'$HOME/.hermes/hermes-agent'); import proto.agent_pb2 as pb, proto.agent_pb2_grpc as rpc; \
  c=grpc.aio.insecure_channel('unix://$HOME/.hermes/webui/agent.sock'); s=rpc.AgentStub(c); \
  print(asyncio.run(s.Ping(pb.PingRequest())))"

# 3. Run Go (foreground)
./hermes-web-go
# -> listening on 127.0.0.1:8787
# -> agent transport: using gRPC socket /Users/you/.hermes/webui/agent.sock

# 4. Open
open http://127.0.0.1:8787/
```

One-command toggle between Go and Python on the same port:

```bash
./switch-backend.sh go        # build + launch Go on :8787
./switch-backend.sh python    # launch Python on :8787 (isolated /tmp/pyrec-state)
./switch-backend.sh status    # what's on :8787
```

Daemon wrapper (pidfile + log):

```bash
./ctl.sh start    # nohup ./hermes-web-go >> ~/.hermes/web-go.log
./ctl.sh status
./ctl.sh logs     # tail -n 100 ~/.hermes/web-go.log
./ctl.sh stop
```

## Configuration

All via env vars. Defaults match the legacy Python WebUI.

| Variable | Default | Description |
|---|---|---|
| `HERMES_WEBUI_HOST` | `127.0.0.1` | Bind host |
| `HERMES_WEBUI_PORT` | `8787` | Public port (browser talks here) |
| `HERMES_WEBUI_STATIC_DIR` | `./static` | Frontend assets dir (or `embed.FS` in single-binary deploys) |
| `HERMES_WEBUI_DATA_ROOT` | `~/.hermes/webui` | Sessions, workspaces, settings root |
| `HERMES_WEBUI_DATABASE_PATH` | `$DATA_ROOT/webui.db` | SQLite DB |
| `HERMES_WEBUI_STATE_DB_PATH` | `~/.hermes/state.db` | Agent's authoritative transcript (reconciled on every `GET /api/session`) |
| `HERMES_WEBUI_PASSWORD` | _(empty = no auth)_ | If set, enables `POST /api/auth/login` + HttpOnly cookie guard on all `/api/*` |
| `HERMES_WEBUI_AGENT_TRANSPORT` | `auto` | `auto` \| `grpc` \| `http` — see below |
| `HERMES_WEBUI_AGENT_SOCKET` | `~/.hermes/webui/agent.sock` | Unix socket for gRPC shim |
| `HERMES_WEBUI_RUNNER_BASE_URL` | _(empty)_ | HTTP runner base URL (`http://127.0.0.1:8642` / `HERMES_API_URL`) — fallback when gRPC unavailable |
| `HERMES_WEBUI_AGENT_API_KEY` | _(empty)_ | Bearer for HTTP runner |
| `HERMES_WEBUI_LEGACY_PROXY_URL` | _(empty)_ | Python fallback origin, e.g. `http://127.0.0.1:8788` — catch-all for not-yet-migrated routes |

Config loader: `internal/config/config.go` (`config.FromEnv()`).

## Agent Transport (gRPC over Unix Socket)

Two hops, optimized independently:

1. **Browser ↔ Go** — SSE (`EventSource`), unchanged. Token stream, tool events, `done`/`error`.
2. **Go ↔ Hermes agent** — gRPC bidirectional streaming over a **Unix domain socket**. This is where the latency win is.

### Why UDS + gRPC

- UDS skips the TCP/IP loopback stack (kernel byte copy, no handshake).
- gRPC = binary protobuf, HTTP/2 multiplex (multiple concurrent session streams over one long-lived connection), and `Cancel` on the same stream (no extra HTTP call).
- One persistent connection reused across turns — no per-turn dial tax.

### Implementations

`internal/agentclient/` exposes one interface:

```go
type AgentClient interface {
    RunTurn(ctx context.Context, req TurnRequest) (<-chan TurnEvent, error)
    Cancel(ctx context.Context, sessionID string) error
}
```

| Impl | File | Transport | When |
|---|---|---|---|
| `grpcClient` | `grpcclient.go` | gRPC over `agent.sock` | Preferred — default when socket + Ping succeed |
| `httpClient` | `httpclient.go` | HTTP `POST /v1/runs` to `HERMES_API_URL` | Fallback |

Selection lives in `transport.go:NewBestClient` (`auto` mode):

```
startup: dial agent.sock → Ping (500ms timeout)
  ├─ success → grpcClient (+ http fallback wrapped inside)
  └─ fail    → httpClient, log "using HTTP fallback", never error

per-call: grpc RunTurn fails to establish → retry once via httpClient
          (mid-stream errors surface as EventError, no silent fallback)
```

Manual override:

```bash
HERMES_WEBUI_AGENT_TRANSPORT=grpc ./hermes-web-go  # force grpc (falls back to http if Ping fails)
HERMES_WEBUI_AGENT_TRANSPORT=http  ./hermes-web-go  # force http
HERMES_WEBUI_AGENT_TRANSPORT=auto  ./hermes-web-go  # default
```

### Proto

```
proto/agent.proto  →  internal/agentpb/*.pb.go   (go_package hermes-web-go/internal/agentpb;agentpb)
                   →  ~/.hermes/hermes-agent/proto/agent_pb2*.py  (Python shim stubs)
```

Regenerate after editing `proto/agent.proto`:

```bash
# Go
protoc --go_out=. --go-grpc_out=. proto/agent.proto
# Python (inside hermes-agent venv)
~/.hermes/hermes-agent/venv/bin/python -m grpc_tools.protoc \
  -I proto --python_out=$HOME/.hermes/hermes-agent/proto \
  --grpc_python_out=$HOME/.hermes/hermes-agent/proto proto/agent.proto
# fix import in generated *_pb2_grpc.py:  import agent_pb2  →  import proto.agent_pb2
```

### Python Shim

The shim is a thin adapter in front of the existing `/v1/runs` HTTP API — it does **not** reimplement the agent loop.

- Source of truth: `~/.hermes/hermes-agent/gateway/platforms/agent_grpc.py`
- Deployable backup: `deploy/agent-grpc-shim/` (exact runtime copy + proto)
- Service: `~/Library/LaunchAgents/ai.hermes.agent-grpc-shim.plist` (`KeepAlive=true`)
- Listens on: `unix://~/.hermes/webui/agent.sock` (`SOCKET_PATH` env overrides)

Install/restore after `hermes-agent` git pull (which can wipe the shim):

```bash
cp -r deploy/agent-grpc-shim/agent_grpc.py ~/.hermes/hermes-agent/gateway/platforms/
mkdir -p ~/.hermes/hermes-agent/proto
cp -r deploy/agent-grpc-shim/proto/* ~/.hermes/hermes-agent/proto/
launchctl kickstart -k gui/$(id -u)/ai.hermes.agent-grpc-shim
```

The shim forwards **all** event types (token, reasoning, tool, tool_complete, metering, context_status, interim_assistant, done) — parity with `api/streaming.py`'s SSE surface. Go's `httpclient.go` + `internal/stream/writer.go` relay them unchanged to the browser.

## Chat Flow (end-to-end)

```
User types "hello" + Enter
        │
        ▼
POST /api/chat/start {session_id, message, model, workspace}
  Go: validate session, append user message, allocate stream_id
      spawn goroutine: agentclient.RunTurn(ctx, TurnRequest{History, ...})
        │
        ├─grpc path:  gRPC RunTurn(stream) ──TurnEvent──▶ chan (16 buffered)
        └─http path:  POST /v1/runs/stream ──SSE──▶ chan
        │
        ▼
  internal/stream/journal.go — append every TurnEvent to stream journal
  internal/stream/writer.go  — fan out to SSE subscribers
        │
        ▼
GET /api/chat/stream?stream_id=...   (EventSource, 39 call sites in static/messages.js)
  events: token / tool / approval / done / error  (+ 30s ": heartbeat" comment)
  reconnect: ?stream_id replays missed events from journal; ?status checks active flag
        │
        ▼
done event: {session: {messages, tool_calls}, usage: {duration_seconds, tps, ...}}
  Go: persist to webui.db, set _turnDuration/_turnTps on last assistant message
  FE: _finishDone() → renderMessages(), _carryForwardEphemeralTurnFields(), scroll
        │
        ▼
GET /api/session?session_id=...  (next load / poll)
  reconciled transcript: state.db (authoritative) + sidecar tail (fresh timing)
  → worklog "Processed Ns" labels from _turnDuration
```

Key files: `internal/httpserver/chat.go`, `session_state_db.go`, `session_stream.go`,
`internal/agentclient/*.go`, `internal/stream/*`, `static/messages.js`, `static/ui.js`.

### Worklog / "Processed" label

- Live (while `S.activeStreamId`): header ticks `Processed 0s → 1s → …` from `pending_started_at`
  via `_activityProcessedElapsedLabel` + `_updateActiveActivityElapsedTimer` (1s interval).
  In-flight blink guard in `_syncToolCallGroupSummary` keeps the previous label through transient
  group recreation.
- Settled (after `done`): header becomes `Processed Ns` once from `dataset.turnDuration`
  (`_activitySettledProcessedLabel`), sourced from `usage.duration_seconds`.
- Reload: `carryTurnMetaToAllAssistants` restores `_turnDuration` on **all** assistant turns
  (content-key + positional fallback), so every historical turn keeps its `14s/20s/9s` label.
- Restart: `store.ImportSession` preserves turn meta on conflict — boot import from `state.db`
  no longer strips settled durations (655 sessions log line).

## SSE Event Contract

Exact field names — the frontend parses them verbatim (`docs/02-api-parity-mapping.md` §3):

```
token     {"text": "..."}
tool      {"name": "...", "preview": "..."}
approval  {"command": "...", "description": "...", "pattern_keys": [...]}
done      {"session": {...compact fields..., "messages": [...]}, "usage": {...}}
error     {"message": "...", "trace": "..."}
: heartbeat                          (comment line every 30s, no event name)
```

`GET /api/chat/stream/status?stream_id=X` → `{"active": bool, "stream_id": "..."}` (reconnect banner).

## Session Store & Reconciliation

- **webui.db** (`~/.hermes/webui/webui.db`, `modernc.org/sqlite` pure Go) — WebUI-observed projection.
  Schema: `sessions(session_id, title, workspace, model, messages JSON, tool_calls, created_at, updated_at, pinned, archived, project_id)` + `workspaces/settings/projects`.
- **state.db** (`~/.hermes/state.db`) — agent's authoritative transcript (tool_call_id, reasoning, token_count). Read-only from Go, `PRAGMA query_only=ON, busy_timeout=250`.

On every `GET /api/session` and `done` payload, `reconcileSessionMessages()` merges both:

- `stateRows` = authoritative base
- `sidecar` tail newer than `state.db` last timestamp is appended (fresh turn not yet flushed)
- Duplicate detection by `role + content[:80]` in a 40-row tail window
- `carryTurnMetaTo*` copies `_turnDuration/_turnTps/_firstTokenMs/_usedModel` onto the merged view
- Boot import (`importFromStateDB` in `cmd/server/main.go`) is additive and preserves live meta on conflict

## Static Assets

`static/` is copied byte-for-byte from the Python repo and served from disk (`HERMES_WEBUI_STATIC_DIR`, default `./static`). No content changes during migration phases 0–8. Single-binary deploys can switch to `embed.FS` — recommended to avoid "forgot to copy static/" bugs.

Bundle version is cache-busted via `?v=<hash>` in `static/ui.js` / `static/messages.js`.

## Running Modes

| Env | Effect |
|---|---|
| `HERMES_WEBUI_LEGACY_PROXY_URL=http://127.0.0.1:8788` | Go handles native routes, everything else proxied to Python (strangler mode) |
| `HERMES_WEBUI_LEGACY_PROXY_URL=""` (unset) | Go only — unimplemented routes 404 (no proxy) |
| `HERMES_WEBUI_AGENT_TRANSPORT=auto` | Probe grpc socket, fallback to http |
| `HERMES_WEBUI_RUNNER_BASE_URL=""` + no socket | Chat routes still mounted, but return 500 bubble (not 404 "session deleted") |

Route ownership registry: `internal/proxy/registry.go` (`NativeRoutes` map). The catch-all in `internal/httpserver/server.go:mountStaticAndProxy` checks `proxy.IsNative(path)` first.

## ctl.sh & switch-backend.sh

```bash
# ctl.sh — daemon wrapper around ./hermes-web-go
./ctl.sh start    # build not included; expects ./hermes-web-go exists
./ctl.sh stop
./ctl.sh restart
./ctl.sh status   # pid + log path
./ctl.sh logs     # tail ~/.hermes/web-go.log

# switch-backend.sh — swap the public :8787 listener
./switch-backend.sh go        # kill :8787, go build, launch Go, tail /tmp/go-webui.log
./switch-backend.sh python    # kill :8787, launch Python with isolated /tmp/pyrec-state
./switch-backend.sh status
```

Env overrides for both scripts:

```bash
HERMES_HOME=~/.hermes  HERMES_WEBUI_PORT=8787  ./ctl.sh start
PORT=8787  GO_BIN=./hermes-web-go  ./switch-backend.sh go
```

## Development

```bash
go vet ./...
go test ./... -race
go build -o hermes-web-go ./cmd/server

# endpoint inventory (regenerates docs/10-*.md counts)
python3 scripts/endpoint_inventory.py

# live reload (manual)
./hermes-web-go  # restart on file change (no watcher bundled; use air/entr if desired)
curl -s http://127.0.0.1:8787/health | jq .
```

### Project layout

```
hermes-web-go/
  cmd/server/main.go          entry: env, proxy, DB open, importFromStateDB, graceful shutdown
  internal/
    config/                   env → Config
    httpserver/               chi routers, middleware, all /api/* handlers
    proxy/                    reverse proxy + NativeRoutes registry
    store/                    SQLite open + ImportSession (meta-preserving upsert)
    data/                     state.db reader + reconciliation
    agentclient/              AgentClient interface, grpc/http impls, transport probe
    agentpb/                  generated protobuf (agent.proto)
    stream/                   SSE journal + fanout + heartbeat
    approval/                 approval store (replaces Python module-level globals)
    workspace/  upload/  crons/  skillsmem/  auth/  logs/  ...
  static/                     vanilla JS frontend (unchanged)
  proto/agent.proto           gRPC service definition
  deploy/agent-grpc-shim/     backup of shim + proto for reinstall
  docs/                       architecture, parity mapping, inventories, plans
  scripts/endpoint_inventory.py
  ctl.sh  switch-backend.sh
```

## Testing & Parity

```bash
go test ./...                          # all packages
go test ./... -race                    # race detector (required for approval/stream)
go test ./internal/httpserver -run TestChat -v
go test ./internal/agentclient -v      # transport probe + grpc ping
go test ./internal/store -run TestImportSession -v
```

Parity harness: `docs/06-testing-and-parity.md` — response shape, status codes, path-security
invariants (`safe_resolve`), upload ordering, `task_id` vs `session_id`, stream sentinel handling.
Golden fixtures in `testdata/` where noted per doc.

## Deployment

Single binary + static dir:

```bash
GOOS=linux GOARCH=amd64 go build -o hermes-web-go ./cmd/server
scp hermes-web-go static/ user@host:/opt/hermes-web-go/
# or embed static/ and ship one file (see docs/01-architecture-design.md §7)
```

Systemd / LaunchAgent should set at minimum:

```ini
Environment=HERMES_WEBUI_HOST=127.0.0.1
Environment=HERMES_WEBUI_PORT=8787
Environment=HERMES_WEBUI_DATA_ROOT=/home/you/.hermes/webui
Environment=HERMES_WEBUI_LEGACY_PROXY_URL=http://127.0.0.1:8788
Environment=HERMES_WEBUI_AGENT_SOCKET=/home/you/.hermes/webui/agent.sock
Environment=HERMES_WEBUI_AGENT_TRANSPORT=auto
```

Ensure `ai.hermes.gateway` and `ai.hermes.agent-grpc-shim` are `KeepAlive=true` so the socket
survives reboots and `hermes-agent` git pulls (reinstall shim from `deploy/agent-grpc-shim/` after pull).

## Troubleshooting

| Symptom | Check |
|---|---|
| `listening on 127.0.0.1:8787` but `agent transport: using HTTP fallback` | `ls -l ~/.hermes/webui/agent.sock` missing → `launchctl kickstart -k gui/$(id -u)/ai.hermes.agent-grpc-shim` |
| `grpc ping failed` | Shim crashed or proto mismatch — reinstall `deploy/agent-grpc-shim/` + restart shim |
| Chat returns `session deleted` / jumps to new chat | Go chat routes not mounted (no `HERMES_WEBUI_RUNNER_BASE_URL` and no socket) — set one of them |
| `Processed` label bare after reload | `state.db` import stripped timing (fixed: `store.ImportSession` now preserves meta) — restart Go, then reload page |
| `Processed 8s` flashes before answer | Live tick overwriting settled label (fixed in `static/ui.js` blink guard) — hard refresh `Cmd+Shift+R` to bust `?v=` cache |
| `504 Gateway Timeout` on `/api/*` | Legacy Python on `:8788` down — check `HERMES_WEBUI_LEGACY_PROXY_URL` and Python logs |
| `failed to open state.db` | `~/.hermes/state.db` not yet created (first run) — normal, Go falls back to JSON import |

Logs:

```bash
./ctl.sh logs                          # Go
tail -f /tmp/go-webui.log              # switch-backend Go
launchctl print gui/$(id -u)/ai.hermes.agent-grpc-shim  # shim
```

## Docs Index

- [Architecture & design](docs/01-architecture-design.md) — layout, transport design, SQLite choice, approval model
- [API parity mapping](docs/02-api-parity-mapping.md) — the 1:1 contract (all `/api/*` shapes)
- [MVP scope](docs/03-mvp-scope.md)
- [Task breakdown](docs/04-task-breakdown.md)
- [Migration strategy](docs/05-migration-strategy.md) — strangler fig, proxy registry, rollback
- [Testing & parity](docs/06-testing-and-parity.md)
- [Future Vite frontend](docs/07-future-vite-frontend.md)
- [Kanban board](docs/08-kanban-board.md)
- [Endpoint migration inventory](docs/10-endpoint-migration-inventory.md) — `python3 scripts/endpoint_inventory.py`
- [Remaining endpoint audit](docs/11-remaining-endpoint-audit.md)
- [Chat API parity](docs/chat_api_parity.md)

## Non-negotiable rule

No route is removed from the Python proxy until its Go implementation passes the parity harness.
Behavior compatibility is the release gate.
