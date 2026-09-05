import { describe, it, expect } from 'vitest'
import { chatReducer } from '../src/state/chatReducer'
import { initialAppState } from '../src/state/types'
import { createRevStore, acceptRev, peekRev } from '../src/lib/revGuard'
import type { AppState } from '../src/state/types'

function s(): AppState {
  return initialAppState()
}

describe('chatReducer tokens', () => {
  it('accumulates streamed tokens into one live assistant message', () => {
    let st = s()
    st = chatReducer(st, { type: 'token', text: 'Hel' })
    st = chatReducer(st, { type: 'token', text: 'lo' })
    st = chatReducer(st, { type: 'token', text: ' world' })
    expect(st.messages).toHaveLength(1)
    expect(st.messages[0].role).toBe('assistant')
    expect(st.messages[0].content).toBe('Hello world')
    expect((st.messages[0] as { _live?: boolean })._live).toBe(true)
  })

  it('opens a new assistant row after a tool boundary (fresh segment)', () => {
    let st = s()
    st = chatReducer(st, { type: 'token', text: 'Running' })
    st = chatReducer(st, { type: 'tool', id: 't1', name: 'bash' })
    st = chatReducer(st, { type: 'token', text: 'Done' })
    const asst = st.messages.filter((m) => m.role === 'assistant')
    expect(asst).toHaveLength(2)
    expect(asst[0].content).toBe('Running')
    expect(asst[1].content).toBe('Done')
  })
})

describe('interim_assistant accumulation (commit 3f02b57 semantics)', () => {
  it('accumulates interim text into the live message with blank-line join', () => {
    let st = s()
    st = chatReducer(st, { type: 'interim_assistant', text: 'Progress note 1' })
    st = chatReducer(st, { type: 'interim_assistant', text: 'Progress note 2' })
    expect(st.messages).toHaveLength(1)
    expect(st.messages[0].content).toBe('Progress note 1\n\nProgress note 2')
    expect((st.messages[0] as { _interim?: boolean })._interim).toBe(true)
  })

  it('already_streamed closes the live segment without appending', () => {
    let st = s()
    st = chatReducer(st, { type: 'token', text: 'visible text' })
    const before = st.messages.length
    st = chatReducer(st, { type: 'interim_assistant', text: 'echo', already_streamed: true })
    expect(st.messages).toHaveLength(before)
    expect(st.messages[0].content).toBe('visible text')
    // next token starts a fresh row
    st = chatReducer(st, { type: 'token', text: 'next' })
    expect(st.messages).toHaveLength(before + 1)
  })

  it('empty interim text is ignored', () => {
    let st = s()
    const before = st.messages.length
    st = chatReducer(st, { type: 'interim_assistant', text: '   ' })
    expect(st.messages).toHaveLength(before)
  })
})

describe('reasoning', () => {
  it('accumulates provider reasoning on the live message', () => {
    let st = s()
    st = chatReducer(st, { type: 'token', text: 'Answer' })
    st = chatReducer(st, { type: 'reasoning', text: 'think ' })
    st = chatReducer(st, { type: 'reasoning', text: 'more' })
    expect(st.messages[0].reasoning).toBe('think more')
  })

  it('buffers reasoning before the live row exists, flushed at done', () => {
    let st = s()
    st = chatReducer(st, { type: 'reasoning', text: 'pre-think' })
    st = chatReducer(st, { type: 'done', usage: null, session: { session_id: 's1', messages: [{ role: 'assistant', content: 'final' }] } })
    expect(st.messages[0].reasoning).toBe('pre-think')
  })
})

describe('tool calls', () => {
  it('upserts start then complete by id', () => {
    let st = s()
    st = chatReducer(st, { type: 'tool', id: 't1', name: 'bash', args: { cmd: 'ls' } })
    st = chatReducer(st, { type: 'tool_complete', id: 't1', name: 'bash', result: 'ok', is_error: false })
    expect(st.toolCalls).toHaveLength(1)
    expect(st.toolCalls[0].state).toBe('complete')
    expect(st.toolCalls[0].result).toBe('ok')
    expect(st.toolCalls[0]._createdByComplete).toBeUndefined()
  })

  it('tool_complete without prior start creates the card (_createdByComplete)', () => {
    let st = s()
    st = chatReducer(st, { type: 'tool_complete', id: 't9', name: 'read', result: 'data' })
    expect(st.toolCalls).toHaveLength(1)
    expect(st.toolCalls[0]._createdByComplete).toBe(true)
    expect(st.toolCalls[0].state).toBe('complete')
  })

  it('ignores clarify tool events', () => {
    let st = s()
    st = chatReducer(st, { type: 'tool', name: 'clarify' })
    st = chatReducer(st, { type: 'tool_complete', name: 'clarify' })
    expect(st.toolCalls).toHaveLength(0)
  })
})

describe('done settle', () => {
  it('replaces history from server snapshot and computes per-turn usage delta (#1159)', () => {
    let st = s()
    st = { ...st, session: { session_id: 's1', input_tokens: 100, output_tokens: 50, estimated_cost: 0.01, cache_read_tokens: 10, cache_write_tokens: 5 } }
    st = chatReducer(st, { type: 'token', text: 'partial live text' })
    st = chatReducer(st, {
      type: 'done',
      usage: { input_tokens: 160, output_tokens: 90, estimated_cost: 0.02, cache_read_tokens: 20, cache_write_tokens: 15, duration_seconds: 3.5, tps: 42.5 },
      session: { session_id: 's1', messages: [{ role: 'user', content: 'hi' }, { role: 'assistant', content: 'final answer' }] },
    })
    expect(st.busy).toBe(false)
    expect(st.activeStreamId).toBeNull()
    expect(st.messages).toHaveLength(2)
    expect((st.messages[1] as { _turnUsage?: { input_tokens: number } })._turnUsage?.input_tokens).toBe(60)
    expect((st.messages[1] as { _turnUsage?: { output_tokens: number } })._turnUsage?.output_tokens).toBe(40)
    expect((st.messages[1] as { _turnDuration?: number })._turnDuration).toBe(3.5)
    expect((st.messages[1] as { _turnTps?: number })._turnTps).toBe(42.5)
  })

  it('keeps existing history on malformed done payload (no session.messages)', () => {
    let st = s()
    st = chatReducer(st, { type: 'token', text: 'streamed' })
    st = chatReducer(st, { type: 'done', usage: null })
    expect(st.messages).toHaveLength(1)
    expect(st.messages[0].content).toBe('streamed')
  })

  it('no-reply guard pushes fallback assistant message (#373)', () => {
    let st = s()
    st = chatReducer(st, { type: 'done', usage: null, session: { session_id: 's1', messages: [{ role: 'user', content: 'hi' }] } })
    expect(st.messages.some((m) => m.role === 'assistant' && String(m.content).includes('No response received'))).toBe(true)
  })

  it('settles to busy=false exactly once; repeat done is idempotent', () => {
    let st = s()
    st = { ...st, busy: true, activeStreamId: 'stream1' }
    st = chatReducer(st, { type: 'done', usage: null, session: { session_id: 's1', messages: [{ role: 'assistant', content: 'x' }] } })
    const settled = st
    const again = chatReducer(settled, { type: 'done', usage: null, session: { session_id: 's1', messages: [{ role: 'assistant', content: 'x' }] } })
    expect(again.busy).toBe(false)
    expect(again.messages).toEqual(settled.messages)
  })
})

describe('metering', () => {
  it('merges usage and propagates tps; estimated or invalid tps clears it', () => {
    let st = s()
    st = chatReducer(st, { type: 'metering', usage: { input_tokens: 10 }, tps: 33.5, tps_available: true })
    expect(st.lastUsage?.input_tokens).toBe(10)
    expect(st.liveTps).toBe(33.5)
    st = chatReducer(st, { type: 'metering', estimated: true, tps: 99, tps_available: true })
    expect(st.liveTps).toBeNull()
    st = chatReducer(st, { type: 'metering', tps: -1, tps_available: true })
    expect(st.liveTps).toBeNull()
  })
})

describe('todo_state', () => {
  it('replaces snapshot; discards strictly-older ts', () => {
    let st = s()
    st = chatReducer(st, { type: 'todo_state', todos: [{ id: 1 }], ts: 100, source: 'tool', version: 1 })
    expect(st.todos).toEqual([{ id: 1 }])
    st = chatReducer(st, { type: 'todo_state', todos: [{ id: 2 }], ts: 90, source: 'tool', version: 1 })
    expect(st.todos).toEqual([{ id: 1 }]) // stale discarded
    st = chatReducer(st, { type: 'todo_state', todos: [{ id: 3 }], ts: 100, source: 'tool', version: 1 })
    expect(st.todos).toEqual([{ id: 3 }]) // equal ts still applies
  })

  it('drops cross-session payloads', () => {
    let st = s()
    st = { ...st, session: { session_id: 'mine' } }
    st = chatReducer(st, { type: 'todo_state', todos: [{ x: 1 }], session_id: 'other' })
    expect(st.todos).toEqual([])
  })
})

describe('apperror', () => {
  it('pushes error assistant message and settles', () => {
    let st = s()
    st = { ...st, busy: true, activeStreamId: 'x' }
    st = chatReducer(st, { type: 'apperror', kind: 'rate_limit', message: 'slow down' })
    expect(st.busy).toBe(false)
    expect(st.messages.at(-1)?.role).toBe('assistant')
    expect(String(st.messages.at(-1)?.content)).toContain('Rate limit reached')
    expect(String(st.messages.at(-1)?.content)).toContain('slow down')
  })

  it('recovery path adopts server session snapshot', () => {
    let st = s()
    st = { ...st, busy: true }
    st = chatReducer(st, {
      type: 'apperror',
      kind: 'interrupted',
      session: { session_id: 's1', messages: [{ role: 'user', content: 'a' }, { role: 'assistant', content: 'b' }] },
    })
    expect(st.messages).toHaveLength(2)
    expect(st.busy).toBe(false)
  })
})

describe('rev guard (out-of-order snapshot responses)', () => {
  it('accepts newer, rejects stale and duplicate', () => {
    const store = createRevStore()
    expect(acceptRev(store, 'sess1', 5)).toBe(true)
    expect(acceptRev(store, 'sess1', 4)).toBe(false) // stale
    expect(acceptRev(store, 'sess1', 5)).toBe(false) // duplicate
    expect(acceptRev(store, 'sess1', 6)).toBe(true)
    expect(peekRev(store, 'sess1')).toBe(6)
  })

  it('keys are independent per session', () => {
    const store = createRevStore()
    expect(acceptRev(store, 'a', 3)).toBe(true)
    expect(acceptRev(store, 'b', 1)).toBe(true)
    expect(acceptRev(store, 'b', 0)).toBe(false)
  })
})
