# 08 — Kanban / Task Dispatch Board

Added after reviewing an actual screenshot of the running app (`hermes.adityahimaone.space`,
session view). This confirms Kanban is **not** a lightweight to-do widget bolted onto a session —
it's a standalone top-level module (its own icon in the primary app dock, alongside chat/calendar/
other integrations) built around **dispatching tasks to agent workers**, with board/list/stats
views. Treat it as a major feature with its own phase, not a footnote under "Panels."

> Everything below is reconstructed from UI observation, not source code — this doc's job is to
> get the shape right enough to plan around. **Phase 0 must verify every field/endpoint name
> against the actual fork source before Go implementation starts** — treat the tables below as a
> strong first draft, not ground truth, same caveat as `02-api-parity-mapping.md`.

## 1. What the UI shows

**Top bar:** a tenant/scope selector (`H  default ▾`) and a "Kanban" module label — Kanban sits
alongside other top-level sections (chat sessions, etc.), scoped by the same tenant selector.

**Left rail (secondary nav within the Kanban module):** icons suggesting multiple views on the
same task data — chat, calendar, **board (currently active)**, layers, folder, person, checklist,
chart/stats, file/export, settings. Only the board view is visible in the capture; the others
(calendar, stats, list/checklist, export) are inferred from icon meaning and should be verified.

**Filter/control panel (left column of the board view):**
- Search box ("Search tasks")
- `All assignees` filter dropdown
- `All tenants` filter dropdown — **multi-tenant scoping is a real concept here**, not just
  single-workspace; likely maps onto either (a) a separate "tenant" entity, or (b) a rename of
  the existing workspace concept for this module. Phase 0 must determine which.
- `Include archived` checkbox
- `Only mine` checkbox (assignee = current identity)
- Quick-filter stat chips: `5 Stats`, `5 Done` (counts, clickable to filter)
- `Status` dropdown + `Bulk action` button — multi-select tasks, apply a status change or archive
  in bulk
- `Preview dispatcher` / `Run dispatcher` buttons — **this is the operational core of the
  feature**, see §3
- `New task` inline input + `New task` button — quick task creation from a single text field
- Running count: "N visible tasks", followed by a compact scrollable list of task cards mirroring
  the board (title, status badge, `assignee · priority`)

**Board view (main panel):**
- A board selector (`😊 Default ▾`) — **multiple named boards are supported**, not just one.
- View-mode icons top-right (grid / table / play / lightning) — board/table/run/dispatch toggles,
  exact semantics TBD in Phase 0.
- **Swimlanes grouped by assignee** (row label `worker`, with a task count badge) — if there are
  multiple workers/agents, expect one swimlane per assignee.
- **Columns = task status**, left to right: an unlabeled queue-like column (green count badge,
  likely "Queued"/"To do"), `Running` (blue dot), `Blocked` (red dot), `Done` (purple dot) —
  4 statuses observed.
- Task cards show: `task_id` (format `t_XXXXXXXX`, 8 hex chars — an execution/run id style, not
  the 12-char session_id format used elsewhere), a free-text title/description that reads like an
  **agent prompt** ("Test task — bikin file /tmp/kanban-test.txt dengan konten..."), an
  `@assignee` tag, and an `archive` action button.

## 2. Inferred data model

```
Board
  id           string
  name         string           e.g. "Default" — boards are user-creatable/selectable
  tenant_id    string?          if tenants are board-scoped rather than task-scoped — verify

Task  (task_id format: "t_" + 8 hex chars)
  task_id      string
  board_id     string
  title        string           short label shown in compact list view
  description  string           the full prompt/instruction — this is what gets sent to the
                                 agent when dispatched, analogous to a chat message or cron
                                 job's `prompt` field
  assignee     string           worker/agent identifier (drives swimlane grouping)
  tenant       string           multi-tenant scoping, filterable independently of assignee
  priority     string           e.g. "P0" — seen in compact card, ordering/urgency hint
  status       enum             queued | running | blocked | done  (4 columns observed;
                                 verify exact enum values/labels against source)
  archived     bool
  created_at   timestamp
  updated_at   timestamp
  started_at   timestamp?       set when dispatched to a worker
  completed_at timestamp?
  result       string?          likely populated on completion (not visible in this capture —
                                 verify: does the Done card expand to show agent output?)
```

## 3. The dispatcher — this is the feature's actual purpose

`Preview dispatcher` / `Run dispatcher` strongly implies a **scheduler/assignment engine**, not
just manual drag-and-drop between columns:
- **Preview**: dry-run — given current queued tasks and available workers/agents, compute a
  proposed assignment plan (which task goes to which worker, in what order) **without executing
  anything**. Likely returns a plan the user can review before committing.
- **Run**: commit that plan — for each assignment, actually kick off an agent turn using the
  task's `description` as the prompt, move the task to `running`, and track completion back to
  `done` (or `blocked` if the agent run hits an approval gate or error — this is very likely what
  "blocked" means, tying directly into the existing approval system from `01-architecture-design.md`
  §5, not a separate concept).

This means **Kanban is not an independent subsystem** — it's a second entry point into the same
agent-turn execution path chat already uses (`agentclient.RunTurn`), just with tasks queued and
dispatched in bulk/by-policy instead of one interactive message at a time. Architecturally this
is good news: the Go rewrite doesn't need two agent-execution pipelines, it needs one
(`agentclient`, per `01-architecture-design.md` §2b) fed from two different frontends (chat's
`/api/chat/start` and Kanban's dispatcher).

**"Blocked" likely = approval-pending.** If a dispatched task's agent run hits a tool call needing
approval, the natural mapping is: task moves to `Blocked` until someone resolves the approval
(possibly via the same `/api/approval/respond` flow, scoped to that task's session/run instead of
a chat session). Verify this mapping explicitly in Phase 0 — it's the single most important
open question for this feature, since getting it wrong means blocked tasks silently never
recover.

## 4. Endpoints to reverse-engineer in Phase 0 (best-guess shape, unverified)

| Likely path | Purpose |
|---|---|
| `GET /api/kanban/boards` | list boards |
| `POST /api/kanban/boards` | create board |
| `GET /api/kanban/tasks?board_id=&assignee=&tenant=&status=&include_archived=&mine=` | filtered task list, backs both board and compact-list views |
| `POST /api/kanban/tasks` | create task (the "New task" quick-add — likely just `description`, server infers/defaults the rest) |
| `POST /api/kanban/tasks/bulk` | bulk action (status change / archive) on a selected/filtered set |
| `POST /api/kanban/tasks/{id}/archive` | archive single task |
| `GET /api/kanban/dispatcher/preview?board_id=` | compute assignment plan, no side effects |
| `POST /api/kanban/dispatcher/run?board_id=` | commit the plan — spawns agent turns |
| `GET /api/kanban/stats?board_id=` | counts backing the `5 Stats`/`5 Done` chips |

Do not implement against this table directly — it exists to give Phase 0's source audit a
concrete starting hypothesis to confirm or correct, per `02-api-parity-mapping.md` §0's rule.

## 5. Where this fits in the phase plan

Given its scope (its own data model, its own dispatcher/scheduler logic, and a direct dependency
on the agent-execution and approval systems), Kanban gets its own phase rather than folding into
Phase 6 ("Panels", which was scoped for the much lighter crons/skills/memory read-mostly views).
See the renumbered phase table below — insert as **Phase 6.5**, after Panels and before Auth, since
it depends on `agentclient` (Phase 4) and the approval store (Phase 5) both being Go-native
already, but doesn't block anything after it.

## 6. Task breakdown — Phase 6.5: Kanban board

- [ ] Phase 0 follow-up: pull the actual fork's Kanban route/model source, replace every "likely"/
      "verify" annotation above with confirmed fact. Do not start below until this is done.
- [ ] `internal/kanban`: `Board`, `Task` models; SQLite tables (`boards`, `kanban_tasks`) — same
      pure-Go SQLite store as sessions, not a separate storage engine.
- [ ] Confirm and implement the `blocked` ↔ approval-pending mapping (§3) — this is the highest-
      risk piece of this phase; write the concurrency/state test for it before UI polish.
- [ ] Port list/filter/search/stats endpoints (assignee, tenant, status, archived, mine).
- [ ] Port bulk action + single archive.
- [ ] Port `New task` quick-create.
- [ ] Port dispatcher preview (pure computation, no side effects — safe to get wrong and fix,
      unlike run).
- [ ] Port dispatcher run — wire into `agentclient.RunTurn`, same interface chat already uses;
      task transitions to `running` on dispatch, `done`/`blocked` on completion/approval-gate.
- [ ] Swimlane grouping (by assignee) and multi-board selection in the read endpoints' response
      shape, to match the frontend's existing rendering assumptions exactly.
- [ ] Golden-fixture parity per `06-testing-and-parity.md`: record real board/task fixtures from
      the current Python app the same way Phase 0 did for chat/sessions, before writing any Go
      handler for this feature.
- [ ] Remove proxy fallback for `/api/kanban/*` once green.

## 7. Open questions to close in Phase 0 (do not guess further than this doc already has)

1. Are "tenants" a first-class entity, or a rename/alias of workspaces for this module?
2. What are the exact 4 status enum values/labels in source (not just the UI labels)?
3. Does a `Done` card expose the agent's output/result anywhere (click to expand)? Not visible in
   the captured screenshot — check before assuming `result` is unused.
4. What triggers `blocked` precisely — approval only, or also tool errors / timeouts?
5. Is `Run dispatcher` synchronous (blocks until all assignments are kicked off) or itself
   streamed/async with its own progress reporting?
6. Does bulk action support anything beyond status-change/archive (e.g. bulk delete, bulk
   reassign)?
