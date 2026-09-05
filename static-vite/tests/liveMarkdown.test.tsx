import { describe, it, expect, afterEach } from 'vitest'
import { render, cleanup } from '@testing-library/react'
import { LiveMarkdown } from '../src/components/chat/live-markdown'

// smd buffers trailing char(s) until next write or parser_end (vanilla same).
// Tests assert on flushed prefix, not the final buffered char while live.
afterEach(cleanup)

describe('LiveMarkdown (C6) — streaming-markdown incremental render', () => {
  it('renders markdown incrementally (multiple writes)', () => {
    const { container, rerender } = render(<LiveMarkdown text='Hel' live />)
    rerender(<LiveMarkdown text='Hello **wor' live />)
    rerender(<LiveMarkdown text='Hello **world** done ' live />)
    expect(container.querySelector('strong')?.textContent).toBe('world')
    expect(container.textContent).toContain('Hello')
    expect(container.textContent).toContain('done')
  })

  it('flushes trailing buffer on live=false (parser_end)', () => {
    const { container, rerender } = render(<LiveMarkdown text='plain words onl' live />)
    // trailing char still buffered while live — prefix present
    expect(container.textContent).toContain('plain words on')
    rerender(<LiveMarkdown text='plain words only' live={false} />)
    expect(container.textContent).toBe('plain words only')
  })

  it('rebuilds parser when text is not a prefix extension (correction/echo)', () => {
    const { container, rerender } = render(<LiveMarkdown text='first draft text ' live />)
    rerender(<LiveMarkdown text='completely different ' live />)
    expect(container.textContent).not.toContain('first draft')
    expect(container.textContent).toContain('completely different')
  })

  it('renders fenced code block after settle', () => {
    const { container, rerender } = render(<LiveMarkdown text='```js\nconst x = 1\n```' live />)
    rerender(<LiveMarkdown text='```js\nconst x = 1\n```\n' live={false} />)
    expect(container.querySelector('pre, code')).toBeTruthy()
  })

  it('renders plain text while live (prefix flush)', () => {
    const { container } = render(<LiveMarkdown text='plain words ' live />)
    expect(container.textContent).toContain('plain words')
  })
})
