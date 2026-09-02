# 04 — Task Breakdown (checkbox-level, ordered)

Work top-to-bottom. Do not start a phase's tasks until the previous phase's exit criteria
(`plan.md` §4) are met and the parity check for that phase (`06-testing-and-parity.md`) is green.

## Phase 0 — Audit & harness

Phase 0 evidence: `testdata/route-inventory.json`, `tools/phase0_harness.py`, `tools/phase0_lifecycle.py`, and tests. Checked-in safe journey covers exactly 3 read-only GET exchanges (`/health`, `/api/sessions`, `/api/workspaces`) against two fresh isolated source-server processes; it does not test mutations. Provider-chat coverage remains **open**: no chat exchange was included because safe repeatability for provider side effects was not established; Phase 0 makes no provider-chat parity claim. Tests never run provider traffic automatically. Normalization remains comparison-only; `replay_request` preserves redacted operational request values for replay.

- [x] Clone the Hermes WebUI fork, read `ARCHITECTURE.md` (or equivalent), reconcile against actual source.
- [x] Enumerate every route; inventory saved in `testdata/route-inventory.json`, including fork-specific Kanban, workspace escape, wiki/knowledge, notes, MCP, gateway/session-event routes.
- [x] Add bounded `tools/phase0_harness.py` record/replay/compare harness. Full live journey blocked because source dependencies are unavailable (`yaml`), so no provider fixture is fabricated.
- [x] Confirm deterministic replay normalization with unit tests: mask secrets and normalize timestamps, session IDs, stream IDs, and related volatile fields in comparison logic; preserve arbitrary content.
- [ ] Provider-chat coverage: establish safe repeatability and parity fixture, or explicitly defer to Phase 4. No parity claim until then.
- [x] Decide + document: default is in-process `AIAgent`; gateway mode is explicit opt-in; Phase 4 needs agent-shim fallback unless gateway contract is separately validated.

## Phase 1 — Skeleton + proxy (pure plumbing, zero behavior change)

- [ ] `go.mod` + package skeleton per `01-architecture-design.md` §1.
- [ ] `internal/httpserver`: chi router, JSON logging middleware (matches Python's log line shape),
      panic-recovery middleware that returns `{"error":"Internal server error"}` (never a trace).
- [ ] `internal/proxy`: `httputil.ReverseProxy` targeting `HERMES_WEBUI_LEGACY_PROXY_URL`; wildcard
      catch-all for every route not yet claimed by a Go handler.
- [ ] Serve `static/*` from Go (start with disk-based `http.FileServer`, switch to `embed.FS` once
      the static tree is confirmed stable for this phase).
- [ ] `/health` implemented natively in Go (talks to session store once it exists; stub `sessions:
      0` acceptable temporarily if store isn't ready — but flag this explicitly, don't ship silently).
- [ ] Deploy: Python process moved to bind an internal-only port; Go takes the public port.
      Confirm from a browser that the app is indistinguishable from before.
- [ ] Exit check: replay Phase 0's golden fixtures against the new Go-fronted stack; 100% match.

## Phase 2 — Read-only data ports

- [ ] SQLite schema + migrations per `01-architecture-design.md` §3.
- [ ] One-shot importer: `~/.hermes/webui/sessions/*.json` (+ `workspaces.json`, `settings.json`,
      `projects.json`) → SQLite. Verify row counts and spot-check content against source files.
- [ ] `internal/session`: store interface + SQLite impl; `GetSession`, `ListSessions` (with the
      pagination Python never had — expose `limit`/`offset` even if the FE doesn't send them yet,
      it's free and sets up Phase F-equivalent cleanliness).
- [ ] `GET /api/session` — Go-native; 400 on missing `session_id` (Rule B11 equivalent).
- [ ] `GET /api/sessions`, `GET /api/sessions/search` — Go-native.
- [ ] `internal/workspace`: `safe_resolve`, `list_dir`, `read_file_content`, raw-file MIME lookup.
- [ ] `GET /api/list`, `GET /api/file`, `GET /api/file/raw` — Go-native, path-traversal tests
      required (attempt `../../etc/passwd`-style payloads in the parity suite, assert clean 400).
- [ ] `GET /api/workspaces` — Go-native.
- [ ] Remove Python proxy fallback **only** for the routes above, one at a time, each gated by a
      green parity run.
- [ ] Exit check: MVP definition of done (`03-mvp-scope.md`) fully satisfied; tag this as the MVP
      release.

## Phase 3 — Mutations

- [ ] `POST /api/session/new`, `/api/session/update`, `/api/session/delete`, `/api/session/rename`.
      Verify Rule #1 (delete never creates) with an explicit test.
- [ ] `POST /api/workspaces/add` (permissive validation), `/remove`, `/rename`.
- [ ] `internal/upload`: multipart parser (Go's `mime/multipart` stdlib is fine here — this is one
      case where Python's manual parser existed only to route around a stdlib gap that Go doesn't
      have; use the stdlib directly, don't hand-roll). Filename sanitization + size cap parity.
- [ ] `POST /api/upload`, `/api/file/save`, `/api/file/create`, `/api/file/delete`.
- [ ] `GET /api/session/export` (Content-Disposition header parity).
- [ ] Remove proxy fallback for these routes once green.

## Phase 4 — Chat + streaming

### 4a. Ship the safe path first (as originally planned — no transport risk here)

- [ ] `internal/agentclient`: define the `AgentClient` interface (`RunTurn`, `Cancel`) and the
      shared `TurnEvent` type per `01-architecture-design.md` §2b.
- [ ] Implement `httpClient` (plain HTTP to the existing gateway / 9router, or the Phase-0-decided
      shim). Confirm the `task_id` keyword-equivalent contract end-to-end.
- [ ] `internal/stream`: stream registry (`sync.Map` or mutex+map), per-stream `chan streamEvent`,
      30s heartbeat, event types `token`/`tool`/`approval`/`done`/`error`.
- [ ] `POST /api/chat/start` — validates session, saves user message, spawns goroutine, returns
      `{stream_id, session_id}` immediately.
- [ ] `GET /api/chat/stream` — SSE writer, forwards channel events, handles disconnect cleanly
      (check `r.Context().Done()`), cleans up the stream registry entry in a deferred func.
- [ ] `GET /api/chat/stream/status` — reconnect-banner support.
- [ ] `POST /api/chat` — synchronous fallback (blocks until the goroutine completes).
- [ ] Concurrency test: fire two concurrent chats in two different sessions, assert no
      cross-contamination (this is the test that proves the Phase-6-of-Python-doc TD1 fix is real
      in Go, per `01-architecture-design.md` §6).
- [ ] Remove proxy fallback for chat routes once green — this is the highest-risk cutover, budget
      extra soak time (run both old and new in shadow/compare mode if feasible before fully cutting).
      **`httpClient` is the only transport in play at this point — ship and soak this before touching 4b.**

### 4b. Add the fast path on top, with zero risk to 4a (follow-up within Phase 4)

- [ ] Hermes-side: add a minimal gRPC server to the agent shim (or confirm the fork's gateway
      already supports one) exposing the same `RunTurn`/`Cancel` semantics over a Unix domain
      socket (`~/.hermes/webui/agent.sock` by default).
- [ ] Go-side: implement `grpcClient` behind the same `AgentClient` interface — must translate its
      wire events into the identical `TurnEvent` shape `httpClient` already produces.
- [ ] Implement the boot-time capability probe (short timeout, non-fatal) and per-call fallback
      to `httpClient` on mid-stream gRPC failure, per `01-architecture-design.md` §2b items 1–2.
- [ ] Wire `HERMES_WEBUI_AGENT_TRANSPORT` (`auto`|`grpc`|`http`, default `auto`) and
      `HERMES_WEBUI_AGENT_SOCKET` env vars; confirm omitting both preserves current behavior exactly.
- [ ] Parity test: replay the Phase 0 golden fixtures through `grpcClient` and assert identical
      `TurnEvent` sequences to `httpClient`'s — same bar as any other route before it's trusted.
- [ ] Explicit fallback-injection test: kill/disable the gRPC socket mid-session, assert the next
      turn transparently falls back to `httpClient` with no error surfaced to the browser and only
      an info-level log line.
- [ ] Load/soak `auto` mode for a full day (per `06-testing-and-parity.md` §4) before making `grpc`
      the assumed default in any deployment docs — `auto` itself never needs to change, only your
      confidence in it.
- [ ] Only after this soak: upgrade the Hermes shim on your VPS to the gRPC-capable version. Until
      that upgrade happens, `auto` mode keeps everyone on `httpClient` automatically — nothing to
      coordinate, nothing that can break the web UI in the interim.

## Phase 5 — Approvals

- [ ] `internal/approval`: `ApprovalStore` per `01-architecture-design.md` §5.
- [ ] `GET /api/approval/pending`, `POST /api/approval/respond`.
- [ ] Wire approval surfacing into the `tool` SSE event path (approval appears within the same
      stream immediately after the triggering tool call, not only via polling).
- [ ] Permanent-allowlist persistence (SQLite instead of Python's flat file) + import any existing
      allowlist during the Phase 2 importer (retroactively, or as an add-on migration step here).
- [ ] Rule #9 test: multi-pattern approval, assert all `pattern_keys` get approved, not just the
      first.
- [ ] Remove proxy fallback for approval routes once green.

## Phase 6 — Panels (crons, skills, memory)

- [ ] `internal/crons`: wrapper calling whatever the Hermes agent side exposes for
      `list_jobs`/`create_job`/`pause`/`resume`/`run` (likely another small HTTP call to the agent
      process, same seam as chat — do not reimplement cron scheduling logic in Go).
- [ ] `GET /api/crons`, `/api/crons/output`, `POST /api/crons/run|pause|resume|create`.
- [ ] `internal/skillsmem`: read `SKILL.md` files / skill registry, `MEMORY.md`/`USER.md`/`SOUL.md`.
- [ ] `GET /api/skills`, `/api/skills/content`, `/api/memory`.
- [ ] Remove proxy fallback once green.

## Phase 7 — Auth + observability

- [ ] `internal/auth`: password check, signed HttpOnly+SameSite=Strict cookie, 30-day sliding
      validity (verify actual fork behavior first).
- [ ] `POST /api/auth/login`; middleware gate on all API routes when `HERMES_WEBUI_PASSWORD` set.
- [ ] `/health` final shape: `active_streams`, `uptime_seconds`.
- [ ] Structured JSON request logging finalized (ts/method/path/status/ms).
- [ ] Graceful shutdown (drain in-flight SSE streams, don't hard-kill on `ctl.sh stop`).
- [ ] `ctl.sh`-equivalent daemon script updated to manage the Go binary (start/stop/restart/status/
      logs), keep the same CLI UX so muscle memory transfers.

## Phase 8 — Full cutover

- [ ] Confirm 100% of routes are Go-native; `internal/proxy` package has zero remaining callers.
- [ ] Delete `internal/proxy`, delete `HERMES_WEBUI_LEGACY_PROXY_URL` from config docs.
- [ ] Stop starting the Python process at all (update `ctl.sh`/systemd units/whatever supervises
      it). If the Phase 0 decision required the "agent shim," that one small Python process is
      the sole exception and should be clearly labeled as intentionally-retained, not leftover debt.
- [ ] Re-measure the Phase-MVP metrics table (`03-mvp-scope.md`) end-to-end; record final numbers.
- [ ] Tag a release. This is the "backend migration fully done" milestone that gates Phase 9.

## Phase 9 — Frontend → Vite (future, out of scope until Phase 8 sign-off)

See `07-future-vite-frontend.md` for the (intentionally lighter-weight, since it's further out)
plan for this phase.
