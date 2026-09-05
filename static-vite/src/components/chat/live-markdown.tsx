// LiveMarkdown — C6: streaming-markdown incremental renderer.
// Mirrors vanilla _smdNewParser / _smdWrite / _smdEndParser semantics:
//  • parser bound to a DOM element via default_renderer
//  • only the delta beyond already-written text is fed (prefix tracking)
//  • on non-prefix correction the parser + DOM are rebuilt from scratch
//  • parser_end flushes on live→false / unmount
//  • ITALIC_UND / STRONG_UND suppressed live (vanilla _smdRendererWithoutUnderscoreEmphasis)
// Static import (12KB, vanilla loads it globally too) keeps writes synchronous —
// no async race between parser setup and React commit.

import * as React from 'react'
import {
  parser,
  parser_end,
  parser_write,
  default_renderer,
  ITALIC_UND,
  STRONG_UND,
  type Parser,
  type Default_Renderer,
} from 'streaming-markdown'

export function LiveMarkdown({ text, live }: { text: string; live: boolean }) {
  const hostRef = React.useRef<HTMLDivElement | null>(null)
  const parserRef = React.useRef<Parser | null>(null)
  const writtenRef = React.useRef('')

  const endParser = React.useCallback(() => {
    const p = parserRef.current
    if (p) {
      try {
        parser_end(p)
      } catch {}
      parserRef.current = null
      writtenRef.current = ''
    }
  }, [])

  React.useEffect(() => () => endParser(), [endParser])

  const ensureParser = React.useCallback((): Parser | null => {
    const el = hostRef.current
    if (!el) return null
    if (parserRef.current) return parserRef.current
    const renderer = default_renderer(el) as Default_Renderer
    const baseAddToken = renderer.add_token
    renderer.add_token = (data, token) => {
      if (token === ITALIC_UND || token === STRONG_UND) return
      baseAddToken(data, token)
    }
    const p = parser(renderer)
    parserRef.current = p
    writtenRef.current = ''
    return p
  }, [])

  const writeDelta = React.useCallback(
    (nextText: string) => {
      const el = hostRef.current
      if (!el) return
      // Non-prefix correction → rebuild parser + clear DOM (vanilla _smdWrite guard)
      if (writtenRef.current && !nextText.startsWith(writtenRef.current)) {
        endParser()
        el.replaceChildren()
      }
      const p = ensureParser()
      if (!p) return
      const delta = nextText.slice(writtenRef.current.length)
      if (!delta) return
      try {
        parser_write(p, delta)
      } catch {}
      writtenRef.current = nextText
    },
    [endParser, ensureParser],
  )

  React.useEffect(() => {
    if (live) {
      writeDelta(text)
    } else {
      if (text) writeDelta(text)
      endParser()
    }
  }, [text, live, writeDelta, endParser])

  return <div ref={hostRef} className="msg-body assistant-body" />
}
