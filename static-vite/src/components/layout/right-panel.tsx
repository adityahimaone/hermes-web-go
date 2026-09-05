// RightPanel — Phase D: vanilla aside.rightpanel (Workspace: files/artifacts/
// todos + preview). Lives directly under .layout, sibling of the sidebar —
// NOT a sidebar panel-view. Collapsed state mirrors vanilla dataset toggle.

import { useState } from 'react'
import { t } from '../../i18n'
import { WorkspacePanel } from '../workspace/workspace-panel'
import { useWorkspace } from '../../hooks/useWorkspace'
import type { SessionMeta } from '../../state/types'

export function RightPanel({ session }: { session: SessionMeta | null }) {
  const workspace = useWorkspace(session)
  const [tab, setTab] = useState<'files' | 'artifacts' | 'todos'>('files')

  return (
    <aside className="rightpanel" data-active-tab={tab}>
      <div className="resize-handle" id="rightpanelResize" />
      <div className="panel-header">
        <div className="workspace-panel-title-group">
          <span id="workspacePanelHeading" className="workspace-panel-heading" title="Workspace">
            Workspace
          </span>
        </div>
        <div className="panel-header-actions">
          <button
            type="button"
            className="panel-icon-btn close-preview has-tooltip has-tooltip--bottom"
            id="btnClearPreview"
            data-tooltip={t('workspace_close_preview')}
            data-i18n-title="workspace_close_preview"
            aria-label={t('workspace_close_preview')}
            onClick={workspace.closePreview}
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" /></svg>
          </button>
        </div>
      </div>
      <WorkspacePanel
        session={session}
        state={workspace.state}
        onNavigate={(p) => void workspace.loadDir(p)}
        onOpenFile={(p) => void workspace.openFile(p)}
        onClosePreview={workspace.closePreview}
        onRawUrl={(p) => workspace.rawFileUrl(p, { download: true })}
        onNavigateUp={workspace.navigateUp}
        activeTab={tab}
        setActiveTab={setTab}
      />
    </aside>
  )
}
