# Phase 2 Read-only data ports implementation plan

Goal: move read-only session and workspace routes to Go while preserving Python response semantics and safe file access.

Architecture: SQLite owns imported read data. The importer reads existing JSON state without mutating it. Native handlers use session/workspace stores; all other routes remain proxied.

Files:
- Create `migrations/001_initial.sql`: SQLite schema.
- Create `internal/store/store.go`: SQLite schema, import, query primitives.
- Create `internal/session/session.go`: session model/store and JSON-compatible response helpers.
- Create `internal/workspace/workspace.go`: anchored path resolution, directory and file reads.
- Create `internal/data/data.go`: Hermes home discovery and one-shot import orchestration.
- Modify `internal/config/config.go`: data root/database configuration.
- Modify `cmd/server/main.go`: open/import SQLite and wire dependencies.
- Modify `internal/httpserver/server.go`: native Phase 2 handlers.
- Modify `internal/proxy/registry.go`: register native routes.
- Add package and HTTP tests for importer, pagination/search, traversal, MIME and route wiring.

Order:
1. TDD SQLite schema/import and session store.
2. TDD workspace safe operations.
3. TDD native HTTP handlers and registry wiring.
4. Run full tests, vet, build, live root smoke against copied state; compare source spot checks.
5. Update task-breakdown status only after all checks pass.
