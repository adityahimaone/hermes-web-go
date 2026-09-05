import { useState } from 'react'
import { t } from '../../i18n'
import { HermesMark } from '../ui/hermes-mark'

/**
 * App titlebar — transcribed from vanilla index.html <header class="app-titlebar">.
 * Phase B scope: static chrome (profile btn hidden, hamburger, brand, new-chat,
 * reload). Live behaviors (profile dropdown, mobile sidebar toggle) land with
 * their panels.
 */
export function AppTitlebar() {
  const [sub] = useState('')

  return (
    <header className="app-titlebar" role="banner">
      <div className="app-titlebar-left">
        <button
          className="app-titlebar-profile"
          id="titlebarProfileBtn"
          type="button"
          aria-label={t('switch_profile')}
          style={{ display: 'none' }}
        >
          <span className="app-titlebar-profile-icon" aria-hidden="true">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2" /><circle cx="12" cy="7" r="4" /></svg>
          </span>
          <span className="app-titlebar-profile-label" id="titlebarProfileLabel">default</span>
          <span className="app-titlebar-profile-chevron" aria-hidden="true">
            <svg width="8" height="8" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><polyline points="6 9 12 15 18 9" /></svg>
          </span>
        </button>
        <button
          className="app-titlebar-hamburger has-tooltip has-tooltip--bottom"
          id="btnHamburger"
          type="button"
          data-tooltip="Menu"
          aria-label={t('menu')}
        >
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><line x1="3" y1="6" x2="21" y2="6" /><line x1="3" y1="12" x2="21" y2="12" /><line x1="3" y1="18" x2="21" y2="18" /></svg>
        </button>
      </div>
      <div className="app-titlebar-inner">
        <span className="app-titlebar-icon" aria-hidden="true">
          <HermesMark />
        </span>
        <span className="app-titlebar-title" id="appTitlebarTitle">Hermes</span>
        <span className="app-titlebar-sub" id="appTitlebarSub" hidden={sub === ''}>{sub}</span>
      </div>
      <div className="app-titlebar-spacer" aria-hidden="true" />
      <button
        className="app-titlebar-new-chat"
        id="btnTitlebarNewChat"
        type="button"
        aria-label={t('new_conversation')}
        data-i18n-title="new_conversation"
        title={t('new_conversation')}
      >
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" width="16" height="16">
          <line x1="12" y1="5" x2="12" y2="19" />
          <line x1="5" y1="12" x2="19" y2="12" />
        </svg>
      </button>
      <button className="app-titlebar-reload" id="btnReload" type="button" aria-label={t('reload')} title={t('reload_page')} onClick={() => window.location.reload()}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" width="16" height="16">
          <polyline points="23 4 23 10 17 10" />
          <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10" />
        </svg>
      </button>
    </header>
  )
}
