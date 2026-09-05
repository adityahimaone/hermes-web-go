// useChatStream — C2 facade: owns the EventSource lifecycle, feeds the pure
// reducer through a safe state updater, and exposes the app state plus a
// send() action. Reconnect/backoff and approval/clarify UI wiring land with
// C5/C6; this facade is the single dispatch boundary.

import { useCallback, useEffect, useRef, useState } from 'react'
import { chatReducer } from '../state/chatReducer'
import { initialAppState, type AppState, type ChatEvent } from '../state/types'
import { wireChatSSE, type SseSubscription } from '../lib/sse'
import { useRevGuard, type RevGuard } from './useRevGuard'

/** CSRF-safe fetch (vanilla inline wrapper in index.html head). */
export async function api(path: string, init?: RequestInit): Promise<Response> {
  const headers = new Headers(init?.headers)
  const csrf = (window as unknown as { __hermesCsrf?: string }).__hermesCsrf
  if (csrf) headers.set('X-Hermes-CSRF-Token', csrf)
  if (init?.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
  return fetch(path, { ...init, headers, credentials: 'same-origin' })
}

export interface UseChatStream {
  state: AppState
  send(text: string, opts?: { model?: string; model_provider?: string | null }): Promise<void>
  stop(): void
}

export function useChatStream(): UseChatStream {
  const [state, setState] = useState<AppState>(initialAppState)
  const subRef = useRef<SseSubscription | null>(null)
  const revGuard: RevGuard = useRevGuard()
  const busyRef = useRef(false)

  const dispatch = useCallback((ev: ChatEvent) => {
    setState((prev) => chatReducer(prev, ev))
  }, [])

  const stop = useCallback(() => {
    subRef.current?.close()
    subRef.current = null
    busyRef.current = false
    setState((prev) => ({ ...prev, busy: false, activeStreamId: null }))
  }, [])

  // Close on unmount (session switch / teardown).
  useEffect(() => () => stop(), [stop])

  const send = useCallback(
    async (text: string, opts?: { model?: string; model_provider?: string | null }) => {
      const trimmed = text.trim()
      if (!trimmed || busyRef.current) return
      busyRef.current = true

      // Optimistic user row; server snapshot at done replaces history.
      setState((prev) => ({
        ...prev,
        busy: true,
        messages: [...prev.messages, { role: 'user', content: trimmed }],
      }))

      try {
        const res = await api('/api/chat/start', {
          method: 'POST',
          body: JSON.stringify({
            text: trimmed,
            model: opts?.model,
            model_provider: opts?.model_provider ?? null,
          }),
        })
        if (!res.ok) {
          busyRef.current = false
          setState((prev) => ({
            ...prev,
            busy: false,
            messages: [...prev.messages, { role: 'assistant', content: `**Error:** HTTP ${res.status}` }],
          }))
          return
        }
        const start = (await res.json()) as { stream_id?: string; session_id?: string }
        const streamId = start.stream_id ?? ''
        setState((prev) => ({ ...prev, activeStreamId: streamId || null }))

        const url = new URL(
          `api/chat/stream${streamId ? `?stream_id=${encodeURIComponent(streamId)}` : ''}`,
          document.baseURI || location.href,
        )
        const source = new EventSource(url.href, { withCredentials: true })
        subRef.current = wireChatSSE(source, {
          onReducerEvent: (ev) => {
            if (ev.type === 'done' || ev.type === 'apperror') busyRef.current = false
            dispatch(ev)
          },
        })
      } catch {
        busyRef.current = false
        setState((prev) => ({
          ...prev,
          busy: false,
          messages: [...prev.messages, { role: 'assistant', content: '**Error:** Connection failed.' }],
        }))
      }
    },
    [dispatch],
  )

  /** Apply a fetched session snapshot through the rev guard (C3 integration). */
  const applySnapshot = useCallback(
    (sessionKey: string, rev: number, snapshot: Partial<AppState>) => {
      if (!revGuard.accept(sessionKey, rev)) return
      setState((prev) => ({ ...prev, ...snapshot }))
    },
    [revGuard],
  )

  return { state, send, stop }
}

// applySnapshot is exported via the hook closure in Phase D (sessions); kept
// internal here so the facade surface stays minimal for C2/C3.
