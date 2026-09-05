import * as React from 'react'
import * as ReactDOM from 'react-dom/client'
import './theme.css'
import { AppTitlebar } from './components/layout/app-titlebar'
import { NavRail } from './components/layout/nav-rail'
import { Sidebar } from './components/layout/sidebar'
import { MainView } from './components/layout/main-view'
import { RightPanel } from './components/layout/right-panel'
import { loadLocale, t } from './i18n'
import { useChatStream } from './hooks/useChatStream'
import { useState } from 'react'
import type { SessionMeta } from './state/types'

loadLocale()

// Pathname routing (boot.js port, doc 15 §5): /login renders the Login
// route instead of the chat shell. Onboarding wizard stays vanilla-phase E5.
function isLoginRoute(): boolean {
  const re = /(?:^|\/)login$/
  return re.test(window.location.pathname.replace(/\/+$/, ''))
}

function Shell() {
  const chat = useChatStream()
  // Active session: set by useSessions.loadSession; drives workspace + chat view.
  const [activeSession, setActiveSession] = useState<SessionMeta | null>(null)
  // Nav switching (Phase D): rail tabs toggle which sidebar panel-view is active.
  const [activePanel, setActivePanel] = useState('chat')
  // Workspace rightpanel — vanilla default collapsed (dataset bootstrap:
  // localStorage 'hermes-webui-workspace-panel' !== 'open' → closed).
  const [wsOpen, setWsOpen] = useState(
    (() => {
      try {
        return localStorage.getItem('hermes-webui-workspace-panel') === 'open'
      } catch {
        return false
      }
    })(),
  )

  const toggleWs = () => {
    setWsOpen((prev) => {
      const next = !prev
      try {
        localStorage.setItem('hermes-webui-workspace-panel', next ? 'open' : 'closed')
      } catch {
        /* private mode */
      }
      // chrome.css gates visibility on html[data-workspace-panel] (vanilla).
      document.documentElement.dataset.workspacePanel = next ? 'open' : 'closed'
      return next
    })
  }

  return (
    <>
      <AppTitlebar />
      <div className={`layout${wsOpen ? '' : ' workspace-panel-collapsed'}`}>
        <NavRail active={activePanel} onSwitch={setActivePanel} />
        <Sidebar
          activeSession={activeSession}
          onSessionChange={setActiveSession}
          activePanel={activePanel}
          onPanelSwitch={setActivePanel}
        />
        <MainView chat={chat} activeSession={activeSession} />
        <button
          className="workspace-panel-edge-toggle has-tooltip has-tooltip--left"
          id="btnWorkspacePanelEdgeToggle"
          type="button"
          data-tooltip={wsOpen ? t('workspace_panel_hide') : t('workspace_panel_show')}
          aria-label={wsOpen ? t('workspace_panel_hide') : t('workspace_panel_show')}
          aria-expanded={wsOpen}
          onClick={toggleWs}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><polyline points="15 18 9 12 15 6" /></svg>
        </button>
        <RightPanel session={activeSession} />
      </div>
    </>
  )
}

const rootEl = document.getElementById('root')
if (rootEl) {
  // Lazy login route keeps the chat shell out of the login bundle path.
  const LoginRoute = React.lazy(() => import('./components/auth/login-route'))
  ReactDOM.createRoot(rootEl).render(
    <React.StrictMode>
      {isLoginRoute() ? (
        <React.Suspense fallback={null}>
          <LoginRoute />
        </React.Suspense>
      ) : (
        <Shell />
      )}
    </React.StrictMode>,
  )
}
