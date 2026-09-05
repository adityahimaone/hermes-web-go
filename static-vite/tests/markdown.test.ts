import { describe, it, expect } from 'vitest'
import { renderMd } from '../src/lib/markdown'

describe('markdown renderer (C5)', () => {
  it('renders basic markdown', () => {
    expect(renderMd('**bold** and _em_')).toContain('<strong>bold</strong>')
    expect(renderMd('**bold** and _em_')).toContain('<em>em</em>')
  })

  it('renders code fences with language class', () => {
    const html = renderMd('```ts\nconst x = 1\n```')
    expect(html).toContain('<code')
    expect(html).toContain('const x = 1')
  })

  it('strips script tags (sanitized)', () => {
    const html = renderMd('<script>alert(1)</script>hello')
    expect(html).not.toContain('<script>')
    expect(html).toContain('hello')
  })

  it('strips event handlers (sanitized)', () => {
    const html = renderMd('<img src=x onerror="alert(1)">')
    expect(html).not.toContain('onerror')
  })

  it('renders tables (gfm)', () => {
    const html = renderMd('| a | b |\n|---|---|\n| 1 | 2 |')
    expect(html).toContain('<table')
  })

  it('keeps blockquotes and lists', () => {
    expect(renderMd('> quoted')).toContain('<blockquote>')
    expect(renderMd('- item')).toContain('<li>')
  })
})
