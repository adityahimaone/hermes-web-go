// lib/sse.ts — EventSource wrapper with the exact vanilla event vocabulary
// (static/messages.js _wireSSE). No renaming; unknown events ignored.

import type { ChatEvent } from '../state/types'

/** SSE event names the chat stream emits (verbatim from messages.js). */
export const CHAT_SSE_EVENTS = [
  'token',
  'interim_assistant',
  'reasoning',
  'tool',
  'tool_complete',
  'todo_state',
  'approval',
  'clarify',
  'state_saved',
  'title',
  'title_status',
  'context_status',
  'goal',
  'goal_continue',
  'bg_task_complete',
  'done',
  'stream_end',
  'pending_steer_leftover',
  'compressing',
  'compressed',
  'metering',
  'apperror',
  'warning',
] as const

export type ChatSSEEventName = (typeof CHAT_SSE_EVENTS)[number]

/** Events the pure reducer consumes (the rest are UI-side concerns). */
const REDUCER_EVENTS: ReadonlySet<string> = new Set([
  'token',
  'interim_assistant',
  'reasoning',
  'tool',
  'tool_complete',
  'todo_state',
  'done',
  'metering',
  'apperror',
  'stream_end',
])

export interface SseSubscription {
  close(): void
}

/**
 * Wire an EventSource to reducer events. `onReducerEvent` receives only the
 * subset the reducer owns; `onRaw` sees every named event (UI concerns like
 * approval/clarify/title/compressing wire separately in Phase C5/E).
 * `onError` maps the native error event for the reconnect layer.
 */
export function wireChatSSE(
  source: EventSource,
  handlers: {
    onReducerEvent?: (ev: ChatEvent, raw: MessageEvent) => void
    onRaw?: (name: ChatSSEEventName, raw: MessageEvent) => void
    onError?: (err: Event) => void
  },
): SseSubscription {
  const cleanups: Array<() => void> = []
  for (const name of CHAT_SSE_EVENTS) {
    source.addEventListener(name, (raw: MessageEvent) => {
      handlers.onRaw?.(name, raw)
      if (!REDUCER_EVENTS.has(name)) return
      let data: unknown
      try {
        data = JSON.parse(typeof raw.data === 'string' && raw.data ? raw.data : '{}')
      } catch {
        return // malformed payload — ignore exactly like vanilla try/catch
      }
      const ev = normalizeEvent(name, data)
      if (ev) handlers.onReducerEvent?.(ev, raw)
    })
    cleanups.push(() => source.removeEventListener(name, () => {}))
  }
  const onErr = (err: Event) => handlers.onError?.(err)
  source.addEventListener('error', onErr)
  return {
    close() {
      for (const fn of cleanups) fn()
      source.removeEventListener('error', onErr)
      try {
        if (source.readyState !== 2) source.close()
      } catch {
        /* already closed */
      }
    },
  }
}

/** Map a raw SSE payload to the typed ChatEvent (server field names kept). */
function normalizeEvent(name: string, data: unknown): ChatEvent | null {
  const d = (data ?? {}) as Record<string, unknown>
  switch (name) {
    case 'token':
      return { type: 'token', text: String(d.text ?? '') }
    case 'interim_assistant':
      return {
        type: 'interim_assistant',
        text: String(d.text ?? ''),
        already_streamed: !!d.already_streamed,
        reasoning_echo: !!d.reasoning_echo,
      }
    case 'reasoning':
      return { type: 'reasoning', text: String(d.text ?? '') }
    case 'tool':
      return { type: 'tool', ...d } as ChatEvent
    case 'tool_complete':
      return { type: 'tool_complete', ...d } as ChatEvent
    case 'todo_state':
      return {
        type: 'todo_state',
        todos: Array.isArray(d.todos) ? (d.todos as never[]) : [],
        ts: Number(d.ts) || 0,
        source: String(d.source ?? 'tool'),
        version: Number(d.version) || 1,
        session_id: d.session_id as string | undefined,
      }
    case 'done':
      return { type: 'done', ...d } as ChatEvent
    case 'metering':
      return { type: 'metering', ...d } as ChatEvent
    case 'apperror': {
      // Server field is `type` ('rate_limit'|'cancelled'|...); the reducer's
      // discriminator also lives on `type`, so remap to `kind` at the edge.
      const { type: errorKind, ...rest } = d
      return { type: 'apperror', kind: errorKind as string, ...(rest as Record<string, unknown>) } as unknown as ChatEvent
    }
    case 'stream_end':
      return { type: 'stream_end', session_id: d.session_id as string | undefined }
    default:
      return null
  }
}
