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
