import { IconChat, IconInsights, IconKanban, IconLogs, IconMemory, IconProfiles, IconSettings, IconSkills, IconSpaces, IconTasks, IconTodos } from '../ui/icons'
import { t } from '../../i18n'

// Rail tabs — same ids/data-panel/labels/order as vanilla (chat, tasks, kanban,
// skills, memory, workspaces, profiles, todos, insights, [dashboard hidden],
// logs, spacer, settings).
const RAIL_TABS = [
  { panel: 'chat', label: 'tab_chat', Icon: IconChat },
  { panel: 'tasks', label: 'tab_tasks', Icon: IconTasks },
  { panel: 'kanban', label: 'tab_kanban', Icon: IconKanban },
  { panel: 'skills', label: 'tab_skills', Icon: IconSkills },
  { panel: 'memory', label: 'tab_memory', Icon: IconMemory },
  { panel: 'workspaces', label: 'tab_workspaces', Icon: IconSpaces },
  { panel: 'profiles', label: 'tab_profiles', Icon: IconProfiles },
  { panel: 'todos', label: 'tab_todos', Icon: IconTodos },
  { panel: 'insights', label: 'tab_insights', Icon: IconInsights },
  // dashboard link (hidden by default in vanilla) arrives with panels phase
  { panel: 'logs', label: 'tab_logs', Icon: IconLogs },
] as const

/**
 * Primary nav rail (desktop) — vanilla class names preserved; switching wires
 * up in the panels phase. Chat is the default active tab.
 */
export function NavRail({ active = 'chat' }: { active?: string }) {
  return (
    <nav className="rail" aria-label="Primary navigation">
      {RAIL_TABS.map(({ panel, label, Icon }) => (
        <button
          key={panel}
          className={`rail-btn nav-tab has-tooltip${panel === active ? ' active' : ''}`}
          data-panel={panel}
          type="button"
          data-tooltip={t(label as Parameters<typeof t>[0])}
          data-i18n-title={label}
          aria-label={t(label as Parameters<typeof t>[0])}
        >
          <Icon />
        </button>
      ))}
      <div className="rail-spacer" />
      <button
        className={`rail-btn nav-tab has-tooltip${active === 'settings' ? ' active' : ''}`}
        data-panel="settings"
        type="button"
        data-tooltip={t('tab_settings')}
        data-i18n-title="tab_settings"
        aria-label={t('tab_settings')}
      >
        <IconSettings />
        <span className="auth-warning-badge" id="authWarningBadgeDesktop" style={{ display: 'none', position: 'absolute', top: 4, right: 4, width: 8, height: 8, borderRadius: '50%', background: '#e05' }} />
      </button>
    </nav>
  )
}
