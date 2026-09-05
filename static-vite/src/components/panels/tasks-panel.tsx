// TasksPanel (crons) — Phase E1, panels.js port. DOM parity with vanilla
// #panelTasks: .cron-item rows, agent/script badge, active/paused partition,
// paused <details> collapse persisted to localStorage (key verbatim).
// Detail pane (form/runs) is Phase E2 — list + statuses here.

import { useCallback, useEffect, useState } from 'react'
import { api } from '../../hooks/useChatStream'
import { t } from '../../i18n'

export interface CronJob {
  id: number | string
  name: string
  paused?: boolean
  no_agent?: boolean
  read_only?: boolean
  profile?: string
  schedule_kind?: string
  schedule_value?: string
  prompt?: string
  last_run_at?: number | null
  next_run_at?: number | null
  [k: string]: unknown
}

interface StatusMeta {
  state: 'active' | 'paused'
  label: string
  listClass: string
}

function statusMeta(job: CronJob): StatusMeta {
  const paused = Boolean(job.paused)
  return paused
    ? { state: 'paused', label: t('cron_status_paused') || 'paused', listClass: 'paused' }
    : { state: 'active', label: t('cron_status_active') || 'active', listClass: 'active' }
}

export function TasksPanel() {
  const [jobs, setJobs] = useState<CronJob[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [activeKey, setActiveKey] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const res = await api('/api/crons')
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      const data = (await res.json()) as { jobs?: CronJob[] }
      setJobs(data.jobs ?? [])
      setError(null)
    } catch (e) {
      setError(String(e))
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const active: CronJob[] = []
  const paused: CronJob[] = []
  for (const job of jobs ?? []) (statusMeta(job).state === 'paused' ? paused : active).push(job)

  const row = (job: CronJob) => {
    const status = statusMeta(job)
    const key = String(job.id)
    return (
      <div
        key={key}
        className={`cron-item${job.read_only ? ' readonly' : ''}${key === activeKey ? ' active' : ''}`}
        style={job.read_only ? { opacity: 0.78 } : undefined}
        onClick={() => setActiveKey(key)}
      >
        <div className="cron-header">
          {job.no_agent === false ? (
            <span className="cron-agent-badge" title="Agent mode">🤖</span>
          ) : (
            <span className="cron-script-badge" title={t('cron_script_badge_title') || 'Script job (no agent)'}>📜</span>
          )}
          <span className="cron-name" title={String(job.name)}>{String(job.name)}</span>
          <span className="cron-profile-badge" title={`Owner profile: ${job.profile ?? 'default'}`}>
            {job.profile ?? 'default'}
          </span>
          <span className={`cron-status ${status.listClass}`}>{status.label}</span>
          {job.read_only ? (
            <span className="cron-status disabled" title="Read-only from another profile">Read-only</span>
          ) : null}
        </div>
      </div>
    )
  }

  if (error) return <div style={{ padding: 12, color: 'var(--accent)', fontSize: 12 }}>{t('error_prefix')}{error}</div>

  return (
    <div className="cron-list" id="cronList">
      {jobs === null ? (
        <div style={{ padding: 12, color: 'var(--muted)', fontSize: 12 }}>{t('loading')}</div>
      ) : jobs.length === 0 ? (
        <div style={{ padding: 16, color: 'var(--muted)', fontSize: 12 }}>{t('cron_no_jobs')}</div>
      ) : (
        <>
          {active.map(row)}
          {paused.length > 0 && (
            <PausedSection jobs={paused.map((j) => ({ job: j, node: row(j) }))} />
          )}
        </>
      )}
    </div>
  )
}

function PausedSection({ jobs }: { jobs: Array<{ job: CronJob; node: React.ReactNode }> }) {
  const [open, setOpen] = useState(() => {
    try {
      return localStorage.getItem('cron-paused-collapsed') !== '0'
    } catch {
      return false
    }
  })
  const label = t('cron_status_paused') || 'paused'
  const header = label.charAt(0).toUpperCase() + label.slice(1)
  return (
    <details
      className="cron-paused-section"
      open={open}
      onToggle={(e) => {
        const isOpen = (e.target as HTMLDetailsElement).open
        setOpen(isOpen)
        try {
          localStorage.setItem('cron-paused-collapsed', isOpen ? '0' : '1')
        } catch { /* private mode */ }
      }}
    >
      <summary className="cron-paused-summary">{`${header} (${jobs.length})`}</summary>
      <div className="cron-paused-inner">{jobs.map((e) => e.node)}</div>
    </details>
  )
}
