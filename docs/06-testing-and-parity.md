# 06 — Testing & Parity Verification

Goal: never trust "it looks right in the browser" as the bar for cutting a route over from
proxied to native. Every route needs an automated parity check before its proxy fallback is
removed.

## 1. Golden fixture harness (built in Phase 0, used through Phase 8)

Phase 0 implementation: `tools/phase0_harness.py`. `record` accepts JSON journey plus local base URL; `compare` compares normalized exchanges. Each recorded row has redacted/normalized `request` data for comparison plus redacted, unnormalized `replay_request` operational data; replay uses only `replay_request`, so session IDs and POST bodies remain usable without storing secrets. `replay_request` is excluded from comparison. Normalization remains comparison-only. Authorization/cookie/API-key/password/token/secret/CSRF values redact even when embedded in arbitrary query/header/body strings. Session, stream, run, task, request IDs and timestamp fields normalize through one exchange-wide mapping; repeated relationships remain stable and inconsistent relationships fail. Volatile response headers such as `Date` drop while relevant headers remain. SSE response bodies parse into ordered `{event,data}` records. Replay preserves JSON bodies. Opaque `body_base64` requires explicit `body_base64_safe: true`; `body_fixture` requires a relative path under explicit fixture root and rejects absolute/traversal paths. Parent output directories are created. HTTP/transport errors fail closed: the run aborts with no fixture row rather than recording a partial parity result. Harness never fabricates model output.

```bash
python3 tools/phase0_lifecycle.py --source /path/to/hermes-webui-personal \
  --python /tmp/hermes-webui-phase0-venv/bin/python --journey testdata/phase0-safe-journey.json \
  --output-dir /tmp/hermes-phase0-run
```

The checked-in journey is intentionally safe and declarative: exactly 3 read-only GET exchanges (`/health`, `/api/sessions`, and `/api/workspaces`); no mutations, provider traffic, credentials, or fixture file reads. Output uses a unique `run-*` subdirectory when requested directory is non-empty. Lifecycle roots are removed by default after each run; pass `retain_roots=True` only for explicit manual review. Harness never fabricates model output.

Live result (2026-09-02): `tools/phase0_lifecycle.py` passed fresh A/B comparison with exactly 3 bounded GET exchanges against unmodified `hermes-webui-personal`, using `/tmp/hermes-webui-phase0-venv`, distinct temporary state/home/workspace roots, and workspace substitution. This 3-GET route set captures no session ID (its responses carry no scalar session_id), so no ID remapping was exercised in this run. No provider chat was run in this lifecycle; safe repeatability was not established, so no chat parity claim is made. Comparative audit: `hermes-web-studio` was inspected read-only via CodeGraph; its Go `Handler` routes differ from primary parity source `hermes-webui-personal/api/routes.py`, so no routes were merged into this inventory.

`tools/generate_route_inventory.py` regenerates `testdata/route-inventory.json` from primary source. `/share`, `/api/gateway/start`, `/api/gateway/stop`, and `/api/gateway/restart` are included.

No checked-in provider fixture is claimed: provider availability, credentials, model output, and approval/tool execution are deployment-specific. Real traffic belongs in disposable temporary Hermes workspace/session with safe no-tool prompt; inspect redacted fixture before use.

- Script a realistic set of user journeys against the **existing, unmodified Python server**:
  - New session → send chat message → observe SSE stream → approval triggered → respond →
    upload a file → browse workspace → open a file → view crons/skills/memory panels →
    rename session → archive session → delete session.
- Record full request/response pairs (headers worth keeping: `Content-Type`,
  `Content-Disposition`; drop volatile ones like `Date`) and, for SSE, the ordered event sequence
  with payloads.
- Normalize volatile fields before comparison: `session_id`, timestamps, `stream_id` — replace
  with stable placeholders (`<SESSION_ID>`, `<TS>`) using a consistent substitution table built
  from the fixture's own first occurrence of each value, so relative consistency is still checked
  (e.g. the same session_id appearing in two different response fields must still match each
  other after normalization).
- Store fixtures under version control (`testdata/fixtures/*.json` or similar) — they are the
  spec, in executable form, more trustworthy than this prose document once they exist.

## 2. Parity test types, per phase

| Phase | What to test | How |
|---|---|---|
| 1 | Static files byte-identical; proxy passthrough transparent | `diff` response bytes for every static asset; fire a sample of fixture requests through the Go-fronted proxy, confirm byte-identical to direct-Python baseline |
| 2 | Read-only JSON shapes match, field-for-field | JSON deep-equal against normalized fixtures; explicit path-traversal attack fixtures (expect clean 400, not 500) |
| 3 | Mutation side effects match (session/file state after the call) | Call the endpoint, then re-fetch via a read-only endpoint already proven in Phase 2, diff against expected post-state |
| 4 | SSE event sequence + payload shapes match; concurrency safety; **both `agentclient` transports produce identical `TurnEvent` sequences; fallback is invisible to the browser** | Replay a fixture chat, assert event **types and order** match (content will differ since it's LLM output — assert shape/type, not exact text); dedicated `go test -race` concurrent-session test; run the same fixture replay once through `httpClient` and once through `grpcClient`, diff the event sequences; a dedicated fallback-injection test that kills the gRPC socket mid-stream and asserts the turn completes via `httpClient` with no error event surfaced |
| 5 | Approval state transitions match | Simulate `once`/`session`/`always`/`deny` against fixture scenarios, assert `_pending`-equivalent state before/after each |
| 6 | Cron/skills/memory read shapes match; cron actions have correct side effects | Same JSON deep-equal approach as Phase 2 |
| 7 | Auth gate behavior; `/health` shape | Attempt access without cookie → 401 JSON (not redirect); with valid cookie → 200; `/health` field-for-field |
| 8 | Full end-to-end fixture replay with zero Python process running | The ultimate gate — if this passes with Python fully stopped, Phase 8 is real |

## 3. Regression gate (ongoing, mirrors the Python project's own `test_regressions.py` idea)

Port the spirit of the original's "permanent regression gate" and its numbered Critical Rules
(`02-api-parity-mapping.md` §8) into actual Go tests, one test per rule, named after the rule:

```go
func TestRule1_DeleteNeverCreates(t *testing.T) { ... }
func TestRule3_AgentContractUsesTaskID(t *testing.T) { ... }
func TestRule6_NeverAutoCreateSessionOnBoot(t *testing.T) { ... }
func TestRule9_ApprovalIteratesAllPatternKeys(t *testing.T) { ... }
// etc.
```

These run on every commit, forever — they encode the exact bugs the original project already
fixed once and explicitly warned future contributors not to reintroduce. Treat that warning as
applying equally to this rewrite.

## 4. Load/soak testing before each cutover

For each route moving from proxied → native (per `05-migration-strategy.md` §5): run the new
native handler under realistic single-user load (this is a homelab tool, not a multi-tenant SaaS
— "realistic" means one interactive user plus scheduled cron jobs firing concurrently, not
thousands of RPS) for at least one full day of real usage before removing the proxy fallback for
that route.

## 5. Manual browser test pass (once per phase, before tagging)

The original project keeps a `TESTING.md` manual browser test plan alongside its automated
coverage. Do the same here at minimum for: composer send/receive, approval card interaction,
file tree browsing + preview (image and markdown), session list operations (pin/archive/delete/
rename/search), and the Control Center panels (crons/skills/memory). Automated tests catch shape
regressions; a human catches "this looks wrong" regressions that JSON-diffing can't.

## 6. Definition of "safe to remove Python proxy for route X"

All of the following, not any one of them:
1. Golden fixture parity tests green for route X.
2. Relevant Critical Rule regression tests (§3) green.
3. One full day of shadow-mode or real soak usage with no discrepancy logged (`05-migration-
   strategy.md` §5).
4. Manual browser pass covering route X's user-facing feature, done once post-cutover.
5. Rollback for route X has been dry-run at least once (flip its registry entry back to proxied,
   confirm behavior reverts cleanly).
