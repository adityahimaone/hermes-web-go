#!/usr/bin/env node
/**
 * A4 gate: the Go shell renderer (internal/httpserver/shell_render.go)
 * string-substitutes __WEBUI_VERSION__, __MAX_UPLOAD_BYTES__ and
 * __CSRF_TOKEN_JSON__ into the served index.html. Vite must not mangle
 * those literal tokens during build — this script fails the build if any
 * placeholder is missing from dist/index.html.
 *
 * Exit 0 = all placeholders present. Exit 1 = missing, with a list.
 */
import { readFileSync, existsSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = dirname(fileURLToPath(import.meta.url))
const ROOT = resolve(__dirname, '..')
const DIST = resolve(ROOT, 'dist', 'index.html')

const REQUIRED = ['__WEBUI_VERSION__', '__MAX_UPLOAD_BYTES__', '__CSRF_TOKEN_JSON__']

const html = existsSync(DIST) ? readFileSync(DIST, 'utf8') : null
if (!html) {
  console.error(`[check-placeholders] ${DIST} not found — run "npm run build" first.`)
  process.exit(1)
}

const missing = REQUIRED.filter((p) => !html.includes(p))
if (missing.length) {
  console.error('[check-placeholders] MISSING placeholders in dist/index.html:')
  for (const m of missing) console.error(`  - ${m}`)
  process.exit(1)
}

console.log('[check-placeholders] all 3 placeholders survived the build ✓')
