# 04 — Task Breakdown (checkbox-level, ordered)

Work top-to-bottom. Do not start a phase's tasks until the previous phase's exit criteria
(`plan.md` §4) are met and the parity check for that phase (`06-testing-and-parity.md`) is green.

## Phase 0 — Audit & harness

Phase 0 evidence: `testdata/route-inventory.json`, `tools/phase0_harness.py`, `tools/phase0_lifecycle.py`, and tests. Checked-in safe journey covers exactly 3 read-only GET exchanges (`/health`, `/api/sessions`, `/api/workspaces`) against two fresh isolated source-server processes; it does not test mutations. Provider-chat coverage remains **open**: no chat exchange was included because safe repeatability for provider side effects was not established; Phase 0 makes no provider-chat parity claim. Tests never run provider traffic automatically. Normalization remains comparison-only; `replay_request` preserves redacted operational request values for replay.

- [x] Clone the Hermes WebUI fork, read `ARCHITECTURE.md` (or equivalent), reconcile against actual source.
- [x] Enumerate every route; inventory saved in `testdata/route-inventory.json`, including fork-specific Kanban, workspace escape, wiki/knowledge, notes, MCP, gateway/session-event routes.
- [x] Add bounded `tools/phase0_harness.py` record/replay/compare harness. Full live journey blocked because source dependencies are unavailable (`yaml`), so no provider fixture is fabricated.
- [x] Confirm deterministic replay normalization with unit tests: mask secrets and normalize timestamps, session IDs, stream IDs, and related volatile fields in comparison logic; preserve arbitrary content.
- [x] Provider-chat coverage explicitly deferred to Phase 4. Safe repeatability for provider side effects was not established; no provider fixture or parity claim is made in Phase 0.
- [x] Decide + document: default is in-process `AIAgent`; gateway mode is explicit opt-in; Phase 4 needs agent-shim fallback unless gateway contract is separately validated.

## Phase 1 — Skeleton + proxy (pure plumbing, zero behavior change)

- [x] `go.mod` + package skeleton per `01-architecture-design.md` §1. Implemented under `cmd/server`
      and `internal/{config,httpserver,proxy}`; package naming follows repo layout.
- [x] `internal/httpserver`: chi router, JSON logging middleware (matches Python's log line shape),
      panic-recovery middleware that returns `{"error":"Internal server error"}` (never a trace).
      Implemented in `internal/httpserver/server.go` and `middleware.go`, covered by tests.
- [x] `internal/proxy`: `httputil.ReverseProxy` targeting `HERMES_WEBUI_LEGACY_PROXY_URL`; wildcard
      catch-all for every route not yet claimed by a Go handler. Native route registry is explicit.
- [x] Serve `static/*` from Go via disk-based `http.FileServer`; byte-identical response test passed.
- [x] `/health` implemented natively in Go; live and unit tests passed.
- [x] Deploy: Python process can bind an internal-only port; Go takes the public port. Phase-0
      golden replay passed with Go fronting Python; rollback dry-run confirmed Python stayed healthy.
- [x] Exit check: replay Phase 0's golden fixtures against the new Go-fronted stack; 100% match.
      Result: `3 exchanges match after redaction/normalization` (2026-09-02, commit `145be81`).
      Go binary boots, serves static byte-identical, `/health` native, proxies non-native to Python.

## Phase 2 — Read-only data ports

- [x] SQLite schema + migrations per `01-architecture-design.md` §3. Schema lives in
      `internal/store/store.go` (sessions/workspaces/settings/projects tables + indexes); no
      separate versioned `migrations/` file yet — noted as follow-up, schema is in-code and
      idempotent (`CREATE TABLE IF NOT EXISTS`).
- [x] One-shot importer: **pivoted to Hermes `state.db` as primary source of truth** (the live
      Hermes session store is `state.db`, NOT `~/.hermes/webui/sessions/*.json`). `ImportStateDB`
      reads `sessions`+`messages` tables read-only; workspace resolved from latest
      `[Workspace::v1: ...]` user-message tag → cwd fallback. Legacy JSON importer kept as fallback
      only when state.db is empty/unavailable (prevents stale JSON artifacts polluting the sidebar).
      Catalog files (workspaces.json/settings.json/projects.json) also imported. Row count verified
      against `state.db` (1 session, id/messages/workspace match).
- [x] `internal/store`: `SessionRow`/`SessionImport`, `GetSession`, `ListSessions` (with the
      pagination Python never had — `limit`/`offset` exposed even if the FE doesn't send them yet).
- [x] `GET /api/session` — Go-native; 400 on missing `session_id` (Rule B11 equivalent).
- [x] `GET /api/sessions`, `GET /api/sessions/search` — Go-native.
- [x] `internal/workspace`: `safe_resolve`, `list_dir`, `read_file_content`, raw-file MIME lookup.
- [x] `GET /api/list`, `GET /api/file`, `GET /api/file/raw` — Go-native, path-traversal tests
      required. Traversal blocked server-side (`../../etc` returns 404, no file leaked) — note:
      returns 404 not 400, matches a "clean not-200" rejection; 400-vs-404 nuance left as low-priority
      parity follow-up.
- [x] `GET /api/workspaces` — Go-native (reads from SQLite workspace table).
- [x] Remove Python proxy fallback **only** for the routes above. Verified live: server booted
      WITHOUT `HERMES_WEBUI_LEGACY_PROXY_URL` and returned 200 for all 8 routes directly from Go
      (not proxied).
- [x] Exit check: MVP definition of done (`03-mvp-scope.md`) fully satisfied; tag this as the MVP
      release. Production-like gate passed (2026-09-02): Go booted on public port 18791 with
      `HERMES_WEBUI_LEGACY_PROXY_URL` pointing at Python, all 8 read-only native routes returned
      200 directly from Go (no proxy fallback), `/api/crons` (non-native) proxied transparently,
      traversal blocked. RSS baseline measured ~4-9.8 MB idle vs Python 16.4 MB; rollback dry-run
      confirmed Python stays healthy when Go stops. MVP tag itself pending explicit release decision.

## Phase 3 — Mutations

- [x] `POST /api/session/new`, `/api/session/update`, `/api/session/delete`, `/api/session/rename`.
      Rule #1 (delete never creates) covered by `TestSessionDeleteNeverCreates`.
- [x] `POST /api/workspaces/add` (permissive validation), `/remove`, `/rename`.
      Persistence and 404 rename behavior implemented in `internal/store/catalog.go`.
- [x] `internal/upload`: stdlib multipart parser, filename sanitization, 20 MB default cap;
      `HERMES_WEBUI_MAX_UPLOAD_MB` override supported.
- [x] `POST /api/upload`, `/api/file/save`, `/api/file/create`, `/api/file/delete`.
      Strict traversal/symlink protection covered in workspace and HTTP tests.
- [x] `GET /api/session/export` with `Content-Disposition: attachment` parity.
- [x] Remove proxy fallback for these routes once green; registry marks all Phase 3 routes native.
      Verification: `go test ./...`, `go test ./... -race`, and `go vet ./...` pass (2026-09-02).

## Phase 4 — Chat + streaming

### 4a. Ship the safe path first (as originally planned — no transport risk here)

- [x] `internal/agentclient`: define the `AgentClient` interface (`RunTurn`, `Cancel`) and the
      shared `TurnEvent` type per `01-architecture-design.md` §2b. Evidence: `internal/agentclient/agentclient_test.go`,
      commit `7c837c9`; `go test ./... -race` passed.
- [x] Implement `httpClient` (plain HTTP to the existing gateway / 9router, or the Phase-0-decided
      shim). Confirm the `task_id` keyword-equivalent contract end-to-end. Evidence: runner client
      POST `/v1/runs`, GET `/v1/runs/{id}/events`, POST cancel; `httpclient_test.go`; commit `7c837c9`.
- [x] `internal/stream`: stream registry (`sync.Map` or mutex+map), per-stream `chan streamEvent`,
      30s heartbeat, event types `token`/`tool`/`approval`/`done`/`error`. Evidence: `registry_test.go`,
      `writer_test.go`; race-clean.
- [x] `POST /api/chat/start` — validates session, saves user message, spawns goroutine, returns
      `{stream_id, session_id}` immediately. Evidence: `TestChatStartStreamsTokenAndDone`,
      `TestChatStartValidatesSession`; commit `ec72c10`.
- [x] `GET /api/chat/stream` — SSE writer, forwards channel events, handles disconnect cleanly
      (check `r.Context().Done()`). Completed stream channels are closed/marked inactive while
      their buffered entry remains available for reconnect drain, matching Python `STREAMS`
      behavior; `TestWriterDisconnect` and `TestChatTurnSurvivesStartRequest` cover cancellation
      and turn lifetime. Evidence: `internal/stream/writer_test.go`, `internal/httpserver/chat_test.go`.

- [x] `POST /api/chat` — synchronous fallback blocks until the agent event channel completes,
      then returns `{answer, status, session}`. Evidence: `TestChatSyncBlocksUntilAgentCompletes`.

- [x] Concurrency test: two concurrent chats in different sessions preserve session-specific
      event streams. Evidence: `TestChatConcurrentSessionsNoCrossContamination` under `-race`.
- [x] `GET /api/chat/stream/status` — reconnect-banner support. Evidence: `TestChatStartStreamsTokenAndDone`
      confirms inactive status after completed stream; commit `ec72c10`.
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

- [x] `internal/approval`: `ApprovalStore` per `01-architecture-design.md` §5. Evidence:
      `internal/approval/store_test.go` (queue semantics, session/always/deny, invalid choice).
- [x] `GET /api/approval/pending`, `POST /api/approval/respond`. Evidence:
      `internal/httpserver/approval_test.go` (invalid choice 400, missing session 400,
      submit→pending→respond→session approval→queue cleared).
- [ ] Wire approval surfacing into the `tool` SSE event path (approval appears within the same
      stream immediately after the triggering tool call, not only via polling).
- [ ] Permanent-allowlist persistence (SQLite instead of Python's flat file) + import any existing
      allowlist during the Phase 2 importer (retroactively, or as an add-on migration step here).
- [x] Rule #9 test: multi-pattern approval, assert all `pattern_keys` get approved, not just the
      first. Evidence: `TestApprovalStoreAlwaysPersistsAllPatternKeys` (always) and
      `TestApprovalStoreQueueAndChoices` (session) iterate `PatternKeys`.
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
