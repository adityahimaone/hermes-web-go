# Implementation Plan — Vite Frontend (Phases A–G, all 15 modules)

Executes `docs/15-vite-frontend-design-decisions.md` under the constraints of
`docs/14-vite-frontend-migration-plan.md`. Checkboxes are ordered; each phase's exit gate must pass
before the next begins. Vanilla `static/` is never modified. No commit/push between phases without
Adit's explicit approval (per standing work preference: TDD red→green, fresh full verification,
independent reviews before push).

## Conventions for every phase

- Working tree: repo root `hermes-web-go`; new code lives only in `static-vite/` (+ `scripts/`
  generator) — `ponytail:` single vite app, split packages only if build time hurts.
- Verification commands run fresh at each gate: `npm run build` (tsc -b && vite build), `vitest run`,
  then the phase-specific parity check below.
- Every landed fix enumerated in doc 15 §1 (rev guard, worklog machine, interim_assistant
  accumulation, CSRF wrapper, anti-FOUC bootstrap, base-href calculator) gets an explicit test or
  verbatim-port confirmation before its phase closes.

## Phase A — Scaffold + design tokens + entry invariants

- [ ] A1. `npm create vite` (react-ts) inside `static-vite/`; pin deps: react@19, tailwindcss@4 +
      `@tailwindcss/vite`; TS strict; add vitest + jsdom. Vite config: `base: './'` (relative, for
      subpath mount), single input `index.html` (share.html input added in Phase E).
- [ ] A2. Extract tokens: copy every `:root` / `:root.dark` / `[data-skin=…]` / `[data-fontSize…]`
      custom-property block from `static/style.css` verbatim into `src/styles/tokens.css`
      (mechanical script, reviewed output). `src/theme.css` = `@import "tailwindcss"` + `@theme`
      referencing the same variables — no re-derived values.
- [ ] A3. Blank parity page: renders only tokens; vitest computed-style comparison vs a fixture of
      `static/style.css` values — **0 diffs**. This is the exit gate.
- [ ] A4. Entry invariants (doc 15 §4): base-href calculator, anti-FOUC bootstrap, CSRF inline
      wrapper, `__HERMES_CONFIG__`/`__WEBUI_VERSION__` placeholders, workspace/sidebar dataset
      bootstraps — all present in `static-vite/index.html`, order preserved. Add a build-time check
      (script) that the placeholders survive `vite build` output.
- [ ] A5. i18n generator: `scripts/convert-i18n.mjs` → `src/i18n/strings.json` + `keys.d.ts`;
      `t(key, vars)` helper; unknown-key unit test.
- [ ] A6. Gate: build green, token diff = 0, placeholder check passes, i18n round-trip test passes.

## Phase B — Layout shell (ui.js chrome)

- [ ] B1. App shell components: topbar, sidebar, composer chrome, outline rail container — markup
      and class names transcribed from vanilla DOM (inspect real rendered DOM, not source-guess).
- [ ] B2. Theme/skin switching wired to `dataset.skin`/`dataset.fontSize` + theme-color meta update.
- [ ] B3. Worklog label port begins here only as a stub display (full state machine in Phase C).
- [ ] B4. Gate: empty-state shell screenshot vs vanilla = pixelmatch 0 (documented anti-alias
      tolerance only), at default + dark + 2 skins.

## Phase C — messages.js port (highest risk)

- [ ] C1. TDD: `state/chatReducer.test.ts` first — event sequences transcribed from vanilla
      `messages.js` handler + doc 06 fixtures: token accumulation, reasoning, tool/tool_complete,
      metering, context_status, interim_assistant accumulation (commit 3f02b57 semantics), done
      settle, error, approval. Reducer red→green.
- [ ] C2. `lib/sse.ts`: EventSource wrapper, exact event vocabulary, no renaming. `useChatStream`
      facade composes reducer + guards.
- [ ] C3. `useRevGuard`: per-session `useRef` high-water-mark; gate every `setMessages`-equivalent
      dispatch; unit test with out-of-order snapshot responses.
- [ ] C4. `useWorklogTiming`: live tick while `activeStreamId` set; settle to
      `usage.duration_seconds` exactly once; blink-guard on tool-call-group recreation; survives
      reload/restart (commit 11c8f47 semantics). Timer-jitter-tolerant tests via fake timers.
- [ ] C5. Message list components: markdown via streaming-markdown@0.2.15 (npm), KaTeX rendering,
      code blocks, tool cards, approval buttons — visual transcription from vanilla DOM.
- [ ] C6. Gate: reducer suite green; live multi-turn conversation with tool calls + approval renders
      identically; worklog timing matches vanilla across reload/restart scenarios.

## Phase D — sessions.js + workspace.js

- [ ] D1. `useSessions`: list/CRUD/pin/rename/search; snapshot application goes through rev guard.
- [ ] D2. Session rail UI parity (collapsible, same spacing).
- [ ] D3. `useWorkspace` + file tree (expand/collapse, lazy preview) + preview pane; path-display
      parity; no client-side path logic beyond what vanilla does.
- [ ] D4. Gate: session CRUD + file browsing screenshots identical; out-of-order session snapshot
      test green.

## Phase E — remaining modules (panels, commands, outline, settings, terminal, auth, i18n wiring, share)

- [ ] E1. panels.js → Control Center (crons, skills, memory views).
- [ ] E2. commands.js → slash-command palette; `/steer`, `/queue`, `/background` surface exactly as
      vanilla does today (absent/disabled identically — Python-only endpoints).
- [ ] E3. outline.js → outline rail.
- [ ] E4. extension_settings.js → settings views.
- [ ] E5. terminal.js → xterm via npm (pin same version as vendored copy).
- [ ] E6. login.js + onboarding.js → `/login`, `/onboarding` routes; pathname switch in `app.tsx`.
- [ ] E7. share.html + share.js → second Vite MPA entry (`src-share/`), bundle-isolated.
- [ ] E8. i18n wiring across all components; audit: no hardcoded UI strings, unknown-key test green.
- [ ] E9. Gate: every screen of §5 mapping table screenshot-compared; behave identically.

## Phase F — Full visual regression pass

- [ ] F1. Playwright harness: run vanilla (`HERMES_WEBUI_STATIC_DIR=static`) and vite
      (`=static-vite/dist`) against the SAME Go backend + same fixture sessions.
- [ ] F2. Screen/state matrix from doc 14 §8 extended with §5's 15-module screens; pixelmatch on
      each; failures triaged as frontend bug vs intentional-none (there are none allowed).
- [ ] F3. Gate: zero unexplained diffs beyond documented anti-aliasing tolerance.

## Phase G — Switch mechanism

- [ ] G1. `switch-frontend.sh` (vanilla|vite|status) writing `.env.frontend` per doc 14 §6 script.
- [ ] G2. `ctl.sh` sources `.env.frontend` when present (start + restart paths).
- [ ] G3. Live toggling test: same session viewed in both frontends after restart; vanilla remains
      the default when no env file exists.
- [ ] G4. Gate: toggling works from a clean checkout clone (installer-path smoke).

## Out of scope (standing)

Phase H visual redesign; any Go backend change; modifying `static/`; state/routing/query libraries
beyond locked decisions.
