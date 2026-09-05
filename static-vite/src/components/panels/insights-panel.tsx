// InsightsPanel — Phase E2, panels.js port. Go payload (misc_reads.go
// insightsRouter): totals, models map, daily_tokens map, activity arrays.
// Renders summary cards + models table + daily token bars. Period selector
// 7/30/90/365 (vanilla default 30).

import { useCallback, useEffect, useState } from 'react'
import { api } from '../../hooks/useChatStream'
import { t } from '../../i18n'

export interface InsightsPayload {
  period_days?: number
  total_sessions?: number
  total_messages?: number
  total_input_tokens?: number
  total_output_tokens?: number
  total_cache_read_tokens?: number
  total_cache_hit_percent?: number
  total_tokens?: number
  total_cost?: number
  models?: Record<string, { sessions?: number; input_tokens?: number; output_tokens?: number; cache_read_tokens?: number; cost?: number; total_tokens?: number }>
  daily_tokens?: Record<string, { input_tokens?: number; output_tokens?: number; total_tokens?: number; cost?: number }>
  activity_by_day?: Array<{ day: string; sessions: number }>
  activity_by_hour?: Array<{ hour: number; sessions: number }>
}

const fmt = (n: number | undefined) => (typeof n === 'number' ? n.toLocaleString() : '—')

export function InsightsPanel() {
  const [days, setDays] = useState('30')
  const [data, setData] = useState<InsightsPayload | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async (period: string) => {
    try {
      const res = await api(`/api/insights?days=${encodeURIComponent(period)}`)
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      setData((await res.json()) as InsightsPayload)
      setError(null)
    } catch (e) {
      setError(String(e))
    }
  }, [])

  useEffect(() => { void load(days) }, [days, load])

  if (error) return <div style={{ padding: 12, color: 'var(--accent)', fontSize: 12 }}>{t('error_prefix')}{error}</div>

  const maxDaily = Math.max(1, ...Object.values(data?.daily_tokens ?? {}).map((d) => d.total_tokens ?? 0))

  return (
    <div className="insights-content" id="insightsContent" style={{ flex: 1, overflowY: 'auto', padding: 12 }}>
      <div className="panel-head-sub" style={{ padding: '0 0 8px' }}>
        <select
          id="insightsPeriod"
          value={days}
          onChange={(e) => setDays(e.target.value)}
          style={{ width: '100%', background: 'var(--input-bg)', color: 'var(--text)', border: '1px solid var(--border)', borderRadius: 6, padding: '4px 8px', fontSize: 12 }}
        >
          <option value="7">7</option>
          <option value="30">30</option>
          <option value="90">90</option>
          <option value="365">365</option>
        </select>
      </div>
      {data === null ? (
        <div style={{ padding: 12, color: 'var(--muted)', fontSize: 12 }}>{t('loading')}</div>
      ) : (
        <>
          <div className="insights-card">
            <div className="insights-card-title">{t('insights_totals_title') || 'Totals'}</div>
            <div className="insights-table" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 4, fontSize: 12 }}>
              <span>{t('insights_sessions') || 'Sessions'}</span><strong>{fmt(data.total_sessions)}</strong>
              <span>{t('insights_messages') || 'Messages'}</span><strong>{fmt(data.total_messages)}</strong>
              <span>{t('insights_tokens') || 'Tokens'}</span><strong>{fmt(data.total_tokens)}</strong>
              <span>{t('insights_cache_hit') || 'Cache hit'}</span><strong>{data.total_cache_hit_percent?.toFixed(1) ?? '—'}%</strong>
              <span>{t('insights_cost') || 'Cost'}</span><strong>${data.total_cost?.toFixed(2) ?? '—'}</strong>
            </div>
          </div>
          {data.models && Object.keys(data.models).length > 0 && (
            <div className="insights-card">
              <div className="insights-card-title">{t('insights_models') || 'Models'}</div>
              <div className="insights-table insights-model-table">
                <div className="insights-table-head"><span>{t('insights_model_name') || 'Model'}</span><span>{t('insights_model_sessions') || 'Sessions'}</span><span>{t('insights_model_tokens') || 'Tokens'}</span><span>{t('insights_model_cost') || 'Cost'}</span></div>
                {Object.entries(data.models).map(([name, m]) => (
                  <div key={name} className="insights-table-row">
                    <span>{name}</span><span>{fmt(m.sessions)}</span><span>{fmt(m.total_tokens)}</span><span>${m.cost?.toFixed(2) ?? '—'}</span>
                  </div>
                ))}
              </div>
            </div>
          )}
          {data.daily_tokens && Object.keys(data.daily_tokens).length > 0 && (
            <div className="insights-card">
              <div className="insights-card-title">{t('insights_daily_tokens') || 'Daily tokens'}</div>
              <div className="insights-daily-token-chart">
                {Object.entries(data.daily_tokens).map(([day, d]) => (
                  <div key={day} className="insights-daily-row" style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 11 }}>
                    <span style={{ width: 76, color: 'var(--muted)' }}>{day}</span>
                    <div style={{ flex: 1, height: 8, background: 'var(--border)', borderRadius: 4, overflow: 'hidden' }}>
                      <div style={{ width: `${((d.total_tokens ?? 0) / maxDaily) * 100}%`, height: '100%', background: 'var(--accent)' }} />
                    </div>
                    <span style={{ width: 70, textAlign: 'right' }}>{fmt(d.total_tokens)}</span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </>
      )}
    </div>
  )
}
