import { useMemo, useState } from 'react'
import { t } from '../i18n'

export interface SlashCommand {
  name: string
  desc: string
  arg?: string
  noEcho?: boolean
}

// Subset of commands.js COMMANDS that have no backend sidecar dep.
// Full list is 28; this covers the palette UX without pulling agent state.
// ponytail: expand when a section proves it needs live /api/models|skills polling.
const COMMANDS: SlashCommand[] = [
  { name: 'help', desc: 'Show help', noEcho: true },
  { name: 'clear', desc: 'Clear conversation', noEcho: true },
  { name: 'compress', desc: 'Compress context', arg: '[focus topic]', noEcho: true },
  { name: 'compact', desc: 'Compact conversation', noEcho: true },
  { name: 'model', desc: 'Switch model', arg: 'model_name', noEcho: true },
  { name: 'workspace', desc: 'Switch workspace', arg: 'name', noEcho: true },
  { name: 'terminal', desc: 'Open terminal', noEcho: true },
  { name: 'new', desc: 'New conversation', noEcho: true },
  { name: 'usage', desc: 'Show token usage', noEcho: true },
  { name: 'theme', desc: 'Switch theme', arg: 'name', noEcho: true },
  { name: 'skills', desc: 'List skills', arg: 'query' },
  { name: 'use', desc: 'Use a skill', arg: 'skill-name', noEcho: true },
  { name: 'stop', desc: 'Stop current turn', noEcho: true },
  { name: 'branch', desc: 'Branch conversation', arg: '[name]', noEcho: true },
]

function filterCommands(prefix: string): SlashCommand[] {
  const q = prefix.toLowerCase().replace(/^\//, '')
  if (!q) return COMMANDS.slice(0, 8)
  return COMMANDS.filter((c) => c.name.startsWith(q))
}

export function useSlashCommands(input: string) {
  const [selectedIdx, setSelectedIdx] = useState(0)

  const slashOffset = useMemo(() => {
    if (!input.startsWith('/')) return -1
    if (input.includes('\n')) return -1
    // Only trigger on first token — like vanilla _activeSlashCommandOffset
    const sp = input.indexOf(' ')
    if (sp > 0) return -1
    return 0
  }, [input])

  const matches = useMemo(() => {
    if (slashOffset < 0) return []
    const token = input.slice(slashOffset).split(/\s/)[0] ?? ''
    return filterCommands(token)
  }, [input, slashOffset])

  const visible = slashOffset >= 0 && matches.length > 0

  const nav = (dir: number) => {
    if (!matches.length) return
    setSelectedIdx((prev) => {
      let next = prev + dir
      if (next < 0) next = matches.length - 1
      if (next >= matches.length) next = 0
      return next
    })
  }

  const reset = () => setSelectedIdx(0)

  return { matches, visible, selectedIdx, nav, reset, slashOffset }
}

export function getSlashLabel(c: SlashCommand): string {
  // Mirrors vanilla cmd-item-name rendering; keep unit-testable pure
  return `/${c.name}${c.arg ? ` ${c.arg}` : ''}`
}

// exposed for tests
export const _COMMANDS = COMMANDS
