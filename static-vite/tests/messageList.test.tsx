import { describe, it, expect } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { afterEach } from 'vitest'
import { MessageList } from '../src/components/chat/message-list'
import { chatReducer } from '../src/state/chatReducer'
import { initialAppState } from '../src/state/types'

// Integration: reducer output feeds the component (C5 ↔ C1 contract).
afterEach(cleanup)

describe('MessageList (C5) rendering from reducer state', () => {
  it('renders user + assistant rows from a full turn', () => {
    let st = initialAppState()
    st = chatReducer(st, { type: 'token', text: 'Hello' })
    st = chatReducer(st, { type: 'token', text: ' world' })
    st = chatReducer(st, {
      type: 'done',
      usage: { input_tokens: 10, output_tokens: 5, duration_seconds: 2 },
      session: { session_id: 's1', messages: [{ role: 'user', content: 'hi' }, { role: 'assistant', content: 'Hello world' }] },
    })
    render(<MessageList messages={st.messages} toolCalls={st.toolCalls} />)
    expect(screen.getAllByText('hi').length).toBeGreaterThan(0)
    expect(screen.getByText('Hello world')).toBeTruthy()
  })

  it('renders markdown block content', () => {
    render(
      <MessageList
        messages={[{ role: 'assistant', content: '**bold** text' }]}
        toolCalls={[]}
      />,
    )
    expect(document.querySelector('strong')).toBeTruthy()
  })

  it('renders reasoning as a details block', () => {
    render(
      <MessageList
        messages={[{ role: 'assistant', content: 'answer', reasoning: 'silent thought' }]}
        toolCalls={[]}
      />,
    )
    const details = document.querySelector('details.assistant-thinking')
    expect(details).toBeTruthy()
    expect(details?.textContent).toContain('silent thought')
  })

  it('renders tool cards with running/done state', () => {
    render(
      <MessageList
        messages={[]}
        toolCalls={[
          { id: 't1', name: 'bash', state: 'start', args: { cmd: 'ls' } },
          { id: 't2', name: 'read', state: 'complete', result: 'data' },
        ]}
      />,
    )
    expect(document.querySelectorAll('.tool-card')).toHaveLength(2)
    expect(document.querySelector('.tool-card-running')).toBeTruthy()
    expect(document.querySelectorAll('.tool-card-badge--done')).toHaveLength(1)
  })

  it('does not render empty-state content (that lives in MainView)', () => {
    render(<MessageList messages={[]} toolCalls={[]} />)
    expect(document.querySelector('.messages-inner')?.children.length ?? 0).toBe(0)
  })
})
