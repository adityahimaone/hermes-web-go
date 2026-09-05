// Pure chat reducer — event sequences transcribed from static/messages.js
// _wireSSE handlers. No side effects, no Date.now() (wall-clock passed in via
// event payloads or `nowMs` option), so sequences are deterministically
// testable. Fields keep server names verbatim.

import type { AppState, ChatEvent, ChatMessage, ToolCall, Usage } from './types'

export interface ReducerOptions {
  /** deterministic clock for _ts stamping (tests pass fixed values) */
  nowMs?: number
}

function lastAssistant(messages: ChatMessage[]): ChatMessage | null {
  for (let i = messages.length - 1; i >= 0; i--) {
    if (messages[i].role === 'assistant') return messages[i]
  }
  return null
}

/** upsertLiveToolCall: match by id, else (name+state) for completes without id. */
function upsertToolCall(toolCalls: ToolCall[], ev: Extract<ChatEvent, { type: 'tool' | 'tool_complete' }>): { toolCalls: ToolCall[]; call: ToolCall; createdByComplete: boolean } {
  const state = ev.type === 'tool' ? 'start' : 'complete'
  let idx = -1
  if (ev.id) {
    idx = toolCalls.findIndex((tc) => tc.id === ev.id)
  } else {
    idx = toolCalls.findIndex((tc) => tc.name === ev.name && tc.state === state)
  }
  const createdByComplete = idx === -1 && ev.type === 'tool_complete'
  if (idx === -1) {
    const call: ToolCall = {
      id: ev.id,
      name: ev.name,
      state,
      args: (ev as { args?: unknown }).args,
      result: (ev as { result?: unknown }).result,
      started_at: (ev as { started_at?: number }).started_at,
      completed_at: (ev as { completed_at?: number }).completed_at,
    }
    if (ev.type === 'tool_complete') {
      call.is_error = !!ev.is_error
      call._createdByComplete = true
    }
    return { toolCalls: [...toolCalls, call], call, createdByComplete }
  }
  const call = { ...toolCalls[idx] }
  if (ev.type === 'tool_complete') {
    call.state = 'complete'
    call.result = ev.result
    call.is_error = !!ev.is_error
    call.completed_at = (ev as { completed_at?: number }).completed_at
  } else {
    call.state = 'start'
    call.args = (ev as { args?: unknown }).args
  }
  const next = toolCalls.slice()
  next[idx] = call
  return { toolCalls: next, call, createdByComplete: false }
}

export function chatReducer(state: AppState, event: ChatEvent, opts: ReducerOptions = {}): AppState {
  switch (event.type) {
    case 'token': {
      const messages = state.messages.slice()
      const last = messages.length > 0 ? messages[messages.length - 1] : null
      if (last && last.role === 'assistant' && last._live) {
        const next: ChatMessage = { ...last, content: String(last.content ?? '') + event.text }
        messages[messages.length - 1] = next
      } else {
        messages.push({ role: 'assistant', content: event.text, _live: true } as ChatMessage)
      }
      return { ...state, messages }
    }

    case 'interim_assistant': {
      const visible = String(event.text ?? '').trim()
      if (!visible) return state
      if (event.already_streamed) {
        // Already rendered live: close the live segment (next token opens a new
        // assistant row) — mirrors _resetAssistantSegment after a flush.
        return closeLiveSegment(state)
      }
      const messages = state.messages.slice()
      const last = messages.length > 0 ? messages[messages.length - 1] : null
      if (last && last.role === 'assistant' && last._live) {
        const prevContent = String(last.content ?? '')
        messages[messages.length - 1] = {
          ...last,
          content: prevContent ? `${prevContent}\n\n${visible}` : visible,
          _interim: true,
        }
        return { ...state, messages }
      }
      messages.push({ role: 'assistant', content: visible, _live: true, _interim: true })
      return { ...state, messages }
    }

    case 'reasoning': {
      const messages = state.messages.slice()
      const last = messages.length > 0 ? messages[messages.length - 1] : null
      if (last && last.role === 'assistant' && last._live) {
        messages[messages.length - 1] = { ...last, reasoning: (last.reasoning ?? '') + event.text }
        return { ...state, messages }
      }
      // No live row yet — buffer in state._liveReasoning is skipped: vanilla only
      // persists reasoning to the settled message at done; the live card UI
      // (Phase C5) reads the same accumulator via useChatStream. Keep it on the
      // state as a pending buffer.
      return { ...state, _pendingReasoning: (state._pendingReasoning ?? '') + event.text } as AppState
    }

    case 'tool':
    case 'tool_complete': {
      if (event.name === 'clarify') return state
      const { toolCalls, createdByComplete } = upsertToolCall(state.toolCalls, event)
      // A tool boundary ends the live text segment: next tokens start a new
      // assistant row (vanilla _freshSegment = true + _resetAssistantSegment).
      const state2 = closeLiveSegment({ ...state, toolCalls })
      if (createdByComplete && event.type === 'tool_complete') {
        // tool_complete with no prior start still creates a card (vanilla
        // _createdByComplete path) — nothing extra to record here beyond the
        // upsert above; the renderer shows it identically.
      }
      return state2
    }

    case 'todo_state': {
      if (!Array.isArray(event.todos)) return state
      if (event.session_id && state.session && event.session_id !== state.session.session_id) return state
      const incomingTs = Number(event.ts) || 0
      const currentTs = (state.todoStateMeta && Number(state.todoStateMeta.ts)) || 0
      if (incomingTs && currentTs && incomingTs < currentTs) return state // stale snapshot
      return {
        ...state,
        todos: event.todos,
        todoStateMeta: {
          ts: incomingTs || (opts.nowMs ?? 0) / 1000,
          source: String(event.source || 'tool'),
          version: Number(event.version) || 1,
        },
      }
    }

    case 'metering': {
      let next: AppState = state
      if (event.usage) {
        next = { ...next, lastUsage: { ...(state.lastUsage ?? {}), ...event.usage } }
      }
      const tps = typeof event.tps === 'number' && event.tps > 0 && event.tps_available !== false && event.estimated !== true ? event.tps : null
      return { ...next, liveTps: tps } as AppState
    }

    case 'done': {
      return applyDone(state, event, opts)
    }

    case 'apperror': {
      return applyAppError(state, event)
    }

    case 'stream_end': {
      // Server closed the channel without a done payload: keep accumulators,
      // let the reconnect/recovery layer (useChatStream) drive the next action.
      return state
    }

    default:
      return state
  }
}

function closeLiveSegment(state: AppState): AppState {
  const messages = state.messages.slice()
  const last = messages.length > 0 ? messages[messages.length - 1] : null
  if (last && last.role === 'assistant' && last._live) {
    messages[messages.length - 1] = { ...last, _live: false }
  }
  return { ...state, messages, _pendingReasoning: undefined } as AppState
}

function applyDone(state: AppState, event: Extract<ChatEvent, { type: 'done' }>, opts: ReducerOptions): AppState {
  const d = event
  const nowS = (opts.nowMs ?? 0) / 1000
  let messages = state.messages
  const session = d.session ?? null

  // Capture previous totals BEFORE overwriting (vanilla #1159 per-turn delta).
  const prevIn = state.session?.input_tokens ?? 0
  const prevOut = state.session?.output_tokens ?? 0
  const prevCost = state.session?.estimated_cost ?? 0
  const prevCacheRead = state.session?.cache_read_tokens ?? 0
  const prevCacheWrite = state.session?.cache_write_tokens ?? 0

  // Never replace live history from a malformed done payload.
  if (session && Array.isArray(session.messages)) {
    messages = session.messages
  }

  const nextMessages = messages.slice()
  const lastAsstIdx = (() => {
    for (let i = nextMessages.length - 1; i >= 0; i--) if (nextMessages[i].role === 'assistant') return i
    return -1
  })()
  const lastAsst = lastAsstIdx >= 0 ? { ...nextMessages[lastAsstIdx] } : null

  if (lastAsst) {
    // Attach pending provider reasoning (vanilla: persist reasoningText trace).
    if (state._pendingReasoning && !lastAsst.reasoning) lastAsst.reasoning = state._pendingReasoning
    // Stamp _ts when missing.
    if (!lastAsst._ts && !lastAsst.timestamp) lastAsst._ts = nowS
    // Per-turn usage delta (#1159) — only when totals increased.
    if (d.usage) {
      const curIn = d.usage.input_tokens || 0
      const curOut = d.usage.output_tokens || 0
      const curCost = d.usage.estimated_cost || 0
      const curCacheRead = d.usage.cache_read_tokens || 0
      const curCacheWrite = d.usage.cache_write_tokens || 0
      if (curIn > prevIn || curOut > prevOut || curCacheRead > prevCacheRead || curCacheWrite > prevCacheWrite) {
        lastAsst._turnUsage = {
          input_tokens: Math.max(0, curIn - prevIn),
          output_tokens: Math.max(0, curOut - prevOut),
          estimated_cost: Math.max(0, curCost - prevCost),
          cache_read_tokens: Math.max(0, curCacheRead - prevCacheRead),
          cache_write_tokens: Math.max(0, curCacheWrite - prevCacheWrite),
          turn_cache_hit_percent: d.usage.turn_cache_hit_percent,
        }
      }
      if (typeof d.usage.duration_seconds === 'number') lastAsst._turnDuration = d.usage.duration_seconds
      if (typeof d.usage.tps === 'number' && d.usage.tps > 0) lastAsst._turnTps = d.usage.tps
      if (d.usage.gateway_routing) lastAsst._gatewayRouting = d.usage.gateway_routing
    }
    if (lastAsstIdx >= 0) nextMessages[lastAsstIdx] = lastAsst
  }

  // No-reply guard (#373): agent returned nothing.
  const hasAssistantText = nextMessages.some((m) => m.role === 'assistant' && String(m.content ?? '').trim())
  if (!hasAssistantText) {
    nextMessages.push({ role: 'assistant', content: '**No response received.** Check your API key and model selection.' })
  }

  const usage = d.usage
    ? { ...(state.lastUsage ?? {}), ...d.usage }
    : state.lastUsage

  return {
    ...state,
    session: session ?? state.session,
    messages: nextMessages,
    busy: false,
    activeStreamId: null,
    liveTps: undefined,
    _pendingReasoning: undefined,
    lastUsage: usage,
  } as AppState
}

function applyAppError(state: AppState, event: Extract<ChatEvent, { type: 'apperror' }>): AppState {
  const d = event
  const messages = state.messages.slice()
  if (d.session && Array.isArray(d.session.messages)) {
    // Server delivered a settled session snapshot (recovery path).
    return {
      ...state,
      session: d.session,
      messages: d.session.messages,
      busy: false,
      activeStreamId: null,
      liveTps: undefined,
      _pendingReasoning: undefined,
    } as AppState
  }
  const label =
    d.kind === 'cancelled'
      ? 'Task cancelled'
      : d.kind === 'interrupted'
        ? 'Response interrupted'
        : d.kind === 'rate_limit'
          ? 'Rate limit reached'
          : 'Error'
  const hint = d.hint ? `\n\n*${d.hint}*` : ''
  messages.push({
    role: 'assistant',
    content: `**${label}:** ${d.message || 'An error occurred.'}${hint}`,
    provider_details: d.details || undefined,
  })
  return {
    ...state,
    messages,
    busy: false,
    activeStreamId: null,
    liveTps: undefined,
    _pendingReasoning: undefined,
  } as AppState
}

/** Utility for the hook layer: merge a Usage snapshot into lastUsage. */
export function mergeUsage(prev: Usage | null, incoming: Usage): Usage {
  return { ...(prev ?? {}), ...incoming }
}
