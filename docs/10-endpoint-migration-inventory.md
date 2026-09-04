# 10 — Endpoint Migration Inventory (native Go vs legacy proxy)

Companion to `02-api-parity-mapping.md`. This file records the **current** migration state
of every `/api/*` endpoint between the native Go backend (`hermes-web-go`) and the legacy
Python WebUI runner (`hermes-webui-personal`).

Generated from source scan (not guesses):

- Python endpoint set: `grep parsed.path == /api/...` in `api/routes.py` (handle_get / handle_post / handle_put / handle_patch / handle_delete).
- Go endpoint set: route registrations in `internal/httpserver/*.go` plus `internal/proxy/registry.go`.

Re-run the inventory with the script below whenever routes change:

```
python3 scripts/endpoint_inventory.py
```

## Summary

| | Count |
|---|---|
| Python `/api/*` endpoints (legacy) | 229 |
| Native Go `/api/*` endpoints | 138 |
| **Not yet migrated (proxied to Python)** | **91** |
| Go-only endpoints (no Python equivalence) | `none` |

Every route not listed in the "Native Go" table below is reverse-proxied to the legacy
Python runner (`HERMES_WEBUI_LEGACY_PROXY_URL`) by the catch-all in
`internal/httpserver/server.go` (`mountStaticAndProxy`, `proxy.IsNative()` check).

## Native Go endpoints (already implemented, no proxy)

| Method | Path | Source |
|---|---|---|
| GET | `/health` | `server.go` |
| GET | `/api/session` | `data.go` |
| GET | `/api/sessions` | `data.go` |
| GET | `/api/sessions/search` | `data.go` |
| GET | `/api/list` | `data.go` |
| GET | `/api/file` | `data.go` |
| GET | `/api/file/raw` | `data.go` |
| GET | `/api/session/export` | `data.go` |
| GET | `/api/chat/stream` | `chat.go` |
| GET | `/api/chat/stream/status` | `chat.go` |
| GET | `/api/approval/pending` | `approval.go` |
| GET | `/api/crons` | `crons.go` |
| GET | `/api/crons/output` | `crons.go` |
| GET | `/api/crons/recent` | `crons.go` |
| GET | `/api/crons/history` | `crons.go` |
| GET | `/api/crons/status` | `crons.go` |
| GET | `/api/skills` | `skillsmem.go` |
| GET | `/api/skills/content` | `skillsmem.go` |
| GET | `/api/skills/usage` | `skillsmem.go` |
| GET | `/api/memory` | `skillsmem.go` |
| GET | `/api/auth/status` | `auth.go` |
| GET | `/api/logs` | `logs_route.go` |
| POST | `/api/client-events/log` | `misc.go` |
| GET | `/api/session/compress/status` | `misc.go` |
| GET | `/api/health/agent` | `misc.go` |
| GET | `/api/transcribe/capability` | `misc_reads.go` |
| GET | `/api/wiki/status` | `misc_reads.go` |
| GET | `/api/insights` | `misc_reads.go` |
| GET | `/api/updates/check` | `misc_reads.go` |
| GET | `/api/onboarding/status` | `misc_reads.go` |
| GET | `/api/git-info` | `git_info.go` |
| POST | `/api/auth/logout` | `misc_wave2.go` |
| GET | `/api/commands` | `misc_wave2.go` |
| GET | `/api/commands/bundles` | `misc_wave2.go` |
| GET | `/api/personalities` | `misc_wave2.go` |
| GET | `/api/prompts` | `misc_wave2.go` |
| POST | `/api/default-model` | `misc_wave2.go` |
| GET | `/api/knowledge` | `misc_wave2.go` |
| POST | `/api/csp-report` | `misc_wave2.go` |
| GET | `/api/notes` | `misc_wave3.go` |
| GET | `/api/notes/sources` | `misc_wave3.go` |
| GET | `/api/notes/search` | `misc_wave3.go` |
| GET | `/api/notes/item` | `misc_wave3.go` |
| GET | `/api/wiki/browse` | `misc_wave3.go` |
| GET | `/api/wiki/page` | `misc_wave3.go` |
| GET | `/api/workspaces/suggest` | `misc_wave3.go` |
| GET | `/api/workspaces/health` | `misc_wave3.go` |
| GET | `/api/workspaces/filemap` | `misc_wave3.go` |
| GET | `/api/plugins` | `misc_wave3.go` |
| GET | `/api/git/status` | `git_panel.go` |
| GET | `/api/git/branches` | `git_panel.go` |
| GET | `/api/git/diff` | `git_panel.go` |
| POST | `/api/git/stage` | `git_panel.go` |
| POST | `/api/git/unstage` | `git_panel.go` |
| POST | `/api/git/discard` | `git_panel.go` |
| POST | `/api/git/commit` | `git_panel.go` |
| POST | `/api/git/commit-selected` | `git_panel.go` |
| POST | `/api/git/fetch` | `git_panel.go` |
| POST | `/api/git/pull` | `git_panel.go` |
| POST | `/api/git/push` | `git_panel.go` |
| POST | `/api/git/checkout` | `git_panel.go` |
| POST | `/api/git/stash-checkout` | `git_panel.go` |
| GET | `/api/system/health` | `misc_wave4.go` |
| POST | `/api/session/branch` | `misc_wave4.go` |
| POST | `/api/session/compress/start` | `misc_wave4.go` |
| POST | `/api/admin/reload` | `misc_wave4.go` |
| POST | `/api/shutdown` | `misc_wave4.go` |
| POST | `/api/upload/extract` | `misc_wave4.go` |
| GET | `/api/mcp/servers` | `misc_wave4.go` |
| GET | `/api/mcp/tools` | `misc_wave4.go` |
| POST | `/api/session/undo` | `session_mutations.go` |
| POST | `/api/session/retry` | `session_mutations.go` |
| POST | `/api/session/title/regenerate` | `session_mutations.go` |
| POST | `/api/session/yolo` | `session_mutations.go` |
| GET | `/api/session/yolo/status` | `session_mutations.go` |
| POST | `/api/updates/apply` | `session_mutations.go` |
| POST | `/api/session/new` | `data.go` |
| POST | `/api/session/update` | `data.go` |
| POST | `/api/session/delete` | `data.go` |
| POST | `/api/session/rename` | `data.go` |
| POST | `/api/chat/start` | `chat.go` |
| POST | `/api/chat` | `chat.go` |
| POST | `/api/approval/respond` | `approval.go` |
| POST | `/api/workspaces/add` | `data.go` |
| POST | `/api/workspaces/remove` | `data.go` |
| POST | `/api/workspaces/rename` | `data.go` |
| POST | `/api/file/save` | `data.go` |
| POST | `/api/file/create` | `data.go` |
| POST | `/api/file/delete` | `data.go` |
| POST | `/api/upload` | `data.go` |
| POST | `/api/crons/create` | `crons.go` |
| POST | `/api/crons/run` | `crons.go` |
| POST | `/api/crons/pause` | `crons.go` |
| POST | `/api/crons/resume` | `crons.go` |
| POST | `/api/crons/update` | `crons.go` |
| POST | `/api/crons/delete` | `crons.go` |
| POST | `/api/crons/delivery-options` | `crons.go` |
| POST | `/api/auth/login` | `auth.go` |

Note: the chat route family (`/api/chat*`) is native *only when the Go binary is launched
with `HERMES_WEBUI_RUNNER_BASE_URL` set and a working agent transport*. Otherwise the
catch-all proxies chat back to Python. See `docs/chat_api_parity.md` §env for the exact
launch variables.

## Not yet migrated (proxied to Python at runtime)

Remaining Python endpoint families, **highest-value / frontend-visible first**:

### Chat & agent
- `/api/chat/steer`, `/api/chat/cancel`
- `/api/reasoning`
- `/api/clarify/pending`, `/api/clarify/stream`, `/api/clarify/respond`, `/api/clarify/inject_test`
- `/api/commands*` (list, exec, bundles, moa)

### Session lifecycle extras
- `/api/session/*` batch: `branch`, `duplicate`, `retry`, `undo`, `archive`, `pin`,
  `move`, `truncate`, `clear`, `draft`, `import`, `import_cli`, `export` (export is native),
  `stream`, `status`, `usage`, `yolo`, `compress*`, `compression-recovery/start`,
  `recovery/audit`, `recovery/repair-safe`, `conversation-rounds`, `handoff-summary`,
  `lineage/report`, `anchor-scene`, `title/regenerate`, `toolsets`, `worktree/remove`,
  `worktree/status`, `sessions/cleanup`, `sessions/cleanup_zero_message`,
  `sessions/events`, `sessions/gateway/stream`

### Git & file ops
- `/api/git*` (`/api/git/status`, `add`, `diff`, `commit`, `commit-selected`,
  `commit-message`, `commit-message-selected`, `push`, `pull`, `fetch`, `checkout`,
  `stash-checkout`, `stage`, `unstage`, `discard`, `branches`)
- `/api/git-info`
- `/api/file/*` extras: `create-dir`, `move`, `rename`, `reveal`, `path`, `open-vscode`, `office-save`
- `/api/folder/download`
- `/api/workspace/upload` (note: singular, distinct from `/api/upload` which is native)

### Auth / identity
- `/api/auth/logout`, `/api/auth/oidc/*`, `/api/auth/passkey*`, `/api/auth/passkeys`
- `/api/profile/*` (`active`, `create`, `delete`, `switch`, `update`), `/api/profiles`
- `/api/onboarding/*`
- Deferred (native wave after this one): `/api/git-info`, `/api/updates/check`,
  `/api/transcribe/capability` — each depends on Python helper modules
  (workspace git, update cache, STT) and is deferred rather than half-stubbed;
  documented here so inventory doesn't regress.

### Cron admin (read-side / extra)
- ~~`/api/crons/history`, `/api/crons/recent`, `/api/crons/status`~~ — migrated to native Go 2026-09-04 (`crons.go`: Recent/History/Status; `Recent` mirrors jobs.json filter, `History` lists output-file metadata); `native=86` per `scripts/endpoint_inventory.py` (with `logs` + `compress/status` + `client-events/log` added this wave), plus `/api/logs` earlier

### Skills / memory extras
_Migrated to native on 2026-09-03 (Phase 6): `skillsmem` handles
`/api/skills/{list,content,usage,save,delete,toggle}` and `/api/memory/{read,write}`
(`GET /api/memory` was already native)._
- remaining read extras: `/api/skills/usage` history detail (already native), legacy alias `/api/skills/list` (covered by `/api/skills`)

### Models / providers / settings
- `/api/models`, `/api/models/live`, `/api/models/refresh`
- `/api/model/set`, `/api/model/auxiliary`, `/api/default-model`
- `/api/providers*`, `/api/provider/quota`, `/api/provider/cost-history`
- `/api/personalities`, `/api/personality/set`
- `/api/prompts`, `/api/settings`, `/api/plugins`

### Terminal / background / dashboard
- `/api/terminal/*` (`start`, `input`, `output`, `resize`, `close`)
- `/api/background`, `/api/background/status`
- `/api/bg-task-complete-ack`, `/api/process-complete-ack`
- `/api/dashboard/config`, `/api/dashboard/status`, `/api/project-os/dashboard`

### Projects / workspaces extras
- `/api/projects`, `/api/projects/create`, `/api/projects/delete`, `/api/projects/rename`
- `/api/workspaces/*` extras: `filemap`, `health`, `reorder`, `suggest`

### Notes / knowledge / wiki
- `/api/notes/*` (`item`, `search`, `sources`)
- `/api/knowledge`, `/api/insights`, `/api/media`, `/api/logs`
- `/api/wiki/*` (`browse`, `page`, `status`)

### Auth-adjacent / escape hatch
- `/api/escape/*` (`authorize`, `list`, `file/read`, `file/raw`)
- `/api/rollback/*` (`list`, `diff`, `restore`)

### Upload / media extras
- `/api/upload/extract`
- `/api/transcribe`, `/api/transcribe/capability`, `/api/tts`

### Gateway / infra admin
- `/api/gateway/status`, `/api/gateway/restart`, `/api/gateway/start`, `/api/gateway/stop`
- `/api/health/restart`, `/api/system/health` (health/agent is native via misc.go, 2026-09-04)
- `/api/admin/reload`, `/api/shutdown`, `/api/updates/*` (`check`, `summary`, `apply`, `force`, `clear_lock`)
- `/api/csp-report`

### Extensions / MCP
- `/api/extensions/*` (`install`, `uninstall`, `toggle`, `registry`, `status`, `sidecar-proxy-consent`)
- `/api/mcp/servers`, `/api/mcp/tools`

### Misc
- `/api/btw`, `/api/goal`, `/api/client-events/log`, `/api/kanban*` (board bridge), `/api/list`

## How the proxy catch-all decides

`internal/httpserver/server.go` → `mountStaticAndProxy`:

1. If `proxy.IsNative(r.URL.Path)` → Go already serves it → 404 (native handler already matched).
2. Else if a `proxyHandler` is configured → forward verbatim to the legacy Python runner.
3. Else → 404.

`internal/proxy/registry.go` holds the authoritative `NativeRoutes` map. Keep it in sync
whenever a Go route lands — the inventory script reads the same source.

## How to migrate the next endpoint (checklist)

1. Implement the handler in `internal/httpserver/<area>.go`, wire it to the correct router
   constructor (`DataRouter` / `ChatRouter` / `CronsRouter` / `SkillsMemRouter` / etc.).
2. Add the exact path to `internal/proxy/registry.go` `NativeRoutes`.
3. Add parity tests in the matching `*_test.go` (response shape, status codes, error edges).
4. Re-run `python3 scripts/endpoint_inventory.py` and update this doc's count/table.
5. `go test ./... -race`, `go vet ./...`, `go build ./...`.