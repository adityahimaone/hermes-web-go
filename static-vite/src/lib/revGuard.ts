// Revision guard — port of the vanilla high-water-mark rev filter
// (docs/11-history-race-fix.md; 129 `rev` refs in static/messages.js).
// Pure module: no imports, trivially testable.

const CAP = 256

export interface RevStore {
  marks: Map<string, number>
}

export function createRevStore(): RevStore {
  return { marks: new Map() }
}

/** Returns true if this (key, rev) is fresh (must be applied). */
export function acceptRev(store: RevStore, key: string, rev: number): boolean {
  const cur = store.marks.get(key) ?? 0
  if (rev < cur) return false // stale snapshot from an in-flight older request
  if (rev === cur) return false // exact duplicate replay
  store.marks.set(key, rev)
  if (store.marks.size > CAP) {
    // insertion-order eviction, oldest key first
    const first = store.marks.keys().next().value
    if (first !== undefined) store.marks.delete(first)
  }
  return true
}

/** Peek without mutating (used by useRevGuard to decide skip vs dispatch). */
export function peekRev(store: RevStore, key: string): number {
  return store.marks.get(key) ?? 0
}
