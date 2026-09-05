// SessionList — Phase D: date-grouped, vanilla-parity rows (doc 16 D2).
// Grouping: ★ Pinned / Today / Yesterday / This week / Last week / Older —
// same buckets as vanilla _sessionTimeBucketLabel. Row DOM: .session-item >
// .session-text > .session-title-row (+ .session-time) + .session-meta.

import { useMemo } from 'react'
import { t } from '../../i18n'
import type { SessionMeta } from '../../state/types'
import type { SessionsState, SearchHit } from '../../hooks/useSessions'

interface DateGroup {
  label: string
  items: SessionMeta[]
  isPinned: boolean
}

function serverNowMs(delta: number): number {
  return Date.now() - delta
}

function sortTs(s: SessionMeta, nowDelta: number): number {
  const raw = s.last_message_at ?? s.updated_at ?? s.created_at
  if (typeof raw === 'number') return raw > 1e12 ? raw : raw * 1000
  if (typeof raw === 'string') {
    const parsed = Date.parse(raw)
    if (Number.isFinite(parsed)) return parsed
  }
  return serverNowMs(nowDelta)
}

function bucketLabel(ts: number, nowDelta: number): string {
  const now = new Date(serverNowMs(nowDelta))
  const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime()
  const startOfYesterday = startOfToday - 86_400_000
  // Week starts Monday (vanilla: (day + 6) % 7 back).
  const startOfWeek = startOfToday - ((now.getDay() + 6) % 7) * 86_400_000
  const startOfLastWeek = startOfWeek - 7 * 86_400_000
  if (ts >= startOfToday) return t('session_time_bucket_today')
  if (ts >= startOfYesterday) return t('session_time_bucket_yesterday')
  if (ts >= startOfWeek) return t('session_time_bucket_this_week')
  if (ts >= startOfLastWeek) return t('session_time_bucket_last_week')
  return t('session_time_bucket_older')
}

export function relativeTime(ts: number, nowDelta: number): string {
  if (!ts) return t('session_time_unknown')
  const now = serverNowMs(nowDelta)
  const diffMs = Math.max(0, now - ts)
  const minute = 60_000
  const hour = 60 * minute
  const startOfToday = new Date(now)
  startOfToday.setHours(0, 0, 0, 0)
  if (ts >= startOfToday.getTime()) {
    if (diffMs < minute) return t('session_time_minutes_ago', 1)
    if (diffMs < hour) return t('session_time_minutes_ago', Math.floor(diffMs / minute))
    return t('session_time_hours_ago', Math.floor(diffMs / hour))
  }
  return t('session_time_days_ago', 1)
}

function groupSessions(sessions: SessionMeta[], nowDelta: number): DateGroup[] {
  const sorted = [...sessions].sort((a, b) => sortTs(b, nowDelta) - sortTs(a, nowDelta))
  const pinned = sorted.filter((s) => s.pinned)
  const unpinned = sorted.filter((s) => !s.pinned)
  const groups: DateGroup[] = []
  if (pinned.length) groups.push({ label: '★ Pinned', items: pinned, isPinned: true })
  let curLabel = ''
  let curItems: SessionMeta[] = []
  for (const s of unpinned) {
    const label = bucketLabel(sortTs(s, nowDelta), nowDelta)
    if (label !== curLabel) {
      if (curItems.length) groups.push({ label: curLabel, items: curItems, isPinned: false })
      curLabel = label
      curItems = [s]
    } else {
      curItems.push(s)
    }
  }
  if (curItems.length) groups.push({ label: curLabel, items: curItems, isPinned: false })
  return groups
}

interface RowProps {
  session: SessionMeta
  active: boolean
  nowDelta: number
  match?: SearchHit['match_type']
  preview?: string
  query: string
  onSelect: (sid: string) => void
}

function SessionRow({ session: s, active, nowDelta, preview, onSelect }: RowProps) {
  const ts = sortTs(s, nowDelta)
  const title = (s.display_title as string | undefined) || s.title || 'Untitled'
  const count = typeof s.message_count === 'number' ? s.message_count : 0
  return (
    <div
      className={`session-item${active ? ' active' : ''}${s.archived ? ' archived' : ''}`}
      role="button"
      tabIndex={0}
      onClick={() => onSelect(s.session_id)}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          onSelect(s.session_id)
        }
      }}
    >
      <div className="session-text">
        <div className="session-title-row">
          {s.pinned ? (
            <span className="session-pin-indicator" aria-hidden="true">
              ★
            </span>
          ) : null}
          <span className="session-title" title={title}>
            {title}
          </span>
          <span className="session-time">{relativeTime(ts, nowDelta)}</span>
        </div>
        <div className="session-meta">
          {t('session_meta_messages', count)}
        </div>
        {preview ? (
          <div className="session-search-preview" title={preview}>
            {preview}
          </div>
        ) : null}
      </div>
    </div>
  )
}

export interface SessionListProps {
  sessions: SessionsState['sessions']
  loading: boolean
  error: string | null
  activeSessionId: string | null
  nowDelta: number
  searchHits: SearchHit[] | null
  query: string
  onSelect: (sid: string) => void
  onNew: () => void
}

export function SessionList({
  sessions,
  loading,
  error,
  activeSessionId,
  nowDelta,
  searchHits,
  query,
  onSelect,
}: SessionListProps) {
  const groups = useMemo(
    () => groupSessions(searchHits ?? sessions, nowDelta),
    [searchHits, sessions, nowDelta],
  )
  return (
    <>
      {error ? <div className="session-empty-note">{error}</div> : null}
      {loading && !groups.length ? (
        <div className="session-empty-note">{t('loading')}</div>
      ) : null}
      {!loading && !groups.length && !error ? (
        <div className="session-empty-note">{query ? 'No matches.' : t('no_active_session')}</div>
      ) : null}
      {groups.map((g) => (
        <div className="session-date-group" key={g.label}>
          <div className={`session-date-header${g.isPinned ? ' pinned' : ''}`}>
            <span className="session-date-caret" aria-hidden="true">
              ▾
            </span>
            <span className="session-date-label">{g.label}</span>
          </div>
          <div className="session-date-body">
            {g.items.map((s) => (
              <SessionRow
                key={s.session_id}
                session={s}
                active={s.session_id === activeSessionId}
                nowDelta={nowDelta}
                preview={searchHits ? (s as SearchHit).match_preview : undefined}
                query={query}
                onSelect={onSelect}
              />
            ))}
          </div>
        </div>
      ))}
    </>
  )
}
