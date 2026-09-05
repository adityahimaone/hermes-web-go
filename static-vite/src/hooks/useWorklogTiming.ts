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

  // (Re)arm on stream start; reset settled marker only when a NEW stream id arms.
  useEffect(() => {
    if (activeStreamId && busy) {
      if (startStreamRef.current !== activeStreamId) {
        startRef.current = now()
        startStreamRef.current = activeStreamId
        settledRef.current = null
      }
    } else if (!activeStreamId && !busy) {
      startRef.current = null
      startStreamRef.current = null
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeStreamId, busy])

  // 1s live tick while running.
  useEffect(() => {
    if (!activeStreamId || !busy) return
    const id = setInterval(() => setTick((t) => t + 1), 1000)
    return () => clearInterval(id)
  }, [activeStreamId, busy])

  // Settle exactly once per stream id (blink-guard analogue: duplicates no-op).
  useEffect(() => {
    if (doneDurationSeconds == null || !activeStreamId) return
    if (settledRef.current && settledRef.current.streamId === activeStreamId) return
    settledRef.current = { streamId: activeStreamId, duration: doneDurationSeconds }
  }, [doneDurationSeconds, activeStreamId])

  const liveElapsed =
    activeStreamId && busy && startRef.current != null ? Math.max(0, (now() - startRef.current) / 1000) : null

  return {
    liveElapsed,
    settledDuration: settledRef.current?.duration ?? null,
    settledStreamId: settledRef.current?.streamId ?? null,
    running: !!activeStreamId && busy,
  }
}
