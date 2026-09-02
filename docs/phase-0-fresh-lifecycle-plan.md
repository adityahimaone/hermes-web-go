# Phase 0 Fresh Lifecycle Parity Plan

Goal: prove safe A/B read-only lifecycle produces equivalent normalized exchanges against two fresh isolated Python WebUI instances. Mutation coverage is separate and provider chat remains deferred to Phase 4.

Architecture:
- Keep `tools/phase0_harness.py` transport-only: execute declarative journeys, capture response-derived references, replay requests, compare meaningful status/shape/SSE ordering.
- Add a separate lifecycle runner that owns two source-server processes and separate temporary `HERMES_HOME`, `HERMES_WEBUI_STATE_DIR`, and workspaces.
- Record against fresh Server A, shut it down, replay against fresh Server B, compare, and always release both processes.

Data flow:
`journey spec → fresh Server A → record/capture IDs → shutdown A → fresh Server B → replay/remap IDs → compare → cleanup`

Trust and errors:
- Validate journey and fixture shapes at file boundaries.
- Never copy or print credentials; use existing configured provider only through isolated runtime setup.
- Fail closed on readiness timeout, missing captured references, HTTP/SSE mismatch, provider failure, or cleanup failure.
- Normalize only legitimate runtime values; keep HTTP status, keys, collection cardinality, response shape, and SSE event order strict.

Implementation:

1. Write failing tests
- A replay that starts from `POST /api/session/new` must inject the newly returned session ID into later path/body references.
- A lifecycle runner must start A and B with distinct empty state directories and stop each process on success and failure.
- Comparison must ignore health runtime counters/timestamps but reject meaningful shape/status/cardinality differences.

2. Minimal code
- Fix shared reference capture/materialization in `tools/phase0_harness.py`.
- Add one focused lifecycle runner under `tools/`; no source-repo edits and no new dependency.
- Add one declarative smoke journey under `testdata/`.

3. Verification
- Run targeted tests red, then green.
- Run full `uv run --with pytest python -m pytest -q`.
- Run `python3.11 -m compileall -q tools tests`.
- Regenerate route inventory and prove byte equality.
- Run safe read-only 3-GET record on fresh isolated A and replay on fresh isolated B. Mutation coverage and provider chat are deferred; do not fabricate evidence.
- Require compare exit 0 and redact-check fixtures.
- Run `git diff --check`.

4. Review gates
- Independent spec review, then independent code-quality/security review.
- Parent verifies exact diff and fresh command outputs.
- No commit/push; wait for Adit Phase 0 approval.
