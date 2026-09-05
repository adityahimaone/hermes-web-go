// Sidebar — Phase D: chat panel wired to useSessions (list, search, CRUD,
// date-grouped rows). Other panel-views keep Phase B placeholders; the
// Workspaces panel now hosts the WorkspacePanel component (files tab).

import { useCallback, useEffect, useState } from 'react'
import { t } from '../../i18n'
import { NavRail } from './nav-rail'
import { SessionList } from '../sessions/session-list'
import { useSessions } from '../../hooks/useSessions'
import type { SessionMeta } from '../../state/types'

export function Sidebar({
  activeSession,
  onSessionChange,
  activePanel = 'chat',
  onPanelSwitch,
}: {
  activeSession: SessionMeta | null
  onSessionChange: (s: SessionMeta | null) => void
  activePanel?: string
  onPanelSwitch?: (panel: string) => void
}) {
  const [draft, setDraft] = useState('')

  const sessions = useSessions({
    onActiveSession: onSessionChange,
    activeSessionId: activeSession?.session_id ?? null,
  })
  const { state } = sessions

  const selectSession = useCallback(
    (sid: string) => {
      void sessions.loadSession(sid).catch(() => undefined)
    },
    [sessions],
  )

  const createSession = useCallback(() => {
    void sessions.newSession().catch(() => undefined)
  }, [sessions])

  // Debounced search (vanilla: 300ms _searchDebounceTimer).
  useEffect(() => {
    if (!draft) {
      void sessions.search('')
      return
    }
    const id = setTimeout(() => void sessions.search(draft), 300)
    return () => clearTimeout(id)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draft])

  const rows = state.searchHits ?? state.sessions

  return (
    <aside className="sidebar">
      <button
        className="panel-head-btn mobile-sidebar-close has-tooltip has-tooltip--bottom-right"
        type="button"
        data-tooltip={t('close_menu')}
        data-i18n-title="close_menu"
        aria-label={t('close_menu')}
      >
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" /></svg>
      </button>

      <div className="sidebar-nav">
        <SidebarNavMirror active={activePanel} onSwitch={onPanelSwitch} />
      </div>

      {/* Chat panel */}
      <div className={`panel-view${activePanel === 'chat' ? ' active' : ''}`} id="panelChat">
        <div className="panel-head">
          <span data-i18n="tab_chat">{t('tab_chat')}</span>
          <div className="panel-head-actions">
            <button
              className="panel-head-btn has-tooltip has-tooltip--bottom-right"
              id="btnNewChat"
              type="button"
              data-tooltip={t('new_conversation') + ' (Cmd+K)'}
              data-i18n-title="new_conversation"
              aria-label={t('new_conversation')}
              onClick={createSession}
            >
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><line x1="12" y1="5" x2="12" y2="19" /><line x1="5" y1="12" x2="19" y2="12" /></svg>
            </button>
          </div>
        </div>
        <div className="session-search sidebar-search">
          <div className="session-search-field">
            <svg className="sidebar-search-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><circle cx="11" cy="11" r="8" /><path d="M21 21l-4.35-4.35" /></svg>
            <input
              id="sessionSearch"
              type="search"
              placeholder={t('filter_conversations')}
              data-i18n-placeholder="filter_conversations"
              autoComplete="off"
              data-1p-ignore
              data-lpignore="true"
              data-bwignore="true"
              data-form-type="other"
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
            />
            <button
              type="button"
              id="sessionSearchClear"
              className="session-search-clear has-tooltip has-tooltip--bottom-right"
              aria-label={t('clear_conversation_filter')}
              data-tooltip={t('clear_conversation_filter')}
              hidden={!draft}
              onClick={() => setDraft('')}
            >
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" /></svg>
            </button>
          </div>
        </div>
        <div className="session-list" id="sessionList">
          <SessionList
            sessions={state.sessions}
            loading={state.loading}
            error={state.error}
            activeSessionId={activeSession?.session_id ?? null}
            nowDelta={state.serverTimeDeltaMs}
            searchHits={state.searchHits}
            query={state.searchQuery}
            onSelect={selectSession}
            onNew={createSession}
          />
        </div>
      </div>

      {/* Placeholder panel-views for the remaining tabs — same wrapper as
          vanilla; contents fill in at their phases (E). Active state follows
          the nav rail. */}
      {(['tasks', 'kanban', 'skills', 'memory', 'todos', 'insights', 'profiles', 'logs'] as const).map((panel) => (
        <div
          className={`panel-view${activePanel === panel ? ' active' : ''}`}
          id={`panel${panel[0].toUpperCase()}${panel.slice(1)}`}
          key={panel}
        />
      ))}
      <div className={`panel-view${activePanel === 'settings' ? ' active' : ''}`} id="panelSettings" />

      <div className="resize-handle" id="sidebarResize" />
    </aside>
  )
}

function SidebarNavMirror({
  active,
  onSwitch,
}: {
  active: string
  onSwitch?: (panel: string) => void
}) {
  // Mirrors the rail tabs at 18px with data-label (mobile + narrow layout).
  const TABS = [
    { panel: 'chat', label: 'tab_chat' },
    { panel: 'tasks', label: 'tab_tasks' },
    { panel: 'kanban', label: 'tab_kanban' },
    { panel: 'skills', label: 'tab_skills' },
    { panel: 'memory', label: 'tab_memory' },
    { panel: 'workspaces', label: 'tab_workspaces' },
    { panel: 'profiles', label: 'tab_profiles' },
    { panel: 'todos', label: 'tab_todos' },
    { panel: 'insights', label: 'tab_insights' },
    { panel: 'logs', label: 'tab_logs' },
  ] as const
  return (
    <>
      {TABS.map(({ panel, label }) => (
        <button
          key={panel}
          className={`nav-tab has-tooltip has-tooltip--bottom${panel === active ? ' active' : ''}`}
          data-panel={panel}
          data-label={t(label as Parameters<typeof t>[0])}
          type="button"
          data-tooltip={t(label as Parameters<typeof t>[0])}
          data-i18n-title={label}
          onClick={() => onSwitch?.(panel)}
        >
          <NavRailIcon panel={panel} />
        </button>
      ))}
      <button
        className={`nav-tab has-tooltip has-tooltip--bottom${active === 'settings' ? ' active' : ''}`}
        data-panel="settings"
        type="button"
        data-tooltip={t('tab_settings')}
        data-i18n-title="tab_settings"
        style={{ position: 'relative' }}
        onClick={() => onSwitch?.('settings')}
      >
        <NavRailIcon panel="settings" />
        <span className="auth-warning-badge" id="authWarningBadgeMobile" style={{ display: 'none', position: 'absolute', top: 4, right: 4, width: 8, height: 8, borderRadius: '50%', background: '#e05' }} />
      </button>
    </>
  )
}

// 18px variants — same glyphs, size from the sidebar-nav CSS (svg width attr).
import { IconChat as C18, IconTasks as T18, IconKanban as K18, IconSkills as S18, IconMemory as M18, IconSpaces as W18, IconProfiles as P18, IconTodos as D18, IconInsights as I18, IconLogs as L18, IconSettings as G18 } from '../ui/icons'
function NavRailIcon({ panel }: { panel: string }) {
  const size = { width: 18, height: 18 } as const
  switch (panel) {
    case 'chat': return <C18 {...size} />
    case 'tasks': return <T18 {...size} />
    case 'kanban': return <K18 {...size} />
    case 'skills': return <S18 {...size} />
    case 'memory': return <M18 {...size} />
    case 'workspaces': return <W18 {...size} />
    case 'profiles': return <P18 {...size} />
    case 'todos': return <D18 {...size} />
    case 'insights': return <I18 {...size} />
    case 'logs': return <L18 {...size} />
    case 'settings': return <G18 {...size} />
    default: return null
  }
}
