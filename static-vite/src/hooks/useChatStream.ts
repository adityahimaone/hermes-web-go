// useChatStream — C2 facade: owns the EventSource lifecycle, feeds the pure
// reducer through a safe state updater, and exposes the app state plus a
// send() action. Approval/clarify raw events route to useApprovalClarify.

import { useCallback, useEffect, useRef, useState } from 'react'
import { chatReducer } from '../state/chatReducer'
import { initialAppState, type AppState, type ChatEvent } from '../state/types'
import { wireChatSSE, type SseSubscription } from '../lib/sse'
import { useRevGuard, type RevGuard } from './useRevGuard'
import { useApprovalClarify } from './useApprovalClarify'

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
  approval: ReturnType<typeof useApprovalClarify>['approval']
  clarify: ReturnType<typeof useApprovalClarify>['clarify']
  approvalResponding: ReturnType<typeof useApprovalClarify>['approvalResponding']
  clarifyResponding: ReturnType<typeof useApprovalClarify>['clarifyResponding']
  respondApproval: ReturnType<typeof useApprovalClarify>['respondApproval']
  respondClarify: ReturnType<typeof useApprovalClarify>['respondClarify']
  dismissApproval: () => void
}

export function useChatStream(): UseChatStream {
  const [state, setState] = useState<AppState>(initialAppState)
  const subRef = useRef<SseSubscription | null>(null)
  const revGuard: RevGuard = useRevGuard()
  const busyRef = useRef(false)
  // Ref mirror of state for callbacks that must read the latest stream id
  // without re-creating (stop()).
  const stateRef = useRef<AppState>(state)
  stateRef.current = state

  const approvalClarify = useApprovalClarify((text) => {
    setState((prev) => ({
      ...prev,
      messages: [...prev.messages, { role: 'user', content: text, _clarify_response: true }],
    }))
  })

  const dispatch = useCallback((ev: ChatEvent) => {
    setState((prev) => chatReducer(prev, ev))
  }, [])

  const stop = useCallback(() => {
    // Server-side cancel (real endpoint, backend cf685a2): aborts the relay,
    // persists the partial answer, emits canonical done. The done event then
    // settles the reducer; SSE close happens at stream_end/done.
    const sid = stateRef.current.activeStreamId
    if (sid) {
      void api('/api/chat/cancel?stream_id=' + encodeURIComponent(sid), { method: 'POST' })
        .then((res) => {
          if (!res.ok && res.status !== 404) {
            // 404 = stream already gone; anything else — close locally so the
            // UI never wedges busy.
            subRef.current?.close()
            subRef.current = null
            busyRef.current = false
            setState((prev) => ({ ...prev, busy: false, activeStreamId: null }))
          }
        })
        .catch(() => {
          subRef.current?.close()
          subRef.current = null
          busyRef.current = false
          setState((prev) => ({ ...prev, busy: false, activeStreamId: null }))
        })
    } else {
      subRef.current?.close()
      subRef.current = null
      busyRef.current = false
      setState((prev) => ({ ...prev, busy: false, activeStreamId: null }))
    }
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
          // 409 = overlapping turn (backend cf685a2 live-turn guard). The
          // previous optimistic user row stays; tell the user why nothing ran.
          const errMsg =
            res.status === 409
              ? '**A turn is already running.** Stop it or wait for it to finish before sending a new message.'
              : `**Error:** HTTP ${res.status}`
          setState((prev) => ({
            ...prev,
            busy: false,
            messages: [...prev.messages, { role: 'assistant', content: errMsg }],
          }))
          return
        }
        const start = (await res.json()) as { stream_id?: string; session_id?: string }
        const streamId = start.stream_id ?? ''
        if (start.session_id) approvalClarify.setSession(start.session_id)
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
          onRaw: (name, raw) => {
            if (name !== 'approval' && name !== 'clarify') return
            try {
              const data = JSON.parse(typeof raw.data === 'string' && raw.data ? raw.data : '{}') as Record<string, unknown>
              approvalClarify.onRawSSE(name, data)
            } catch {}
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
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

  const dismissApproval = useCallback(() => {
    approvalClarify.clearForSession(null)
  }, [approvalClarify])

  return {
    state,
    send,
    stop,
    approval: approvalClarify.approval,
    clarify: approvalClarify.clarify,
    approvalResponding: approvalClarify.approvalResponding,
    clarifyResponding: approvalClarify.clarifyResponding,
    respondApproval: approvalClarify.respondApproval,
    respondClarify: approvalClarify.respondClarify,
    dismissApproval,
  }
}

// applySnapshot is exported via the hook closure in Phase D (sessions); kept
// internal here so the facade surface stays minimal for C2/C3.
