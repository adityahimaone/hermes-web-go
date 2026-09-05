import { useEffect } from 'react'
import { t } from '../../i18n'
import { HermesEmptyMark } from '../ui/hermes-mark'

/**
 * Main view — empty-state + messages skeleton for Phase B.
 * The live messages/composer wiring (SSE, approval, queue, worklog, outline)
 * lands in Phase C; this file carries only the structural chrome that the
 * Phase B pixelmatch measures.
 */
export function MainView() {
  // Keep the same a11y announcer / reconnect / offline shells as vanilla
  // so the layout occupies the same geometry (their inner copy is hidden
  // until needed, just like in the vanilla shell).
  useEffect(() => {
    // Phase B: no side-effects; useEffect exists as a seam for future
    // resize/worklog observers without adding runtime cost now.
  }, [])

  return (
    <main className="main">
      {/* Update / stale-client banners: present but hidden (display:none) */}
      <div className="update-banner" id="updateBanner" style={{ display: 'none' }} />
      <div className="update-banner" id="staleClientBanner" style={{ display: 'none' }} />

      <div id="mainChat" className="main-view">
        <div className="messages-shell">
          <button id="jumpToSessionStartBtn" className="session-jump-btn session-jump-btn--start" aria-label={t('session_jump_start_label')} data-i18n-aria-label="session_jump_start_label" data-i18n-title="session_jump_start_label" title={t('session_jump_start_label')} type="button" style={{ display: 'none' }}>
            <span aria-hidden="true">↑</span><span data-i18n="session_jump_start">{t('session_jump_start')}</span>
          </button>
          <button id="scrollToBottomBtn" className="scroll-to-bottom-btn" style={{ display: 'none' }} type="button" aria-label={t('session_jump_end_label')} data-i18n-aria-label="session_jump_end_label" data-i18n-title="session_jump_end_label" title={t('session_jump_end_label')}>
            <span aria-hidden="true">↓</span><span className="session-jump-btn__text" data-i18n="session_jump_end">{t('session_jump_end')}</span>
          </button>
          <button id="outlineToggleBtn" type="button" hidden title={t('conversation_outline')} aria-label={t('conversation_outline')} aria-controls="outlinePanelWrapper">
            ☰
          </button>

          <div className="messages" id="messages">
            <div className="empty-state" id="emptyState">
              <div className="empty-logo">
                <HermesEmptyMark />
              </div>
              <h2 data-i18n="empty_title">{t('empty_title')}</h2>
              <p data-i18n="empty_subtitle">{t('empty_subtitle')}</p>
              <div className="suggestion-grid">
                <button className="suggestion" data-msg="What files are in this workspace?">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" /></svg>
                  <span data-i18n="suggest_files">{t('suggest_files')}</span>
                </button>
                <button className="suggestion" data-msg="What's on my schedule today?">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2" /><rect x="8" y="2" width={8} height={4} rx={1} ry={1} /><line x1="9" y1="12" x2="15" y2="12" /><line x1="9" y1="16" x2={12} y2={16} /></svg>
                  <span data-i18n="suggest_schedule">{t('suggest_schedule')}</span>
                </button>
                <button className="suggestion" data-msg="Help me plan a small project.">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><polygon points="1 6 1 22 8 18 16 22 23 18 23 2 16 6 8 2 1 6" /><line x1="8" y1="2" x2={8} y2={18} /><line x1="16" y1="6" x2={16} y2={22} /></svg>
                  <span data-i18n="suggest_plan">{t('suggest_plan')}</span>
                </button>
              </div>
            </div>
            <div className="messages-inner" id="msgInner" />
            <div id="liveRunStatus" hidden />
            <div id="a11yAnnouncer" className="sr-only" role="status" aria-live="polite" aria-atomic="true" />
            <div id="liveCompressionCards" className="live-compression-cards" />
            <div id="liveToolCards" style={{ display: 'none', maxWidth: 800, margin: '0 auto', width: '100%', padding: '0 24px' }} />
          </div>
        </div>

        <div className="reconnect-banner" id="reconnectBanner" style={{ display: 'none' }}>
          <span id="reconnectMsg">A response may have been in progress when you last left. Reload messages?</span>
          <div style={{ display: 'flex', gap: 8, flexShrink: 0 }}>
            <button className="reconnect-btn">Dismiss</button>
            <button className="reconnect-btn">Reload</button>
          </div>
        </div>

        <div className="offline-banner" id="offlineBanner" role="status" aria-live="assertive" hidden>
          <div className="offline-copy">
            <strong id="offlineTitle" data-i18n="offline_title">{t('offline_title')}</strong>
            <span id="offlineDetails" data-i18n="offline_browser_detail">{t('offline_browser_detail')}</span>
            <span id="offlineAutorefresh" data-i18n="offline_autorefresh">{t('offline_autorefresh')}</span>
          </div>
          <button className="offline-action" id="offlineCheckNow" type="button" data-i18n="offline_check_now">{t('offline_check_now')}</button>
        </div>

        {/* Composer chrome — same structure as vanilla; controls filled in Phase C */}
        <div className="composer-wrap" id="composerWrap">
          <div className="composer-flyout">
            <div id="queueCard" className="queue-card" role="region" aria-label={t('queued_messages')} aria-live="polite">
              <div id="queueChips" className="queue-card-inner" />
            </div>
            <div className="approval-card" id="approvalCard" role="alertdialog" aria-labelledby="approvalHeading" aria-describedby="approvalDesc" hidden aria-hidden="true" inert>
              <div className="approval-inner" />
            </div>
          </div>
          <div className="composer-box" id="composerBox">
            <textarea id="msg" placeholder="Message Hermes…" rows={1} />
            <div className="composer-footer">
              <div className="composer-left" />
              <div className="composer-right">
                <button id="btnSend" className="send-btn" type="button" aria-label={t('send')} data-i18n-aria-label="composer_send" disabled>
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><line x1="12" y1="19" x2="12" y2="5" /><polyline points="5 12 12 5 19 12" /></svg>
                </button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </main>
  )
}

// Empty-state big logo removed — see ui/hermes-mark (HermesEmptyMark).
