import strings from './strings.json'
import type { I18nKey, I18nLocale } from './keys.d.ts'

let current: Record<string, string> =
  (strings as Record<string, Record<string, string>>)['en'] ?? {}

export type { I18nKey, I18nLocale }

/**
 * t(key, …args) — mirrors static/i18n.js semantics verbatim:
 *   - fallback to English if key missing in current locale
 *   - unknown key → return the key itself (loud in dev)
 *   - numbered placeholders {0}, {1}, … replaced positionally from args
 *   - function-valued entries (rare in vanilla) — call with args
 */
export function t(key: I18nKey | string, ...args: unknown[]): string {
  const raw: unknown =
    (current as Record<string, unknown>)[key as string] ??
    ((strings as Record<string, Record<string, unknown>>)['en']?.[key as string])

  if (raw === undefined) return key as string

  // Function-valued entries were stringified by convert-i18n.mjs as
  // "(n) => `1 of ${n} pending`". Revive by evaluating the arrow.
  if (typeof raw === 'string' && /^\s*\(.*\)\s*=>/.test(raw)) {
    try {
      // eslint-disable-next-line @typescript-eslint/no-implied-eval
      const fn = Function(`return (${raw})`)() as (...a: unknown[]) => unknown
      return String(fn(...args))
    } catch {
      // fall through to plain string handling
    }
  }

  const str: string =
    typeof raw === 'function' ? String((raw as (...a: unknown[]) => unknown)(...args)) : String(raw)

  if (args.length === 0 || !str.includes('{')) return str

  // In dev, verify args count: placeholder spans that don't resolve are left as {N}.
  // Vite inlines import.meta.env.DEV — tree-shakes the else branch in prod.
  if (import.meta.env?.DEV) {
    const missing = [...str.matchAll(/\{(\d+)\}/g)].filter((m) => !(m[1] in args))
    if (missing.length) {
      console.warn(`[i18n] t("${key}") — unresolved placeholders: ${missing.map((m) => m[0]).join(', ')}`)
    }
  }

  return str.replace(/\{(\d+)\}/g, (match, idx) =>
    idx in args ? String((args as unknown[])[Number(idx)]) : match,
  )
}

export function setLocale(lang: string): void {
  const all = strings as Record<string, Record<string, string>>
  const resolved: string = lang in all ? lang : 'en'
  current = all[resolved]
  try {
    localStorage.setItem('hermes-lang', resolved)
  } catch {}
  // Safe to access document only when not in SSR/test environments.
  try {
    document.documentElement.lang = (current as Record<string, string>)._speech ?? resolved
  } catch {}
}

export function loadLocale(): void {
  let lang = 'en'
  try {
    lang = localStorage.getItem('hermes-lang') ?? 'en'
  } catch {}
  setLocale(lang)
}

// Expose current key set for tests that enumerate coverage.
export function allKeys(): string[] {
  return Object.keys((strings as Record<string, Record<string, string>>).en)
}
