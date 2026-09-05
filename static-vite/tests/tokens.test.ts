/**
 * A3 exit gate (TDD): tokens.css must stay byte-identical to an INDEPENDENT
 * re-extraction from static/style.css. Catches:
 *   - hand-edits to tokens.css (forbidden — GENERATED header says so)
 *   - extractor drift after style.css changes upstream (regenerate, never edit)
 *
 * The independent extractor here does NOT import scripts/extract-tokens.mjs —
 * it re-derives the token region from first principles (line scan), so a bug
 * in the extractor is caught instead of mirrored.
 */
import { readFileSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it, expect } from 'vitest'

const __dirname = dirname(fileURLToPath(import.meta.url))
const ROOT = resolve(__dirname, '..')
const REPO = resolve(ROOT, '..')

function read(p: string): string {
  return readFileSync(p, 'utf8')
}

/** Independent re-extraction: find the token-region boundary by scanning for
 *  the first chrome layout rule (same objective criterion as the real
 *  extractor, derived independently). */
function independentExtract(src: string): string {
  const markers = [
    '\n  .app-titlebar',
    '\n.app-titlebar',
    '\n  .layout',
    '\n.layout',
  ]
  let end = src.length
  for (const m of markers) {
    const i = src.indexOf(m)
    if (i !== -1 && i < end) end = i
  }
  return src.slice(0, end).replace(/\s+$/, '\n')
}

const bodyOf = (s: string) => s.replace(/^\/\* GENERATED[\s\S]*?\*\/\n\n/, '')

describe('tokens.css parity (A3 exit gate)', () => {
  const tokensCss = read(resolve(ROOT, 'src/styles/tokens.css'))
  const styleCss = read(resolve(REPO, 'static/style.css'))

  it('matches an independent re-extraction byte-for-byte', () => {
    const expected = independentExtract(styleCss)
    expect(bodyOf(tokensCss)).toBe(expected)
  })

  it('contains all 19 skins', () => {
    const skins = [
      'codex','terracotta','ares','mono','graphite','github','slate','poseidon',
      'sisyphus','charizard','sienna','catppuccin','hepburn','nous','geist-contrast',
      'neon','neon-soft','neon-paint','zeus','verdigris',
    ]
    for (const s of skins) {
      expect(tokensCss).toContain(`data-skin="${s}"`)
    }
  })

  it('contains light + dark roots and font-size modifiers', () => {
    expect(tokensCss).toMatch(/^  :root \{/m)
    expect(tokensCss).toContain(':root.dark {')
    expect(tokensCss).toContain(':root[data-font-size="small"]')
    expect(tokensCss).toContain(':root[data-font-size="large"]')
    expect(tokensCss).toContain(':root[data-font-size="xlarge"]')
  })

  it('keeps exact values for spot-checked tokens', () => {
    // Values copied verbatim — no rounding to Tailwind defaults.
    expect(tokensCss).toContain('--bg:#FEFCF7')
    expect(tokensCss).toContain('--accent:#B8860B')
    expect(tokensCss).toContain('--message-body-font-size:14px')
    expect(tokensCss).toContain('--radius-pill:999px')
    expect(tokensCss).toContain('--font-size-xs:11px')
  })
})
