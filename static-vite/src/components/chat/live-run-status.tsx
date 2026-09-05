// LiveRunStatus — C6: vanilla liveRunStatus footer (ui.js _renderLiveRunStatusContent).
// `00:00 · 1.2k tokens · Running` with pulsing dot; hidden in compact worklog mode
// (compact handling lands with worklog display in E-phase; hook provides values).

import * as React from 'react'

export function formatRunElapsed(seconds: number): string {
  const n = Number(seconds)
  if (!Number.isFinite(n) || n < 0) return '00:00'
  const total = Math.max(0, Math.floor(n))
  if (total >= 3600) {
    const h = Math.floor(total / 3600)
    const m = Math.floor((total % 3600) / 60)
    return `${h}h ${String(m).padStart(2, '0')}m`
  }
  const m = Math.floor(total / 60)
  const s = total % 60
  return `${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
}

export function formatTokens(n: number | null | undefined): string {
  if (!n || n < 0) return '0'
  if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M'
  if (n >= 1e3) return (n / 1e3).toFixed(1) + 'k'
  return String(n)
}

export function LiveRunStatus({ elapsedSeconds, tokens }: { elapsedSeconds: number; tokens: number | null }) {
  return (
    <div className="live-run-status live-footer" id="liveRunStatus">
      <span className="live-run-status-dot tool-card-running-dot" />
      <span className="live-run-status-text lf-time">{formatRunElapsed(elapsedSeconds)}</span>
      {tokens != null ? (
        <>
          <span className="lf-sep">·</span>
          <span className="lf-tokens">{formatTokens(tokens)} tokens</span>
        </>
      ) : null}
      <span className="lf-sep">·</span>
      <span className="lf-status">Running</span>
    </div>
  )
}
