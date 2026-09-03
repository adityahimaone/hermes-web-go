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
- No real provider-backed chat turn was run (agent transport not assumed configured).

## Task list
- Updated `docs/04-task-breakdown.md` §Phase 4a: checked the journaled SSE item
  (linked to `c2fd504` evidence); left proxy-removal and transport-parity items
  open — those remain the highest-risk gate.

## Next step
- Continue blueprint order on root: finish remaining §4b parity/soak gates and
  the next `04-task-breakdown` phase before any chat proxy cutover.
