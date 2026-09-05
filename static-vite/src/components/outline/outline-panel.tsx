// Outline panel — outline.js port. Floating list of user messages as jump
// targets. Entries derive from the settled message array (rawIdx addressing
// mirrors vanilla msg-user-<idx> row ids — see message-list.tsx). Gate: only
// on chat view, non-compact viewport, preference enabled (#2124).

import { useEffect, useMemo, useState } from 'react'
import { t } from '../../i18n'
import type { ChatMessage } from '../../state/types'

export function _excerptText(content: ChatMessage['content']): string {
  let text = ''
  if (Array.isArray(content)) {
    text = content
      .filter((p) => p && p.type === 'text')
      .map((p) => String((p as { text?: string }).text || ''))
      .join(' ')
  } else {
    text = String(content || '')
  }
  text = text.trim().replace(/\s+/g, ' ')
  return text.length > 60 ? text.slice(0, 60) + '…' : text
}

export interface OutlineEntry {
  rawIdx: number
  label: number
  excerpt: string
}

export function buildOutlineEntries(messages: ChatMessage[]): OutlineEntry[] {
  const entries: OutlineEntry[] = []
  let userN = 0
  for (let i = 0; i < messages.length; i++) {
    const m = messages[i]
    if (!m || m.role !== 'user') continue
    const text = _excerptText(m.content)
    if (!text) continue
    userN++
    entries.push({ rawIdx: i, label: userN, excerpt: text })
  }
  return entries
}

export function OutlinePanel({
  messages,
  sessionId,
  onJump,
  open,
  onClose,
}: {
  messages: ChatMessage[]
  sessionId: string | null
  onJump: (rawIdx: number) => void
  open: boolean
  onClose: () => void
}) {
  const entries = useMemo(() => buildOutlineEntries(messages), [messages])

  if (!open) return null

  return (
    <div id="outlinePanelWrapper" role="navigation" aria-label={t('conversation_outline')}>
      <div className="outline-header">
        <span>{t('outline_title')}</span>
        <button className="outline-close-btn" type="button" onClick={onClose} aria-label="Close outline">
          ×
        </button>
      </div>
      <div id="outlinePanel">
        {!sessionId ? (
          <p className="outline-empty">{t('outline_empty')}</p>
        ) : entries.length === 0 ? (
          <p className="outline-empty">{t('outline_empty')}</p>
        ) : (
          entries.map((e) => (
            <button
              key={e.rawIdx}
              className="outline-entry"
              type="button"
              onClick={() => onJump(e.rawIdx)}
            >
              <span className="outline-entry-num">{e.label}</span>
              <span className="outline-entry-text">{e.excerpt}</span>
            </button>
          ))
        )}
      </div>
    </div>
  )
}
