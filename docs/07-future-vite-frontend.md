# 07 — Future: Frontend → Vite (deferred until Phase 8 sign-off)

This phase is intentionally under-specified relative to the backend docs — it starts only after
the backend is 100% Go and the Python process is no longer started at all (`04-task-breakdown.md`
Phase 8). Revisit and expand this doc when that milestone is actually reached; don't over-plan a
frontend rewrite against a backend that hasn't stabilized yet.

## Why wait

The current vanilla-JS frontend (`ui.js`, `workspace.js`, `sessions.js`, `messages.js`,
`panels.js`, `commands.js`, `boot.js`) is the thing keeping the backend migration's blast radius
small — because it never changes during Phases 0–8, any bug found in the browser during that
window is provably a backend regression, not a frontend one. Migrating both at once would destroy
that debugging leverage, which is precisely why the brief asks for vanilla-JS-now,
Vite-later, not both together.

## Known reference point (evaluate, don't assume it's sufficient)

Per existing notes, an earlier attempt at a Vite + React (or Vite + TanStack) rewrite of both
frontend and backend exists at `adityahimaone/hermes-web-studio` (`dev` branch), but was judged
"not enough" — i.e., it doesn't yet reach 1:1 functional parity with the current vanilla-JS app.
When Phase 9 starts:
1. Audit `hermes-web-studio`'s frontend against the full parity list in this plan's
   `02-api-parity-mapping.md` (by then updated to reflect the real, migrated Go API) and against
   the UI/UX conventions in the upstream project's own `DESIGN.md`/`UIUX-GUIDE.md` philosophy
   ("calm console": conversational content over agent metadata, tool traces as quiet disclosure
   rows, not first-class chat cards).
2. Decide reuse-vs-rewrite per component/module, rather than assuming the whole thing is either
   fully reusable or fully discarded.

## Suggested (non-binding) shape for Phase 9, once started

- Stack: Vite + vanilla TS (or a lightweight framework, matching whatever `hermes-web-studio`
  already committed to, if it's kept) — this is a decision to make *at Phase 9 kickoff* with
  fresh eyes on the finished Go API, not now.
- Reuse the same API contract this entire plan exists to nail down — the Go backend's routes
  don't change for Phase 9; only the client changes. This is the payoff of doing the backend
  migration properly first: the frontend rewrite becomes a pure client-side exercise against a
  already-stable, already-tested API.
- Feature-flag or dual-serve during the frontend cutover too (same strangler-fig instinct as the
  backend): serve the new Vite build at an alternate path first (`/app-next/` or similar), dogfood
  it personally, then flip the default once confident, keeping the vanilla build available as a
  fallback for a period — mirrors `05-migration-strategy.md`'s rollback philosophy.
- Re-run the same parity discipline (`06-testing-and-parity.md`) against the UI layer: golden
  screenshots or DOM-snapshot fixtures instead of JSON fixtures, same "don't remove the fallback
  until parity is proven" rule.

## Explicit non-goals until Phase 9 actually starts

- No frontend code changes during Phases 0–8, including "small" ones. If a backend response shape
  must change for some reason during migration, change the Go handler to match the *existing*
  frontend expectation — not the other way around. The frontend is the fixed reference point
  until this phase begins.
