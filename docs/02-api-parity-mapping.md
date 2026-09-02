# 02 — API Parity Mapping (the literal 1:1 contract)

## Phase 0 audit evidence (2026-09-02)

Route dispatch uses `api.routes.handle_get`, `handle_post`, `handle_patch`, `handle_delete`, and `handle_put`; it is not decorator-based. Reproducible extracted inventory: `testdata/route-inventory.json`, generated from the read-only fork's `api/routes.py`.

Actual dispatch-literal counts: GET 105, POST 139, PATCH 2, DELETE 3, PUT 1 (250 total; prefix routes count once per method). Inventory includes fork-specific Kanban dispatch, wiki/knowledge, notes, MCP, project/dashboard, remote workspace/file escape routes, profile/provider/plugin management, and gateway/session event streams. JSON inventory is source evidence; stable rows below are not exhaustive until Phase 1 imports inventory.

This is the authoritative "must reproduce exactly" list. Treat it like the original project's
own `ARCHITECTURE.md` §18 (Endpoint Reference): **living document, update it the moment you
learn your fork differs from what's below.** Everything here was reconstructed from the public
architecture documentation of the upstream project this fork is based on — verify each row
against the actual Hermes WebUI fork source before marking it done, since a
personal fork (kanban gates, remote SSH, evomem tweaks) has diverged from upstream.

## 0. First task of Phase 0

Before writing any Go: dump the *actual* current route table from the fork (`grep -n
"parsed.path ==" server.py api/routes.py` or equivalent) and reconcile it against the table
below. Add any fork-specific routes (kanban, remote SSH spaces, evomem) as new rows — they are
not in the upstream doc this mapping was built from, and this file is wrong until they're added.

## 1. GET endpoints

| Path | Query params | Response shape | Notes |
|---|---|---|---|
| `/` , `/index.html` | — | HTML app shell | Served from `static/index.html`, unchanged |
| `/health` | — | `{status, sessions}` (+ `active_streams`, `uptime_seconds` if fork has them) | Keep field names exact |
| `/api/session` | `session_id` | full session incl. `messages` | **400 if `session_id` missing** — do not silently create (Rule from Python B11 fix) |
| `/api/sessions` | — | list of `compact()` dicts, sorted `updated_at` desc | compact = no `messages` field |
| `/api/sessions/search` | `q` | filtered list, case-insensitive title match; empty `q` = same as `/api/sessions` | |
| `/api/list` | `session_id`, `path` (default `.`) | directory listing, dirs first then files, case-insensitive alpha, max 200 entries | path traversal must 400/403, not 500 |
| `/api/file` | `session_id`, `path` | `{path, content, size, lines}`, UTF-8 `errors=replace`, 200KB cap | |
| `/api/file/raw` | `session_id`, `path` | raw bytes, correct `Content-Type` from extension map, no size cap | used for image preview; 404 JSON if missing |
| `/api/chat/stream` | `stream_id` | SSE stream: `token`/`tool`/`approval`/`done`/`error` events | long-lived; 30s heartbeat comment |
| `/api/chat/stream/status` | `stream_id` | `{active: bool, stream_id}` | reconnect-banner support |
| `/api/approval/pending` | `session_id` | `{pending: entry|null}` | polled every 1500ms by FE while busy |
| `/api/workspaces` | — | `{workspaces: [...], last: path}` | |
| `/api/crons` | — | `{jobs: [...]}` | includes disabled jobs |
| `/api/crons/output` | `job_id`, `limit` | `{outputs: [{filename, content}]}` | |
| `/api/skills` | — | `{skills: [{name, description, category}]}` | |
| `/api/skills/content` | `name` | full skill data incl. `SKILL.md` content | |
| `/api/memory` | — | `{memory, user, soul, *_path, *_mtime}` | reads `MEMORY.md`/`USER.md`/`SOUL.md` |
| `/api/session/export` | `session_id` | full session JSON, `Content-Disposition: attachment` | |
| `/static/*` | — | file bytes w/ correct `Content-Type` | can become `embed.FS` in Go, same URL shape |

Test-only/dev endpoint (`/api/approval/inject_test`) — port only if the fork's own test suite
depends on it; otherwise omit from production build behind a build tag, don't ship a debug-only
approval injector in a production binary by accident.

## 2. POST endpoints

| Path | Body fields | Response | Notes |
|---|---|---|---|
| `/api/upload` | multipart: `session_id`, `file` | `{filename, path, size}` | **must be checked before generic JSON body read** — Go equivalent of Python's ordering rule: don't consume the request body generically before the multipart-specific handler runs |
| `/api/session/new` | `model?`, `workspace?` | new session (full) | defaults: last-used workspace, not a hardcoded default |
| `/api/session/update` | `session_id`, `workspace?`, `model?` | updated session | 404 if unknown session |
| `/api/session/delete` | `session_id` | `{ok: true}` | **never** create a replacement session server-side; that's a frontend concern (Rule #1) |
| `/api/session/rename` | `session_id`, `title` | `{session: compact}` | truncate to 80 chars |
| `/api/chat/start` | `session_id`, `message`, `model?`, `workspace?` | `{stream_id, session_id}` | spawns the turn; returns immediately |
| `/api/chat` | same as above | full result, blocks until done | sync fallback; FE doesn't use it, keep for parity/debugging |
| `/api/approval/respond` | `session_id`, `choice` (`once`\|`session`\|`always`\|`deny`) | `{ok: true, choice}` | 400 on invalid `choice` |
| `/api/workspaces/add` | `path`, `name?` | — | **permissive** validation: only rejects non-existent/non-dir/system-root paths |
| `/api/workspaces/remove` | `path` | — | ok even if not present |
| `/api/workspaces/rename` | `path`, `name` | — | 404 if not found |
| `/api/file/save` | `session_id`, `path`, `content` | — | write to existing file only |
| `/api/file/create` | `session_id`, `path`, `content?` | `{ok, path}` | error if file already exists |
| `/api/file/delete` | `session_id`, `path` | `{ok: true}` | path-traversal protected |
| `/api/crons/create` | `prompt`, `schedule`, `name?`, `deliver?`, `skills?`, `model?` | `{ok, job}` or 400 | |
| `/api/crons/run` | `job_id` | `{ok, status}` | runs in background, returns immediately |
| `/api/crons/pause` | `job_id` | `{ok, job}` or 404 | |
| `/api/crons/resume` | `job_id` | `{ok, job}` or 404 | |
| `/api/auth/login` | password | sets HttpOnly+SameSite=Strict cookie | only active if `HERMES_WEBUI_PASSWORD` set |

## 3. SSE event payload shapes (exact — the frontend parses these field names)

```
token     {"text": "..."}
tool      {"name": "...", "preview": "..."}
approval  {"command": "...", "description": "...", "pattern_keys": [...]}
done      {"session": { ...compact fields..., "messages": [...] }}
error     {"message": "...", "trace": "..."}
```

## 4. Path-security invariants (must hold in Go exactly as in Python)

- `safe_resolve(root, requested)`: resolve, then assert result is under `root` — reject with a
  clean 400, never a stack trace, on traversal attempts (`../../etc/passwd` etc.).
- **Two-tier workspace trust** (do not collapse these into one function):
  - *Registration* (`/api/workspaces/add`) — permissive: only blocks non-existent path, non-directory,
    or system root.
  - *Operation* (file read/write inside a workspace) — strict: path must be under home dir, in the
    saved workspace list, or under the configured default workspace.

## 5. Session model field-for-field

```
session_id   string, 12-char hex (keep format — frontend/localStorage key format assumes this)
title        string, auto-set from first 64 chars of first user message
workspace    string, absolute path
model        string, e.g. "anthropic/claude-sonnet-4.6"
messages     []Message (OpenAI-format: {role, content, attachments?})
tool_calls   []ToolCall
created_at   float unix timestamp
updated_at   float unix timestamp, bumped on every save
pinned       bool
archived     bool
project_id   *string
```

`compact()` = everything except `messages` (and probably `tool_calls`) — used for list views.

## 6. Upload semantics

- Field names: `session_id`, `file`.
- Sanitize filename: replace non-word chars with `_`, truncate to 200 chars.
- Default cap 20MB, override via `HERMES_WEBUI_MAX_UPLOAD_MB`.
- Write to `session.workspace / safe_name`.

## 7. Auth semantics (if `HERMES_WEBUI_PASSWORD` set)

- Minimal login page/form → `POST /api/auth/login`.
- HttpOnly + SameSite=Strict cookie on success.
- Every API endpoint checks the cookie when the env var is set; 401 JSON otherwise, not a redirect
  (the frontend is an SPA and expects JSON error handling, not a 302).
- Cookie validity: 30 days from last activity (sliding, not fixed — match existing behavior if
  the fork implements sliding; verify against actual source).

## 8. Critical rules carried forward from the Python `ARCHITECTURE.md` §17 (do not regress)

1. **Delete never creates.** `/api/session/delete` must never trigger session creation server-side.
2. **Upload-body-consumption ordering** has a Go equivalent: don't read/decode the generic JSON
   body before dispatching to the multipart handler for `/api/upload` — structure routing so the
   multipart handler gets the raw, unconsumed body.
3. **`task_id`, not `session_id`**, wherever the agent-turn contract is invoked (see `agentclient`).
4. **End-of-stream sentinel handling**: whatever signals "no more tokens" to the SSE writer must
   be handled explicitly and distinctly from a legitimate empty-string token.
5. **Capture the active session id before any async/await-equivalent work** — in Go this means:
   read `session_id` into a local variable at the top of the handler before spawning the
   goroutine that runs the turn; don't close over a mutable outer reference that could change.
6. **Never auto-create a session on boot/first load.** Only two user actions create sessions:
   the "+" button and sending a message with no active session.
7. **All shared in-memory state (sessions cache, approval `_pending`, stream registry) must be
   mutex/`sync.Map`-protected** — Go's races are easier to introduce silently than Python's GIL-
   protected dict ops, so this needs explicit `go test -race` coverage, not just "it compiles."
8. **Never leak stack traces to API clients.** 500s return `{"error": "Internal server error"}`;
   log the real error server-side only.
9. **Iterate `pattern_keys` (plural), never assume singular `pattern_key`**, when approving.

## 9. Fork-specific features to inventory in Phase 0 (not in the upstream doc, must be reverse-engineered from the actual repo)

- Kanban board / gates (per memory: "kanban gates" mentioned as a personal-fork feature).
- Remote SSH space support.
- "evomem" tweaks (memory system changes vs. upstream `MEMORY.md`/`USER.md`/`SOUL.md` handling).

> Action item: before Phase 1 starts, read the actual fork's `api/routes.py` (or equivalent) and
> append a row per fork-specific endpoint here, with the same level of detail as the tables above.
> Do not guess at these from the upstream doc — they are fork additions the upstream architecture
> notes don't cover.
