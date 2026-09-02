# Hermes WebUI: Python → Go Rewrite — Master Plan

> Target repo: `adityahimaone/hermes-web-go` (migration workspace for the Hermes WebUI fork)
> Goal: a **lightweight, fully 1:1-functional** replacement backend written in Go,
> migrated **incrementally while the Python backend keeps running in production**,
> with the current vanilla-JS frontend kept as-is until the backend migration is 100% done —
> only then do we move the frontend to Vite.
> Reference (not sufficient as-is, but reusable): `adityahimaone/hermes-web-studio` (dev branch).

This file is the entry point. Detailed instructions live alongside this file:

| Doc | Purpose |
|---|---|
| [`01-architecture-design.md`](01-architecture-design.md) | Target Go architecture, package layout, tech choices, how Go talks to the existing Python/Hermes agent core |
| [`02-api-parity-mapping.md`](02-api-parity-mapping.md) | Every existing Python endpoint/behavior mapped to its Go equivalent — the literal 1:1 contract |
| [`03-mvp-scope.md`](03-mvp-scope.md) | What "MVP" means here, cut lines, definition of done |
| [`04-task-breakdown.md`](04-task-breakdown.md) | Ordered, checkbox-level task list per phase |
| [`05-migration-strategy.md`](05-migration-strategy.md) | How to run Go + Python side-by-side safely (strangler-fig proxy), rollback plan |
| [`06-testing-and-parity.md`](06-testing-and-parity.md) | How we prove 1:1 parity before flipping traffic, regression gates |
| [`07-future-vite-frontend.md`](07-future-vite-frontend.md) | Deferred: frontend rewrite to Vite, only after backend is 100% Go |
| [`08-kanban-board.md`](08-kanban-board.md) | Kanban/task-dispatch board — data model, dispatcher semantics, its own phase |

---

## 1. Why this plan looks the way it does

Two hard constraints from the brief shape every decision below:

1. **"When migration/rewrite is partial with Python"** — the Go backend must be able to run
   in production *while parts of the app still depend on the Python/Hermes agent core*. This is not
   a big-bang rewrite. It is a **strangler-fig migration**: Go becomes the single public entry
   point on day one (it takes over the port the browser talks to), and internally it either (a)
   serves a route itself, or (b) transparently reverse-proxies that route to the old Python
   process running on a private port, until that route's Go implementation is ready and verified.
2. **Frontend stays vanilla JS** until backend migration is complete, then moves to Vite. This
   means the Go server must serve the **exact same `static/` files, the same routes, the same
   JSON shapes** the current vanilla JS already expects — zero frontend changes during backend
   migration. Any accidental response-shape drift breaks the UI silently, so parity-first, not
   rewrite-first.

The one piece that *cannot* be trivially ported line-for-line: the Python backend currently
**imports and calls `AIAgent` in-process** (`run_agent.AIAgent`, `agent.run_conversation(...)`).
That is Python application code (tool loop, LLM calls, memory, skills), not just an HTTP handler —
rewriting it in Go would mean re-implementing the entire Hermes agent, which is out of scope and
not what "lightweight web UI rewrite" means. Decision (detailed in `01-architecture-design.md`):
the Go server does **not** re-implement the agent. It calls the in-process `AIAgent` decision seam
   defined in `01-architecture-design.md` §2b. Agent transport is internal implementation detail;
   Phase 0 evidence does not include provider traffic and makes no gateway-only claim.

## 2. Non-negotiable outcomes (1:1 parity checklist, summarized)

Full detail in `02-api-parity-mapping.md`. At a glance, the Go backend must reproduce:

- [ ] Every documented GET/POST endpoint, same path, same query params, same JSON field names
      (snake_case in, snake_case out — matches current behavior, no accidental camelCase).
- [ ] SSE streaming (`token`/`tool`/`approval`/`done`/`error` events) with the same event names
      and payload shapes the frontend's `messages.js` SSE handlers already parse.
- [ ] Session model + on-disk-compatible or DB-backed store, same session_id format assumptions
      the frontend relies on (opaque string, don't assume length/format beyond what the FE checks).
- [ ] Approval flow (`/api/approval/pending`, `/api/approval/respond`, poll-and-surface-on-tool).
- [ ] Multipart upload semantics (field names `session_id`, `file`; sanitize + 20MB default cap).
- [ ] Workspace file browsing with the two-tier trust model (`validate_workspace_to_add` permissive
      vs `resolve_trusted_workspace` strict) and path-traversal protection.
- [ ] Cron / Skills / Memory read-only panels and cron run/pause/resume actions.
- [ ] The 9 "Critical Rules" from the Python `ARCHITECTURE.md` (Section 17) that must never
      regress — restated in Go terms in `02-api-parity-mapping.md` §8.
- [ ] Optional password auth (`HERMES_WEBUI_PASSWORD`), signed cookie, same cookie semantics.
- [ ] `/health` endpoint shape (`status`, `sessions`, and later `active_streams`, `uptime_seconds`).
- [ ] Same static file layout served from the same paths (`static/index.html`, `style.css`,
      `ui.js`, `workspace.js`, `sessions.js`, `messages.js`, `panels.js`, `commands.js`, `boot.js`) —
      Go changes **nothing** here in phase 1–6; it just serves the files.
- [ ] The Kanban/task-dispatch board (boards, swimlanes-by-assignee, status columns, dispatcher
      preview/run, bulk actions, archive, multi-tenant filtering) — a major module in its own
      right, detailed in `docs/08-kanban-board.md`, not an afterthought under "panels".

## 3. High-level target shape (see `01-architecture-design.md` for full detail)

```
Browser ── HTTP/SSE ──▶  Go server (public port, e.g. 8787)
                              │
                    ┌─────────┴─────────────────────────┐
                    │                                     │
            routes implemented in Go             routes not yet migrated
            (sessions, files, uploads,           (reverse-proxied, byte-for-byte,
             approvals, workspaces, health,       to the legacy Python process on
             static assets, auth)                 an internal-only port, e.g. 8788)
                    │
                    ▼
        chat/streaming routes call out to
        the Hermes agent process over HTTP
        (existing gateway pattern, port 8642 /
         9router), NOT re-implemented in Go
```

- **Language/runtime:** Go 1.23+, no CGO (so cross-compiling and the eventual container stay simple).
- **Router:** `chi` (lightweight, stdlib-compatible, easy middleware for logging/auth).
- **DB:** SQLite via `modernc.org/sqlite` (pure Go, no CGO) for sessions/workspaces/settings/projects,
  replacing the current per-session JSON files + `_index.json`. (Rationale and schema in doc 01.)
- **Streaming:** stdlib `net/http` + `http.Flusher` for SSE — same approach as Python, ported concept
  for concept (queue → channel, `queue.Queue` → Go `chan`, `STREAMS` map → `sync.Map`/mutex-guarded map).
- **Concurrency:** Go's model *removes* Python's TD1 (thread-global env-var) problem for free —
  Go passes context explicitly per-request instead of `os.environ`. This is a place where the
  rewrite is strictly better, not just equivalent; call this out in `01-architecture-design.md`.
- **Process supervision:** keep `ctl.sh`-equivalent semantics (start/stop/restart/status/logs),
  ship as a single static Go binary (this is most of the "lightweight" win — no venv, no pip,
  no 300–400MB RSS Python process for the web layer).

## 4. Phasing (see `04-task-breakdown.md` for the checkbox version)

| Phase | Name | Exit criteria |
|---|---|---|
| 0 | Audit & harness | **Open/blocking:** endpoint/behavior inventory and harness are prepared, but Phase 0 cannot close until real supervised traffic creates and replays a golden fixture against disposable state |
| 1 | Skeleton + proxy | Go binary boots, serves `static/`, proxies 100% of `/api/*` to Python untouched; this is the cutover of the *public port* with zero behavior change |

## Phase 1 status (2026-09-02)

Phase 1 landed on branch `phase-1`, commit `145be81`. Go binary boots, serves `static/` byte-identical (222182-byte `index.html` matches both Python source and the vendored copy), `/health` native JSON `{status,sessions}`, and proxies non-native routes to the legacy Python on an internal port. Verified by `go test ./...`, `go vet ./...`, `go build ./...`, a live two-process smoke (Go front :18787 → Python legacy :18788), and a Phase 0 harness replay (`3 exchanges match after redaction/normalization`).

Known deviation: stdlib `http.FileServer` 301-redirects `/static/index.html` to `/static/`, but the Python WebUI serves it directly. Phase 1 special-cases `/static/index.html` to serve bytes without the redirect (regression test: `TestStaticIndexServesWithoutRedirect`). This is the only behavioral shim in Phase 1.

The `/api/sessions` 500 in the smoke run is a **Python backend state issue**, not a Go proxy bug: a direct request to the legacy Python (`curl http://127.0.0.1:18788/api/sessions`) also returns 500 with `TypeError: '<' not supported between instances of 'str' and 'float'` inside `_build_session_list_cache_payload`. The Go reverse proxy forwarded the request and received the backend's 500 unchanged, which is correct proxy behavior. `/api/workspaces` (200) and other routes forward cleanly.
| 2 | Read-only data ports | Sessions (list/get/search), workspaces, files (list/read/raw), health — Go-native, Python proxy removed for these routes only |
| 3 | Mutations | Session CRUD, uploads, file ops (create/save/delete), workspace add/remove/rename |
| 4 | Chat + streaming | `/api/chat/start`, `/api/chat/stream`, `/api/chat` fallback — Go owns HTTP/SSE plumbing, delegates actual generation to the Hermes agent gateway |
| 5 | Approvals | Full approval state machine in Go (replaces Python's module-level `_pending` dict) |
| 6 | Panels | Crons, skills, memory (read + cron run/pause/resume/create) |
| 6.5 | Kanban board | Task-dispatch board fully ported — see `docs/08-kanban-board.md`; own data model, dispatcher, wired into `agentclient` + approvals |
| 7 | Auth + observability | Password auth, structured logging, `/health` parity, graceful shutdown |
| 8 | Full cutover | Python process no longer started at all; Go is 100% of the backend; proxy code deleted |
| 9 (future) | Frontend → Vite | Only starts after Phase 8 sign-off; see `07-future-vite-frontend.md` |

## Phase 0 status (2026-09-02)

Phase 0 bounded lifecycle evidence (2026-09-02): fresh A/B run against unmodified source passed exactly 3 GET exchanges covering `/health`, `/api/sessions`, and `/api/workspaces`, with isolated state and workspace substitution. This 3-GET route set captures no session ID, so no ID remapping was exercised. Provider chat was excluded because safe repeatability was not established. Phase 0 provider-chat coverage remains open; no provider output is fabricated or claimed.

Comparative audit: `hermes-web-studio` was reviewed read-only. Its Go `Handler` routes and gateway implementation differ from primary source `hermes-webui-personal/api/routes.py`; no comparative routes were merged into the primary inventory.

## 5. How to use this plan day-to-day

1. Work one phase at a time, in order — each phase in `04-task-breakdown.md` has its own
   "definition of done" and a parity check (`06-testing-and-parity.md`) before moving on.
2. Never remove the Python proxy fallback for a route until that route's Go implementation has
   passed the parity harness. The proxy is the safety net that makes "partial migration" viable.
3. Update `02-api-parity-mapping.md` the moment you discover an undocumented behavior in the
   Python code (there will be some — the Python `ARCHITECTURE.md` explicitly says version/line
   counts drift). Treat that file as living, the same way the original repo treats its own
   `ARCHITECTURE.md`.
4. Do not touch anything under `static/` during phases 0–8. That's the whole point of keeping
   the frontend vanilla JS during the backend migration.
