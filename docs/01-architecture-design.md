# 01 — Architecture & Design

## Phase 0 chat decision

Fork default chat is **in-process**: `api.agent_runtime` lazily loads `run_agent.AIAgent`, and `/api/chat` plus `/api/chat/start` execute agent inside WebUI process. Source `README.md` documents chat migration scope but does not document `HERMES_WEBUI_CHAT_BACKEND=gateway`; gateway mode is described in this design as a proposed/conditional integration. Phase 4 must retain an **agent shim** fallback for parity unless deployment explicitly enables and validates gateway mode. Go must not assume `HERMES_API_URL` alone provides current WebUI agent contract.

## 1. Package layout

```
hermes-webui-go/
  cmd/
    server/main.go            Entry point: flags/env parsing, boot sequence, graceful shutdown
  internal/
    config/                   Env var loading, discovery of HERMES_HOME, config.yaml parsing
    httpserver/                Router setup (chi), middleware (logging, auth, recover), static file serving
    proxy/                     Reverse-proxy layer to legacy Python backend (phase 1-7 only; deleted in phase 8)
    session/                   Session model, store interface, SQLite implementation
    workspace/                 File ops: list_dir, read_file, safe path resolution, trust levels
    upload/                    Multipart parsing + handler
    stream/                    SSE engine: stream registry, event types, heartbeat
    approval/                  Approval state machine (replaces Python module-level _pending)
    agentclient/               Transport client to the Hermes agent (chat execution delegate).
                               Two implementations behind one interface — see §2b — selected at
                               runtime, with automatic fallback so the web UI never errors out.
    crons/                     Thin client wrapping cron.jobs-equivalent HTTP calls (or direct DB/file read if cron state is file-based)
    skillsmem/                 Skills + memory panel read endpoints
    auth/                      Optional password auth, signed cookies
  static/                      UNCHANGED — copied byte-for-byte from the existing repo
  migrations/                  SQLite schema migrations (goose or golang-migrate)
  Dockerfile
  ctl.sh                       Same daemon-wrapper UX as today, now wrapping a Go binary
  go.mod / go.sum
```

Rule of thumb ported directly from the Python `ARCHITECTURE.md` Phase A lesson: **keep the
entrypoint thin.** `cmd/server/main.go` should look like `server.py` after its own Phase A —
a router + middleware stack + `main()`, with all real logic in `internal/*` packages. Do not
let `main.go` grow into a monolith the way the pre-Phase-A Python file did.

## 2. Why Go does not re-implement the agent loop

The Python `AIAgent` class (imported from `run_agent`) owns: tool selection per platform,
the LLM call loop, `stream_delta_callback`/`tool_progress_callback` wiring, memory/skills/todo
state, and the `run_conversation(user_message=, conversation_history=, task_id=)` contract
(note: **`task_id`, not `session_id`** — this is Critical Rule #3 from the original doc and it
still matters wherever Go builds the request to whatever executes the agent turn).

Re-implementing that in Go would not be a "web UI rewrite" — it would be reimplementing Hermes
itself, in a different language, which is explicitly out of scope ("makes it more lightweight",
not "replace the agent"). So the Go server's job for chat is narrower and clearer:

1. Own the HTTP/SSE surface exactly as today (`/api/chat/start`, `/api/chat/stream`, `/api/chat`).
2. Own session persistence exactly as today (append the turn, save, compute title).
3. Own the approval surfacing/polling exactly as today.
4. For **the actual model+tool turn**, call out to the existing Hermes agent process over its
   already-supported gateway HTTP API (`HERMES_API_URL`, OpenAI-compatible via 9router on
   `127.0.0.1:8642`) using **the same execution shape** the Python `runner-local` adapter mode
   already establishes in the upstream project (WebUI as a client to an external runner). We are
   not inventing a new integration point — we're standardizing on one that already exists.

This has a nice side effect: it also gives you a clean seam for the "IDX / market-alpha" and other
Hermes skills you already run on the VPS — they keep running exactly as they do today, on the
Python/Hermes side, completely undisturbed by this rewrite.

If, on inspection of the actual fork, no gateway/runner mode is wired up yet and the fork only
supports in-process `AIAgent` calls: the fallback is a **thin Python "agent shim" process** —
a small script that wraps `AIAgent(...).run_conversation(...)` and exposes it over the transport
described in §2b below (not necessarily REST — see below). This shim is not a rewrite of the
agent, and is the smallest possible Python surface left after migration — explicitly called out
in `05-migration-strategy.md` as the one long-lived piece of Python that's expected to remain (by
design, not as tech debt) unless/until the agent core itself is ported.

## 2b. Go ↔ Hermes transport: fast path with a safe fallback

There are two distinct hops in this system, and they are optimized independently:

1. **Browser ↔ Go** stays SSE, unchanged, for as long as the vanilla-JS frontend exists (Phase 9
   gates any change here — see `07-future-vite-frontend.md`). Not part of this section.
2. **Go ↔ Hermes agent** is an internal hop this rewrite fully controls, and is where the speed
   improvement below actually applies.

**Why plain HTTP-to-gateway is slower than it needs to be:** if each chat turn opens a fresh HTTP
connection to `127.0.0.1:8642`, every turn pays a TCP handshake + HTTP header parse on top of
going through the full TCP/IP loopback stack, even though Go and the Hermes agent process live on
the same host.

**Target design — `agentclient` behind one interface, two implementations:**

```go
type AgentClient interface {
    RunTurn(ctx context.Context, req TurnRequest) (<-chan TurnEvent, error)
    Cancel(ctx context.Context, sessionID string) error
}
```

| Implementation | Transport | When used |
|---|---|---|
| `grpcClient` (preferred) | gRPC bidirectional streaming over a **Unix domain socket** (e.g. `~/.hermes/webui/agent.sock`) | Default, once the Hermes-side shim/gateway supports it |
| `httpClient` (fallback) | Plain HTTP to the existing gateway (`HERMES_API_URL` / 9router on `127.0.0.1:8642`), same as originally planned in §2 | Used automatically if the gRPC socket is unavailable, and always available as a manual override |

Why this combination:
- **Unix domain socket** skips the TCP/IP stack entirely (same-host, kernel just copies bytes) —
  free latency win regardless of what protocol rides on top of it.
- **gRPC bidirectional streaming** replaces JSON-per-token with binary protobuf, and — because
  it's bidirectional — lets Go send a `Cancel` on the *same* open stream instead of a separate
  HTTP call (meaningfully faster stop-generation and tighter approval round-trips).
- **One persistent connection, reused across turns** (not opened fresh per message) removes the
  handshake tax that matters more than the wire format itself. `grpcClient` keeps a long-lived
  connection (HTTP/2 multiplexes multiple concurrent session streams over it) instead of dialing
  per turn.

### Never breaking the existing flow

This is a pure internal optimization — it must be invisible to the browser and must never turn
a previously-working chat into an error:

1. **Startup capability probe, not a hard requirement.** On boot, `agentclient` attempts to dial
   `agent.sock`. If it succeeds and the shim responds to a lightweight handshake/ping RPC, use
   `grpcClient`. If the socket doesn't exist, the dial fails, or the handshake times out (short
   timeout, e.g. 500ms — this must never make boot slow), fall back to `httpClient` silently and
   log at info level (`"agent transport: using HTTP fallback (grpc socket unavailable)"`), not
   error level — this is an expected, supported mode, not a failure.
2. **Per-call fallback, not just per-boot.** If `grpcClient.RunTurn` fails outright (connection
   drop, socket gone mid-run — the shim restarted, say), `agentclient` retries that specific call
   once against `httpClient` before surfacing an error to the SSE stream. The user should see
   "a bit slower this turn," never "chat is broken."
3. **Feature flag for manual override.** `HERMES_WEBUI_AGENT_TRANSPORT=grpc|http|auto` (default
   `auto` = the probe-then-fallback behavior above). This gives an escape hatch during Phase 4
   rollout: force `http` if the gRPC path misbehaves in the field, with zero code changes.
4. **Identical `TurnEvent` shape regardless of transport.** Both implementations translate their
   wire format into the same internal `TurnEvent` struct (`token`/`tool`/`approval`/`done`/`error`
   — the same fields `internal/stream` already expects per §4 below). The SSE layer, the session
   save logic, and the approval surfacing code have **zero knowledge of which transport is in
   use**. This is what actually guarantees "web UI doesn't error" — the fast path and the safe
   path are indistinguishable above the `agentclient` interface.
5. **Golden-fixture parity applies to both.** Per `06-testing-and-parity.md`, replay the same
   fixture chats through `httpClient` and (once available) `grpcClient` and assert identical
   `TurnEvent` sequences. The gRPC path is not allowed to ship until it passes the exact same
   parity suite the HTTP path already passes.
6. **Rollout order:** ship `httpClient` first (this is what Phase 4 originally planned — no risk
   change there), get it soaking in production per the existing Phase 4 plan, *then* add
   `grpcClient` + the shim's gRPC server as a follow-up within Phase 4, defaulting to `auto` so it
   only activates once the socket is actually present — meaning existing installs keep working
   unmodified the moment this ships, and only pick up the speed win once the shim is upgraded too.

## 3. Session store: SQLite vs. JSON files

Current Python: one JSON file per session (`sessions/{id}.json`) + `_index.json` for O(1) listing,
with an in-process `OrderedDict` LRU cache capped at 100.

Go decision: **SQLite table**, not JSON files. Rationale:
- Removes the entire class of Python TD6 "full directory scan" problem structurally — `ORDER BY
  updated_at DESC LIMIT ? OFFSET ?` replaces the index-file hack entirely, and pagination (called
  out as a "future improvement" in the Python roadmap, Phase F/J) becomes close to free.
- `modernc.org/sqlite` is pure Go (no CGO), so the "lightweight, single static binary" goal holds.
- Still trivially backup-able (single file), still greppable enough for debugging with the
  `sqlite3` CLI, which preserves the "easy to poke at from a terminal" spirit of the original.

Schema sketch (finalize in migration files, not here):

```sql
CREATE TABLE sessions (
  session_id   TEXT PRIMARY KEY,      -- keep the same 12-char hex format for FE compatibility
  title        TEXT NOT NULL DEFAULT '',
  workspace    TEXT NOT NULL,
  model        TEXT NOT NULL,
  messages     TEXT NOT NULL,         -- JSON array, OpenAI-format, stored as TEXT (not normalized —
                                       -- matches current "just a blob" semantics, avoids scope creep)
  tool_calls   TEXT NOT NULL DEFAULT '[]',
  created_at   REAL NOT NULL,
  updated_at   REAL NOT NULL,
  pinned       INTEGER NOT NULL DEFAULT 0,
  archived     INTEGER NOT NULL DEFAULT 0,
  project_id   TEXT
);
CREATE INDEX idx_sessions_updated_at ON sessions(updated_at DESC);

CREATE TABLE workspaces (path TEXT PRIMARY KEY, name TEXT);
CREATE TABLE settings   (key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE projects   (id TEXT PRIMARY KEY, name TEXT, color TEXT);
```

Migration note: on first boot against an existing install, provide a one-shot importer that reads
`~/.hermes/webui/sessions/*.json` and `workspaces.json`/`settings.json`/`projects.json` and
populates SQLite, so existing users don't lose history. This importer is a Phase 2 task item.

## 4. SSE streaming engine (Go terms)

Direct port of the Python concept, not a redesign:

| Python | Go |
|---|---|
| `queue.Queue` per stream | buffered `chan streamEvent` per stream |
| `STREAMS = {}` + `STREAMS_LOCK` | `sync.Map[string]chan streamEvent` (or mutex + map) |
| daemon thread running `_run_agent_streaming` | goroutine calling `agentclient.RunTurn(ctx, ...)` |
| `queue.get(timeout=30)` heartbeat | `select` with a 30s `time.After` case writing `: heartbeat\n\n` |
| catch `BrokenPipeError`/`ConnectionResetError` silently | check `r.Context().Done()` / write error handling, log at debug not error |
| event types: `token`, `tool`, `approval`, `done`, `error` | same string event names, same JSON field names, unchanged |
| `on_token(text)`; `None` = end-of-stream sentinel | Go: use a distinct event/close-channel signal instead of a magic `nil` string — cleaner in Go, same external behavior |

`GET /api/chat/stream/status?stream_id=X` parity endpoint is kept for the same reconnect-banner
behavior the frontend's `boot.js`/`messages.js` already implement — **do not** invent a different
reconnection mechanism; the frontend isn't changing.

## 5. Approval state machine

Python keeps `_pending`/`_lock`/`_permanent_approved` as module-level globals in
`tools/approval.py`, relying on Python's shared-process import caching. Go has no equivalent
import-caching trick and doesn't need one — this is simply:

```go
type ApprovalStore struct {
    mu        sync.Mutex
    pending   map[string]PendingApproval   // keyed by session_id ("session_key")
    permanent map[string]bool               // permanently-approved pattern_keys
}
```

Same semantics to preserve:
- `once`/`session` choice → approve for this session only (Python: `approve_session`).
- `always` choice → approve permanently + persist to disk allowlist (Python: `approve_permanent`
  + `save_permanent_allowlist`) — Go should persist this in SQLite `settings` or a dedicated
  `approvals_allowlist` table, not a flat file, for consistency with the rest of the store.
- `deny` → just remove from pending, no side effect.
- **Rule #9 from the original doc**: always iterate `pattern_keys` (plural), never assume a
  single `pattern_key`. Carry this rule forward verbatim in `02-api-parity-mapping.md` §8.

## 6. Concurrency model — where Go is structurally better

Python's Critical/TD1 issue (env vars are process-global: `TERMINAL_CWD`, `HERMES_EXEC_ASK`,
`HERMES_SESSION_KEY` set via `os.environ`, clobbered by concurrent requests for *different*
sessions) does not need a Go equivalent at all. Go passes a per-request context object explicitly
into `agentclient.RunTurn(ctx, session, workspace, execAsk, ...)` — there is no shared mutable
global to race on. Document this in the plan as an explicit **improvement**, not just parity, so
nobody "faithfully" re-introduces a global-variable bug while porting.

## 7. Static asset serving

Go serves `static/index.html`, `style.css`, and the JS modules from disk (or embedded via
`embed.FS` for a truly single-binary deploy — recommended, since it removes a class of "forgot to
copy static/ next to the binary" deploy bugs and fits the "lightweight, easy install" goal). No
content changes to these files during phases 0–8. `embed.FS` is a good target because it keeps
the "one binary, `scp` it, run it" deploy story the original `ctl.sh`/`start.sh` scripts aimed for.

## 8. Config & environment variables (must all be honored)

Port every env var from the Python `ARCHITECTURE.md` §3 table with the same name and semantics:
`HERMES_WEBUI_HOST`, `HERMES_WEBUI_PORT`, `HERMES_WEBUI_DEFAULT_WORKSPACE`,
`HERMES_WEBUI_STATE_DIR`, `HERMES_CONFIG_PATH`, `HERMES_WEBUI_DEFAULT_MODEL`,
`HERMES_WEBUI_PASSWORD`, `HERMES_WEBUI_SKIP_ONBOARDING`, `HERMES_HOME`,
`HERMES_WEBUI_MAX_UPLOAD_MB`. Add one new one for the migration itself:
`HERMES_WEBUI_LEGACY_PROXY_URL` (internal port where the still-running Python process listens),
consumed only by `internal/proxy` and deleted along with that package in Phase 8. Also add
`HERMES_WEBUI_AGENT_TRANSPORT` (`auto`|`grpc`|`http`, default `auto`) and
`HERMES_WEBUI_AGENT_SOCKET` (default `~/.hermes/webui/agent.sock`) per §2b — both are optional;
omitting them preserves the selected in-process `AIAgent` decision and does not imply gateway-only
behavior; provider traffic remains outside Phase 0 evidence.

## 9. Logging & observability

Match Python's structured JSON-per-request log line shape (`ts`, `method`, `path`, `status`,
`ms`) so any existing log-parsing habits/scripts keep working: `{"ts":"...","method":"GET",
"path":"/health","status":200,"ms":0.1}`. Use Go's `log/slog` with a JSON handler to get this
almost for free.
