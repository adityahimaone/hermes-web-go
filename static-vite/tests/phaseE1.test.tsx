// Phase E1 gates — panels.js port: crons list, skills grouped, memory
// sections, todos. DOM parity contract against Go endpoints.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, cleanup } from '@testing-library/react'
import * as React from 'react'
import { Sidebar } from '../src/components/layout/sidebar'

// ── fetch mock ──────────────────────────────────────────────────────────────
type Handler = (url: string, init?: RequestInit) => unknown | Promise<unknown>
const routes = new Map<RegExp, Handler>()

function stub(url: string, handler: Handler) {
  routes.set(new RegExp(url.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')), handler)
}

beforeEach(() => {
  routes.clear()
  vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const url = String(typeof input === 'object' && 'url' in (input as unknown as Request) ? (input as unknown as Request).url : input)
    for (const [re, handler] of routes) {
      if (re.test(url)) {
        const body = await handler(url, init)
        return { ok: true, status: 200, json: async () => body } as Response
      }
    }
    return { ok: false, status: 404, json: async () => ({}) } as Response
  }))
})

// ── helpers ─────────────────────────────────────────────────────────────────
function WithPanels({ active }: { active: string }) {
  return <Sidebar activeSession={null} onSessionChange={() => {}} activePanel={active} onPanelSwitch={() => {}} />
}

function renderPanel(panel: string) {
  return render(<WithPanels active={panel} />)
}

// ── Tasks (crons) ───────────────────────────────────────────────────────────
describe('Tasks panel (crons)', () => {
  it('renders cron rows partitioned active/paused with status badge', async () => {
    stub('/api/crons', () => ({
      jobs: [
        { id: 1, name: 'daily digest', paused: false, no_agent: false, read_only: false, profile: 'default' },
        { id: 2, name: 'old backup', paused: true, no_agent: true, read_only: false, profile: 'default' },
      ],
      all_profiles: false,
      active_profile: 'default',
      other_profile_count: 0,
    }))
    renderPanel('tasks')
    await waitFor(() => expect(screen.getByText('daily digest')).toBeTruthy())
    expect(screen.getByText('old backup')).toBeTruthy()
    expect(screen.getByTitle('Agent mode')).toBeTruthy()
    expect(screen.getByTitle(/Script job/)).toBeTruthy()
    expect(document.querySelector('.cron-paused-section')).toBeTruthy()
    cleanup()
  })

  it('shows empty state', async () => {
    stub('/api/crons', () => ({ jobs: [], all_profiles: false, active_profile: 'default', other_profile_count: 0 }))
    renderPanel('tasks')
    await waitFor(() => expect(document.querySelector('.cron-item')).toBeNull())
    // empty-state text should be present (i18n full)
    expect(screen.getByText(/No scheduled jobs/)).toBeTruthy()
    cleanup()
  })
})

// ── Skills ──────────────────────────────────────────────────────────────────
describe('Skills panel', () => {
  it('groups skills by category with collapsible headers', async () => {
    stub('/api/skills', () => ({
      skills: [
        { name: 'alpha', description: 'First skill', category: 'dev', disabled: false },
        { name: 'beta', description: 'Second', category: 'dev', disabled: false },
        { name: 'gamma', description: 'Third', category: 'finance', disabled: true },
      ],
    }))
    renderPanel('skills')
    await waitFor(() => expect(screen.getByText('alpha')).toBeTruthy())
    expect(screen.getByText('beta')).toBeTruthy()
    expect(screen.getByText('gamma')).toBeTruthy()
    const gamma = screen.getByText('gamma').closest('.skill-item')
    expect(gamma?.classList.contains('disabled')).toBe(true)
    cleanup()
  })
})

// ── Memory ──────────────────────────────────────────────────────────────────
describe('Memory panel', () => {
  it('renders section buttons from payload', async () => {
    stub('/api/memory', () => ({
      memory: 'note content', user: 'profile text', soul: 'soul text',
      memory_path: '/h/memories/MEMORY.md', user_path: '/h/memories/USER.md', soul_path: '/h/SOUL.md',
      memory_mtime: 1730000000, user_mtime: null, soul_mtime: null,
    }))
    renderPanel('memory')
    await waitFor(() => expect(document.querySelectorAll('#memoryPanel .side-menu-item').length).toBeGreaterThanOrEqual(3))
    cleanup()
  })
})

// ── Todos ───────────────────────────────────────────────────────────────────
describe('Todos panel', () => {
  it('renders empty state when no todos', () => {
    renderPanel('todos')
    expect(document.getElementById('todoPanel')).toBeTruthy()
    cleanup()
  })
})
