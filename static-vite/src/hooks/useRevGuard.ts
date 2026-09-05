// useRevGuard — per-session high-water-mark hook (C3).
// Gates every snapshot-equivalent dispatch: stale or duplicate server
// responses never reach the reducer. Port of docs/11-history-race-fix.md.

import { useMemo, useRef } from 'react'
import { acceptRev, createRevStore, peekRev } from '../lib/revGuard'

export interface RevGuard {
  /** true → dispatch; false → stale/duplicate, drop */
  accept(sessionKey: string, rev: number): boolean
  peek(sessionKey: string): number
}

export function useRevGuard(): RevGuard {
  const storeRef = useRef<ReturnType<typeof createRevStore> | null>(null)
  if (!storeRef.current) storeRef.current = createRevStore()
  return useMemo(
    () => ({
      accept: (key: string, rev: number) => acceptRev(storeRef.current!, key, rev),
      peek: (key: string) => peekRev(storeRef.current!, key),
    }),
    [],
  )
}
