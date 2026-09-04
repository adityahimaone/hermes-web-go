# 04 — Task Breakdown (checkbox-level, ordered)

Work top-to-bottom. Do not start a phase's tasks until the previous phase's exit criteria
(`plan.md` §4) are met and the parity check for that phase (`06-testing-and-parity.md`) is green.

> **Status note:** this file now reflects actual progress from `hermes-web-go` (commits through
> `ec72c10`, 2026-09-02), not just the original plan. Phases 0–3 and 4a are substantially complete;
> Phase 4b was blocked and is now unblocked with concrete code — see `docs/09-phase4b-unblock.md`.

## Phase 0 — Audit & harness — ✅ done (with caveats)

Evidence: `testdata/route-inventory.json`, `tools/phase0_harness.py`, `tools/phase0_lifecycle.py`.
Checked-in safe journey covers 3 read-only GET exchanges (`/health`, `/api/sessions`,
`/api/workspaces`) against two fresh isolated source-server processes — mutations not covered.
**Provider-chat coverage remains explicitly open**: no chat exchange was included because safe
repeatability for provider side effects wasn't established; Phase 0 makes no provider-chat parity
claim, and tests never run provider traffic automatically. Normalization is comparison-only;
`replay_request` preserves redacted operational request values for replay.

- [x] Clone the fork, read `ARCHITECTURE.md`, reconcile against actual source.
- [x] Enumerate every route; inventory in `testdata/route-inventory.json`, including fork-specific
      Kanban, workspace escape, wiki/knowledge, notes, MCP, gateway/session-event routes.
- [x] Bounded `tools/phase0_harness.py` record/replay/compare harness. Full live journey blocked
      because a source dependency (`yaml`) is unavailable — no provider fixture fabricated.
- [x] Deterministic replay normalization, unit-tested: secrets masked, timestamps/session
      IDs/stream IDs normalized in comparison logic; arbitrary content preserved.
- [x] Provider-chat coverage explicitly deferred to Phase 4.
- [x] Decision recorded: default is in-process `AIAgent`; gateway mode is explicit opt-in; Phase 4
      needs the agent-shim fallback unless the gateway contract is separately validated.

## Phase 1 — Skeleton + proxy — ✅ done

- [x] `go.mod` + package skeleton (`cmd/server`, `internal/{config,httpserver,proxy}`).
- [x] `internal/httpserver`: chi router, JSON logging middleware, panic-recovery middleware
      (never leaks a trace) — `server.go`/`middleware.go`, tested.
- [x] `internal/proxy`: `httputil.ReverseProxy` targeting `HERMES_WEBUI_LEGACY_PROXY_URL`,
      explicit native-route registry, wildcard catch-all for everything else.
- [x] `static/*` served from Go via disk-based `http.FileServer`; byte-identical response test passed.
- [x] `/health` implemented natively; live and unit tests passed.
- [x] Deploy: Go on the public port, Python on an internal-only port; Phase-0 golden replay passed
      Go-fronted; rollback dry-run confirmed Python stays healthy.
- [x] Exit check: `3 exchanges match after redaction/normalization` (2026-09-02, commit `145be81`).

## Phase 2 — Read-only data ports — ✅ done

- [x] SQLite schema in `internal/store/store.go` (sessions/workspaces/settings/projects + indexes),
      in-code idempotent (`CREATE TABLE IF NOT EXISTS`) — versioned `migrations/` still a follow-up.
- [x] One-shot importer **pivoted to Hermes `state.db` as primary source of truth** (the live
      session store, not `~/.hermes/webui/sessions/*.json`). `ImportStateDB` reads
      `sessions`+`messages` read-only; workspace resolved from the latest
      `[Workspace::v1: ...]` tag → cwd fallback. Legacy JSON importer kept only as a fallback when
      `state.db` is empty/unavailable. Row count verified against `state.db`.
- [x] `internal/store`: `SessionRow`/`SessionImport`, `GetSession`, `ListSessions` (with
      `limit`/`offset` pagination the Python version never had).
- [x] `GET /api/session` — Go-native; 400 on missing `session_id`.
- [x] `GET /api/sessions`, `GET /api/sessions/search` — Go-native.
- [x] `internal/workspace`: `safe_resolve`, `list_dir`, `read_file_content`, raw-file MIME lookup.
- [x] `GET /api/list`, `GET /api/file`, `GET /api/file/raw` — Go-native, traversal blocked
      (`../../etc` → 404). **Known parity nuance:** returns 404, not 400, on traversal — matches
      "clean not-200 rejection" but not the exact status code originally assumed; low-priority
      follow-up, not a security gap.
- [x] `GET /api/workspaces` — Go-native (SQLite-backed).
- [x] Proxy fallback removed for the above; verified live with no `HERMES_WEBUI_LEGACY_PROXY_URL`
      set — all 8 routes return 200 directly from Go.
- [x] Exit check / MVP tag gate: production-like run (2026-09-02) on public port 18791 with the
      legacy proxy URL configured — all 8 native routes served directly by Go, `/api/crons`
      (non-native) transparently proxied, traversal blocked. RSS baseline ~4–9.8MB idle vs Python's
      16.4MB. Rollback dry-run confirmed. **Explicit MVP release tag itself still pending.**

## Phase 3 — Mutations — ✅ done

- [x] `POST /api/session/new|update|delete|rename`. Rule #1 (delete never creates) covered by
      `TestSessionDeleteNeverCreates`.
- [x] `POST /api/workspaces/add|remove|rename` — `internal/store/catalog.go`.
- [x] `internal/upload`: stdlib multipart, filename sanitization, 20MB default cap,
      `HERMES_WEBUI_MAX_UPLOAD_MB` override.
- [x] `POST /api/upload`, `/api/file/save|create|delete` — traversal/symlink protection tested.
- [x] `GET /api/session/export` — `Content-Disposition: attachment` parity.
- [x] Proxy fallback removed; `go test ./...`, `-race`, and `go vet ./...` all green (2026-09-02).

## Phase 4 — Chat + streaming

### 4a. Safe path (HTTP transport) — ✅ done

- [x] `internal/agentclient`: `AgentClient` interface (`RunTurn`, `Cancel`), shared `TurnEvent`
      type — `agentclient_test.go`, commit `7c837c9`, `-race` clean.
- [x] `httpClient`: POST `/v1/runs`, GET `/v1/runs/{id}/events`, POST cancel —
      `httpclient_test.go`, commit `7c837c9`.
- [x] `internal/stream`: registry, per-stream channel, 30s heartbeat, event types — race-clean.
- [x] `POST /api/chat/start` — validates session, saves message, spawns goroutine, returns
      `{stream_id, session_id}` — `TestChatStartStreamsTokenAndDone`, `TestChatStartValidatesSession`.
- [x] `GET /api/chat/stream` — SSE writer, clean disconnect handling; completed streams close/mark
      inactive while the buffered entry stays available for reconnect drain (matches Python
      `STREAMS` behavior) — `writer_test.go`, `chat_test.go`.
- [x] `POST /api/chat` — sync fallback, blocks until the event channel completes —
      `TestChatSyncBlocksUntilAgentCompletes`.
- [x] Concurrency test: two concurrent chats in different sessions, no cross-contamination —
      `TestChatConcurrentSessionsNoCrossContamination` under `-race`.
- [x] `GET /api/chat/stream/status` — reconnect-banner support, confirmed via
      `TestChatStartStreamsTokenAndDone`.
- [ ] Remove proxy fallback for chat routes — **blocked on `docs/10-chat-parity-fixes.md` AND
      `docs/11-history-race-fix.md`** (known chat-parity gaps: a critical HTTP-transport SSE parser
      bug that silently breaks the Phase 4b `auto`-mode fallback safety net, a duplicate completion
      event, dropped partial answers on agent errors, and a separate async race where rapid sends
      can let a stale session snapshot overwrite a newer, longer visible transcript). Fix and test
      all of §10 and §11 before this cutover — budget extra soak time regardless; this is the
      highest-risk cutover in the whole plan.

### 4b. Fast path (gRPC over Unix socket) — was BLOCKED, now unblocked — see `docs/09-phase4b-unblock.md`

Two independent blockers existed: (1) **HARD** — no gRPC server on the Python side at all, only
HTTP `/v1/runs`; (2) **SOFT** — Go's `internal/agentclient/transport.go` didn't compile (undefined
`grpcDialContext`, wrong return count from the selector). `docs/09-phase4b-unblock.md` now
contains working code for both, plus a mock-server test strategy that validates (2) without
waiting on (1). Updated checklist:

- [ ] Apply the `transport.go` fix from `docs/09-phase4b-unblock.md` §1 (defines `grpcDialContext`,
      fixes `NewBestClient`'s return signature to `(AgentClient, error)`).
- [ ] Finish `grpcclient.go` per §2 — `RunTurn`/`Cancel` with fallback-on-establish-failure (not
      mid-stream resume — see the design note in §2 for why).
- [ ] Wire `cmd/server/main.go` per §3 (`HERMES_WEBUI_AGENT_TRANSPORT`/`_SOCKET` → `NewBestClient`).
- [ ] Add the four mock-server tests from §4 (`TestNewBestClient_NoSocketFallsBackToHTTP`,
      `TestNewBestClient_ForcedGRPCUnavailableErrors`, `TestNewBestClient_AutoPrefersGRPCWhenAvailable`,
      `TestGRPCFallbackOnCrash`) — **these can be written and run today, with zero Python-side work**,
      since they only need a mock `agentpb.AgentServer`.
- [ ] Python side: stand up `gateway/platforms/agent_grpc.py` per §5 — generate stubs
      (`grpcio`/`grpcio-tools`), verify the two payload-shape TODOs against the real
      `api_server_runs.py` (`run_id` field name, whether `/v1/runs/{id}/events` is already SSE,
      whether a `/v1/runs/cancel` route exists yet), run standalone, confirm `Ping` responds over
      the real Unix socket.
- [ ] **Confirmed manually — shim ran, `Ping` answered `hermes-agent-grpc-shim/0.1`, socket
      verified — but the shim was only running by hand and is gone now (2 orphan processes had to
      be killed).** Close this properly via `docs/09-phase4b-unblock.md` §7: add the single-instance
      `flock` guard (§7a), install the systemd unit (§7b), fold status/restart into `ctl.sh` (§7c),
      and run the §7d verification checklist (survives reboot, survives `kill -9`, refuses a second
      instance, Go's `auto` mode picks it up without manual restart).
- [ ] Integration: force `HERMES_WEBUI_AGENT_TRANSPORT=grpc` (not `auto`) for the first real
      end-to-end turn, so any integration bug fails loudly instead of silently falling back to
      HTTP and masking the problem.
- [ ] Parity test: replay Phase 0 golden fixtures through `grpcClient`, diff `TurnEvent` sequences
      against `httpClient`'s.
- [ ] Explicit fallback-injection test against the **real** shim (not just the mock): kill the
      gRPC socket mid-session, assert the next turn falls back to `httpClient` with no error
      surfaced to the browser, only an info-level log line.
- [ ] Soak `auto` mode for a full day before treating `grpc` as the assumed default anywhere in
      deployment docs.
- [ ] Only then: upgrade the production shim. Until that upgrade happens, `auto` keeps everyone on
      `httpClient` automatically — nothing to coordinate, nothing that can break the web UI.

## Phase 5 — Approvals — mostly done

- [x] `internal/approval`: `ApprovalStore` — `store_test.go` (queue semantics, session/always/deny,
      invalid choice).
- [x] `GET /api/approval/pending`, `POST /api/approval/respond` — `approval_test.go`.
- [x] Approval surfaced within the same SSE stream immediately after the triggering tool call, not
      only via polling — `chat.go` submits `EventApproval` to the shared store before forwarding
      to the stream; `TestChatStartApprovalEventPopulatesStore`.
- [ ] Permanent-allowlist persistence: SQLite side is done (`persist.go`, `TestStorePersistsPermanentToDB`,
      `TestStoreLoadsPermanentsOnBoot`, `TestSQLitePersistenceRoundTrip`, wired via `NewStoreP`) —
      **importing any existing Python flat-file allowlist during the Phase 2 importer is still open.**
- [x] Rule #9 test: multi-pattern approval, all `pattern_keys` approved, not just the first —
      `TestApprovalStoreAlwaysPersistsAllPatternKeys`, `TestApprovalStoreQueueAndChoices`.
- [ ] Remove proxy fallback for approval routes once the allowlist-import gap above is closed.

## Phase 6 — Panels (crons, skills, memory) — partial

- [ ] `internal/crons`: reads file-backed `jobs.json`/output natively (the current agent exposes
      no cron HTTP mutation endpoints yet) — **scheduler mutations remain proxy-backed** until an
      agent-side seam exists for pause/resume/create/run.
- [ ] `GET /api/crons`, `/api/crons/output` — native, live-verified (12 real jobs from
      `~/.hermes/cron/jobs.json`) — `TestCronsList`, `TestCronsOutput`, `TestCronsOutputRejectsTraversal`.
      `POST /api/crons/run|pause|resume|create` — still proxy-backed, see above.
- [ ] `internal/skillsmem`: native read package — frontmatter skill discovery, nested skill lookup,
      memory files all working. **Agent-specific external skill namespaces/config redaction still
      open.**
- [ ] `GET /api/skills`, `/api/skills/content`, `/api/memory` — all three native and live-verified
      (`TestSkillsListRoute`, `TestSkillsContentRoute`, `TestMemoryRoute`, traversal/missing checks).
- [ ] Remove proxy fallback once the cron-mutation seam and skill-namespace gaps above are closed.

## Phase 6.5 — Kanban board — not started

Task-dispatch board (boards, swimlanes by assignee, status columns, dispatcher preview/run, bulk
actions, archive, multi-tenant filtering). See `docs/08-kanban-board.md` for the full data model,
dispatcher semantics, and open questions — this phase has not started; Phase 0's source
verification for this feature specifically is still outstanding (the doc's spec was built from a
UI screenshot, not source).

## Phase 7 — Auth + observability — not started

- [ ] `internal/auth`: password check, signed HttpOnly+SameSite=Strict cookie, 30-day sliding
      validity (verify actual fork behavior first).
- [ ] `POST /api/auth/login`; middleware gate on all API routes when `HERMES_WEBUI_PASSWORD` set.
- [ ] `/health` final shape: `active_streams`, `uptime_seconds`.
- [ ] Structured JSON request logging finalized (ts/method/path/status/ms).
- [ ] Graceful shutdown (drain in-flight SSE streams, don't hard-kill on `ctl.sh stop`).
- [ ] `ctl.sh`-equivalent daemon script updated for the Go binary, same CLI UX.

## Phase 8 — Full cutover — not started

- [ ] Confirm 100% of routes are Go-native; `internal/proxy` has zero remaining callers.
- [ ] Delete `internal/proxy`, delete `HERMES_WEBUI_LEGACY_PROXY_URL` from config docs.
- [ ] Stop starting the Python process at all — except the gRPC agent shim from Phase 4b, which is
      intentionally retained (per `01-architecture-design.md` §2b), clearly labeled as such.
- [ ] Re-measure the Phase-MVP metrics table end-to-end; record final numbers.
- [ ] Tag a release — the "backend migration fully done" milestone that gates Phase 9.

## Phase 9 — Frontend → Vite (future, out of scope until Phase 8 sign-off)

See `docs/07-future-vite-frontend.md`.
