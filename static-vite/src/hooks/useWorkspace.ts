// useWorkspace — Phase D: file tree + preview for the session workspace.
// Endpoints verbatim Go: GET /api/list, GET /api/file, GET /api/file/raw.
// No client-side path logic beyond vanilla (doc 16 D3): join/normalize only.

import { useCallback, useEffect, useRef, useState } from 'react'
import { api } from './useChatStream'
import type { SessionMeta } from '../state/types'

export interface WorkspaceEntry {
  name: string
  path: string
  type: 'dir' | 'file' | 'symlink'
  size: number | null
  mtime_ns: number | null
  birthtime_ns: number | null
  workspace_sort_rank: number
}

export interface ListPayload {
  entries: WorkspaceEntry[]
  signature: string
  path: string
  workspace: string
  workspace_recovered: boolean
}

export interface FilePayload {
  path: string
  content: string
  size: number
  lines: number
}

export interface WorkspaceState {
  currentDir: string
  entries: WorkspaceEntry[]
  loading: boolean
  error: string | null
  workspaceRoot: string | null
  preview: { path: string; payload: FilePayload | null; loading: boolean } | null
}

const initialState: WorkspaceState = {
  currentDir: '.',
  entries: [],
  loading: false,
  error: null,
  workspaceRoot: null,
  preview: null,
}

/** Vanilla-parity join (workspace.js _joinWorkspacePath): '.' stays root. */
export function joinWorkspacePath(dir: string, name: string): string {
  if (!dir || dir === '.') return name
  return `${dir.replace(/\/+$/, '')}/${name}`
}

export function useWorkspace(session: SessionMeta | null) {
  const [state, setState] = useState<WorkspaceState>(initialState)
  const genRef = useRef(0)
  const sessionId = session?.session_id ?? null

  const loadDir = useCallback(
    async (path: string) => {
      if (!sessionId) return
      const gen = ++genRef.current
      setState((prev) => ({ ...prev, currentDir: path || '.', loading: true, error: null }))
      try {
        const res = await api(
          `/api/list?session_id=${encodeURIComponent(sessionId)}&path=${encodeURIComponent(path || '.')}`,
        )
        if (gen !== genRef.current) return // stale (session switched / newer load)
        if (!res.ok) {
          const err = (await res.json().catch(() => ({}))) as { error?: string }
          setState((prev) => ({
            ...prev,
            loading: false,
            error: err.error ?? `HTTP ${res.status}`,
          }))
          return
        }
        const data = (await res.json()) as ListPayload
        if (gen !== genRef.current) return
        setState((prev) => ({
          ...prev,
          entries: data.entries ?? [],
          loading: false,
          workspaceRoot: data.workspace || prev.workspaceRoot,
          // workspace_recovered: server replaced a deleted workspace — adopt it
          currentDir: data.path || path || '.',
        }))
      } catch (e) {
        if (gen !== genRef.current) return
        setState((prev) => ({ ...prev, loading: false, error: String(e) }))
      }
    },
    [sessionId],
  )

  // Reload on session change; reset to root.
  useEffect(() => {
    setState((prev) => ({ ...prev, currentDir: '.', preview: null, entries: [], error: null }))
    if (sessionId) void loadDir('.')
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId])

  const openFile = useCallback(
    async (path: string) => {
      if (!sessionId) return
      setState((prev) => ({ ...prev, preview: { path, payload: null, loading: true } }))
      try {
        const res = await api(
          `/api/file?session_id=${encodeURIComponent(sessionId)}&path=${encodeURIComponent(path)}`,
        )
        if (!res.ok) throw new Error(`HTTP ${res.status}`)
        const payload = (await res.json()) as FilePayload
        setState((prev) => ({ ...prev, preview: { path, payload, loading: false } }))
      } catch (e) {
        setState((prev) => ({
          ...prev,
          preview: { path, payload: null, loading: false },
          error: String(e),
        }))
      }
    },
    [sessionId],
  )

  const closePreview = useCallback(() => {
    setState((prev) => ({ ...prev, preview: null }))
  }, [])

  /** Raw file URL for download/browser-open (Go serves dangerous MIME as attachment). */
  const rawFileUrl = useCallback(
    (path: string, opts?: { download?: boolean; inline?: boolean }) => {
      if (!sessionId) return null
      const p = new URLSearchParams({
        session_id: sessionId,
        path,
      })
      if (opts?.download) p.set('download', '1')
      if (opts?.inline) p.set('inline', '1')
      return `/api/file/raw?${p.toString()}`
    },
    [sessionId],
  )

  const navigateUp = useCallback(() => {
    if (!state.currentDir || state.currentDir === '.') return
    const parts = state.currentDir.split('/').filter(Boolean)
    parts.pop()
    void loadDir(parts.join('/') || '.')
  }, [state.currentDir, loadDir])

  return { state, loadDir, openFile, closePreview, rawFileUrl, navigateUp }
}
