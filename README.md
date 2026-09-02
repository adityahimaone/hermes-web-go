# Hermes Web Go

Lightweight Go backend migration plan for Hermes WebUI.

This repository is the execution baseline for migrating the existing Python web backend to Go
with strict 1:1 behavior parity. The migration is incremental: Go becomes the public entry point,
while routes that are not yet migrated continue to reverse-proxy to the legacy Python service.
The existing vanilla JavaScript frontend remains unchanged until the backend migration is complete.

## Documentation

- [Master plan](docs/plan.md)
- [Architecture and design](docs/01-architecture-design.md)
- [API parity mapping](docs/02-api-parity-mapping.md)
- [MVP scope](docs/03-mvp-scope.md)
- [Task breakdown](docs/04-task-breakdown.md)
- [Migration strategy](docs/05-migration-strategy.md)
- [Testing and parity verification](docs/06-testing-and-parity.md)
- [Future Vite frontend](docs/07-future-vite-frontend.md)

## Migration order

1. Audit the existing Python implementation and freeze golden fixtures.
2. Add the Go server and legacy Python reverse proxy.
3. Migrate read-only routes, then mutations.
4. Migrate chat, SSE streaming, approvals, and panels.
5. Complete observability and full cutover.
6. Consider the Vite frontend only after backend parity is signed off.

## Non-negotiable rule

No route is removed from the Python proxy until its Go implementation passes the parity harness.
Behavior compatibility is the release gate.

## Status

Documentation baseline established. Implementation starts at Phase 0 in
[`docs/04-task-breakdown.md`](docs/04-task-breakdown.md).
