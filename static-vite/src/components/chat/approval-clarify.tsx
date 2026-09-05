// ApprovalCard — C6: transcription of vanilla index.html approval-card markup
// (lines 489-517) with respond endpoints from messages.js respondApproval.
// Vanilla quirks kept: dismiss marks approval dismissed; respond posts
// {session_id, choice, approval_id}; "1 of N" counter; collapse chevron.

import * as React from 'react'
import { t } from '../../i18n'

export interface ApprovalPending {
  approval_id?: string
  run_id?: string
  description?: string
  command?: string
  pattern_key?: string
  pattern_keys?: string[]
  [k: string]: unknown
}

export interface ClarifyPending {
  clarify_id?: string
  question?: string
  choices?: unknown[]
  [k: string]: unknown
}

export function ApprovalCard({
  pending,
  pendingCount = 1,
  responding,
  onRespond,
  onDismiss,
}: {
  pending: ApprovalPending
  pendingCount?: number
  /** choice currently in-flight (buttons disabled) */
  responding?: string | null
  onRespond: (choice: 'once' | 'session' | 'always' | 'deny') => void
  onDismiss: () => void
}) {
  const [collapsed, setCollapsed] = React.useState(false)
  const keys = pending.pattern_keys ?? (pending.pattern_key ? [pending.pattern_key] : [])
  const desc = (pending.description || '') + (keys.length ? ` [${keys.join(', ')}]` : '')
  const cmd = pending.command || ''
  const busy = !!responding

  const btn = (choice: 'once' | 'session' | 'always' | 'deny', label: string, icon: React.ReactNode, extraClass: string, disabled = false) => (
    <button
      type="button"
      className={`approval-btn ${extraClass}`}
      disabled={busy || disabled}
      aria-pressed={responding === choice}
      onClick={() => onRespond(choice)}
    >
      <span className="approval-btn-icon">{icon}</span>
      <span className="approval-btn-label">{label}</span>
      {choice === 'once' ? <kbd className="approval-kbd">↵</kbd> : null}
    </button>
  )

  return (
    <div className={`approval-card visible${collapsed ? ' collapsed' : ''}`} id="approvalCard" role="alertdialog" aria-labelledby="approvalHeading" aria-describedby="approvalDesc">
      <div className="approval-inner">
        <div className="approval-header">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true"><path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" /><line x1="12" y1="9" x2="12" y2="13" /><line x1="12" y1="17" x2="12.01" y2="17" /></svg>
          <span id="approvalHeading">{t('approval_heading')}</span>
          <button
            type="button"
            className="approval-collapse"
            aria-expanded={!collapsed}
            aria-label={collapsed ? 'Expand approval' : 'Collapse approval'}
            title={collapsed ? 'Expand approval' : 'Collapse approval'}
            onClick={() => setCollapsed((c) => !c)}
          >
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><polyline points={collapsed ? '18 15 12 9 6 15' : '6 9 12 15 18 9'} /></svg>
          </button>
          <button type="button" className="approval-dismiss" aria-label="Dismiss approval" title="Dismiss approval" onClick={onDismiss}>
            <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" /></svg>
          </button>
        </div>
        <div className="approval-desc" id="approvalDesc">{desc}</div>
        <div className="approval-cmd" id="approvalCmd">{cmd}</div>
        <div className="approval-counter" id="approvalCounter" style={{ display: pendingCount > 1 ? '' : 'none' }}>
          {pendingCount > 1 ? t('approval_pending_count', pendingCount) : ''}
        </div>
        <div className="approval-btns" id="approvalBtns">
          {btn('once', t('approval_btn_once'), <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><polyline points="20 6 9 17 4 12" /></svg>, 'once')}
          {btn('session', t('approval_btn_session'), <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><rect x="3" y="11" width="18" height="11" rx="2" ry="2" /><path d="M7 11V7a5 5 0 0 1 10 0v4" /></svg>, 'session')}
          {btn('always', t('approval_btn_always'), <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2" /></svg>, 'always')}
          {btn('deny', t('approval_btn_deny'), <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" /></svg>, 'deny')}
        </div>
      </div>
    </div>
  )
}

export function ClarifyCard({
  pending,
  responding,
  onRespond,
}: {
  pending: ClarifyPending
  responding?: boolean
  onRespond: (value: string) => void
}) {
  const [collapsed, setCollapsed] = React.useState(false)
  const [value, setValue] = React.useState('')
  const inputRef = React.useRef<HTMLInputElement | null>(null)
  const choices: string[] = Array.isArray(pending.choices) ? pending.choices.map(String) : []

  return (
    <div className={`clarify-card visible${collapsed ? ' collapsed' : ''}`} id="clarifyCard" role="dialog" aria-labelledby="clarifyHeading" aria-describedby="clarifyQuestion clarifyHint">
      <div className="clarify-inner">
        <div className="clarify-header">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true"><path d="M12 17h.01" /><path d="M9.09 9a3 3 0 1 1 5.82 1c0 2-3 2-3 4" /><circle cx="12" cy="12" r="10" /></svg>
          <span id="clarifyHeading">{t('clarify_heading')}</span>
          <span className="clarify-countdown" id="clarifyCountdown" />
          <button
            type="button"
            className="clarify-collapse"
            aria-expanded={!collapsed}
            aria-label={collapsed ? 'Expand clarification' : 'Collapse clarification'}
            title={collapsed ? 'Expand clarification' : 'Collapse clarification'}
            onClick={() => setCollapsed((c) => !c)}
          >
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><polyline points={collapsed ? '18 15 12 9 6 15' : '6 9 12 15 18 9'} /></svg>
          </button>
        </div>
        <div className="clarify-question" id="clarifyQuestion">{pending.question || ''}</div>
        {choices.length ? (
          <div className="clarify-choices" id="clarifyChoices">
            {choices.map((c) => (
              <button key={c} type="button" className="clarify-choice" disabled={responding} onClick={() => onRespond(c)}>
                {c}
              </button>
            ))}
          </div>
        ) : null}
        <div className="clarify-response">
          <input
            ref={inputRef}
            className="clarify-input"
            id="clarifyInput"
            type="text"
            autoComplete="off"
            placeholder={t('clarify_input_placeholder')}
            value={value}
            disabled={responding}
            onChange={(e) => setValue(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault()
                const v = value.trim()
                if (v) onRespond(v)
              }
            }}
          />
          <button
            className="clarify-submit"
            id="clarifySubmit"
            disabled={responding || !value.trim()}
            onClick={() => {
              const v = value.trim()
              if (v) onRespond(v)
            }}
          >
            {t('clarify_send')}
          </button>
        </div>
        <div className="clarify-hint" id="clarifyHint">{t('clarify_hint')}</div>
      </div>
    </div>
  )
}
