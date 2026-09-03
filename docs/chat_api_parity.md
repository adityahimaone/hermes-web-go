# Chat API Parity: Go WebUI vs Python Legacy

## Overview
Dokumen ini membandingkan implementasi chat API antara Go WebUI (baru) dan Python Legacy WebUI untuk memastikan fitur chat berfungsi identik. Digunakan sebagai referensi saat migrasi atau debugging.

---

## 1. Endpoint Comparison

### 1.1 Start Chat
| Aspect | Python Legacy | Go WebUI | Status |
|--------|--------------|----------|--------|
| Endpoint | `POST /api/chat/start` | `POST /api/chat/start` | ✅ Parity |
| Request Body | `{session_id, message}` | `{session_id, message}` | ✅ Parity |
| Response | `{stream_id, session_id}` | `{stream_id, session_id}` | ✅ Parity |
| Auth | Bearer token | Bearer token | ✅ Parity |

### 1.2 Stream SSE
| Aspect | Python Legacy | Go WebUI | Status |
|--------|--------------|----------|--------|
| Endpoint | `GET /api/chat/stream?stream_id=X` | `GET /api/chat/stream?stream_id=X` | ✅ Parity |
| Format | SSE `data: {...}` | SSE `data: {...}` | ✅ Parity |
| Event Types | `message.delta`, `run.completed`, `done` | `message.delta`, `run.completed`, `done` | ✅ Parity |

### 1.3 Session History
| Aspect | Python Legacy | Go WebUI | Status |
|--------|--------------|----------|--------|
| Load Session | `GET /api/session/{id}` | `GET /api/session/{id}` | ✅ Parity |
| Messages Field | Full history array | Full history array | ✅ Parity (fixed) |
| Persist User Msg | ✅ Yes | ✅ Yes | ✅ Parity |
| Persist Assistant Msg | ✅ Yes | ✅ Yes | ✅ Parity (fixed commit 6cabfdb) |

---

## 2. Critical Bug Fixes & Logs

### 2.1 History Loss Bug (FIXED)
**Symptom**: Setelah Hermes reply, user balas lagi → reply Hermes sebelumnya hilang dari UI.

**Root Cause**: 
- File: `internal/httpserver/chat.go:108-120`
- Issue: Token accumulator (`strings.Builder`) tidak pernah persist assistant message ke DB setelah stream selesai.
- Impact: Turn kedua load session dari DB → hanya user messages → assistant reply hilang.

**Fix Applied**:
```go
// chat.go:111-114 (after channel close)
if answer.Len() > 0 {
    _ = store.AppendMessage(db, sessionID, map[string]any{
        "role": "assistant", 
        "content": answer.String()
    })
}
```

**Verification Log**:
```
Session: 5dfa58eefd1a
Turn 1: "first" → stream d7951a40cf10 → assistant persisted
Turn 2: "second" → stream 1274b553cb19 → assistant persisted
Total messages: 17
Last message: {"role":"assistant","content":"Second received. Persistence masih jalan."}
Status: ✅ VERIFIED LIVE
Commit: 6cabfdb
```

### 2.2 Done Event Session Payload (FIXED)
**Symptom**: Frontend replace `S.messages` dengan empty array saat receive `event: done`.

**Root Cause**:
- File: `internal/httpserver/chat.go:EventDone emission`
- Issue: Go kirim `EventDone` tanpa session payload; Python kirim full session snapshot.
- Frontend behavior: `messages.js:6188` replace `S.messages = d.session.messages || []` → wipe live-streamed content.

**Fix Applied**:
```go
// chat.go:117-138 (before EventDone)
row, err := store.GetSession(db, sessionID)
if err == nil {
    var messages []map[string]any
    if row.Messages != "" {
        _ = json.Unmarshal([]byte(row.Messages), &messages)
    }
    sessionMap := map[string]any{
        "session_id":    row.ID,
        "title":         row.Title,
        "workspace":     row.Workspace,
        "model":         row.Model,
        "created_at":    row.CreatedAt,
        "updated_at":    row.UpdatedAt,
        "pinned":        row.Pinned,
        "archived":      row.Archived,
        "project_id":    row.ProjectID,
        "message_count": len(messages),
        "messages":      messages,
    }
    doneData = map[string]any{"session": sessionMap}
}
// Emit with payload
ch <- agentclient.TurnEvent{Type: agentclient.EventDone, Data: doneData}
```

**Test Log**:
```
TestChatConcurrentSessionsNoCrossContamination: PASS (0.02s)
TestChatStartStreamsTokenAndDone: PASS
TestChatDoneIncludesFullSessionHistory: PASS
All httpserver tests: PASS (1.759s)
```

---

## 3. Live Trace: Session `0733f8d8ac96` (2026-09-03)

### User symptom

After restart, chat can persist correctly. During a new turn, the browser sometimes shows no Hermes Thinking bubble; sending another message makes the previous answer appear. Browser automation was attempted, but macOS Accessibility/Screen Recording permissions blocked CuaDriver. Backend and SSE were traced directly with the same browser endpoints.

### Evidence

```text
GET /api/session?session_id=0733f8d8ac96 -> 200
messages before test: 10 (user, assistant pairs)
POST /api/chat/start -> stream_id 888c30d7eff3
GET /api/chat/stream?stream_id=888c30d7eff3 -> token/tool/done frames
messages in done.session: 12 (user, assistant pairs)
```

Backend persisted full history. `done` contained the complete session snapshot, so this trace ruled out the earlier DB persistence and empty-done-payload bugs for this turn.

### Root cause found: reasoning event parity gap

Python normalizes gateway `reasoning.available` and internal `_thinking` progress to SSE event `reasoning` (`api/gateway_chat.py:493-505`). Frontend listens for `reasoning` at `static/messages.js:5826-5847` and renders the live Thinking card.

Go `translateGatewayEvent` had no `reasoning` / `reasoning.available` cases. It returned `false` from the default branch, dropping the event before `chat.go` and the SSE writer. Therefore:

```text
gateway reasoning -> Go parser drops -> no browser reasoning event -> no Thinking bubble
```

### Fix applied (LIVE, pushed)

- Add `agentclient.EventReasoning`.
- Parse `reasoning`, `reasoning.available`, with `text` / `delta` / `content` fallback.
- Serialize `reasoning` as `event: reasoning` with `{text}` payload.
- Relay and persist reasoning alongside assistant answer.
- Add parser regression coverage.

Pushed in `5f68f8f fix: preserve reasoning events in Go chat stream` and running
live on `8787` (binary restarted, health `{"status":"ok"}`).

Fresh gates passed after fix:

```text
go test ./... -race        PASS
go vet ./...               PASS
go build ./cmd/server      PASS
node --check static/messages.js PASS
git diff --check           PASS
```

Browser automation status: CuaDriver captured Brave (macOS Accessibility +
Screen Recording granted for Brave). See §4 for the live browser trace.

---

## 4. Follow-up Issue: History Temporarily Disappears In Active Room

### Reported behavior

While staying inside one session room, previously visible Hermes responses suddenly disappear. Switching to another session and returning makes the responses visible again. This suggests persisted history may remain intact while the active frontend projection is stale or overwritten. It can happen after a new chat trigger; the initial page/session load looks correct.

### Current evidence

```text
Session: 0733f8d8ac96
SQLite messages after live turn: json_array_length(messages) = 22
Go GET /api/session?session_id=0733f8d8ac96: 200
```

A prior live trace for the same session showed:

```text
POST /api/chat/start -> stream_id 888c30d7eff3
GET /api/chat/stream?stream_id=888c30d7eff3 -> token/tool/done
Done payload -> full session snapshot, 12 messages at that point
```

Switching rooms restores the transcript. That behavior is consistent with DB persistence succeeding and the active room's in-memory `S.messages` or DOM being replaced/cleared. It does not prove the backend is innocent; both sides remain candidates until browser console/network logs are captured.

### Main suspects

1. Frontend async race: `/api/chat/stream` terminal callbacks (`done`, `stream_end`, SSE `error`) can overlap with `_restoreSettledSession()` and session refresh callbacks. These paths can all call `renderMessages()` and mutate `S.messages`.
2. Frontend state replacement: `static/messages.js` writes `S.messages` in the done handler and recovery path. `static/sessions.js`, `static/ui.js`, `static/commands.js`, and `static/outline.js` also replace it during refresh/command/session transitions.
3. Live snapshot merge: `_carryForwardEphemeralTurnFields()` preserves ephemeral fields but does not protect against a valid, shorter/stale session snapshot replacing a longer visible transcript. The `stream_end` recovery path has special handling for terminal markers; verify it covers normal history-loss timing.
4. Backend timing: Go persists assistant content before emitting `done`, but a session GET racing immediately after user-message persistence may briefly return a pre-assistant snapshot. Frontend must not commit that transient snapshot over a live transcript after terminal settlement.

### Live browser trace (2026-09-03, Brave via CuaDriver)

Captured and exercised the actual Hermes UI in Brave (window
`hello #2 — Hermes - Brave`, address `127.0.0.1:8787/session/0733f8d8ac96`).
Sent messages through the real composer via keyboard; polled the backend
between captures.

```text
state                 DB len  field message_count  user  assistant  tool
before send           22      6                   6     11         5
after  send #1        27      10                  10    12         5
(test history check)
after  send #2        31      11                  11    15         5
(cek midstream)
```

Live captures (vision) after each send showed the full transcript rendered
including prior assistant replies — the count badge `17` and all bubbles were
present. **No history loss reproduced in this session during these turns.**

### Count badge ruled out as a bug

The header count `17` and the API `message_count` are different, intentional
quantities:

- `message_count` (Go `messageCount`, `internal/httpserver/data.go:714`)
  counts only `role == "user"` rows. DB `0733f8d8ac96` → 6 user rows → field 6.
- Header `17` is the FE renderable count = `user + assistant` (17), excluding
  `tool` rows (5). Python legacy returns `len(messages)` (all 22).

So the earlier suspicion of "17 vs 22 count drift" is NOT a bug — both derive
from the same persisted array, filtered differently.

### Active-stream ramp (new lead)

During back-to-back sends from the same tab, `GET /health` `active_streams`
ramped 1 → 2 → 3 and stayed elevated, meaning multiple `/api/chat/stream`
EventSource connections were alive concurrently. If a second turn starts
before the first terminal event settles, the FE runs overlapping
`done`/`stream_end`/`_restoreSettledSession` paths that all mutate `S.messages`.
That is the likeliest window for a transient wipe — and why the loss appears
"sometimes, right after sending", then clears on room switch (fresh
`/api/session` reload restores the truth).

### Required next-agent investigation

Capture one reproduction while DevTools is open for `/session/0733f8d8ac96`,
specifically watching `S.activeStreamId` / `active_streams` during rapid
back-to-back sends:

1. Record `S.messages.length`, `S.session.session_id`, `S.activeStreamId` before send, on every SSE event, after `done`, after `stream_end`, and after every `/api/session` response.
2. Log every assignment to `S.messages` (messages.js, sessions.js, ui.js, commands.js, outline.js) with source function + incoming count.
3. Capture request order/bodies for start, stream, session; confirm whether a 2nd stream can interleave before the 1st `done`.
4. Compare visible DOM count vs `S.messages.length` right after the wipe, then switch room and compare vs DB/API.
5. Reproduce with + without network throttling; if only throttled → FE async ordering race.
6. Add a regression test at the lifecycle/integration boundary; pure helper tests are insufficient.

### Interim classification (updated after live browser trace)

```text
Persisted DB/API history: present in latest trace
Active-room display: loss NOT reproduced across 2 live sends this session;
                      reported by user as intermittent "right after sending"
Likely layer: frontend state/lifecycle race
Backend possibility: stale snapshot or stream terminal ordering
Confidence: moderate — backend persistence fully verified; FE race is lead,
            driven by observed concurrent active_streams ramp
```

---

## 5. SSE Event Format Reference

### 3.1 Token Delta
```
data: {"event":"message.delta","delta":"Hello","session_id":"abc123","stream_id":"def456"}
```

### 3.2 Run Completed
```
data: {"event":"run.completed","output":"Full response text","session_id":"abc123","run_id":"run_xyz"}
```

### 3.3 Done (with session payload)
```
data: {"event":"done","session":{"session_id":"abc123","title":"Chat","messages":[...],"message_count":5}}
```

---

## 4. Database Schema (SQLite)

### sessions table
```sql
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    title TEXT,
    workspace TEXT,
    model TEXT,
    messages TEXT,  -- JSON array of {role, content}
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    pinned INTEGER DEFAULT 0,
    archived INTEGER DEFAULT 0,
    project_id TEXT
);
```

### Message Persistence Flow
1. User message: `store.AppendMessage(db, sid, {role:"user", content:"..."})` → immediate on `/api/chat/start`
2. Assistant message: `store.AppendMessage(db, sid, {role:"assistant", content:"..."})` → on stream channel close (after all tokens accumulated)
3. Done event: Fetch fresh session from DB → include in `EventDone.Data.session`

---

## 5. Known Issues — superseded by `docs_update/3-sept-2026-part-3/10-chat-parity-fixes.md`

The table here now reflects *actual* priority per Phase 4b (auto-mode safety
net must not be dead) — it supersedes the original doc's priorities. Detailed
code/test snippets live in `10-chat-parity-fixes.md` §§1–3; only the status row
stays here.

| # | Issue | Priority (actual) | Status | Where |
|---|---|---|---|---|
| 5.4 | HTTP parser drops all events (read only `event:` line, gateway is `data: {event:...}`) | **Highest** | ✅ Fixed in `httpclient.go` `readEvents` + `translateGatewayEvent` | §§1, 1T |
| 5.1 | Duplicate completion `run.completed` → `done` | Medium | ✅ Fixed in `chat.go` relay (swallow + idempotent `finishTurn`) | §2, test |
| 5.3 | Partial answer dropped on agent error | Medium | ✅ Fixed via same `finishTurn("partial")` | §2, test |
| 5.2 | Frontend blind replace `S.messages` on absent payload | Low | ✅ Fixed `messages.js:6188` guard | §3, todo |

§§1, 1T detail: the §5.4 parser fix (buffer, JSON `event` fallback, swallow),
and its regression test `TestReadEvents_ParsesEventTypeFromJSONPayload`. §2 is the
relay-loop refactor; §3 is the frontend guard.

---

## 6. Testing Commands

### Unit Tests
```bash
cd /Users/adityahimawan/Development/hermes-web-go
go test ./internal/httpserver -run TestChat -v
go test ./internal/store ./internal/stream
```

### Live Verification
```bash
# Health check
curl -s http://127.0.0.1:8787/health | jq .

# Start chat
curl -X POST http://127.0.0.1:8787/api/chat/start \
  -H "Content-Type: application/json" \
  -d '{"session_id":"test123","message":"hello"}' | jq .

# Stream SSE
curl -N "http://127.0.0.1:8787/api/chat/stream?stream_id=<STREAM_ID>"

# Check session history
curl -s "http://127.0.0.1:8787/api/session/<SESSION_ID>" | jq '.messages | length'
```

### Build & Restart
```bash
cd /Users/adityahimawan/Development/hermes-web-go
go build -o hermes-web-go ./cmd/server
pkill -f './hermes-web-go' || true
HERMES_WEBUI_LEGACY_PROXY_URL="http://127.0.0.1:52378" \
HERMES_WEBUI_RUNNER_BASE_URL="http://127.0.0.1:8642" \
./hermes-web-go &
```

---

## 7. Architecture Diagram

```
Browser → Go WebUI (8787) → gRPC Shim → Gateway (8642) → Agent
              ↓
         SQLite DB (sessions table)
              ↓
         SSE Stream ←──┐
                        │
         Token Accumulator (strings.Builder)
                        │
         Persist on Channel Close
                        │
         Done Event + Session Payload
```

---

## 8. Commit History (Chat Fixes)

| Commit | Description | Files Changed |
|--------|-------------|---------------|
| `6cabfdb` | Persist assistant reply to session history on turn completion | `chat.go` (+9 lines) |
| `[PENDING]` | Include session payload in done event to preserve history | `chat.go`, `chat_test.go` |

---

## 9. Environment Variables

```bash
HERMES_WEBUI_LEGACY_PROXY_URL=http://127.0.0.1:52378  # Python fallback
HERMES_WEBUI_RUNNER_BASE_URL=http://127.0.0.1:8642    # Gateway
HERMES_WEBUI_AGENT_API_KEY=<REDACTED>                 # Auth key
```

---

## 10. Contact & Handoff Notes

**For Next Agent**:
1. Read this doc first → understand parity status.
2. Check pending issues §5 → prioritize based on user request.
3. Use testing commands §6 → verify before/after changes.
4. Reference commit history §8 → avoid re-fixing solved bugs.
5. Key files:
   - `internal/httpserver/chat.go` — main chat logic
   - `internal/httpserver/chat_test.go` — unit tests
   - `static/messages.js` — frontend SSE handler
   - `internal/store/store.go` — DB operations

**Debug Tips**:
- History loss? Check `AppendMessage` calls in `chat.go`.
- SSE format wrong? Compare Python `_translate_event` vs Go `writer.go`.
- Test timeout? Remove shared gates; use buffered channels (see §2.2 test fix).
- Frontend wipe? Verify `EventDone.Data.session` populated.

---

*Generated: 2026-09-03*
*Last Updated: After commit 6cabfdb + done payload fix*
*Status: Production-ready with known minor issues (§5)*

</content>