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
| Native Go `/api/*` endpoints | 189 |
| **Not yet migrated (proxied to Python)** | **55** |
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
| GET/POST | `/api/updates/check` | `updates_check.go` (wave 15: real branch check — fetch, behind, compare URL, 10-min cache; POST = force) |
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
| GET | `/api/rollback/list` | `misc_wave7.go` |
| GET | `/api/rollback/diff` | `misc_wave7.go` |
| POST | `/api/rollback/restore` | `misc_wave7.go` |
| POST | `/api/personality/set` | `misc_wave7.go` |
| POST | `/api/projects/create` | `misc_wave7.go` |
| POST | `/api/projects/rename` | `misc_wave7.go` |
| POST | `/api/projects/delete` | `misc_wave7.go` |
| GET | `/api/extensions/status` | `misc_wave7.go` |
| GET | `/api/extensions/registry` | `misc_wave7.go` |
| POST | `/api/extensions/toggle` | `misc_wave7.go` |
| POST | `/api/extensions/install` | `extensions_gallery.go` (wave 17: sha256 verify, zip-slip guards, install manifest) |
| POST | `/api/extensions/uninstall` | `extensions_gallery.go` (wave 17: manifest-driven file removal + empty-dir pruning) |
| POST | `/api/extensions/sidecar-proxy-consent` | `extensions_gallery.go` (wave 17: loopback-origin consent in extension-overrides.json) |
| POST | `/api/onboarding/complete` | `misc_wave7.go` |
| GET | `/api/onboarding/oauth/poll` | `onboarding_oauth.go` (wave 16) |
| POST | `/api/onboarding/oauth/start` | `onboarding_oauth.go` (wave 16: codex device-code + anthropic Claude Code link) |
| POST | `/api/onboarding/oauth/cancel` | `onboarding_oauth.go` (wave 16) |
| POST | `/api/onboarding/probe` | `onboarding_oauth.go` (wave 16: /models probe, error taxonomy parity) |
| POST | `/api/onboarding/setup` | `onboarding_setup.go` (wave 18: config.yaml comment-preserving write + .env key, provider matrix, confirm_overwrite guard, SKIP_ONBOARDING short-circuit) |
| POST | `/api/session/anchor-scene` | `misc_wave7.go` |
| POST | `/api/workspaces/reorder` | `misc_wave7.go` |
| POST | `/api/updates/force` | `misc_wave7.go` |
| GET | `/api/sessions/events` | `session_events_sse.go` |
| GET | `/api/sessions/gateway/stream` | `session_events_sse.go` |
| POST | `/api/escape/authorize` | `escape.go` |
| GET | `/api/escape/list` | `escape.go` |
| GET | `/api/escape/file/read` | `escape.go` |
| GET | `/api/escape/file/raw` | `escape.go` |
| GET | `/api/gateway/status` | `escape.go` |
| GET | `/api/gateway/start` | `escape.go` |
| POST | `/api/gateway/stop` | `escape.go` |
| POST | `/api/gateway/restart` | `escape.go` |
| POST | `/api/workspace/upload` | `escape.go` |
| POST | `/api/chat/cancel` | `escape.go` |
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
  `move`, `truncate`, `clear`, `draft`, `export` (export is native),
  `stream`, `status`, `usage`, `yolo`, `compress*`, `compression-recovery/start`,
  `conversation-rounds`,
  `anchor-scene`, `title/regenerate`, `toolsets`,
  `sessions/cleanup`, `sessions/cleanup_zero_message`,
  `sessions/events`, `sessions/gateway/stream`

### Git & file ops
- `/api/git*` (`/api/git/status`, `add`, `diff`, `commit`, `commit-selected`,
  `commit-message`, `commit-message-selected`, `push`, `pull`, `fetch`, `checkout`,
  `stash-checkout`, `stage`, `unstage`, `discard`, `branches`)
- `/api/git-info`
- ~~`/api/file/*` extras: `create-dir`, `move`, `rename`, `reveal`, `path`, `open-vscode`, `office-save`~~ — migrated to native Go 2026-09-04 (`file_ops.go`: `Wave19Router`; symlink-leaf guard before exists() (dangling symlink → 400), new_name charset check, move-into-itself guard, vscode config block + host/container prefix translation; `office-save` validates ext/dir/symlink then returns 503 — no OOXML editor bundled in Go runtime, deliberate)
- ~~`/api/folder/download`~~ — migrated to native Go 2026-09-04 (`file_ops.go`: zip stream, pre-flight walk with 413 on `HERMES_WEBUI_FOLDER_ZIP_MAX_FILES` (default 50000) / `HERMES_WEBUI_FOLDER_ZIP_MAX_MB` (default 1024), symlinks escaping the workspace skipped, `Connection: close` parity)
- `/api/workspace/upload` (note: singular, distinct from `/api/upload` which is native)

### Auth / identity
- `/api/auth/logout`, `/api/auth/oidc/*`, `/api/auth/passkey*`, `/api/auth/passkeys`
- `/api/profile/*` (`active`, `create`, `delete`, `switch`, `update`), `/api/profiles`
- `/api/onboarding/oauth/start` codex worker token persistence is partial: writes auth.json entry (subset of Python fields)
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
- ~~`/api/terminal/*`~~ (`start`, `input`, `output`, `resize`, `close`) — **implemented** (Wave 9)
- `/api/background`, `/api/background/status`
- `/api/bg-task-complete-ack` — **implemented** (Wave 10)
- `/api/process-complete-ack` — **implemented** (410 Gone alias, Wave 10)
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
- `/api/share/create` — **stays proxied (deliberate, Wave 20)**: the snapshot builder embeds the agent's force-redaction engine (`agent/redact.py`, ~1500 lines) as an ALWAYS-ON public-boundary guard plus `MEDIA:` base64 embedding with magic-byte validation and SVG sanitization; half-porting a public security boundary is worse than not porting it
- ~~`/api/share/revoke`~~ — **implemented** (Wave 20, `share_media.go`: tombstone `shares/<token>.json` (`revoked_at`), token from session sidecar JSON or shares-dir scan by `source_session_id` (CLI-session parity), sidecar fields cleared atomically)
- `GET /api/share/{token}` — **implemented** (Wave 20, `share_media.go`: public payload projection `title/messages/message_count` + optional epoch `created_at`/`updated_at`, revoked → 404, `Cache-Control: no-store` + `X-Robots-Tag: noindex, nofollow`; dynamic chi route, not counted in the distinct-path registry)
- `/api/media` — **stays proxied (deliberate, Wave 20)**: coupled to `api/media_snapshots.py` content-addressed digests (`?snap=`), session media tokens, and CSP-sandboxed HTML serving

### Gateway / infra admin
- `/api/gateway/status`, `/api/gateway/restart`, `/api/gateway/start`, `/api/gateway/stop`
- `/api/health/restart`, `/api/system/health` (health/agent is native via misc.go, 2026-09-04)
- `/api/admin/reload`, `/api/shutdown`, `/api/updates/*` (`check`, `summary`, `apply`, `force`)
- `/api/updates/clear_lock` — **implemented** (Wave 10, no-delete manual-recovery parity)
- `/api/session/lineage/report` — **implemented** (Wave 11, continuation-chain walk + child branches from state.db)
- `/api/session/recovery/audit` — **implemented** (Wave 11, read-only core audit: shrunken/orphan-bak/index-gaps/missing-sidecars)
- `/api/session/recovery/repair-safe` — **implemented** (Wave 12: restore-from-bak + sidecar materialization + index rebuild, 409 when not clean)
- `/api/session/handoff-summary` — **implemented** (Wave 12, deterministic fallback only: no LLM in Go, same fallback text Python uses when its LLM fails; threshold + transcript from state.db)
- `/api/session/worktree/status` — **implemented** (Wave 13: dirty/untracked/ahead-behind/listed snapshot; terminal lock from native terminal registry)
- `/api/session/worktree/remove` — **implemented** (Wave 13: dirty/untracked/unpushed guards with force, unlock + git worktree remove)
- `/api/shutdown` — **already native** (pre-existing registration; Wave 13 verified live: responding + SIGINT)
- `/api/session/import` — **implemented** (Wave 14: new-session import from JSON export)
- `/api/session/import_cli` — **implemented** (Wave 14: state.db transcript → WebUI row; refresh path with prefix-equality guard)
- `/api/csp-report`

### Extensions / MCP
- `/api/extensions/*` remaining (`registry`/`status`/`toggle` are native in wave7 but with simplified state model; `install` download path only allows hermes-webui.github.io, live-download path not E2E-verified against the real gallery)
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