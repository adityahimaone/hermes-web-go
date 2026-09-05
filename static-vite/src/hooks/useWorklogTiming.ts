// useWorklogTiming — C4: live elapsed tick while a stream is active; settles
// to usage.duration_seconds exactly once at done (commit 11c8f47 semantics);
// re-arming a new stream restarts the tick without leaking the old interval.
// Timer-jitter tolerant: pure math from injected clock, setInterval only
// drives re-render.

import { useEffect, useRef, useState } from 'react'

export interface WorklogTiming {
  /** live elapsed seconds while streaming; null when idle */
  liveElapsed: number | null
  /** settled duration from the done event (set exactly once per stream) */
  settledDuration: number | null
  /** stream id the settledDuration belongs to */
  settledStreamId: string | null
  /** true between send() and done/apperror */
  running: boolean
}

interface Options {
  activeStreamId: string | null
  busy: boolean
  /** final duration from the done event's usage (null when not yet settled) */
  doneDurationSeconds: number | null
  /** deterministic clock injection for tests */
  now?: () => number
}

function defaultNow() {
  return Date.now()
}

export function useWorklogTiming({ activeStreamId, busy, doneDurationSeconds, now = defaultNow }: Options): WorklogTiming {
  const [, setTick] = useState(0)
  const startRef = useRef<number | null>(null)
  const startStreamRef = useRef<string | null>(null)
  const settledRef = useRef<{ streamId: string; duration: number } | null>(null)

  // Arm synchronously during render (not deferred to useEffect) so
  // liveElapsed is non-null on the first paint — fixes first-frame test.
  if (activeStreamId && busy) {
    if (startStreamRef.current !== activeStreamId) {
      startRef.current = now()
      startStreamRef.current = activeStreamId
      settledRef.current = null
    }
  } else if (!activeStreamId && !busy) {
    if (startRef.current !== null || startStreamRef.current !== null) {
      startRef.current = null
      startStreamRef.current = null
    }
  }

  // 1s live tick while running.
  useEffect(() => {
    if (!activeStreamId || !busy) return
    const id = setInterval(() => setTick((t) => t + 1), 1000)
    return () => clearInterval(id)
  }, [activeStreamId, busy])

  // Settle exactly once per stream id (blink-guard analogue: duplicates no-op).
  // Runs synchronously during render (same reason as arming above): the done
  // payload and activeStreamId arrive in the same commit, so settledDuration
  // is visible on the settle paint without waiting an effect pass.
  if (doneDurationSeconds != null && activeStreamId) {
    if (!settledRef.current || settledRef.current.streamId !== activeStreamId) {
      settledRef.current = { streamId: activeStreamId, duration: doneDurationSeconds }
    }
  }

  const liveElapsed =
    activeStreamId && busy && startRef.current != null ? Math.max(0, (now() - startRef.current) / 1000) : null

  return {
    liveElapsed,
    settledDuration: settledRef.current?.duration ?? null,
    settledStreamId: settledRef.current?.streamId ?? null,
    running: !!activeStreamId && busy,
  }
}
