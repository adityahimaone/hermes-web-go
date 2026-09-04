# 09 — Phase 4b Unblock: gRPC Transport Fix (Go + Python)

Supersedes the standalone "Phase 4b — Blocking & Migration" note. That note correctly diagnosed
two problems; this doc fixes both with concrete code. Keep the note's "who builds what" table for
tracking, but treat **this doc as the source of truth for the actual fix**.

## Recap of the two blockers

1. **[HARD] Python side has no gRPC server.** The gateway only exposes `/v1/runs` over HTTP
   (aiohttp, port 8642). No `.proto`, no socket, no gRPC server exists yet. This blocks
   `grpcClient` from ever having anything to talk to.
2. **[SOFT] Go side `internal/agentclient/transport.go` is broken.** Undefined
   `grpcDialContext`, wrong return count from the selector function. This blocks compilation of
   the transport-selection logic even once the Python side exists.

Fix order matters: **fix Go's compile error first** (it's cheap and unblocks testing the fallback
logic against a fake/mock gRPC server immediately), **then** stand up the real Python shim. You
do not have to wait for the Python side to finish (1) — a mock `agentpb.AgentServer` in a Go test
is enough to validate the selector, probe, and fallback logic end-to-end before the real shim
exists.

## 1. Fix: `internal/agentclient/transport.go`

Root cause per the blocking note: `grpcDialContext` is referenced but never defined, and the
selector function returns the wrong number of values (likely returning just `AgentClient` where
callers expect `(AgentClient, error)`, or vice versa). Full corrected file below — adjust package-
relative import paths (`agentpb`, `httpClient` constructor name) to match what's actually in your
tree; the logic and signatures are the part that matters.

```go
// internal/agentclient/transport.go
package agentclient

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"hermes-web-go/internal/agentpb"
)

// TransportMode selects which AgentClient implementation NewBestClient builds.
// Values come directly from HERMES_WEBUI_AGENT_TRANSPORT — "" behaves like "auto".
type TransportMode string

const (
	TransportAuto TransportMode = "auto"
	TransportGRPC TransportMode = "grpc"
	TransportHTTP TransportMode = "http"
)

// TransportConfig carries everything NewBestClient needs. Zero-value Mode
// behaves as TransportAuto; zero-value ProbeTimeout defaults to 500ms.
type TransportConfig struct {
	Mode         TransportMode // HERMES_WEBUI_AGENT_TRANSPORT
	SocketPath   string        // HERMES_WEBUI_AGENT_SOCKET
	ProbeTimeout time.Duration
}

// grpcDialContext dials a gRPC server over a Unix domain socket. This was the
// missing symbol — grpc-go treats "unix:<path>" as a first-class target
// scheme via its passthrough resolver, so no custom net.Dialer is required.
func grpcDialContext(ctx context.Context, socketPath string) (*grpc.ClientConn, error) {
	if socketPath == "" {
		return nil, fmt.Errorf("agentclient: empty socket path")
	}
	target := "unix:" + socketPath
	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("agentclient: dial %s: %w", target, err)
	}
	return conn, nil
}

// probeGRPC attempts a bounded-time Ping against the socket. A non-nil error
// here is EXPECTED and NON-FATAL whenever the shim isn't running yet or
// hasn't been upgraded — callers must treat it purely as "use http", never
// log it above info level and never fail startup because of it.
func probeGRPC(ctx context.Context, socketPath string, timeout time.Duration) (*grpc.ClientConn, error) {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := grpcDialContext(probeCtx, socketPath)
	if err != nil {
		return nil, err
	}

	client := agentpb.NewAgentClient(conn)
	if _, err := client.Ping(probeCtx, &agentpb.PingRequest{}); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("agentclient: grpc ping failed: %w", err)
	}
	return conn, nil
}

// NewBestClient is the single call site cmd/server/main.go should use. It
// returns (AgentClient, error) — exactly two values, which is the return-count
// fix the blocking note called out. It NEVER errors out of a missing/broken
// gRPC socket in "auto" mode; that condition always degrades to httpClient.
// It only errors if TransportGRPC is explicitly forced and unavailable, or if
// Mode is an unrecognized string — both are misconfiguration, not runtime
// flakiness, so failing loudly there is correct.
func NewBestClient(ctx context.Context, cfg TransportConfig, fallback *HTTPClient) (AgentClient, error) {
	timeout := cfg.ProbeTimeout
	if timeout <= 0 {
		timeout = 500 * time.Millisecond
	}

	switch cfg.Mode {
	case TransportHTTP:
		slog.Info("agent transport: http (forced by config)")
		return fallback, nil

	case TransportGRPC:
		conn, err := probeGRPC(ctx, cfg.SocketPath, timeout)
		if err != nil {
			return nil, fmt.Errorf("agent transport: grpc forced but unavailable: %w", err)
		}
		slog.Info("agent transport: grpc (forced by config)", "socket", cfg.SocketPath)
		return NewGRPCClient(conn, fallback), nil

	case TransportAuto, "":
		conn, err := probeGRPC(ctx, cfg.SocketPath, timeout)
		if err != nil {
			slog.Info("agent transport: using HTTP fallback (grpc socket unavailable)",
				"socket", cfg.SocketPath, "reason", err)
			return fallback, nil
		}
		slog.Info("agent transport: grpc", "socket", cfg.SocketPath)
		return NewGRPCClient(conn, fallback), nil

	default:
		return nil, fmt.Errorf("agent transport: unknown mode %q (want auto|grpc|http)", cfg.Mode)
	}
}
```

## 2. Fill in `internal/agentclient/grpcclient.go` (RunTurn/Cancel with per-call fallback)

The blocking note says this file "compiles, untested." The one design decision worth being
explicit about, since it directly affects the fallback-injection test the task list already asks
for: **fallback happens per-call (i.e. on the *next* `RunTurn`), not by resuming a stream that has
already started emitting tokens.** Silently retrying mid-stream risks re-running tool calls that
already had side effects (wrote a file, sent a message) — that's worse than surfacing a clean
error for the in-flight turn and letting the *next* message succeed over HTTP. This matches the
task list's own wording ("assert the **next turn** transparently falls back") — so this isn't a
new design, just making the existing plan's intent explicit in code:

```go
// internal/agentclient/grpcclient.go
package agentclient

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"google.golang.org/grpc"

	"hermes-web-go/internal/agentpb"
)

// GRPCClient implements AgentClient over a gRPC bidirectional stream. It always
// holds a non-nil fallback (the http AgentClient) and falls back to it whenever
// the stream cannot be *established* — never mid-stream, see doc note above.
type GRPCClient struct {
	conn     *grpc.ClientConn
	client   agentpb.AgentClient
	fallback AgentClient
}

func NewGRPCClient(conn *grpc.ClientConn, fallback AgentClient) *GRPCClient {
	return &GRPCClient{
		conn:     conn,
		client:   agentpb.NewAgentClient(conn),
		fallback: fallback,
	}
}

func (g *GRPCClient) RunTurn(ctx context.Context, req TurnRequest) (<-chan TurnEvent, error) {
	stream, err := g.client.RunTurn(ctx, &agentpb.RunTurnRequest{
		SessionId:   req.SessionID,
		TaskId:      req.TaskID,
		Message:     req.Message,
		Workspace:   req.Workspace,
		Model:       req.Model,
		Provider:    req.Provider,
		HistoryJson: req.HistoryJSON,
		Attachments: req.Attachments,
	})
	if err != nil {
		slog.Warn("agent transport: grpc RunTurn failed to start, falling back to http", "err", err)
		return g.fallback.RunTurn(ctx, req)
	}

	out := make(chan TurnEvent, 16)
	go func() {
		defer close(out)
		for {
			ev, err := stream.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				// Mid-stream failure: surface ONE clean error event and stop.
				// Do not silently retry here — see design note above.
				select {
				case out <- TurnEvent{Type: "error", Error: fmt.Sprintf("agent stream interrupted: %v", err)}:
				case <-ctx.Done():
				}
				return
			}
			select {
			case out <- TurnEvent{
				Type:     ev.Type,
				Text:     ev.Text,
				Name:     ev.Name,
				Preview:  ev.Preview,
				DataJSON: ev.DataJson,
				Error:    ev.Error,
			}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (g *GRPCClient) Cancel(ctx context.Context, sessionID string) error {
	if _, err := g.client.Cancel(ctx, &agentpb.CancelRequest{SessionId: sessionID}); err != nil {
		slog.Warn("agent transport: grpc Cancel failed, falling back to http", "err", err)
		return g.fallback.Cancel(ctx, sessionID)
	}
	return nil
}

func (g *GRPCClient) Close() error {
	return g.conn.Close()
}
```

## 3. Wire into `cmd/server/main.go`

```go
transportCfg := agentclient.TransportConfig{
	Mode:       agentclient.TransportMode(os.Getenv("HERMES_WEBUI_AGENT_TRANSPORT")),
	SocketPath: envOrDefault("HERMES_WEBUI_AGENT_SOCKET",
		filepath.Join(stateDir, "agent.sock")),
}
httpFallback := agentclient.NewHTTPClient(gatewayURL) // whatever the existing 4a constructor is
agent, err := agentclient.NewBestClient(context.Background(), transportCfg, httpFallback)
if err != nil {
	log.Fatalf("agent transport init: %v", err) // only reachable on misconfiguration, see §1
}
```

Omitting both env vars entirely must produce identical behavior to today's Phase 4a (pure
`httpClient`, since the socket won't exist and the probe will fail harmlessly) — write this as an
explicit test (`TestNewBestClient_NoSocketFallsBackToHTTP`) rather than trusting it by inspection.

## 4. Tests to add now, before the Python shim exists

These only need a **mock gRPC server implementing `agentpb.AgentServer`** (grpc-go's standard
testing pattern — an in-process `bufconn` listener or a real Unix socket in a temp dir), not the
real Hermes shim. Write these first; they unblock validating (2) above independent of (1):

```go
// internal/agentclient/transport_test.go
func TestNewBestClient_NoSocketFallsBackToHTTP(t *testing.T) {
	cfg := TransportConfig{Mode: TransportAuto, SocketPath: "/tmp/does-not-exist.sock"}
	client, err := NewBestClient(context.Background(), cfg, fakeHTTPClient(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := client.(*HTTPClient); !ok {
		t.Fatalf("expected HTTPClient fallback, got %T", client)
	}
}

func TestNewBestClient_ForcedGRPCUnavailableErrors(t *testing.T) {
	cfg := TransportConfig{Mode: TransportGRPC, SocketPath: "/tmp/does-not-exist.sock"}
	_, err := NewBestClient(context.Background(), cfg, fakeHTTPClient(t))
	if err == nil {
		t.Fatal("expected error when grpc is forced but socket is unavailable")
	}
}

func TestNewBestClient_AutoPrefersGRPCWhenAvailable(t *testing.T) {
	sock := startMockAgentServer(t) // helper: bufconn or temp-dir unix socket + agentpb.RegisterAgentServer
	cfg := TransportConfig{Mode: TransportAuto, SocketPath: sock}
	client, err := NewBestClient(context.Background(), cfg, fakeHTTPClient(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := client.(*GRPCClient); !ok {
		t.Fatalf("expected GRPCClient when socket is healthy, got %T", client)
	}
}

func TestGRPCFallbackOnCrash(t *testing.T) {
	sock := startMockAgentServer(t)
	cfg := TransportConfig{Mode: TransportAuto, SocketPath: sock}
	client, _ := NewBestClient(context.Background(), cfg, fakeHTTPClient(t))

	stopMockAgentServer(t, sock) // simulate the shim dying mid-session

	events, err := client.RunTurn(context.Background(), TurnRequest{SessionID: "s1", Message: "hi"})
	if err != nil {
		t.Fatalf("expected transparent fallback, got error: %v", err)
	}
	// Assert the events came from the fake HTTP client, e.g. via a sentinel
	// field your fakeHTTPClient stamps onto every TurnEvent it produces.
}
```

This directly satisfies the task list's still-open items: *"Write `grpcclient_test.go` (mock gRPC
server + parity test vs `httpClient`)"* and *"Write `TestGRPCFallbackOnCrash`"*.

## 5. The real blocker: Python gRPC shim (`gateway/platforms/agent_grpc.py`)

This is the piece nothing above can be validated against for real. Skeleton below — it adds **no
new agent logic**, purely wraps the existing `/v1/runs` HTTP API described in
`api_server_runs.py`. Verify the actual request/response field names in that file before treating
this as final; the shape below is a best-effort guess based on the proto contract you already
wrote, not confirmed against `api_server_runs.py` source.

```python
# gateway/platforms/agent_grpc.py
"""Thin gRPC adapter in front of the existing /v1/runs HTTP API.

No new agent logic lives here. This process's only job is to give the Go
WebUI a lower-latency, bidirectional-streaming transport option over a Unix
domain socket, while every other client keeps using the HTTP API unchanged.
"""
import asyncio
import json
import os

import grpc
import httpx

from proto import agent_pb2, agent_pb2_grpc

GATEWAY_HTTP_BASE = os.environ.get("HERMES_GATEWAY_HTTP_URL", "http://127.0.0.1:8642")
SOCKET_PATH = os.environ.get(
    "HERMES_WEBUI_AGENT_SOCKET",
    os.path.expanduser("~/.hermes/webui/agent.sock"),
)


class AgentServicer(agent_pb2_grpc.AgentServicer):
    def __init__(self):
        self._http = httpx.AsyncClient(base_url=GATEWAY_HTTP_BASE, timeout=None)

    async def Ping(self, request, context):
        return agent_pb2.PingResponse(version="hermes-agent-grpc-shim/0.1")

    async def RunTurn(self, request, context):
        # 1. Start the run via the existing endpoint — verify field names
        #    against api_server_runs.py before trusting this payload shape.
        payload = {
            "session_id": request.session_id,
            "task_id": request.task_id,
            "message": request.message,
            "workspace": request.workspace,
            "model": request.model,
            "provider": request.provider,
            "history": json.loads(request.history_json) if request.history_json else [],
            "attachments": list(request.attachments),
        }
        resp = await self._http.post("/v1/runs", json=payload)
        resp.raise_for_status()
        run_id = resp.json()["run_id"]  # verify this key name against source

        # 2. Re-stream the run's existing event feed as TurnEvents. If
        #    /v1/runs/{id}/events is already SSE, use httpx's streaming
        #    response (as below) rather than polling — polling would
        #    reintroduce exactly the latency this whole effort exists to
        #    remove.
        async with self._http.stream("GET", f"/v1/runs/{run_id}/events") as stream:
            async for line in stream.aiter_lines():
                if not line.strip():
                    continue
                event = json.loads(line)
                yield agent_pb2.TurnEvent(
                    type=event.get("type", ""),
                    text=event.get("text", ""),
                    name=event.get("name", ""),
                    preview=event.get("preview", ""),
                    data_json=json.dumps(event.get("data", {})),
                    error=event.get("error", ""),
                )
                if event.get("type") in ("done", "error"):
                    return

    async def Cancel(self, request, context):
        # Verify this endpoint exists; if /v1/runs has no cancel route yet,
        # this is a second, smaller gap to close before Cancel can work over
        # grpc — Cancel is unary and low-risk to add native support for.
        await self._http.post("/v1/runs/cancel", json={"session_id": request.session_id})
        return agent_pb2.CancelResponse()


async def serve():
    server = grpc.aio.server()
    agent_pb2_grpc.add_AgentServicer_to_server(AgentServicer(), server)

    if os.path.exists(SOCKET_PATH):
        os.remove(SOCKET_PATH)
    os.makedirs(os.path.dirname(SOCKET_PATH), exist_ok=True)
    server.add_insecure_port(f"unix://{SOCKET_PATH}")

    await server.start()
    print(f"agent grpc shim listening on unix://{SOCKET_PATH}")
    await server.wait_for_termination()


if __name__ == "__main__":
    asyncio.run(serve())
```

Setup steps on the Python side (per the original blocking note, restated as concrete commands):

```bash
cd ~/.hermes/hermes-agent
pip install grpcio grpcio-tools
mkdir -p proto
cp /path/to/hermes-web-go/proto/agent.proto proto/agent.proto
python -m grpc_tools.protoc -Iproto --python_out=proto --grpc_python_out=proto proto/agent.proto
# then run gateway/platforms/agent_grpc.py alongside (not instead of) the existing
# aiohttp gateway process — two processes, same host, until this is proven stable
```

## 6. Updated unblock order (concrete, supersedes the note's generic table)

1. **Now, no dependency:** fix `transport.go` (§1) + `grpcclient.go` (§2) + the mock-server tests
   (§4). This alone turns "broken, doesn't compile" into "compiles, tested against a fake, proven
   correct fallback logic" — real progress with zero Python-side work.
2. **Python, can start in parallel with 1:** generate stubs, write `agent_grpc.py` (§5), verify the
   two payload-shape TODOs against `api_server_runs.py`, run it standalone, confirm `Ping` responds
   over the real Unix socket from a throwaway Go client or `grpcurl -unix-socket`.
3. **Integration:** point the real Go binary at the real socket (`HERMES_WEBUI_AGENT_TRANSPORT=grpc`
   forced, not `auto`, for this step — you want failures loud during integration testing, not
   silently falling back), run one real chat turn end-to-end, diff its `TurnEvent` sequence against
   an equivalent HTTP-path run per the existing Phase 0 golden-fixture approach.
4. **Before treating the shim as deployed at all: make it a supervised, persistent service, not a
   process you started by hand in a terminal** — see §7. A manually-run shim that dies when your
   SSH session ends is not "the fast path is live," it's "the fast path was live for a few minutes."
5. **Only then:** flip the default to `auto` in deployment, soak for a day per the existing task
   list, then consider `grpc` the assumed transport.

## 7. Persistent service supervision for the shim (closes "runs manually, not as a service")

Current observed state — shim process clean, socket absent, no crash, `Ping` previously answered
`hermes-agent-grpc-shim/0.1` — is exactly what `auto` mode is *for*: Go silently fell back to
`httpClient` and nothing broke. That's the safety net working as designed. But it's not a
deployment state to leave things in; the shim needs to (a) start on boot, (b) restart on crash,
(c) never leave two copies fighting over the same socket (which is almost certainly how you got
"2 orphan process entries" — a restart without cleanly killing the previous one), and (d) clean up
a stale socket file on startup so a crash-then-restart doesn't get "address already in use."

### 7a. Prevent orphans: single-instance guard inside the shim itself

Don't rely on the process manager alone to prevent double-starts — add a `flock`-based guard so a
second instance refuses to start rather than fighting the first one over the socket:

```python
# gateway/platforms/agent_grpc.py — add near the top, before serve()
import fcntl
import sys

PID_LOCK_PATH = os.environ.get(
    "HERMES_WEBUI_AGENT_SHIM_LOCK",
    os.path.expanduser("~/.hermes/webui/agent_grpc_shim.lock"),
)

def acquire_single_instance_lock():
    """Refuse to start if another instance already holds this lock. Returns the
    open file handle — keep a reference for the process lifetime, or the lock
    releases early."""
    os.makedirs(os.path.dirname(PID_LOCK_PATH), exist_ok=True)
    lock_file = open(PID_LOCK_PATH, "w")
    try:
        fcntl.flock(lock_file, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        print(f"agent grpc shim: another instance already holds {PID_LOCK_PATH}, exiting")
        sys.exit(1)
    lock_file.write(str(os.getpid()))
    lock_file.flush()
    return lock_file  # caller must keep this alive


async def serve():
    _lock_handle = acquire_single_instance_lock()  # noqa: F841 — kept alive intentionally

    server = grpc.aio.server()
    agent_pb2_grpc.add_AgentServicer_to_server(AgentServicer(), server)

    # Stale socket cleanup — safe now that the lock above guarantees we're the
    # only instance that could legitimately hold it.
    if os.path.exists(SOCKET_PATH):
        os.remove(SOCKET_PATH)
    os.makedirs(os.path.dirname(SOCKET_PATH), exist_ok=True)
    server.add_insecure_port(f"unix://{SOCKET_PATH}")

    await server.start()
    print(f"agent grpc shim listening on unix://{SOCKET_PATH}")
    await server.wait_for_termination()
```

This turns "kill orphan processes by hand" into "the second copy refuses to start, loudly, in the
log" — the failure mode becomes visible instead of silent socket contention.

### 7b. Run it as a systemd service (matches your existing Hetzner VPS deployment)

Since [[vps-infrastructure]] is already a systemd-managed Hetzner box, give the shim its own unit
rather than a background `nohup`/`screen` session — this is what actually fixes "runs manually":

```ini
# /etc/systemd/system/hermes-agent-grpc-shim.service
[Unit]
Description=Hermes Agent gRPC shim (Unix socket adapter over /v1/runs)
After=network.target hermes-agent-gateway.service
Wants=hermes-agent-gateway.service

[Service]
Type=simple
User=%i
WorkingDirectory=/home/%i/.hermes/hermes-agent
Environment=HERMES_GATEWAY_HTTP_URL=http://127.0.0.1:8642
Environment=HERMES_WEBUI_AGENT_SOCKET=/home/%i/.hermes/webui/agent.sock
ExecStart=/home/%i/.hermes/hermes-agent/venv/bin/python -m gateway.platforms.agent_grpc
Restart=on-failure
RestartSec=2
# Cap restart frequency so a genuinely broken shim doesn't spin forever and
# spam the log — after 5 failures in 60s, systemd stops trying and you get a
# clear "failed" unit state to investigate, instead of infinite silent retries.
StartLimitIntervalSec=60
StartLimitBurst=5

[Install]
WantedBy=multi-user.target
```

```bash
# enable + start
sudo systemctl daemon-reload
sudo systemctl enable --now hermes-agent-grpc-shim@$(whoami).service

# verify
sudo systemctl status hermes-agent-grpc-shim@$(whoami).service
journalctl -u hermes-agent-grpc-shim@$(whoami).service -f
```

Adjust the `%i`/template-unit syntax if you'd rather hardcode the user — templated is only useful
if you might ever run this for more than one OS user, which a single-user homelab box probably
never needs; a plain (non-templated) unit with the paths hardcoded is simpler and equally correct
here.

### 7c. Fold into `ctl.sh` (Phase 7 item, brought forward for this one script)

Phase 7 already plans a `ctl.sh`-equivalent daemon script for the Go binary. Extend that same
script now to cover the shim's systemd unit too, so `ctl.sh status` reports on both processes in
one place instead of you having to remember two different commands:

```bash
# ctl.sh — add alongside the existing Go-binary start/stop/status/logs commands
shim_status() {
    systemctl is-active --quiet hermes-agent-grpc-shim@"$USER".service \
        && echo "agent-grpc-shim: running" \
        || echo "agent-grpc-shim: stopped"
}
shim_restart() {
    sudo systemctl restart hermes-agent-grpc-shim@"$USER".service
}
```

### 7d. Verify the fix actually closes the gap you reported

- [ ] `sudo systemctl enable --now hermes-agent-grpc-shim@$(whoami).service`
- [ ] Reboot the VPS (or at minimum, kill your SSH session / the manual process) — confirm the
      socket comes back on its own, with **zero manual intervention**, which is the actual bar
      "not a persistent service" was failing.
- [ ] `kill -9` the shim's PID directly (simulating a crash, not a clean stop) — confirm systemd
      restarts it within `RestartSec` and the socket reappears.
- [ ] Start a second copy by hand while the service is running — confirm it exits immediately with
      the "another instance already holds the lock" message from §7a, rather than silently fighting
      over the socket (this is almost certainly what produced the "2 orphan process entries" you
      found — worth confirming the fix actually prevents recurrence, not just cleaning up after it
      once).
- [ ] With the service running, restart the Go binary in `auto` mode — confirm the boot log now
      says `agent transport: grpc` instead of the `HTTP fallback` line, proving Go picked up the
      now-persistent shim automatically.

