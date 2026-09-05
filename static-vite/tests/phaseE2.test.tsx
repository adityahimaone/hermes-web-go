// Phase E2 gates — panels.js port slice 2: Kanban, Insights, Logs,
// Profiles, Workspaces. Contract vs Go payloads; kanban is proxy-only in
// Go (registry.go has no /api/kanban) → error card, not infinite spinner.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, cleanup } from '@testing-library/react'
import * as React from 'react'
import { Sidebar } from '../src/components/layout/sidebar'

type Handler = (url: string, init?: RequestInit) => unknown | Promise<unknown>
const routes = new Map<RegExp, Handler | 'fail502'>()

function stub(url: string, handler: Handler | 'fail502') {
  routes.set(new RegExp(url.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')), handler)
}

beforeEach(() => {
  routes.clear()
  vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const url = String(typeof input === 'object' && 'url' in (input as unknown as Request) ? (input as unknown as Request).url : input)
    for (const [re, handler] of routes) {
      if (re.test(url)) {
        if (handler === 'fail502') return { ok: false, status: 502, json: async () => ({ Type: 'ProxyError', Error: 'legacy not connected' }) } as Response
        const body = await handler(url, init)
        return { ok: true, status: 200, json: async () => body } as Response
      }
    }
    return { ok: false, status: 404, json: async () => ({}) } as Response
  }))
})

function WithPanels({ active }: { active: string }) {
  return <Sidebar activeSession={null} onSessionChange={() => {}} activePanel={active} onPanelSwitch={() => {}} />
}

function renderPanel(panel: string) {
  return render(<WithPanels active={panel} />)
}

// ── Kanban ──────────────────────────────────────────────────────────────────
describe('Kanban panel', () => {
  it('renders unavailable error card on 502 (proxy-only backend)', async () => {
    stub('/api/kanban/board', 'fail502')
    renderPanel('kanban')
    await waitFor(() => {
      const el = document.querySelector('#kanbanList')
      expect(el?.textContent).not.toMatch(/Loading\.\.\./)
    }, { timeout: 3000 })
    expect(document.querySelector('#kanbanList .kanban-unavailable')).toBeTruthy()
    cleanup()
  })

  it('renders task cards from board payload', async () => {
    stub('/api/kanban/board', () => ({
      ok: true,
      board: 'main',
      tasks: [
        { id: 'T1', title: 'Ship it', status: 'ready', priority: 1, assignee: 'adit' },
        { id: 'T2', title: 'Blocked thing', status: 'blocked', priority: 2, assignee: 'adit' },
      ],
    }))
    stub('/api/kanban/stats', () => ({ total: 2, ready: 1, blocked: 1, done: 0 }))
    renderPanel('kanban')
    await waitFor(() => expect(screen.getByText('Ship it')).toBeTruthy())
    expect(screen.getByText('Blocked thing')).toBeTruthy()
    cleanup()
  })
})

// ── Insights ────────────────────────────────────────────────────────────────
describe('Insights panel', () => {
  it('renders totals + models table from Go payload', async () => {
    stub('/api/insights', () => ({
      period_days: 30,
      total_sessions: 42,
      total_messages: 900,
      total_input_tokens: 1500000,
      total_output_tokens: 300000,
      total_cache_read_tokens: 1200000,
      total_cache_hit_percent: 44.4,
      total_tokens: 1800000,
      total_cost: 12.34,
      models: {
        'gpt-5': { sessions: 30, input_tokens: 1200000, output_tokens: 200000, cache_read_tokens: 900000, cost: 9.0, total_tokens: 1400000 },
        'codex': { sessions: 12, input_tokens: 300000, output_tokens: 100000, cache_read_tokens: 300000, cost: 3.34, total_tokens: 400000 },
      },
      daily_tokens: { '2026-09-01': { input_tokens: 50000, output_tokens: 10000, total_tokens: 60000, cost: 0.4 } },
      activity_by_day: [{ day: 'Mon', sessions: 5 }, { day: 'Tue', sessions: 8 }],
      activity_by_hour: [{ hour: 0, sessions: 0 }, { hour: 9, sessions: 7 }],
    }))
    renderPanel('insights')
    await waitFor(() => expect(screen.getByText(/42/)).toBeTruthy())
    expect(screen.getByText('gpt-5')).toBeTruthy()
    expect(screen.getByText('codex')).toBeTruthy()
    cleanup()
  })
})

// ── Logs ────────────────────────────────────────────────────────────────────
describe('Logs panel', () => {
  it('renders log lines with severity classes from /api/logs', async () => {
    stub('/api/logs', () => ({
      file: 'agent', tail: 200, truncated: false, total_bytes: 10, mtime: 0, hint: '',
      lines: ['2026-09-05 INFO hello', '2026-09-05 WARNING careful', '2026-09-05 ERROR boom'],
    }))
    renderPanel('logs')
    await waitFor(() => expect(document.querySelector('#logsContent .log-line-error')).toBeTruthy())
    expect(document.querySelector('#logsContent .log-line-warning')).toBeTruthy()
    expect(document.querySelector('#logsContent .log-line-info')).toBeTruthy()
    cleanup()
  })
})

// ── Profiles ────────────────────────────────────────────────────────────────
describe('Profiles panel', () => {
  it('renders profile rows with active badge', async () => {
    stub('/api/profiles', () => ({
      profiles: [
        { name: 'default', path: '/h', is_active: true },
        { name: 'karina', path: '/h/profiles/karina', is_active: false },
      ],
      active: 'default',
    }))
    stub('/api/profile/active', () => ({ name: 'default', path: '/h', is_default: true }))
    renderPanel('profiles')
    await waitFor(() => expect(screen.getByText('default')).toBeTruthy())
    expect(screen.getByText('karina')).toBeTruthy()
    cleanup()
  })
})

// ── Workspaces panel (sidebar list — tree already D) ───────────────────────
describe('Workspaces panel', () => {
  it('renders workspace rows from /api/workspaces', async () => {
    stub('/api/workspaces', () => ({
      workspaces: [
        { path: '/Users/adit/proj', name: 'proj' },
        { path: '/Users/adit/dev', name: 'dev' },
      ],
      last: '/Users/adit/proj',
      terminal_remote_backend: false,
    }))
    renderPanel('workspaces')
    await waitFor(() => expect(screen.getByText('proj')).toBeTruthy())
    expect(screen.getByText('dev')).toBeTruthy()
    cleanup()
  })
})
