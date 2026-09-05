// KanbanPanel — Phase E2, panels.js port. Kanban is proxy-only in the Go
// registry (no /api/kanban route) — when the legacy Python sidecar is down
// the panel shows an error card, never an infinite spinner. When a board
// payload arrives, render lanes (ready/blocked/done/archived) with task
// cards. Detail/modals are later; list parity here.

import { useCallback, useEffect, useMemo, useState } from 'react'
import { api } from '../../hooks/useChatStream'
import { t } from '../../i18n'

export interface KanbanTask {
  id: string | number
  title: string
  status?: string
  priority?: number
  assignee?: string
  [k: string]: unknown
}

const LANES = ['ready', 'blocked', 'done', 'archived'] as const

export function KanbanPanel() {
  const [tasks, setTasks] = useState<KanbanTask[] | null>(null)
  const [unavailable, setUnavailable] = useState<string | null>(null)
  const [q, setQ] = useState('')

  const load = useCallback(async () => {
    setUnavailable(null)
    try {
      const res = await api('/api/kanban/board')
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      const data = (await res.json()) as { tasks?: KanbanTask[]; board?: unknown }
      setTasks(Array.isArray(data.tasks) ? data.tasks : [])
    } catch (e) {
      setUnavailable(String(e))
      setTasks(null)
    }
  }, [])

  useEffect(() => { void load() }, [load])

  const filtered = useMemo(() => {
    if (!tasks) return null
    const needle = q.toLowerCase().trim()
    return needle ? tasks.filter((tk) => String(tk.title).toLowerCase().includes(needle)) : tasks
  }, [tasks, q])

  const lanes = useMemo(() => {
    if (!filtered) return null
    const out = new Map<string, KanbanTask[]>()
    for (const lane of LANES) out.set(lane, [])
    for (const tk of filtered) {
      const lane = LANES.includes((tk.status ?? '') as (typeof LANES)[number]) ? (tk.status as string) : 'ready'
      out.get(lane)!.push(tk)
    }
    return out
  }, [filtered])

  return (
    <>
      <div className="kanban-filter-stack">
        <div className="sidebar-search">
          <svg className="sidebar-search-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><circle cx="11" cy="11" r="8" /><path d="M21 21l-4.35-4.35" /></svg>
          <input
            id="kanbanSearch"
            type="search"
            placeholder={t('kanban_search_tasks') || 'Search tasks'}
            data-i18n-placeholder="kanban_search_tasks"
            value={q}
            onChange={(e) => setQ(e.target.value)}
          />
        </div>
      </div>
      <div className="kanban-list" id="kanbanList">
        {unavailable !== null ? (
          <div className="kanban-unavailable detail-alert" style={{ padding: 12, color: 'var(--accent)', fontSize: 12 }}>
            {t('error_prefix')}{unavailable}
          </div>
        ) : tasks === null ? (
          <div style={{ padding: 12, color: 'var(--muted)', fontSize: 12 }}>{t('loading')}</div>
        ) : tasks.length === 0 ? (
          <div style={{ padding: 12, color: 'var(--muted)', fontSize: 12 }}>{t('kanban_no_tasks') || 'No tasks'}</div>
        ) : (
          [...(lanes ?? new Map())].map(([lane, items]) => (
            <div key={lane} className={`kanban-lane kanban-lane-${lane}`}>
              <div className="kanban-lane-header">{lane} ({items.length})</div>
              {items.map((tk: KanbanTask) => (
                <div key={String(tk.id)} className="kanban-card" data-task-id={String(tk.id)}>
                  <div className="kanban-card-title">{String(tk.title)}</div>
                  {tk.assignee ? <div className="kanban-card-meta">{String(tk.assignee)}</div> : null}
                </div>
              ))}
            </div>
          ))
        )}
      </div>
    </>
  )
}
