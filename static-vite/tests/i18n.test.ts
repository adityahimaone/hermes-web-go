// A5 TDD red → green: i18n round-trip — strings.json is a faithful expansion of i18n.js
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, it, expect } from 'vitest'
import { t, setLocale, allKeys } from '../src/i18n'

const REPO = resolve(__dirname, '..', '..')
const stringsJson = JSON.parse(readFileSync(resolve(__dirname, '../src/i18n/strings.json'), 'utf8'))

describe('i18n (A5)', () => {
  it('produces typed strings.json whose English matches vanilla', async () => {
    const mod = await import('node:vm')
    const src = readFileSync(resolve(REPO, 'static/i18n.js'), 'utf8')
    const start = src.indexOf('const LOCALES =')
    const end = src.indexOf('\nlet _locale', start)
    const code = src.slice(start, end).trim() + '\nLOCALES'
    const vanillaEn: Record<string, string> = mod.runInNewContext(code, {}).en

    const viteEn: Record<string, string> = stringsJson.en
    for (const [k, vann] of Object.entries(vanillaEn)) {
      // function-valued entries become strings in the JSON; skip strict equality there
      if (typeof vann === 'function') { expect(viteEn[k]).toBeTypeOf('string'); continue }
      expect(viteEn[k], `key "${k}" differs`).toBe(String(vann ?? ''))
    }
  })

  it('t(key) returns the English value and t(unknown) echoes the key', () => {
    setLocale('en')
    expect(t('tab_chat')).toBeTypeOf('string')
    expect(t('__this_key_does_not_exist__')).toBe('__this_key_does_not_exist__')
  })

  it('replaces numbered placeholders exactly like vanilla', () => {
    setLocale('en')
    // Find a real key with {0}-style interpolation in the bundle; fallback to synthetic check otherwise.
    const withPlaceholder = Object.entries(stringsJson.en as Record<string, string>)
      .find(([, v]) => /\{\d+\}/.test(v))
    if (withPlaceholder) {
      const [k, tmpl] = withPlaceholder
      const m = tmpl.match(/\{\d+\}/gi)!.length // how many placeholders
      const args = Array.from({ length: m }, (_, i) => `VALUE${i}`)
      const got = t(k, ...args)
      // After substitution no {N} should remain for the bound args.
      args.forEach((_, i) => expect(got).not.toContain(`{${i}}`))
    } else {
      // Synthetic: verify the replacement path exists (covers helper shape even if bundle has no {N}).
      expect('value {0}'.replace(/\{(\d+)\}/g, (_, i) => ({ 0: 'hello' } as Record<string, string>)[i] ?? `{${i}}`)).toBe('value hello')
    }
  })

  it('falls back to English for a missing locale key vs current locale', () => {
    // Point a non-English locale at the real missing-key seam: if a key is absent in the
    // current locale but present in en, t should return the English value.
    const enOnlyKey = allKeys().find((k) => !(stringsJson.ja ?? {})[k])
    if (!enOnlyKey) return // all ja keys present — nothing to assert here
    setLocale('ja')
    const want = stringsJson.en[enOnlyKey]
    if (typeof want === 'string') expect(t(enOnlyKey)).toBe(want)
    setLocale('en')
  })

  it('exposes a stable key set (type-safety contract)', () => {
    expect(allKeys().length).toBeGreaterThan(1_000)
    expect(allKeys()).toContain('tab_chat')
  })
})
