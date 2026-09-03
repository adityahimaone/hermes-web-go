# Hermes Web Go — Progress Log (root, isolated local testing)

Date: 2026-09-03 (WIB)
Head: `c2fd504 feat: chat SSE journal — fan-out + Last-Event-ID replay`
Scope: Root execution (no isolated worktrees)

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

## Local execution (isolated, real binary, no mocks)
- Config: `127.0.0.1:18787`, `HERMES_WEBUI_DATA_ROOT=/tmp/hermes-web-go-local/data`,
  `HERMES_WEBUI_STATIC_DIR=./static`, `HERMES_WEBUI_DATABASE_PATH` inside temp root.
- `go build ./cmd/server -o /tmp/hermes-web-go-local/hermes-web-go` — OK.
- `GET http://127.0.0.1:18787/health` — `200 {"active_streams":0,"status":"ok","uptime_seconds":0}`.
- `GET http://127.0.0.1:18787/static/index.html` — `200`, `222182` bytes.
- `GET http://127.0.0.1:18787/api/sessions` — `200` (verified Go-native).
- `ctl.sh stop` with explicit `HERMES_WEBUI_PID_FILE`/`LOG_FILE` — process stopped, port freed.
- No real provider-backed chat turn was run (agent transport not assumed configured).

## Task list
- Updated `docs/04-task-breakdown.md` §Phase 4a: checked the journaled SSE item
  (linked to `c2fd504` evidence); left proxy-removal and transport-parity items
  open — those remain the highest-risk gate.

## Next step
- Continue blueprint order on root: finish remaining §4b parity/soak gates and
  the next `04-task-breakdown` phase before any chat proxy cutover.
