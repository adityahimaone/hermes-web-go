// SkillsPanel — Phase E1, panels.js port (#paneSkills). Group by category,
// collapsible sections, disable toggle on the little pill (stopPropagation),
// search filter. Class names verbatim vanilla for chrome.css parity.

import { useCallback, useEffect, useMemo, useState } from 'react'
import { api } from '../../hooks/useChatStream'
import { t } from '../../i18n'

export interface SkillItem {
  name: string
  description: string
  category: string
  disabled?: boolean
}

export function SkillsPanel() {
  const [skills, setSkills] = useState<SkillItem[] | null>(null)
  const [q, setQ] = useState('')
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const res = await api('/api/skills')
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      const data = (await res.json()) as { skills?: SkillItem[] }
      setSkills(data.skills ?? [])
      setError(null)
    } catch (e) {
      setError(String(e))
    }
  }, [])

  useEffect(() => { void load() }, [load])

  const filtered = useMemo(() => {
    if (!skills) return null
    const needle = q.toLowerCase().trim()
    if (!needle) return skills
    return skills.filter(
      (s) =>
        (s.name ?? '').toLowerCase().includes(needle) ||
        (s.description ?? '').toLowerCase().includes(needle) ||
        (s.category ?? '').toLowerCase().includes(needle),
    )
  }, [skills, q])

  const cats = useMemo(() => {
    if (!filtered) return null
    const out = new Map<string, SkillItem[]>()
    for (const s of filtered) {
      const cat = s.category || '(general)'
      const cur = out.get(cat)
      if (cur) cur.push(s)
      else out.set(cat, [s])
    }
    return out
  }, [filtered])

  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())

  const toggleCat = (cat: string) =>
    setCollapsed((prev) => {
      const next = new Set(prev)
      if (next.has(cat)) next.delete(cat)
      else next.add(cat)
      return next
    })

  const toggleSkill = async (name: string, currentlyEnabled: boolean) => {
    try {
      const res = await api('/api/skills/toggle', {
        method: 'POST',
        body: JSON.stringify({ name, enabled: !currentlyEnabled }),
      })
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      // optimistic patch
      setSkills((prev) => (prev ? prev.map((s) => (s.name === name ? { ...s, disabled: !currentlyEnabled ? true : false } : s)) : prev))
    } catch {
      /* panel already shows status via t(), leave toast to future phase */
    }
  }

  if (error) return <div style={{ padding: 12, color: 'var(--accent)', fontSize: 12 }}>{t('error_prefix')}{error}</div>

  return (
    <div id="skillsList" className="skills-list">
      <div className="skills-search sidebar-search">
        <svg className="sidebar-search-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><circle cx="11" cy="11" r="8" /><path d="M21 21l-4.35-4.35" /></svg>
        <input
          id="skillsSearch"
          type="search"
          placeholder={t('search_skills') || 'Search skills...'}
          data-i18n-placeholder="search_skills"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
      </div>
      {filtered === null ? (
        <div style={{ padding: 12, color: 'var(--muted)', fontSize: 12 }}>{t('loading')}</div>
      ) : filtered.length === 0 ? (
        <div style={{ padding: 12, color: 'var(--muted)', fontSize: 12 }}>{t('skills_no_match')}</div>
      ) : (
        [...(cats ?? new Map<string, SkillItem[]>())].sort(([a], [b]) => a.localeCompare(b)).map(([cat, items]) => {
          const isCollapsed = collapsed.has(cat)
          items.sort((a: SkillItem, b: SkillItem) => a.name.localeCompare(b.name))
          return (
            <div key={cat} className={`skills-category${isCollapsed ? ' collapsed' : ''}`}>
              <div className="skills-cat-header" data-cat={cat} onClick={() => toggleCat(cat)}>
                <span className="cat-chevron" style={{ display: 'inline-flex', transition: 'transform .15s', transform: isCollapsed ? '' : 'rotate(90deg)' }}>▸</span>{' '}
                {cat} <span style={{ opacity: 0.5 }}>({items.length})</span>
              </div>
              {items.map((skill: SkillItem) => (
                <div
                  key={skill.name}
                  className={`skill-item${skill.disabled ? ' disabled' : ''}`}
                  style={{ display: isCollapsed ? 'none' : undefined }}
                >
                  <span
                    className={`skill-toggle${skill.disabled ? '' : ' enabled'}`}
                    title={skill.disabled ? t('skill_disabled') : t('skill_enabled')}
                    onClick={(ev) => {
                      ev.stopPropagation()
                      toggleSkill(skill.name, !skill.disabled)
                    }}
                  />
                  <span className="skill-name">{skill.name}</span>
                  <span className="skill-desc">{skill.description ?? ''}</span>
                </div>
              ))}
            </div>
          )
        })
      )}
    </div>
  )
}
