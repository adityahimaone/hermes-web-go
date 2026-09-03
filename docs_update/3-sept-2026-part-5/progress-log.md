# Hermes Web Go — Progress Log (root, isolated local testing)

Date: 2026-09-03 (WIB)
Head: `c2fd504 feat: chat SSE journal — fan-out + Last-Event-ID replay`
Scope: Root execution (no isolated worktrees)
Live: `http://127.0.0.1:18787/` — running (use `./ctl.sh stop` to terminate)

## What was done
- Chat SSE migrated from single-consumer channel to journaled broadcast:
  bounded retention, monotonic `Seq`, multi-subscriber fan-out, terminal replay,
  `Last-Event-ID` + `?after_seq=` resume, `id: <streamID>:<seq>` frames.
- Router health + `chat/stream/status` now use `JournalRegistry`.
- Added `internal/stream/{journal,journal_registry,journal_writer}` + focused tests.
- Kept proxy and all unrelated untracked audit artifacts unstaged.

## Verification evidence (fresh, this run)
- `go test -race ./...` — PASS
- `go vet ./...` — PASS
- `git diff --check` — PASS
- Focused: `TestJournal*` + `TestChatStreamFanout|Replay` — PASS

## Local execution (isolated, real binary, browser-accessible)
- Config: `127.0.0.1:18787`, `HERMES_WEBUI_DATA_ROOT=/tmp/hermes-web-go-local/data`,
  `HERMES_WEBUI_STATIC_DIR=./static`, binary `hermes-web-go`.
- `go build ./cmd/server -o ./hermes-web-go` — OK.
- `GET /health` — `200 {"active_streams":0,"status":"ok","uptime_seconds":0}`.
- `GET /static/index.html` — `200`, `222182` bytes.
- `GET /api/sessions` — `200` (verified Go-native, isolated SQLite).
- `GET /api/crons` — `500 legacy API unavailable` (no proxy configured; expected in isolated mode without `HERMES_WEBUI_LEGACY_PROXY_URL`).
- Port proving persistence: `lsof -nP -iTCP:18787 -sTCP:LISTEN` — `LISTEN`.
- Access log proves `GET /static/*` and page boot; stop via `ctl.sh stop` is intentionally NOT run here — keep the local instance accessible at http://127.0.0.1:18787/.

## Real Hermes verification (grpc, 2026-09-03 WIB — `http://127.0.0.1:18787`)
- Runtime bound to real Hermes: `HERMES_WEBUI_AGENT_TRANSPORT=grpc`,
  `HERMES_WEBUI_AGENT_SOCKET=~/.hermes/webui/agent.sock`; real shim socket present;
  gateway `127.0.0.1:8642` health returned `{"status":"ok","version":"0.21.0"}`.
- `POST /api/chat/start {"session_id":"fac1a43c1329","message":"ping"}` → `200`,
  stream `78cfb9369324`.
- `GET /api/chat/stream?stream_id=78cfb9369324` → `id: ...:1 token pong Adit`,
  `id: ...:2 token pong Adit`, `id: ...:3 done`; session persisted with
  `message_count: 12`, model `openai/gpt-5.4-mini`.
- Shim log recorded `events HTTP 200` and token/done events. `stream/status` after
  completion returned `active:false,replay_available:true`.
- Runtime-only evidence. No deterministic parity fixture claim.
- Deterministic transport parity: `TestGRPCAndHTTPProduceSameTurnEventSequence` and
  `TestGRPCFallbackOnCrash` — semantic `TurnEvent` equality + HTTP fallback verified.
- Chat history forwarding: `TestChatStartForwardsSessionHistory` — prior messages plus current
  user message reach agent boundary in order; HTTP payload now includes `history`.

## Task list
- Updated `docs/04-task-breakdown.md` §Phase 4a and §4b with verified journal + real
  provider-backed gRPC evidence. Remaining 4b parity/fallback/soak items and
  chat proxy removal stay open — highest-risk gates.

## Next step
- Continue blueprint order on root: finish remaining §4b parity/soak gates and
  the next `04-task-breakdown` phase before any chat proxy cutover.

---

## Phase D — parity defect fixes (2026-09-03, second pass)

Blueprint §2.3 defect list closed on Go side. Committed as one change set.

## What was done
- Health: `Health` + `NewHealth(...).WithDB(db)`; `/health` now returns full
  Python contract (`sessions`, `active_streams`, `active_runs`, `runs`,
  `last_run_finished_at`, `server_started_at`, float `uptime_seconds`,
  `accept_loop`), optional `checks` with `?deep=1`, `503` when degraded.
- Session DTO: `messageCounts` (total + user role count) replaces single
  counter; export `hermes-{sid}.json` + `user_message_count`.
- `pinned`/`archived` accept JSON booleans.
- File cap exact: 400,000 bytes (routes + workspace `CreateFile`/`SaveFile`).
- `/api/file/save` returns `{ok, path, size}`; Office extensions rejected with
  `/api/file/office-save` hint (Python parity).
- `/api/file/delete` supports `recursive` (workspace `DeleteRecursive`).
- `/api/workspaces`: persisted `last` (`last_workspace.txt`) +
  `terminal_remote_backend`; add/remove/rename return full `workspaces` list.
- `/api/file/raw`: `?download=1`, `?inline=1`, force-attachment for
  HTML/SVG/XHTML, CSP sandbox for inline HTML preview.

## Verification evidence (fresh, this run)
- `go build ./...` — PASS
- `go test ./...` — PASS (all packages)
- `go test -race ./...` — PASS
- `go vet ./...` — PASS
- `git diff --check` — PASS
- New regression: `TestFileOpsEnforce400KCap`, extended
  `TestSessionListItemMatchesPythonSidebarSemantics`,
  `TestSessionExportAttachment` (`hermes-{sid}.json`).

## Next step
- Any residual blueprint items beyond §2.3 list (per remaining `04-task-breakdown`
  phases) and frontend verification against the new response shapes.

---

## Family-1 session lifecycle native (2026-09-03, fourth pass)

## What was done
- Added `internal/httpserver/session_family.go` with 9 native routes (Family-1
  slice of the session-lifecycle family): `/api/session/status`, `/usage`,
  `/pin`, `/archive`, `/move`, `/toolsets`, `/draft` (GET/POST), `/truncate`,
  `/clear` — all pure DB projections over the SQLite store.
- `/status` matches Python `session_status()` exactly: no `rev`/`messages`/
  `pinned` leak, title strip, `agent_running` from stream-registry liveness
  (global signal; Python's per-session via `active_stream_id` — Go store has no
  stream column yet, documented limitation). `/usage` zeroes token counters
  (importer carries no token fields).
- `/pin` enforces quota 3 (`pinned_sessions_limit` default mirror), 400 on
  exceed; `/move` checks project existence (404 unknown), empty string
  unassigns; `/toolsets` requires non-empty list of non-empty strings or null
  (mirrors `_validate_session_toolsets_shape`); `/draft` caps 50k text + 50
  files, returns `unchanged: true` on no-op, never touches `updated_at`
  (mirrors `save(touch_updated_at=False)`); `/truncate` guards negative /
  non-integer `keep_count` (400), bumps `rev`, returns `compact|messages`;
  `/clear` truncates to zero + resets title to `Untitled` (mirrors #3542), returns compact.
- Schema: `enabled_toolsets` + `composer_draft` columns (default `''`) +
  `migrateSessionColumns` for existing DBs; `copySessionJSON` importer carries
  both fields.
- Registered all 9 native in `internal/proxy/registry.go` (`NativeMethods`
  method-aware — draft is GET+POST) so proxy fallback never sees them.
- `.gitignore` += `/hermes-web-go` (stray build binary).

## Verification evidence (fresh, this run)
- `go build ./...` — PASS
- `go test ./...` — PASS (all packages)
- `go test -race ./...` — PASS
- `go vet ./...` — PASS
- `git diff --check` — PASS
- New regressions: `session_family_test.go` — `TestSessionFamilyHandlers`
  (17 subtests: status shape, pin quota, toolsets validation incl empty/blank,
  draft caps, truncate negative, clear, move unknown project, agent_running
  liveness), `TestSessionFamilyNativeNoProxyFallback`.

## Deferred (documented in code + 04-task-breakdown)
- `/api/session/duplicate`: Python copies tokens/personality/toolsets +
  continuation semantics; Go projection lacks those fields — exact parity
  impossible until importer learns them.
- `/api/sessions/cleanup`: filesystem sweep of Untitled/empty session files,
  not a DB projection — needs importer-aware file reconciliation.

## Next step
- Frontend verification against the new response shapes; remaining
  session-lifecycle family items (duplicate/cleanup) after importer enrichment.

---

## Phase 6 — skills/memory writes native (2026-09-03, third pass)

## What was done
- Added `internal/skillsmem/mutations.go` + route wiring: `POST /api/skills/save`,
  `/api/skills/delete`, `/api/skills/toggle`, `/api/memory/write` — mirror the
  Python handlers (validation, symlink refusal, traversal rejection).
- `ToggleSkill` round-trips `config.yaml` through `yaml.Node` so comments and
  key order survive (matches Python ruamel round-trip semantics); writes
  through a `.tmp` + rename so the file is never half-written.
- `FindSkillMD` extracted (dir-name then frontmatter-name lookup) and reused by
  content/delete/toggle.
- Registered the 4 write routes native in `internal/proxy/registry.go`
  (`NativeMethods` + `NativeRoutes`).
- Added `gopkg.in/yaml.v3` dependency.

## Verification evidence (fresh, this run)
- `go build ./...` — PASS
- `go test ./...` — PASS (all packages)
- `go test -race ./...` — PASS
- `go vet ./...` — PASS
- `git diff --check` — PASS
- New regressions: `mutations_test.go` (7 tests: round-trips, traversal,
  symlink-clobber protection, comment/order preservation),
  `TestSkillsSaveDeleteNative`, `TestMemoryWriteNative`,
  `TestSkillsToggleNativePreservesConfig`.

## Next step
- Cron mutation seam (agent-side scheduler) before proxy removal for crons;
  remaining Phase 6/8 inventory + proxy deletion after that.
