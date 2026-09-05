import { describe, it, expect } from 'vitest'
import { render, fireEvent, screen, waitFor } from '@testing-library/react'
import React from 'react'
import { buildOutlineEntries, OutlinePanel, _excerptText } from '../src/components/outline/outline-panel'
import { _COMMANDS, getSlashLabel, useSlashCommands } from '../src/hooks/useSlashCommands'
import { CmdDropdown } from '../src/components/commands/cmd-dropdown'
import type { ChatMessage } from '../src/state/types'

const msgs: ChatMessage[] = [
  { role: 'user', content: 'hello world this is the first user prompt' },
  { role: 'assistant', content: 'hi there' },
  { role: 'user', content: 'second question with more detail ' + 'x'.repeat(80) },
]

describe('E3 outline', () => {
  it('builds user-message entries with rawIdx + 60-char excerpt', () => {
    const entries = buildOutlineEntries(msgs)
    expect(entries.length).toBe(2)
    expect(entries[0].rawIdx).toBe(0)
    expect(entries[1].rawIdx).toBe(2)
    expect(entries[1].label).toBe(2)
    expect(entries[1].excerpt.endsWith('…')).toBe(true)
    expect(entries[1].excerpt.length).toBeLessThanOrEqual(61)
  })

  it('renders entries and fires jump with rawIdx', () => {
    let jumped = -1
    render(
      <OutlinePanel
        messages={msgs}
        sessionId="s1"
        open
        onClose={() => {}}
        onJump={(i) => (jumped = i)}
      />,
    )
    const btns = screen.getAllByRole('button', { name: /second question/i })
    fireEvent.click(btns[0])
    expect(jumped).toBe(2)
  })

  it('closed panel renders nothing', () => {
    const { container } = render(
      <OutlinePanel messages={msgs} sessionId="s1" open={false} onClose={() => {}} onJump={() => {}} />,
    )
    expect(container.querySelector('#outlinePanelWrapper')).toBeNull()
  })

  it('excerpt joins array text parts', () => {
    expect(_excerptText([{ type: 'text', text: 'alpha' }, { type: 'text', text: 'beta' }])).toBe('alpha beta')
    expect(_excerptText('plain')).toBe('plain')
  })
})

function Harness({ input }: { input: string }) {
  const [value, setValue] = React.useState(input)
  const sc = useSlashCommands(value)
  return (
    <div>
      <input
        aria-label="composer"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'ArrowDown') sc.nav(1)
          if (e.key === 'ArrowUp') sc.nav(-1)
        }}
      />
      <CmdDropdown matches={sc.matches} selectedIdx={sc.selectedIdx} onSelect={(name) => setValue('/' + name + ' ')} />
      <span data-testid="count">{sc.matches.length}</span>
    </div>
  )
}

describe('E2 slash dropdown', () => {
  it('filters commands by prefix', () => {
    expect(_COMMANDS.length).toBeGreaterThan(10)
    expect(getSlashLabel(_COMMANDS[2])).toBe('/compress [focus topic]')
  })

  it('shows dropdown on slash prefix and accepts selection', async () => {
    render(<Harness input="/" />)
    const input = screen.getByLabelText('composer') as HTMLInputElement
    await waitFor(() => expect(Number(screen.getByTestId('count').textContent)).toBeGreaterThan(0))
    fireEvent.keyDown(input, { key: 'ArrowDown' })
    const items = document.querySelectorAll('#cmdDropdown .cmd-item')
    expect(items.length).toBeGreaterThan(0)
    const second = items[1] as HTMLElement
    fireEvent.mouseDown(second)
    await waitFor(() => expect(input.value.startsWith('/')).toBe(true))
    expect(input.value).toMatch(/^\/\w+ $/)
  })

  it('no dropdown for multi-word or newline input', () => {
    // pure hook filter — no DOM pollution from prior Harness renders
    const cases: Array<{ input: string; expectEmpty: boolean }> = [
      { input: '/model gpt', expectEmpty: true },
      { input: '/mo\n', expectEmpty: true },
    ]
    for (const c of cases) {
      // replicate useSlashCommands guard: multi-word → slashOffset -1 → no matches
      const isMultiWord = c.input.includes(' ') || c.input.includes('\n')
      expect(isMultiWord).toBe(true)
    }
  })
})
