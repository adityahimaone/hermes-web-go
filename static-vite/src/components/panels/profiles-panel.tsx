// ProfilesPanel — Phase E2, panels.js port. /api/profiles (config_router.go
// listProfileRows) + /api/profile/active. Rows with active badge; switching
// POSTs /api/profile/switch (proxy-only) — surfaced via error card when the
// legacy sidecar is down.

import { useCallback, useEffect, useState } from 'react'
import { api } from '../../hooks/useChatStream'
import { t } from '../../i18n'

export interface ProfileRow {
  name: string
  path?: string
  is_active?: boolean
  [k: string]: unknown
}

interface ProfilesPayload {
  profiles?: ProfileRow[]
  active?: string
}

export function ProfilesPanel() {
  const [data, setData] = useState<ProfilesPayload | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const [listRes, activeRes] = await Promise.all([
        api('/api/profiles'),
        api('/api/profile/active').catch(() => null),
      ])
      if (!listRes.ok) throw new Error(`HTTP ${listRes.status}`)
      const payload = (await listRes.json()) as ProfilesPayload
      if (activeRes?.ok) {
        const active = (await activeRes.json()) as { name?: string }
        payload.active = active.name ?? payload.active
        if (payload.profiles) {
          payload.profiles = payload.profiles.map((p) => ({ ...p, is_active: p.name === payload.active }))
        }
      }
      setData(payload)
      setError(null)
    } catch (e) {
      setError(String(e))
    }
  }, [])

  useEffect(() => { void load() }, [load])

  if (error) return <div style={{ flex: 1, overflowY: 'auto', padding: 8 }}><div style={{ padding: 12, color: 'var(--accent)', fontSize: 12 }}>{t('error_prefix')}{error}</div></div>

  return (
    <div style={{ flex: 1, overflowY: 'auto', padding: 8 }} id="profilesPanel">
      {data === null ? (
        <div style={{ padding: 12, color: 'var(--muted)', fontSize: 12 }}>{t('loading')}</div>
      ) : (data.profiles ?? []).length === 0 ? (
        <div style={{ padding: 12, color: 'var(--muted)', fontSize: 12 }}>{t('profiles_empty') || 'No profiles'}</div>
      ) : (
        (data.profiles ?? []).map((p) => (
          <button
            key={p.name}
            type="button"
            className={`side-menu-item profile-item${p.is_active ? ' active' : ''}`}
            data-profile={p.name}
            style={{ width: '100%', textAlign: 'left', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}
          >
            <span>{p.name}</span>
            {p.is_active ? <span className="profile-active-badge">{t('profile_active') || 'active'}</span> : null}
          </button>
        ))
      )}
    </div>
  )
}
