// useSessions — Phase D: session list, CRUD, selection, search.
// Field names verbatim from the Go contract (internal/httpserver/data.go).
// List/snapshot application is rev-guarded (doc 15 §5).

import { useCallback, useEffect, useRef, useState } from 'react'
import { api } from './useChatStream'
import { useRevGuard } from './useRevGuard'
import type { SessionMeta } from '../state/types'

export interface SessionsListPayload {
  sessions: SessionMeta[]
  sidebar_reference_sessions?: unknown[]
  other_profile_count?: number
  archived_webui_count?: number
  archived_cli_count?: number
  webui_session_count?: number
  cli_session_count?: number
  server_time?: number
  server_tz?: string
  active_profile?: string
}

export interface SearchHit extends SessionMeta {
  match_type?: 'title' | 'content'
  match_preview?: string
}

/** Sessions payload fields the Go backend actually serves (data.go:428). */
export interface SessionsState {
  sessions: SessionMeta[]
  loading: boolean
  error: string | null
  searchQuery: string
  searchHits: SearchHit[] | null
  counts: {
    webui: number
    otherProfiles: number
    archivedWebui: number
    archivedCli: number
  }
  serverTimeDeltaMs: number
}

const initialState: SessionsState = {
  sessions: [],
  loading: true,
  error: null,
  searchQuery: '',
  searchHits: null,
  counts: { webui: 0, otherProfiles: 0, archivedWebui: 0, archivedCli: 0 },
  serverTimeDeltaMs: 0,
}

export function useSessions(options: {
  onActiveSession?: (session: SessionMeta | null) => void
  activeSessionId?: string | null
}) {
  const { onActiveSession, activeSessionId } = options
  const [state, setState] = useState<SessionsState>(initialState)
  const revGuard = useRevGuard()
  const genRef = useRef(0)
  void activeSessionId

  const applyList = useCallback(
    (payload: SessionsListPayload, gen: number) => {
      // Rev guard: list snapshot keyed per generation — stale gen dropped.
      if (!revGuard.accept('sessions-list', gen)) return
      setState((prev) => ({
        ...prev,
        sessions: payload.sessions ?? [],
        loading: false,
        error: null,
        counts: {
          webui: Number(payload.webui_session_count ?? payload.sessions?.length ?? 0),
          otherProfiles: Number(payload.other_profile_count ?? 0),
          archivedWebui: Number(payload.archived_webui_count ?? 0),
          archivedCli: Number(payload.archived_cli_count ?? 0),
        },
        serverTimeDeltaMs:
          typeof payload.server_time === 'number' && payload.server_time > 0
            ? Date.now() - payload.server_time * 1000
            : prev.serverTimeDeltaMs,
      }))
    },
    [revGuard],
  )

  const refresh = useCallback(async () => {
    const gen = ++genRef.current
    try {
      const res = await api('/api/sessions')
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      if (gen !== genRef.current) return // stale response, drop (rev guard)
      const payload = (await res.json()) as SessionsListPayload
      if (gen !== genRef.current) return
      applyList(payload, gen)
    } catch (e) {
      if (gen !== genRef.current) return
      setState((prev) => ({ ...prev, loading: false, error: String(e) }))
    }
  }, [applyList, revGuard])

  useEffect(() => {
    void refresh()
  }, [refresh])

  // ── Search (GET /api/sessions/search — Go serves title + content) ──
  const search = useCallback(async (q: string) => {
    const trimmed = q.trim()
    setState((prev) => ({ ...prev, searchQuery: trimmed }))
    if (!trimmed) {
      setState((prev) => ({ ...prev, searchHits: null }))
      return
    }
    try {
      const res = await api(`/api/sessions/search?q=${encodeURIComponent(trimmed)}&content=1&depth=5`)
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      const data = (await res.json()) as { sessions: SearchHit[] }
      setState((prev) =>
        prev.searchQuery === trimmed ? { ...prev, searchHits: data.sessions ?? [] } : prev,
      )
    } catch {
      setState((prev) => (prev.searchQuery === trimmed ? { ...prev, searchHits: [] } : prev))
    }
  }, [])

  // ── CRUD (POST endpoints, field names verbatim Go) ──
  const mutate = useCallback(
    async (path: string, body: Record<string, unknown>, successMessage?: string) => {
      const res = await api(path, { method: 'POST', body: JSON.stringify(body) })
      if (!res.ok) throw new Error(`${path}: HTTP ${res.status}`)
      if (successMessage !== undefined) {
        // Sessions whose membership changed must re-list.
        void refresh()
      }
      return res
    },
    [refresh],
  )

  const newSession = useCallback(
    async (opts?: { profile?: string; workspace?: string | null }) => {
      const body: Record<string, unknown> = {
        profile: opts?.profile ?? 'default',
      }
      if (opts?.workspace !== undefined) body.workspace = opts.workspace
      const res = await api('/api/session/new', { method: 'POST', body: JSON.stringify(body) })
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      const data = (await res.json()) as { session?: SessionMeta; session_id?: string }
      void refresh()
      return data
    },
    [refresh],
  )

  const loadSession = useCallback(
    async (sid: string) => {
      const res = await api(
        `/api/session?session_id=${encodeURIComponent(sid)}&messages=1&resolve_model=0`,
      )
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      const data = (await res.json()) as { session: SessionMeta }
      onActiveSession?.(data.session)
      return data.session
    },
    [onActiveSession],
  )

  const pin = useCallback(
    (sid: string, pinned: boolean) =>
      mutate('/api/session/pin', { session_id: sid, pinned }, 'ok').then(() => undefined),
    [mutate],
  )
  const rename = useCallback(
    (sid: string, title: string) =>
      mutate('/api/session/rename', { session_id: sid, title }, 'ok').then(() => undefined),
    [mutate],
  )
  const archive = useCallback(
    (sid: string, archived = true) =>
      mutate('/api/session/archive', { session_id: sid, archived }, 'ok').then(() => undefined),
    [mutate],
  )
  const remove = useCallback(
    (sid: string) =>
      mutate('/api/session/delete', { session_id: sid }, 'ok').then(() => undefined),
    [mutate],
  )

  return {
    state,
    refresh,
    search,
    newSession,
    loadSession,
    pin,
    rename,
    archive,
    remove,
  }
}
