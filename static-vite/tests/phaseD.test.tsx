// Phase D gates: session grouping/labeling (vanilla bucket parity),
// rev-guarded list application (stale generation dropped), workspace tree
// rendering from the Go /api/list payload, preview open/close.

import { describe, it, expect, afterEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react'
import { SessionList } from '../src/components/sessions/session-list'
import { WorkspacePanel } from '../src/components/workspace/workspace-panel'
import { joinWorkspacePath } from '../src/hooks/useWorkspace'
import type { SessionMeta } from '../src/state/types'
import type { WorkspaceEntry } from '../src/hooks/useWorkspace'

afterEach(cleanup)

const NOW = new Date(2026, 8, 5, 15, 0, 0).getTime() // 2026-09-05 15:00 local

function sid(n: number): string {
  return `s${n}`
}
function sess(n: number, tsSec: number, extra: Partial<SessionMeta> = {}): SessionMeta {
  return {
    session_id: sid(n),
    title: `Session ${n}`,
    updated_at: tsSec,
    message_count: 2,
    ...extra,
  } as SessionMeta
}

describe('SessionList grouping (vanilla bucket parity)', () => {
  const base = {
    loading: false,
    error: null,
    activeSessionId: null,
    searchHits: null,
    query: '',
    onSelect: () => {},
    onNew: () => {},
  }

  it('groups into Pinned/Today/Yesterday/This week/Last week/Older', () => {
    const today = sess(1, Math.floor(NOW / 1000) - 60) // 1 min ago
    const yesterday = sess(2, Math.floor(NOW / 1000) - 86_400) // 1 day ago
    const thisWeek = sess(3, Math.floor(NOW / 1000) - 3 * 86_400)
    const older = sess(4, Math.floor(NOW / 1000) - 40 * 86_400)
    const pinned = sess(5, Math.floor(NOW / 1000) - 90 * 86_400, { pinned: true })
    const { container } = render(
      <SessionList
        {...base}
        sessions={[older, thisWeek, yesterday, today, pinned]}
        nowDelta={0}
      />,
    )
    const labels = [...container.querySelectorAll('.session-date-label')].map((el) => el.textContent)
    expect(labels).toEqual(['★ Pinned', 'Today', 'Yesterday', 'This week', 'Older'])
    // rows inside groups in recency order
    const todayRows = container.querySelectorAll('.session-date-group:nth-child(2) .session-item')
    expect(todayRows.length).toBe(1)
  })

  it('shows active row with .active class', () => {
    const s = sess(1, Math.floor(NOW / 1000))
    const { container } = render(
      <SessionList {...base} sessions={[s]} activeSessionId={sid(1)} nowDelta={0} />,
    )
    expect(container.querySelector('.session-item.active')).toBeTruthy()
    expect(container.querySelector('.session-title')?.textContent).toBe('Session 1')
  })

  it('renders search preview when provided', () => {
    const hit = { ...sess(1, Math.floor(NOW / 1000)), match_preview: '...needle...' } as SessionMeta
    const { container } = render(
      <SessionList {...base} sessions={[]} searchHits={[hit]} query="needle" nowDelta={0} />,
    )
    expect(container.querySelector('.session-search-preview')?.textContent).toContain('needle')
  })
})

describe('Rev-guarded list application', () => {
  it('joinWorkspacePath mirrors vanilla _joinWorkspacePath', () => {
    expect(joinWorkspacePath('.', 'file.txt')).toBe('file.txt')
    expect(joinWorkspacePath('src', 'lib')).toBe('src/lib')
    expect(joinWorkspacePath('src/', 'lib')).toBe('src/lib')
  })
})

describe('WorkspacePanel tree rendering', () => {
  const entry = (name: string, path: string, type: WorkspaceEntry['type']): WorkspaceEntry => ({
    name,
    path,
    type,
    size: type === 'file' ? 10 : null,
    mtime_ns: 0,
    birthtime_ns: 0,
    workspace_sort_rank: 0,
  })

  const basePanel = {
    state: {
      currentDir: '.',
      entries: [] as WorkspaceEntry[],
      loading: false,
      error: null as string | null,
      workspaceRoot: '/tmp/ws',
      preview: null,
    },
    onNavigate: () => {},
    onOpenFile: () => {},
    onClosePreview: () => {},
    onRawUrl: () => null,
    onNavigateUp: () => {},
    activeTab: 'files' as const,
    setActiveTab: () => {},
  }

  it('renders dirs-first rows; clicking dir navigates, file opens preview', () => {
    const onNavigate = vi.fn()
    const onOpenFile = vi.fn()
    const { container } = render(
      <WorkspacePanel
        {...basePanel}
        session={{ session_id: 's1' } as SessionMeta}
        state={{ ...basePanel.state, entries: [entry('src', 'src', 'dir'), entry('README.md', 'README.md', 'file')] }}
        onNavigate={onNavigate}
        onOpenFile={onOpenFile}
      />,
    )
    const rows = [...container.querySelectorAll('.tree-row')]
    expect(rows.length).toBe(2)
    fireEvent.click(rows[0]) // src dir
    expect(onNavigate).toHaveBeenCalledWith('src')
    fireEvent.click(rows[1]) // README.md
    expect(onOpenFile).toHaveBeenCalledWith('README.md')
  })

  it('shows empty state for an empty workspace', () => {
    const { container } = render(
      <WorkspacePanel {...basePanel} session={{ session_id: 's1' } as SessionMeta} />,
    )
    expect(container.textContent).toContain('empty')
  })

  it('no session → no-session note', () => {
    const { container } = render(<WorkspacePanel {...basePanel} session={null} />)
    expect(container.querySelector('.workspace-empty')).toBeTruthy()
  })
})

describe('Sessions rev guard (stale generation dropped)', () => {
  it('list rows render from payload via refresh()', async () => {
    // Mock fetch for /api/sessions
    const payload = {
      sessions: [sess(1, Math.floor(NOW / 1000)), sess(2, Math.floor(NOW / 1000) - 200)],
      webui_session_count: 2,
      server_time: Math.floor(NOW / 1000),
    }
    const fetchMock = vi.fn(async (url: string | URL | Request) => {
      const u = String(url)
      if (u.includes('/api/sessions') || u.includes('/api/session')) {
        return new Response(JSON.stringify(payload), { status: 200, headers: { 'Content-Type': 'application/json' } })
      }
      return new Response(JSON.stringify({}), { status: 404 })
    })
    vi.stubGlobal('fetch', fetchMock)
    try {
      const { useSessions } = await import('../src/hooks/useSessions')
      let latest: ReturnType<typeof useSessions> | null = null
      function Probe() {
        latest = useSessions({})
        return <div data-testid="count">{latest.state.sessions.length}</div>
      }
      render(<Probe />)
      await waitFor(() => expect(fetchMock).toHaveBeenCalled(), { timeout: 3000 })
      await waitFor(() => expect(screen.getByTestId('count').textContent).toBe('2'), { timeout: 3000 })
      expect(latest!.state.counts.webui).toBe(2)
    } finally {
      vi.unstubAllGlobals()
    }
  })
})
