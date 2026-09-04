# 11 — History Disappears In Active Room (Race Condition Fix)

Source: `chat_api_parity.md` §4 live trace, session `0733f8d8ac96`, 2026-09-03. This is a
different class of bug from `docs/10-chat-parity-fixes.md` — those were parsing/persistence bugs
with a single deterministic root cause. This one is a **race condition**, confirmed by the
`active_streams` ramp (1→2→3 during rapid sends) and the fact that switching rooms and back
restores the "lost" history — that specific symptom rules out data loss and points at **stale
async state clobbering fresh state**.

## Reading the evidence against the four suspects

The doc's own trace already does most of the diagnostic work — here's what it actually proves and
rules out:

| Suspect | Verdict from the evidence | Why |
|---|---|---|
| 4 (backend timing) | **Not the root cause, but a real contributing signal** | A `GET /api/session` racing between the user-message write and the assistant-message write *will* legitimately return a transient, shorter snapshot — that's correct behavior for an unversioned read, not a bug. The bug is that nothing downstream can tell "this snapshot is old" from "this snapshot is current." |
| 1 (async race) | **Directly confirmed** | `active_streams` ramping to 3 means 3 concurrent `EventSource` connections were alive, each with its own terminal (`done`/`stream_end`) callback capable of firing at any time, in any order. |
| 2 (multiple mutation sites) | **Confirmed, and it's what turns a race into a visible bug** | Five different files write directly to `S.messages`. Without a shared arbiter, "last write wins" — and "last" is decided by network/scheduling timing, not by which snapshot is actually newest. |
| 3 (snapshot merge) | **This is the actual mechanism** | `_carryForwardEphemeralTurnFields()` protects specific fields but nothing validates that an incoming snapshot's `messages` array is at least as current as what's already rendered. Any of the five call sites can commit a stale, shorter array over a longer live one. |

**Root cause, stated plainly:** there is no shared, monotonic notion of "how current is this
snapshot" anywhere in the system. Every write to `S.messages` is unconditional. The fix is to give
every session snapshot a monotonically increasing version number, generated atomically by the one
place that's allowed to say "the truth changed" — the DB write — and refuse to apply any snapshot
whose version is behind the last one already applied for that session.

## The fix: a `rev` column + one shared frontend gate

This is a two-sided fix, and — importantly — it is **strictly additive**. Nothing about the wire
format, existing fields, or existing call sites' inputs changes; `rev` is a new optional field.
Any client that ignores it (old cached JS, a debugging curl, whatever) behaves exactly as before.

### 1. Backend: atomic `rev` counter, incremented in the same transaction as every message write

```sql
-- migration
ALTER TABLE sessions ADD COLUMN rev INTEGER NOT NULL DEFAULT 0;
```

```go
// internal/store/store.go
// AppendMessage now returns the new rev, incremented atomically in the same
// transaction as the message write. This is what makes rev trustworthy as a
// monotonic marker — it can never be observed out of order relative to the
// messages array it describes, because they're written together.
func AppendMessage(db *sql.DB, sessionID string, msg map[string]any) (rev int64, err error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var messagesJSON string
	if err := tx.QueryRow(`SELECT messages FROM sessions WHERE id = ?`, sessionID).Scan(&messagesJSON); err != nil {
		return 0, err
	}
	var messages []map[string]any
	if messagesJSON != "" {
		_ = json.Unmarshal([]byte(messagesJSON), &messages)
	}
	messages = append(messages, msg)
	updated, err := json.Marshal(messages)
	if err != nil {
		return 0, err
	}

	if _, err := tx.Exec(
		`UPDATE sessions SET messages = ?, rev = rev + 1, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		string(updated), sessionID,
	); err != nil {
		return 0, err
	}
	if err := tx.QueryRow(`SELECT rev FROM sessions WHERE id = ?`, sessionID).Scan(&rev); err != nil {
		return 0, err
	}
	return rev, tx.Commit()
}
```

```go
// Every place that builds a session JSON payload — GET /api/session, the
// done-event snapshot from chat_api_parity.md §2.2, session list/search —
// must include rev. Example for the done-event builder in chat.go:
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
	"rev":           row.Rev,          // NEW — see §11 fix
	"message_count": len(messages),
	"messages":      messages,
}
```

### 2. Frontend: one shared gate, all five mutation sites route through it

```javascript
// static/messages.js — add near the top, shared by every file that currently
// writes S.messages directly.
window.__sessionRevHighWater = window.__sessionRevHighWater || {};

/**
 * The single arbiter for "is this snapshot allowed to become what the user
 * sees." Every call site that currently does `S.messages = x.messages`
 * (messages.js done handler, sessions.js refresh, ui.js, commands.js,
 * outline.js) must route through this instead.
 */
function applySessionSnapshot(sessionPayload, sourceLabel) {
    if (!sessionPayload || !Array.isArray(sessionPayload.messages)) {
        console.warn(`[applySessionSnapshot] rejected: no valid messages array (source=${sourceLabel})`);
        return false; // covers chat_api_parity.md §5.2's defensive-guard case too
    }

    const sid = sessionPayload.session_id;
    const incomingRev = typeof sessionPayload.rev === 'number' ? sessionPayload.rev : null;
    const highWater = window.__sessionRevHighWater[sid] ?? -1;

    if (incomingRev !== null && incomingRev < highWater) {
        // THIS is the fix — a stale snapshot (e.g. the transient GET that
        // raced between the user-message and assistant-message writes,
        // suspect #4) is refused instead of silently overwriting a newer,
        // longer, already-visible transcript.
        console.warn(
            `[applySessionSnapshot] rejected STALE snapshot (source=${sourceLabel}, ` +
            `incoming rev=${incomingRev}, applied rev=${highWater}, session=${sid})`
        );
        return false;
    }

    S.messages = sessionPayload.messages;
    if (incomingRev !== null) {
        window.__sessionRevHighWater[sid] = incomingRev;
    }
    renderMessages();
    return true;
}
```

Replace every direct assignment:

```javascript
// static/messages.js:6188 (the done handler from chat_api_parity.md §2.2)
// Before: S.messages = d.session.messages || [];
// After:
applySessionSnapshot(d.session, 'chat.done');
```

```javascript
// static/sessions.js — session refresh path
applySessionSnapshot(sessionData, 'sessions.refresh');
```

Apply the same replacement in `static/ui.js`, `static/commands.js`, and `static/outline.js`
wherever they currently assign to `S.messages` directly. This is the part that turns "one correct
fix" into "a fix that actually closes the bug" — a single hardened call site with four other files
still writing around it fixes nothing.

### 3. Stream-identity filtering (closes suspect #1's actual trigger, not just its symptom)

The `rev` guard protects the data, but it's worth also stopping a *stale stream's* terminal event
from being processed at all, since acting on it does other work too (rendering, clearing loading
state) beyond just the snapshot assignment:

```javascript
// static/messages.js — inside the SSE handler, before processing done/stream_end
if (evt.stream_id && evt.stream_id !== S.activeStreamId) {
    console.warn(`[sse] ignoring terminal event from superseded stream_id=${evt.stream_id}, active=${S.activeStreamId}`);
    return;
}
```

This directly addresses the observed `active_streams` ramp: when a second turn starts before the
first settles, the first stream's eventual `done` is now a no-op instead of a second, out-of-order
mutation attempt.

## Why this fixes all four suspects at once

- **#4 (backend timing)**: the transient snapshot's `rev` is provably lower than any snapshot taken
  after the assistant message lands, so even if it arrives late, it's rejected.
- **#1 (async race)**: the stream-identity filter stops a superseded stream's terminal event from
  doing anything; the `rev` guard is the backstop if it still fires.
- **#2 (multiple mutation sites)**: they all now call one function; there's no longer a way for any
  of them to silently regress the visible state.
- **#3 (snapshot merge)**: `applySessionSnapshot` *is* the missing validation this suspect
  identified — "does this snapshot deserve to replace what's visible" now has an actual answer.

## Tests to add

```go
// internal/store/store_test.go
func TestAppendMessageRevIsMonotonicAndAtomic(t *testing.T) {
	db := newTestDB(t)
	sid := "s1"
	createSession(t, db, sid)

	rev1, err := AppendMessage(db, sid, map[string]any{"role": "user", "content": "hi"})
	if err != nil || rev1 != 1 {
		t.Fatalf("want rev=1, got rev=%d err=%v", rev1, err)
	}
	rev2, err := AppendMessage(db, sid, map[string]any{"role": "assistant", "content": "hello"})
	if err != nil || rev2 <= rev1 {
		t.Fatalf("want rev2 > rev1, got rev1=%d rev2=%d", rev1, rev2)
	}
}

func TestGetSessionRevMatchesLastAppend(t *testing.T) {
	// AppendMessage's returned rev must equal what a subsequent GetSession reports —
	// otherwise the frontend guard is comparing against a value the backend itself
	// doesn't stand behind.
}
```

```javascript
// A lightweight assertion-style test (adapt to whatever harness the frontend
// uses, if any — if there's no JS test runner yet, at minimum exercise this
// manually per the checklist below before considering the fix verified):
//
// applySessionSnapshot({session_id: 's1', rev: 2, messages: [a,b]}, 'test') // -> true, S.messages = [a,b]
// applySessionSnapshot({session_id: 's1', rev: 1, messages: [a]}, 'test')   // -> false, S.messages unchanged
// applySessionSnapshot({session_id: 's1', rev: 3, messages: [a,b,c]}, 'test') // -> true, S.messages = [a,b,c]
```

## Live verification checklist (adapting the doc's own "Required next-agent investigation")

- [ ] Apply the `rev` migration and backend change; confirm `GetSession`'s `rev` matches
      `AppendMessage`'s returned value for the same write.
- [ ] Apply the frontend `applySessionSnapshot` gate; grep `static/*.js` for any remaining direct
      `S.messages =` assignment outside of `applySessionSnapshot` itself — there should be none.
- [ ] Reproduce the original rapid-send scenario (send message, immediately send a second before
      the first's Thinking bubble resolves) — watch `/health`'s `active_streams` ramp exactly as
      before, but confirm the visible transcript no longer regresses.
- [ ] Add a console assertion during manual testing: log every `applySessionSnapshot` call with its
      `sourceLabel`, incoming `rev`, and accept/reject outcome — confirm you see at least one
      rejected stale snapshot during a rapid-send reproduction, proving the guard is actually
      engaging, not just present but never triggered.
- [ ] Re-run the doc's own CuaDriver browser trace one more time under this fix, watching
      `S.activeStreamId` and `S.messages.length` per its "Required next-agent investigation" §4
      steps 1–4, to get a clean before/after comparison instead of the prior inconclusive trace.

## Where this fits in the task breakdown

Add to Phase 4a in `04-task-breakdown.md`, alongside the `docs/10-chat-parity-fixes.md` items —
this is a second, independent gate before "remove proxy fallback for chat routes," not a
replacement for the first:

- [ ] Add `rev` column + atomic increment in `AppendMessage`; include `rev` in every session JSON
      payload (GET, done event, list/search) — `docs/11-history-race-fix.md` §1.
- [ ] Add `applySessionSnapshot` in `static/messages.js`; migrate all five direct-assignment call
      sites (`messages.js`, `sessions.js`, `ui.js`, `commands.js`, `outline.js`) to use it —
      `docs/11-history-race-fix.md` §2.
- [ ] Add stream-identity filtering for terminal SSE events — `docs/11-history-race-fix.md` §3.
- [ ] `TestAppendMessageRevIsMonotonicAndAtomic`, `TestGetSessionRevMatchesLastAppend`.
- [ ] Run the live verification checklist above; only then consider this bug closed.
