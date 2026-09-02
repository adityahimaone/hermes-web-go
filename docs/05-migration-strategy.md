# 05 — Migration Strategy: Running Go + Python Side-by-Side

This doc exists because of the brief's explicit constraint: *"makes it more lightweight but when
migration/rewrite is partial with python."* The strategy is a classic **strangler fig**: put the
new system in front of the old one, migrate capability by capability, and only remove the old
system once nothing depends on it.

## 1. Network topology during migration

```
Before:                          During migration (Phases 1–7):        After (Phase 8):
Browser ──▶ Python:8787          Browser ──▶ Go:8787                   Browser ──▶ Go:8787
                                          │                                       │
                                          ├─▶ handled natively (growing set)      └─▶ handled natively (100%)
                                          └─▶ proxied to Python:8788 (shrinking set)
```

- Go binds the port the browser/SSH-tunnel actually talks to (`HERMES_WEBUI_PORT`, default 8787).
- The Python process is reconfigured (via its own `HERMES_WEBUI_HOST`/`HERMES_WEBUI_PORT`) to bind
  an **internal-only** port (e.g. 8788, or a Unix domain socket if the Go `net/http/httputil`
  reverse proxy is comfortable with that — simpler to start with plain TCP on localhost).
- Nothing external ever talks to Python directly during the migration window. This is what makes
  the cutover invisible to the user and to the frontend.

## 2. Route ownership registry

`internal/proxy` should not silently guess which routes are "done." Maintain an explicit,
readable registry (a Go map literal or a small YAML file loaded at boot — either is fine, prefer
whichever is easier to diff in code review):

```go
// internal/proxy/registry.go
var NativeRoutes = map[string]bool{
    "/health":              true,
    "/api/session":         true,
    "/api/sessions":        true,
    // ... grows every phase
}
// Anything not in NativeRoutes falls through to the reverse proxy.
```

This registry doubles as living documentation of migration progress — `grep -c true
registry.go` is a crude but honest progress percentage at any point in time.

## 3. Data ownership during the transition

The trickiest part of any strangler migration is **shared state**. Two options, pick based on
what Phase 0's audit reveals:

- **Option A (preferred if feasible):** Go owns the SQLite session store from Phase 2 onward,
  Python is modified minimally (one small patch) to read/write the *same* SQLite DB instead of
  its JSON files, for the routes still served by Python. This avoids any window where "session
  updated via Go" and "session updated via Python" can diverge.
- **Option B (fallback, more isolation but more risk):** Python keeps its JSON files as its
  source of truth for routes it still owns; Go's importer runs periodically (or on-demand) to
  keep SQLite in sync for the routes Go already owns. Only acceptable for read-mostly routes
  (browsing) — never acceptable once mutations (Phase 3+) are split across both stores, because
  that's exactly the kind of split-brain bug this whole plan exists to avoid.

Recommendation: invest in Option A before starting Phase 3 (mutations). It's a small, surgical
change to the Python side (swap `Session.save()`/`Session.load()` internals to hit SQLite) and it
removes an entire category of migration risk for every phase after it.

## 4. Rollback plan

At any point during Phases 1–7, rollback is: **point the browser/tunnel back at Python's original
port, stop the Go process.** Because Python's own on-disk format hasn't been abandoned until
Option A's SQLite-swap patch lands, this is safe pre-Phase-3. After Option A lands, rollback means
"point Python back at binding the public port, with SQLite as its store" — still safe, since
SQLite is the single source of truth either way at that point.

Concretely:
1. Keep the last-known-good Python systemd unit / `ctl.sh` config untouched and documented, not
   deleted, throughout Phases 1–7.
2. Practice the rollback once for real (per `03-mvp-scope.md` item 7) before Phase 3 begins, while
   the blast radius of getting it wrong is still small.
3. Never delete `internal/proxy` or the Python process's config until Phase 8's exit checklist is
   fully green — that package's continued existence *is* the rollback mechanism.

## 5. Traffic verification during cutover of each route

For each route moving from "proxied" to "native" in the registry:
1. Run it in **shadow mode** for a period if practical: Go still proxies to Python for the real
   response, but also calls its own new native handler in parallel (discarding the result) and
   diffs the two — logging any mismatch without affecting the user-visible response. This is
   optional but strongly recommended for the chat/streaming routes (Phase 4), given their
   complexity and centrality.
2. Flip the registry entry to native.
3. Watch logs/error rates for a defined soak period (a day of real usage is enough for a
   single-user homelab deployment like this one) before moving to the next route.
4. Only then remove that route from whatever "still needs Python" checklist you're tracking.

## 6. What stays Python forever (by design, not by accident)

If Phase 0 concludes the fork has no working gateway/runner-mode chat backend, the "agent shim"
described in `01-architecture-design.md` §2 is the one piece of Python that survives Phase 8
intentionally. Document this clearly wherever the project's own README/architecture doc lives
post-migration, so a future contributor doesn't mistake it for incomplete migration — it's a
deliberate, minimal boundary around the part of the system that legitimately is Python (the
agent/tool-loop core), not a web-layer concern this rewrite was ever meant to replace.
