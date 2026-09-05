// MemoryPanel — Phase E1, panels.js port (#panelMemory side menu). Section
// buttons from MEMORY_SECTIONS, payload-driven availability. Detail/edit in
// the main view is Phase E2.

import { useEffect, useState } from 'react'
import { api } from '../../hooks/useChatStream'
import { t } from '../../i18n'

export interface MemoryPayload {
  memory?: string
  user?: string
  soul?: string
  memory_path?: string
  user_path?: string
  soul_path?: string
  memory_mtime?: number | null
  user_mtime?: number | null
  soul_mtime?: number | null
  external_notes_enabled?: boolean
  knowledge_count?: number
  [k: string]: unknown
}

const SECTIONS: Array<{ key: string; label: string; empty: string }> = [
  { key: 'memory', label: t('my_notes') || 'My notes', empty: t('no_notes_yet') || 'No notes yet.' },
  { key: 'user', label: t('user_profile') || 'User profile', empty: t('no_profile_yet') || 'No profile yet.' },
  { key: 'soul', label: t('agent_soul') || 'Agent soul', empty: t('no_soul_yet') || 'No soul yet.' },
]

export function MemoryPanel({ activeSessionId }: { activeSessionId: string | null }) {
  const [data, setData] = useState<MemoryPayload | null>(null)
  const [current, setCurrent] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let alive = true
    const url = activeSessionId
      ? `/api/memory?session_id=${encodeURIComponent(activeSessionId)}`
      : '/api/memory'
    api(url)
      .then(async (res) => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`)
        const payload = (await res.json()) as MemoryPayload
        if (alive) {
          setData(payload)
          setError(null)
        }
      })
      .catch((e) => {
        if (alive) setError(String(e))
      })
    return () => {
      alive = false
    }
  }, [activeSessionId])

  if (error) return <div className="side-menu" id="memoryPanel"><div style={{ padding: 12, color: 'var(--accent)', fontSize: 12 }}>{t('error_prefix')}{error}</div></div>

  return (
    <div className="side-menu" id="memoryPanel">
      {data === null ? (
        <div style={{ padding: 12, color: 'var(--muted)', fontSize: 12 }}>{t('loading')}</div>
      ) : (
        SECTIONS.map((s) => (
          <button
            key={s.key}
            type="button"
            className={`side-menu-item${current === s.key ? ' active' : ''}`}
            onClick={() => setCurrent(s.key)}
          >
            <span>{s.label}</span>
          </button>
        ))
      )}
    </div>
  )
}
