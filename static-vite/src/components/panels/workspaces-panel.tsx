// WorkspacesSidebarPanel — Phase E2, panels.js port. /api/workspaces
// (data.go:627 — { workspaces:[{path,name}], last }). Rows with active
// indicator + rename/remove. List detail (file tree) already lives in
// right-panel's WorkspacePanel (dataRoot/sessions); this panel is the
// workspace registry panel-view in the left sidebar.

import { useCallback, useEffect, useState } from 'react'
import { api } from '../../hooks/useChatStream'
import { t } from '../../i18n'

export interface WorkspaceRow {
  path: string
  name: string
  [k: string]: unknown
}

interface WorkspacesPayload {
  workspaces?: WorkspaceRow[]
  last?: string | null
  terminal_remote_backend?: boolean
}

export function WorkspacesSidebarPanel() {
  const [data, setData] = useState<WorkspacesPayload | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const res = await api('/api/workspaces')
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      setData((await res.json()) as WorkspacesPayload)
      setError(null)
    } catch (e) {
      setError(String(e))
    }
  }, [])

  useEffect(() => { void load() }, [load])

  if (error) return <div style={{ flex: 1, overflowY: 'auto', padding: 8 }}><div style={{ padding: 12, color: 'var(--accent)', fontSize: 12 }}>{t('error_prefix')}{error}</div></div>

  return (
    <div style={{ flex: 1, overflowY: 'auto', padding: 8 }} id="workspacesPanel">
      <div className="panel-head-sub" style={{ padding: '0 0 8px', color: 'var(--muted)', fontSize: 12 }}>{t('workspace_desc') || 'Add and switch workspaces for your sessions.'}</div>
      {data === null ? (
        <div style={{ color: 'var(--muted)', fontSize: 12 }}>{t('loading')}</div>
      ) : (data.workspaces ?? []).length === 0 ? (
        <div style={{ color: 'var(--muted)', fontSize: 12 }}>{t('workspaces_empty') || 'No workspaces'}</div>
      ) : (
        (data.workspaces ?? []).map((ws) => (
          <div
            key={ws.path}
            className={`workspace-item side-menu-item${data.last === ws.path ? ' active' : ''}`}
            data-workspace={ws.path}
            style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '6px 8px', borderRadius: 6 }}
          >
            <span className="workspace-name" title={ws.path}>{ws.name}</span>
            <span className="workspace-path" style={{ opacity: 0.5, fontSize: 11, overflow: 'hidden', textOverflow: 'ellipsis' }} title={ws.path}>{ws.path}</span>
          </div>
        ))
      )}
    </div>
  )
}
