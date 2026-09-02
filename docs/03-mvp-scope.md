# 03 — MVP Scope & Definition of Done

"MVP" here does **not** mean "fewer features than the original." The brief explicitly asks for
**comprehensive, fully-functional 1:1 parity**. What "MVP" means in this plan is: *the smallest
slice of the Go rewrite that can safely go to production first, with the legacy Python proxy
covering everything else* — i.e. Phase 1 + Phase 2 from `plan.md`, not a reduced feature set.

## MVP definition of done

The MVP is done when all of the following are simultaneously true:

1. The Go binary is the **only** process the browser and any reverse proxy/SSH tunnel talk to.
2. `static/*` is served identically (byte-identical) to today, from Go.
3. `/health`, `/api/session`, `/api/sessions`, `/api/sessions/search`, `/api/list`, `/api/file`,
   `/api/file/raw`, `/api/workspaces` are Go-native (SQLite-backed where relevant) and pass the
   full parity test suite (`06-testing-and-parity.md`) against real recorded traffic from the
   Python server.
4. Every other route (`/api/chat/*`, `/api/approval/*`, uploads, file mutations, crons, skills,
   memory) is transparently reverse-proxied to the still-running Python process — the user
   notices **no functional difference** during this window.
5. Existing session data (JSON files under `~/.hermes/webui/sessions/`) has been imported into
   SQLite via the one-shot importer, verified against the original files (row count matches file
   count, spot-checked message content matches).
6. Memory footprint of the Go process alone (excluding the still-running Python process, which
   is expected during MVP) is measured and recorded as the baseline "lightweight" number to beat
   in later phases — this is the number that ultimately proves the rewrite achieved its goal.
7. Rollback plan (`05-migration-strategy.md` §4) has been dry-run at least once: flip back to
   100% Python with a single config change / process restart, no data loss.

## Explicit non-goals for MVP (deferred, not dropped — see phase table in `plan.md`)

- Chat streaming, approvals, uploads, crons, skills, memory panels being Go-native — these stay
  proxied to Python until Phases 4–6. They are still fully functional; they're just not yet
  reimplemented.
- Any frontend change of any kind. The Vite migration is explicitly Phase 9, gated on Phase 8
  sign-off (100% Go backend, Python process no longer started).
- Authentication changes/hardening beyond parity — if the fork doesn't currently run with
  `HERMES_WEBUI_PASSWORD` set, don't add new auth requirements as part of MVP; port what exists.
- Any new features (the personal fork's kanban/remote-SSH/evomem items get ported at parity, not
  redesigned, during this migration — feature redesign is explicitly out of scope for a rewrite
  whose stated goal is "more lightweight," not "different").

## Success metrics to track from day one

| Metric | Baseline (Python) | Target (Go, post full migration) |
|---|---|---|
| Idle RSS memory | ~300–400MB (per user's own VPS test of a comparable community web UI) | Target: well under 100MB idle; record actual measured number, don't guess |
| Cold start time | measure on the actual VPS before starting | should be near-instant for a static Go binary (no venv activation, no import-time work) |
| `/health` p50/p99 latency | measure | should not regress; likely improves |
| Concurrent session correctness | known issue (TD1: process-global env vars) | structurally fixed by design (see `01-architecture-design.md` §6) — verify with a race-condition test that intentionally runs two concurrent chats in different sessions |
| Endpoint parity test pass rate | n/a | 100% before any given route's Python proxy is removed |
