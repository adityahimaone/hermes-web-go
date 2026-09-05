// WorkspacePanel — Phase D: tree (dirs first parity) + breadcrumb + preview.

import { useEffect, useState } from 'react'
import { t } from '../../i18n'
import type { SessionMeta } from '../../state/types'
import type { WorkspaceEntry } from '../../hooks/useWorkspace'

type FileTab = 'files' | 'artifacts' | 'todos'

function BreadcrumbBar({
  path,
  onNavigate,
}: {
  path: string
  onNavigate: (path: string) => void
}) {
  if (!path || path === '.') return null
  const parts = path.split('/').filter(Boolean)
  const crumbs: Array<{ label: string; path: string }> = [{ label: t('workspace_parent_directory'), path: '.' }]
  let cur = ''
  for (const p of parts) {
    cur = cur ? `${cur}/${p}` : p
    crumbs.push({ label: p, path: cur })
  }
  return (
    <div className="breadcrumb-bar" style={{ display: 'flex', gap: 6, padding: '6px 12px', flexWrap: 'wrap' }}>
      {crumbs.map((c, idx) => (
        <span key={c.path}>
          <button
            type="button"
            className="breadcrumb-crumb"
            onClick={() => onNavigate(c.path)}
          >
            {c.label}
          </button>
          {idx < crumbs.length - 1 ? <span> / </span> : null}
        </span>
      ))}
    </div>
  )
}

function TreeRow({
  entry,
  currentDir,
  onOpenDir,
  onOpenFile,
}: {
  entry: WorkspaceEntry
  currentDir: string
  onOpenDir: (path: string) => void
  onOpenFile: (path: string) => void
}) {
  const isDir = entry.type === 'dir'
  const rel = entry.path
  return (
    <div
      className="tree-row"
      role={isDir ? 'button' : undefined}
      tabIndex={isDir || entry.type === 'file' ? 0 : undefined}
      onClick={() => {
        if (isDir) onOpenDir(rel)
        else if (entry.type === 'file') onOpenFile(rel)
      }}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          if (isDir) onOpenDir(rel)
          else if (entry.type === 'file') onOpenFile(rel)
        }
      }}
    >
      <span className={isDir ? 'tree-dir' : 'tree-file'}>{isDir ? '▸ ' : '· '}</span>
      <span className="tree-name">{entry.name}</span>
    </div>
  )
}

export function WorkspacePanel({
  session,
  state,
  onNavigate,
  onOpenFile,
  onClosePreview,
  onRawUrl,
  onNavigateUp,
  activeTab,
  setActiveTab,
}: {
  session: SessionMeta | null
  state: {
    currentDir: string
    entries: WorkspaceEntry[]
    loading: boolean
    error: string | null
    workspaceRoot: string | null
    preview: { path: string; payload: { content: string; size: number; lines: number } | null; loading: boolean } | null
  }
  onNavigate: (path: string) => void
  onOpenFile: (path: string) => void
  onClosePreview: () => void
  onRawUrl: (path: string) => string | null
  onNavigateUp: () => void
  activeTab: FileTab
  setActiveTab: (tab: FileTab) => void
}) {
  if (!session) {
    return (
      <div className="workspace-empty" style={{ padding: 16, color: 'var(--muted)', fontSize: 12 }}>
        {t('no_active_session')}
      </div>
    )
  }

  return (
    <>
      <div className="workspace-panel-tabs" role="tablist" aria-label={t('workspace_panel_views')}>
        {(['files', 'artifacts', 'todos'] as const).map((tab) => (
          <button
            key={tab}
            className={`workspace-panel-tab${activeTab === tab ? ' active' : ''}`}
            type="button"
            role="tab"
            aria-selected={activeTab === tab}
            onClick={() => setActiveTab(tab)}
          >
            {tab === 'files'
              ? t('workspace_files_tab')
              : tab === 'artifacts'
                ? t('workspace_artifacts_tab')
                : t('tab_todos')}
          </button>
        ))}
      </div>
      {activeTab === 'files' ? (
        <>
          <BreadcrumbBar path={state.currentDir} onNavigate={onNavigate} />
          <div className="file-tree" id="fileTree" role="tree">
            {state.loading ? (
              <div className="workspace-empty">{t('loading')}</div>
            ) : state.error ? (
              <div className="workspace-empty">{state.error}</div>
            ) : state.entries.length === 0 ? (
              <div className="workspace-empty">
                <div>{t('workspace_empty_dir')}</div>
                {state.currentDir !== '.' ? (
                  <button type="button" className="workspace-up" onClick={onNavigateUp}>
                    ← {t('workspace_parent_directory')}
                  </button>
                ) : null}
              </div>
            ) : (
              state.entries.map((e) => (
                <TreeRow
                  key={e.path}
                  entry={e}
                  currentDir={state.currentDir}
                  onOpenDir={onNavigate}
                  onOpenFile={onOpenFile}
                />
              ))
            )}
          </div>
          <div className="preview-area visible" id="previewArea" style={state.preview ? undefined : { display: 'none' }}>
            {state.preview ? (
              <>
                <div className="preview-path" id="previewPath">
                  <span>{state.preview.path}</span>
                  <button
                    type="button"
                    className="panel-icon-btn"
                    aria-label={t('copy_relative_path')}
                    onClick={() => {
                      void navigator.clipboard?.writeText(state.preview!.path)
                    }}
                  >
                    {t('copy_relative_path')}
                  </button>
                  {onRawUrl(state.preview.path) ? (
                    <a
                      href={onRawUrl(state.preview.path)!}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="panel-icon-btn"
                      download
                    >
                      Download
                    </a>
                  ) : null}
                  <button
                    type="button"
                    className="panel-icon-btn close-preview"
                    aria-label={t('workspace_close_preview')}
                    onClick={onClosePreview}
                  >
                    ✕
                  </button>
                </div>
                <div className="preview-content">
                  {state.preview.loading ? (
                    <div className="workspace-empty">{t('loading')}</div>
                  ) : state.preview.payload ? (
                    <pre className="preview-pre" style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
                      {state.preview.payload.content}
                    </pre>
                  ) : null}
                </div>
              </>
            ) : null}
          </div>
        </>
      ) : activeTab === 'artifacts' ? (
        <div className="workspace-artifacts" id="workspaceArtifacts">
          <span className="workspace-artifacts-count">0 artifacts</span>
        </div>
      ) : (
        <div className="workspace-todos" id="workspaceTodosPanel">
          <span className="workspace-empty">No todos yet.</span>
        </div>
      )}
    </>
  )
}
