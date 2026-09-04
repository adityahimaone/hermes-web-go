# 10 — Chat Parity Fixes (from `chat_api_parity.md` §5)

Source: `hermes-web-go/docs/chat_api_parity.md`, as of commit `6cabfdb` (2026-09-03). That doc's
§§1–4 are solid — history-loss and done-payload bugs are fixed and live-verified. This doc picks
up its §5 "Known Issues & Pending Work" and gives each one a concrete fix, in the order they
should actually be tackled (not the order they're numbered in the source doc).

## Priority triage — why the order here differs from `chat_api_parity.md`'s numbering

The source doc marks §5.4 "Priority: Low (fix if HTTP fallback needed)". **That's the wrong
priority given `docs/09-phase4b-unblock.md`'s design.** The entire safety story for the gRPC fast
path is: *if gRPC fails, fall back to `httpClient`, and the user never notices.* If `httpClient`'s
own SSE parser is broken, that fallback is fiction — the day the gRPC shim actually has a problem
in production, chat breaks instead of quietly degrading. This needs to be fixed **before** Phase 4b
is trusted in `auto` mode, not filed as a low-priority nice-to-have.

Fix order used below: **§5.4 first** (silent safety-net failure) → **§5.1 + §5.3 together**
(same relay-loop refactor fixes both) → **§5.2 last** (frontend, cosmetic/defensive only).

## 1. Fix §5.4 — `internal/agentclient/httpclient.go` SSE parser (do this first)

**Root cause, restated precisely:** the parser waits for a standalone `event:` line before it will
attach a type to the following `data:` line — but per `chat_api_parity.md` §3, the gateway's SSE
frames only ever look like `data: {"event":"message.delta",...}` — the event name is a **field
inside the JSON payload**, never a separate SSE-level `event:` line. So `eventType` is always
empty, every event gets misrouted or dropped, and the HTTP path is dead on arrival for real
gateway traffic — exactly matching the doc's own note that "live system pakai gRPC via shim
(unaffected)," i.e. this bug has never actually been exercised in production.

```go
// internal/agentclient/httpclient.go
func (c *HTTPClient) readEvents(ctx context.Context, resp *http.Response, out chan<- TurnEvent) error {
	scanner := bufio.NewScanner(resp.Body)
	// Default bufio.Scanner buffer is 64KB — a `done` event carries a full
	// session snapshot (per chat_api_parity.md §2.2) which can exceed that on
	// a long conversation. Widen it so a long history doesn't get silently
	// truncated mid-JSON and fail to parse.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var pendingEventType string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			pendingEventType = "" // blank line = SSE record boundary
			continue
		case strings.HasPrefix(line, "event:"):
			// Kept for forward-compatibility if the gateway ever adds a real
			// event: line — but per the observed format, this branch is
			// currently never hit.
			pendingEventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		case strings.HasPrefix(line, "data:"):
			raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if raw == "" {
				continue
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(raw), &payload); err != nil {
				slog.Warn("agentclient(http): malformed SSE data line, skipping",
					"err", err, "raw_prefix", firstN(raw, 120))
				continue
			}

			// THE FIX: fall back to the JSON payload's own "event" field
			// whenever no SSE-level `event:` line preceded it — which per
			// the gateway's actual format (chat_api_parity.md §3) is always.
			eventType := pendingEventType
			if eventType == "" {
				if et, ok := payload["event"].(string); ok {
					eventType = et
				}
			}

			ev, ok := translateGatewayEvent(eventType, payload)
			if !ok {
				continue // unknown/informational event type — see §2 below
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return scanner.Err()
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
```

```go
// translateGatewayEvent maps the gateway's wire event names
// (message.delta / run.completed / done / error) onto the shared TurnEvent
// shape agentclient already defines, so grpcClient and httpClient produce
// identical events regardless of transport (per 01-architecture-design.md §2b
// item 4). The second return value is false for events that should be
// swallowed rather than forwarded — see the run.completed note in §2 below.
func translateGatewayEvent(eventType string, payload map[string]any) (TurnEvent, bool) {
	switch eventType {
	case "message.delta":
		text, _ := payload["delta"].(string)
		return TurnEvent{Type: EventToken, Text: text}, true

	case "run.completed":
		// §5.1 fix (see §2 below): this is informational only. The `done`
		// event is the single source of truth for "turn is over" — do not
		// forward this as a second completion signal.
		return TurnEvent{}, false

	case "done":
		var sessionRaw []byte
		if s, ok := payload["session"]; ok {
			sessionRaw, _ = json.Marshal(s)
		}
		return TurnEvent{Type: EventDone, DataJSON: string(sessionRaw)}, true

	case "error":
		msg, _ := payload["message"].(string)
		return TurnEvent{Type: EventError, Error: msg}, true

	default:
		slog.Debug("agentclient(http): unrecognized event type, skipping", "type", eventType)
		return TurnEvent{}, false
	}
}
```

**Test to add** (closes the actual gap — this bug shipped because nothing exercised the HTTP path
against realistic gateway output):

```go
// internal/agentclient/httpclient_test.go
func TestReadEvents_ParsesEventTypeFromJSONPayload(t *testing.T) {
	body := `data: {"event":"message.delta","delta":"Hel"}` + "\n\n" +
		`data: {"event":"message.delta","delta":"lo"}` + "\n\n" +
		`data: {"event":"run.completed","output":"Hello"}` + "\n\n" +
		`data: {"event":"done","session":{"session_id":"s1","messages":[]}}` + "\n\n"

	out := make(chan TurnEvent, 10)
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	if err := (&HTTPClient{}).readEvents(context.Background(), resp, out); err != nil {
		t.Fatalf("readEvents: %v", err)
	}
	close(out)

	var got []TurnEvent
	for ev := range out {
		got = append(got, ev)
	}
	// Expect exactly 3 events: two tokens + one done — run.completed swallowed.
	if len(got) != 3 {
		t.Fatalf("want 3 events (2 tokens + done, run.completed swallowed), got %d: %+v", len(got), got)
	}
	if got[0].Type != EventToken || got[0].Text != "Hel" {
		t.Errorf("event 0: %+v", got[0])
	}
	if got[2].Type != EventDone {
		t.Errorf("event 2: want done, got %+v", got[2])
	}
}
```

This test would have caught the original bug (it fails against the old parser, since `eventType`
would stay empty the whole time and every branch below `data:` would fall through
un-translated) — worth confirming that by running it against the pre-fix code once, so you know
the test is actually load-bearing and not just testing the fix against itself.

## 2. Fix §5.1 + §5.3 together — single relay-loop refactor

Both bugs live in the same place (`internal/httpserver/chat.go`'s turn-relay loop) and both are
fixed by the same change: **persistence and completion-signaling should happen in one `defer`
that runs on every exit path**, not only on the clean-completion branch.

```go
// internal/httpserver/chat.go — relay loop
func relayTurn(ctx context.Context, db *sql.DB, sessionID string,
	events <-chan agentclient.TurnEvent, out chan<- StreamEvent) {

	var answer strings.Builder
	var turnErr error
	doneEmitted := false

	// §5.3 fix: persist whatever text accumulated on EVERY exit path —
	// success, error, or context cancellation — not only clean completion.
	// A partial answer the user can see and retry from is strictly better
	// than one silently dropped because the agent errored mid-stream.
	defer func() {
		if answer.Len() == 0 {
			return
		}
		status := "complete"
		if turnErr != nil {
			status = "partial"
		}
		_ = store.AppendMessage(db, sessionID, map[string]any{
			"role":    "assistant",
			"content": answer.String(),
			"status":  status, // frontend can render a subtle "cut off" marker on "partial"
		})
	}()

	for ev := range events {
		switch ev.Type {
		case agentclient.EventToken:
			answer.WriteString(ev.Text)
			out <- StreamEvent{Type: "message.delta", Delta: ev.Text}

		case agentclient.EventError:
			turnErr = errors.New(ev.Error)
			out <- StreamEvent{Type: "error", Message: ev.Error}
			return // deferred persistence above still runs

		case agentclient.EventRunCompleted:
			// §5.1 fix: informational only now (see httpclient.go translate
			// step in §1 for the http path — the grpc path needs the same
			// treatment if it separately forwards run.completed-equivalent
			// events; check internal/agentclient/grpcclient.go's event
			// translation for the same double-signal pattern).
			continue

		case agentclient.EventDone:
			if doneEmitted {
				continue // never emit a second completion signal downstream
			}
			doneEmitted = true
			out <- StreamEvent{Type: "done", Session: buildSessionPayload(db, sessionID)}
			return
		}
	}

	// Channel closed without an explicit done/error event (e.g. upstream
	// hung up) — still emit exactly one completion signal so the frontend's
	// SSE handler doesn't wait forever, and let the defer above persist
	// whatever partial answer exists.
	if !doneEmitted {
		out <- StreamEvent{Type: "done", Session: buildSessionPayload(db, sessionID)}
	}
}
```

**Note for whoever picks this up:** check `internal/agentclient/grpcclient.go`'s event translation
too — if it separately forwards a `run.completed`-shaped event before `done`, apply the same
"swallow it, `done` is the only completion signal" treatment there, so both transports behave
identically per `01-architecture-design.md` §2b item 4 (which specifically requires this).

**Tests to add:**

```go
func TestChatPersistsPartialAnswerOnAgentError(t *testing.T) {
	// simulate: 2 tokens then an EventError, no EventDone
	// assert: AppendMessage called once with role=assistant,
	//         content="<the 2 tokens>", status="partial"
}

func TestChatNeverEmitsTwoCompletionSignals(t *testing.T) {
	// simulate: EventRunCompleted followed by EventDone
	// assert: exactly one StreamEvent{Type:"done"} reaches `out`
}
```

## 3. Fix §5.2 — frontend defensive guard (do last, cosmetic given §1–2 fix the root cause)

```javascript
// static/messages.js, around line 6188
// Before:
//   S.messages = d.session.messages || [];
// After:
if (d.session && Array.isArray(d.session.messages)) {
    S.messages = d.session.messages;
} else {
    console.warn('[messages.js] done event missing valid session.messages — keeping existing history instead of wiping it');
    // S.messages intentionally left untouched
}
```

Low-risk, no behavior change in the normal case — only changes what happens on a malformed/absent
payload, which after §1–2's fixes should no longer occur in practice, but costs nothing to guard
against directly per the frontend-changes-only-when-safe rule in
`07-future-vite-frontend.md`'s spirit (though this is a bugfix, not a frontend rewrite, so it's
fine now rather than waiting for Phase 9).

## 4. Updated priority table (supersedes `chat_api_parity.md` §5's stated priorities)

| # | Issue | Original priority | Actual priority | Why |
|---|---|---|---|---|
| 5.4 | HTTP parser drops all events | Low | **Highest — fix first** | Silently defeats the Phase 4b `auto`-mode safety net; currently untested because nothing exercises this path in production |
| 5.1 | Duplicate completion event | (unstated, implied low) | **Medium — fix with 5.3** | Same code change closes both; low cost to do now |
| 5.3 | Error-path persistence drops partial answers | Medium | **Medium — fix with 5.1** | User-visible data loss on any agent error mid-stream; same relay-loop refactor as 5.1 |
| 5.2 | Frontend blind replace | Not urgent | **Low — do last** | Root cause is fixed by §1–2; this is defense in depth, not a live bug once those land |

## 5. Task-breakdown update

Add to Phase 4a in `04-task-breakdown.md` (it was marked done, but these findings mean "remove
proxy fallback for chat routes" shouldn't happen until this list is closed — a broken fallback
path is exactly the kind of thing that should block a cutover, not follow it):

- [ ] §5.4: fix `httpclient.go`'s `readEvents` to read event type from the JSON payload's `event`
      field (`docs/10-chat-parity-fixes.md` §1); add `TestReadEvents_ParsesEventTypeFromJSONPayload`.
- [ ] §5.1 + §5.3: refactor `chat.go`'s relay loop to a single deferred persist +
      single-completion-signal design (`docs/10-chat-parity-fixes.md` §2); add
      `TestChatPersistsPartialAnswerOnAgentError` and `TestChatNeverEmitsTwoCompletionSignals`.
- [ ] Check `grpcclient.go` for the same run.completed-then-done double-signal pattern; apply the
      same swallow-it fix if present, so both transports match per `01-architecture-design.md` §2b
      item 4.
- [ ] §5.2: add the frontend defensive guard in `static/messages.js` (`docs/10-chat-parity-fixes.md` §3).
- [ ] Re-run the full `internal/httpserver` + `internal/agentclient` test suites with `-race`.
- [ ] Only after all of the above: proceed with "remove proxy fallback for chat routes."
