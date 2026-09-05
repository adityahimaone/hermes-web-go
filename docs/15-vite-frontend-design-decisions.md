# 15 — Vite Frontend Design Decisions (brainstorm outcome, 2026-09-05)

Companion to `14-vite-frontend-migration-plan.md` (the plan stands; this doc records the design
decisions taken within it, corrects its stale assumptions, and closes its open questions). Written
after a code-level research pass over `static/` (the actual vanilla frontend) and the earlier
`hermes-web-studio` Vite attempt. Scope confirmed by Adit: **all 15 vanilla modules in Phases A–G**
— no module left behind — executed sequentially with per-phase exit criteria.

## 1. Corrections to doc 14's assumptions (research findings)

Doc 14 was written partly from memory of module sizes and predates several landed fixes. Verified
against the tree at `3f02b57`:

| Doc 14 said | Actual (verified) | Consequence |
|---|---|---|
| `messages.js` ≈ 39 call sites, "if the rev guard has landed" | 9,333 lines with **129 `rev` references; the history-race guard HAS landed** | React port **must** carry the `useRef` high-water-mark per session; not optional |
| `ui.js` = layout shell, "High risk" re worklog | **21,712 lines**; worklog live-tick→settle + blink-guard are hard-won fixes (commits `11c8f47`, `b6475ae`) | Port the state machine verbatim, not the visual output |
| `i18n.js` not mentioned | **26,402 lines** — the largest module; translation data, not logic | Convert to generated typed JSON + thin `t()`; see §3 |
| 8 modules in the §4 mapping table | **15 modules** in `static/`: ui, messages, sessions, workspace, panels, commands, outline, boot, i18n, extension_settings, terminal, onboarding, assistant_turn_anchors, login, share | Extended mapping in §5; Phase E scope grows |
| index.html not discussed | **1,927 lines** of critical inline scripts: base-href calculator (subpath mount, #2226), anti-FOUC theme/skin/font-size bootstrap (19 skins), CSRF fetch+sendBeacon wrapper, PWA startup | All must survive into the Vite entry; see §4 |
| `__WEBUI_VERSION__` etc. not discussed | Go performs shell template substitution on served static files (commit `39d839b`) | Vite-built `index.html` must keep the `__...__` placeholders literally; Go substitutes at serve time |

## 2. Locked decisions (brainstorm P1–P7)

| # | Decision | Choice |
|---|---|---|
| P1 | Scope | All 15 modules, Phases A–G, sequential execution, exit criteria per phase |
| P2 | Stack | Vite + **React 19** + TypeScript strict + Tailwind v4. **Hooks-only**: no TanStack Router/Query, no state lib, no router lib. `ponytail:` a section may add a lib only when its port proves the need |
| P3 | i18n | One-time generator script converts `i18n.js` → typed JSON; thin `t(key, vars)` helper (~20 lines). No i18next. `ponytail:` add a lib only if plural/ICU is ever genuinely needed |
| P5 | shadcn/ui | **Full** — every component goes through `components/ui/*`, restyled with vanilla tokens. Accept the larger restyle effort for consistency |
| P6 | SSE architecture | `useChatStream` facade (single API for components) composed of: pure `chatReducer` (all event-type cases, unit-testable event sequences), `useRevGuard`, `useWorklogTiming`. May merge into one file later as long as the reducer stays pure |
| P7 | Vendor | **Full npm**: katex, xterm, streaming-markdown pinned (0.2.15) in package.json. Login + onboarding become React routes in the SPA; `share.html` stays a **separate Vite MPA entry** (public, bundle-isolated) |

(P4 — sequential execution — is folded into P1.)

## 3. i18n conversion mechanics

- `static-vite/scripts/convert-i18n.mjs`: reads `static/i18n.js`, emits `src/i18n/strings.json` +
  `src/i18n/keys.d.ts` (type-checked keys). Committed so regeneration is reproducible.
- `t(key, vars)` does flat lookup + `{{var}}` interpolation; locale switching mirrors the vanilla
  `S.lang` semantics. Unknown key = loud error in dev, key name in prod.

## 4. Entry-file invariants (the easy-to-lose list)

`static-vite/index.html` must preserve, in order:

1. base-href calculator inline script (verbatim — subpath mount #2226);
2. anti-FOUC theme/skin/font-size bootstrap inline script (verbatim — runs before React);
3. manifest/favicon/PWA meta links (assets served from `public/`);
4. `__HERMES_CONFIG__` / `__HERMES_WEBUI_BUNDLE_VERSION__` placeholder scripts (Go substitutes);
5. CSRF fetch + sendBeacon wrapper → also ported verbatim as `src/lib/fetch.ts` for programmatic
   use; the inline wrapper stays for any pre-React fetches;
6. workspace-panel / sidebar-collapsed dataset bootstraps.

Theme system: 19 skins apply via `document.documentElement.dataset.skin` + CSS custom-property
overrides copied verbatim from `style.css` into `src/styles/tokens.css` (`:root`, `:root.dark`,
`[data-skin="..."]` blocks). Tailwind v4 `@theme` references those variables — never re-derives
values. Font-size override via `dataset.fontSize` likewise.

## 5. Module → React mapping (extended from doc 14 §4)

| Vanilla module | React home | Notes |
|---|---|---|
| `boot.js` | `main.tsx` / `app.tsx` bootstrap | Pathname switch: `/login` → Login, `/onboarding` → Onboarding, else Chat shell |
| `ui.js` | `components/layout/*`, `useWorklogTiming` | Worklog state machine verbatim |
| `messages.js` | `components/chat/*`, `state/chatReducer.ts`, `useChatStream`, `useRevGuard` | Highest risk; reducer purity = event-sequence tests |
| `sessions.js` | `components/sessions/*`, `useSessions` | CRUD hooks; rev-aware snapshot application |
| `workspace.js` | `components/workspace/*`, `useWorkspace` | Tree expand/collapse + preview parity |
| `panels.js` | `components/panels/*` | Control Center: crons, skills, memory |
| `commands.js` | `components/commands/*` | `/steer`, `/queue`, `/background` absence handled identically to vanilla (still Python-only) |
| `outline.js` | `components/outline/*` | |
| `i18n.js` | `src/i18n/` (generated) | §3 |
| `extension_settings.js` | `components/settings/*` | |
| `terminal.js` | `components/terminal/*` | xterm via npm |
| `onboarding.js` | `components/auth/Onboarding` | Route `/onboarding` |
| `login.js` | `components/auth/Login` | Route `/login` |
| `assistant_turn_anchors.js` | `lib/turnAnchors.ts` + chat components | Anchor/timing logic preserved with messages |
| `share.html` + `share.js` | `share.html` MPA entry + `src-share/` | Second Vite input; public share page stays bundle-isolated |
| `index.html` inline scripts | §4 invariants | |

## 6. Updated phase plan (doc 14 §7, scoped to 15 modules)

| Phase | What | Exit criteria |
|---|---|---|
| A | Scaffold `static-vite/`, tokens.css + @theme, §4 entry invariants, i18n generator | Computed-style diff of every token vs `static/style.css` = 0; placeholders survive build |
| B | Layout shell (ui.js chrome) | Empty-shell screenshot identical to vanilla |
| C | messages.js port: reducer, `useChatStream`, rev guard, worklog machine | Reducer passes event-sequence tests (adapted from doc 06); live chat renders identically incl. tool calls + approval |
| D | sessions.js + workspace.js | CRUD + file browsing identical |
| E | panels, commands, outline, extension_settings, terminal, onboarding, login, i18n wiring, share MPA entry | All screens behave identically; unknown-key i18n audit clean |
| F | Full Playwright + pixelmatch pass, both frontends, same backend + fixtures | Zero unexplained diffs beyond documented anti-aliasing tolerance |
| G | `switch-frontend.sh` + ctl.sh env sourcing | Toggle vanilla↔vite mid-development; vanilla stays default until explicit flip |

Phase H (visual redesign) remains explicitly out of scope, per doc 14.

## 7. Non-goals (unchanged from doc 14 §9)

No Go route/response/SSE changes; `static/` untouched and remains default; no design changes;
independent of backend migration phases.
