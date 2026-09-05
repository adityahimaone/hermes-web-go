import { describe, it, expect, afterEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react'
import { MainView } from '../src/components/layout/main-view'
import type { UseChatStream } from '../src/hooks/useChatStream'
import { initialAppState, type AppState } from '../src/state/types'

afterEach(cleanup)

type ChatOverrides = Partial<Omit<UseChatStream, 'state'>> & { state?: Partial<AppState> }

function makeChat(overrides: ChatOverrides = {}): UseChatStream {
  const { state: statePatch, ...rest } = overrides
  const base: UseChatStream = {
    state: { ...initialAppState(), ...(statePatch ?? {}) },
    send: vi.fn(async () => {}),
    stop: vi.fn(),
    approval: null,
    clarify: null,
    approvalResponding: null,
    clarifyResponding: false,
    respondApproval: vi.fn(async () => true),
    respondClarify: vi.fn(async () => true),
    dismissApproval: vi.fn(),
  }
  return { ...base, ...rest } as UseChatStream
}

describe('MainView C6 integration', () => {
  it('renders approval card with command + buttons; respond wired', async () => {
    const respondApproval = vi.fn(async () => true)
    const chat = makeChat({
      approval: {
        pending: { approval_id: 'a1', description: 'Run shell', command: 'rm -rf /tmp/x', run_id: 'r1' },
        count: 2,
      },
      respondApproval,
    })
    render(<MainView chat={chat} />)
    expect(screen.getByText('Approval required')).toBeTruthy()
    expect(document.getElementById('approvalCmd')?.textContent).toBe('rm -rf /tmp/x')
    // counter shows "1 of 2"
    const counter = document.getElementById('approvalCounter')
    expect(counter?.textContent).toContain('2')
    fireEvent.click(screen.getByText('Allow once'))
    await waitFor(() => expect(respondApproval).toHaveBeenCalledWith('once'))
  })

  it('denial path disables buttons while responding', () => {
    const chat = makeChat({
      approval: { pending: { approval_id: 'a1', description: 'x', command: 'y' }, count: 1 },
      approvalResponding: 'deny',
    })
    render(<MainView chat={chat} />)
    const once = screen.getByText('Allow once').closest('button') as HTMLButtonElement
    expect(once.disabled).toBe(true)
  })

  it('renders clarify card with choices + free input; respond wired', async () => {
    const respondClarify = vi.fn(async () => true)
    const chat = makeChat({
      clarify: { clarify_id: 'c1', question: 'Which db?', choices: ['postgres', 'sqlite'] },
      respondClarify,
    })
    render(<MainView chat={chat} />)
    expect(screen.getByText('Which db?')).toBeTruthy()
    fireEvent.click(screen.getByText('sqlite'))
    await waitFor(() => expect(respondClarify).toHaveBeenCalledWith('sqlite'))
  })

  it('clarify free-text input sends typed value', async () => {
    const respondClarify = vi.fn(async () => true)
    const chat = makeChat({
      clarify: { clarify_id: 'c1', question: 'Name?' },
      respondClarify,
    })
    render(<MainView chat={chat} />)
    const input = document.getElementById('clarifyInput') as HTMLInputElement
    fireEvent.change(input, { target: { value: 'my answer' } })
    fireEvent.click(document.getElementById('clarifySubmit') as HTMLButtonElement)
    await waitFor(() => expect(respondClarify).toHaveBeenCalledWith('my answer'))
  })

  it('shows LiveRunStatus while streaming', () => {
    const chat = makeChat({ state: { activeStreamId: 'st1', busy: true } })
    render(<MainView chat={chat} />)
    // right after mount, liveElapsed is computed synchronously (now-start >= 0)
    const status = document.querySelector('.live-run-status') as HTMLElement | null
    expect(status).toBeTruthy()
    expect(status?.textContent).toContain('Running')
  })

  it('no LiveRunStatus when idle', () => {
    const chat = makeChat({})
    render(<MainView chat={chat} />)
    expect(document.querySelector('.live-run-status')).toBeNull()
  })

  it('composer send is disabled while busy', () => {
    const chat = makeChat({ state: { busy: true } })
    render(<MainView chat={chat} />)
    const send = document.getElementById('btnSend') as HTMLButtonElement
    expect(send.disabled).toBe(true)
  })
})
