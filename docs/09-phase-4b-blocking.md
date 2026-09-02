# Phase 4b — gRPC Fast Path: Blocking & Migration Document

## Status: SUPERSEDED — see `docs/09-phase4b-unblock.md` (authoritative)

This note diagnosed the two blockers and remains useful as *context* (why the task
was blocked, the proto contract, the "who builds what" table). For the actual fix,
treat **`docs_update/3-sept-2026-part-2/09-phase4b-unblock.md`** (merged into
`docs/09-phase4b-unblock.md`) as the source of truth. The Go side and the Hermes
Python shim are both implemented and a persistent macOS launchd service exists
(`ai.hermes.agent-grpc-shim`). Remaining work is parity testing, real-shim fallback
injection, and a soak, all tracked in `docs/04-task-breakdown.md` §4b.

Phase 4b (gRPC bidirectional streaming over Unix socket) is a **two-sided feature** — the Go
side (`hermes-web-go`) and the Python side (`hermes-agent`) must both be built, and the
Python side is the dependency gate.

## Blocking chain

### 1. [HARD] Hermes agent shim — no gRPC server (item 4b-1)

The existing gateway exposes `/v1/runs` over **HTTP only** (aiohttp, port 8642). There is no
gRPC server, no `.proto` file, no `agent.sock` anywhere in the Hermes codebase.

**What needs to exist:**
- A minimal gRPC server process (or embedded in the gateway) that:
  - Listens on a Unix domain socket (`~/.hermes/webui/agent.sock`)
  - Implements the `Agent` service (proto below)
  - Proxies to the existing internal `/v1/runs` HTTP API (no new agent logic)
  - Supports `Ping` (boot probe), `RunTurn` (bidirectional stream), `Cancel` (unary)

**Dependency:** `grpcio` + `grpcio-tools` must be installed in the Hermes venv.

### 2. [SOFT] Go-side grpcClient — partial, needs final wiring

Go-side items (4b items 2–6) are partially done:
- `proto/agent.proto` written
- `internal/agentpb/*.pb.go` generated
- `internal/agentclient/grpcclient.go` — compiles, untested
- `internal/agentclient/transport.go` — BROKEN, needs fix (undefined `grpcDialContext`, wrong return count)
- `cmd/server/main.go` — not yet wired to select transport based on `AgentTransport`/`AgentSocket`

**What needs to finish after the shim exists:**
- Fix `transport.go` — proper `NewBestClient` selector
- Wire `HERMES_WEBUI_AGENT_TRANSPORT`/`_SOCKET` into `buildHandler` in `main.go`
- Write `grpcclient_test.go` (mock gRPC server + parity test vs `httpClient`)
- Write `TestGRPCFallbackOnCrash` (mid-stream fail → auto-retry via http)
- Wire the boot-time capability probe (500ms timeout, non-fatal)
- Live-verify against real gRPC socket
- Full `-race` + vet green

### 3. [SOFT] Parity test suite (4b-5/6)

Once both sides exist, parity tests:
- Replay Phase 0 golden fixtures through `grpcClient` and `httpClient`, diff `TurnEvent` sequences
- Explicit fallback-injection: kill gRPC socket mid-session, assert transparent fallback to HTTP
- Load/soak `auto` mode for a full day before making gRPC the assumed default

## Proto contract

File: `hermes-web-go/proto/agent.proto` (also needed at `hermes-agent/proto/`)

```protobuf
syntax = "proto3";
package hermes.agent.v1;
option go_package = "hermes-web-go/internal/agentpb;agentpb";
option py_package = "proto.agent_pb2";

service Agent {
  rpc Ping(PingRequest) returns (PingResponse);
  rpc RunTurn(RunTurnRequest) returns (stream TurnEvent);
  rpc Cancel(CancelRequest) returns (CancelResponse);
}

message PingRequest {}
message PingResponse { string version = 1; }
message RunTurnRequest {
  string session_id = 1;
  string task_id = 2;
  string message = 3;
  string workspace = 4;
  string model = 5;
  string provider = 6;
  string history_json = 7;
  repeated string attachments = 8;
}
message TurnEvent {
  string type = 1;
  string text = 2;
  string name = 3;
  string preview = 4;
  string data_json = 5;
  string error = 6;
}
message CancelRequest { string session_id = 1; }
message CancelResponse {}
```

## Who builds what

| Side | Files | Priority |
|------|-------|----------|
| **Python** (hermes-agent) | `gateway/platforms/agent_grpc.py` — gRPC server wrapping `/v1/runs` HTTP | **MUST go first** |
| **Python** (hermes-agent) | `proto/agent.proto` — copy from web-go or use as single source | MUST |
| **Python** (hermes-agent) | `venv` — install `grpcio`, `grpcio-tools` | MUST |
| **Go** (hermes-web-go) | `internal/agentclient/transport.go` — fix compile errors | After shim exists |
| **Go** (hermes-web-go) | `cmd/server/main.go` — wire transport selection | After transport.go |
| **Go** (hermes-web-go) | `internal/agentclient/grpcclient_test.go` — mock + parity | After shim exists |
| **Go** (hermes-web-go) | All tests + live verification | Last |

## What `hermes-web-go` already has (uncommitted, 4a + partial 4b)

Current working tree (on `main`, not pushed):

```
M cmd/server/main.go
M internal/config/config.go
M internal/httpserver/middleware.go
M internal/httpserver/server.go
M internal/stream/registry.go
?? ctl.sh
?? internal/auth/           # Phase 7
?? internal/httpserver/auth.go|_test.go  # Phase 7
?? internal/agentpb/        # Phase 4b — generated protobuf
?? proto/agent.proto        # Phase 4b — proto definition
?? internal/agentclient/grpcclient.go  # Phase 4b — compiles, untested
?? internal/agentclient/transport.go   # Phase 4b — BROKEN, needs fix
```

**4a changes (proxy removal):**
- `NewRouterWithAgent` no longer proxies `/api/chat*` to Python
- `allowChatProxy` flag — only `NewRouterWithData` keeps the proxy fallback
- All tests pass with `-race`

**4b changes (partial):**
- `internal/agentpb/` — generated Go code from proto
- `internal/agentclient/grpcclient.go` — GRPCClient struct, `RunTurn`/`Cancel`/`Close`
- `internal/agentclient/transport.go` — BROKEN, needs `grpcDialContext` helper and proper return type

## Quick start for higher model

1. **Python side first:** In `hermes-agent`, install `grpcio`, copy `proto/agent.proto`, generate Python stubs, write gRPC server wrapping existing `/v1/runs` HTTP API.
2. After shim is live: fix `transport.go` in `hermes-web-go`, wire `buildHandler`, run tests.
3. Parity test, soak, then cutover.

## Reference

- `hermes-web-go` repo: `/Users/adityahimawan/Development/hermes-web-go/`
- `hermes-agent` repo: `/Users/adityahimawan/.hermes/hermes-agent/`
- Gateway HTTP `/v1/runs` API: `gateway/platforms/api_server.py` + `api_server_runs.py`
- Design doc: `hermes-web-go/docs/01-architecture-design.md` §2b
- Task breakdown: `hermes-web-go/docs/04-task-breakdown.md` §4b