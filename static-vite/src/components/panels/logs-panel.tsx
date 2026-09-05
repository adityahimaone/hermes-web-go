// LogsPanel — Phase E2, panels.js port. /api/logs?file=&tail= (Go native,
// logs_route.go). Severity class per line (vanilla _logsLineClass): error /
// warning / debug / info. Controls: file select, tail select, severity
// filter, wrap, copy-all.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api } from '../../hooks/useChatStream'
import { t } from '../../i18n'

interface LogsPayload {
  file?: string
  tail?: number
  lines?: string[]
  truncated?: boolean
  total_bytes?: number
  mtime?: number
  hint?: string
}

function lineClass(line: string): string {
  if (/\b(ERROR|CRITICAL|TRACEBACK)\b/.test(line)) return 'log-line-error'
  if (/\b(WARNING|WARN)\b/.test(line)) return 'log-line-warning'
  if (/\b(DEBUG)\b/.test(line)) return 'log-line-debug'
  return 'log-line-info'
}

export function LogsPanel() {
  const [file, setFile] = useState('agent')
  const [tail, setTail] = useState('200')
  const [severity, setSeverity] = useState('all')
  const [wrap, setWrap] = useState(false)
  const [data, setData] = useState<LogsPayload | null>(null)
  const [error, setError] = useState<string | null>(null)
  const boxRef = useRef<HTMLDivElement>(null)

  const load = useCallback(async () => {
    try {
      const res = await api(`/api/logs?file=${encodeURIComponent(file)}&tail=${encodeURIComponent(tail)}`)
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      setData((await res.json()) as LogsPayload)
      setError(null)
    } catch (e) {
      setError(String(e))
    }
  }, [file, tail])

  useEffect(() => { void load() }, [load])

  const lines = useMemo(() => {
    const raw = data?.lines ?? []
    if (severity === 'errors') return raw.filter((l) => lineClass(l) === 'log-line-error')
    if (severity === 'warnings') return raw.filter((l) => lineClass(l) !== 'log-line-info')
    return raw
  }, [data, severity])

  const copyAll = () => {
    void navigator.clipboard?.writeText(lines.join('\n'))
  }

  if (error) return <div style={{ padding: 12, color: 'var(--accent)', fontSize: 12 }}>{t('error_prefix')}{error}</div>

  return (
    <>
      <div className="logs-control-panel">
        <label className="logs-control-label" htmlFor="logsFile">{t('logs_file') || 'File'}</label>
        <select id="logsFile" value={file} onChange={(e) => setFile(e.target.value)}>
          <option value="agent">agent</option>
          <option value="errors">errors</option>
          <option value="gateway">gateway</option>
        </select>
        <label className="logs-control-label" htmlFor="logsTail">{t('logs_tail') || 'Tail'}</label>
        <select id="logsTail" value={tail} onChange={(e) => setTail(e.target.value)}>
          <option value="100">100</option>
          <option value="200">200</option>
          <option value="500">500</option>
          <option value="1000">1000</option>
        </select>
        <label className="logs-control-label" htmlFor="logsSeverityFilter">{t('logs_severity') || 'Severity'}</label>
        <select id="logsSeverityFilter" value={severity} onChange={(e) => setSeverity(e.target.value)}>
          <option value="all">{t('logs_severity_all') || 'All'}</option>
          <option value="errors">{t('logs_severity_errors') || 'Errors'}</option>
          <option value="warnings">{t('logs_severity_warnings') || 'Warnings+'}</option>
        </select>
        <label className="logs-check-row">
          <input id="logsWrap" type="checkbox" checked={wrap} onChange={(e) => setWrap(e.target.checked)} />
          <span>{t('logs_wrap') || 'Wrap lines'}</span>
        </label>
        <button type="button" className="logs-copy" id="logsCopyAll" onClick={copyAll}>{t('logs_copy_all') || 'Copy all'}</button>
      </div>
      <div
        id="logsContent"
        ref={boxRef}
        className="logs-content"
        style={{ flex: 1, overflow: 'auto', padding: '8px 12px', fontFamily: 'var(--mono, monospace)', fontSize: 11, whiteSpace: wrap ? 'pre-wrap' : 'pre' }}
      >
        {data === null ? (
          <div style={{ color: 'var(--muted)', fontSize: 12 }}>{t('loading')}</div>
        ) : (
          lines.map((l, i) => <div key={i} className={`log-line ${lineClass(l)}`}>{l}</div>)
        )}
      </div>
    </>
  )
}
