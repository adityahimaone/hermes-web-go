// Slash-command dropdown — commands.js port (autocomplete slice). Renders
// inside composer-box at vanilla DOM position (.cmd-dropdown). Keyboard nav
// mirrors navigateCmdDropdown: wrap-around, scrollIntoView. Accept replaces
// the typed token with the command name.

import { useEffect, useRef } from 'react'
import { t } from '../../i18n'
import { getSlashLabel, type SlashCommand } from '../../hooks/useSlashCommands'

export function CmdDropdown({
  matches,
  selectedIdx,
  onSelect,
}: {
  matches: SlashCommand[]
  selectedIdx: number
  onSelect: (name: string) => void
}) {
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const el = ref.current?.children[selectedIdx] as HTMLElement | undefined
    if (el && typeof (el as HTMLElement).scrollIntoView === 'function') {
      el.scrollIntoView({ block: 'nearest' })
    }
  }, [selectedIdx])

  if (!matches.length) return null

  return (
    <div className="cmd-dropdown open" id="cmdDropdown" ref={ref}>
      {matches.map((c, i) => {
        const isSkill = false // vanilla badge reserved for /api/skills-sourced commands
        return (
          <div
            key={c.name}
            className={`cmd-item${i === selectedIdx ? ' selected' : ''}${isSkill ? ' cmd-item-skill' : ''}`}
            data-idx={i}
            onMouseDown={(e) => {
              e.preventDefault()
              onSelect(c.name)
            }}
          >
            <div className="cmd-item-name">
              /{c.name}
              {c.arg ? <span className="cmd-item-arg"> {c.arg}</span> : null}
            </div>
            <div className="cmd-item-desc">
              {c.desc ||
                t((`cmd_${c.name}` as Parameters<typeof t>[0]))}
            </div>
            <span className="sr-only">{getSlashLabel(c)}</span>
          </div>
        )
      })}
    </div>
  )
}
