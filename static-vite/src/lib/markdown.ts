// Settled markdown renderer — C5. Uses `marked` (npm) for block parsing and
// DOMPurify for sanitization. This is the settled-transcript path; the live
// streaming path uses streaming-markdown (smd) in the hook layer (C6).
// KaTeX math ($...$ / $$...$$) renders client-side after mount.

import { Marked } from 'marked'
import DOMPurify from 'dompurify'

const marked = new Marked({
  breaks: true,
  gfm: true,
})

/** Render markdown to sanitized HTML (server content is untrusted). */
export function renderMd(raw: string): string {
  const src = String(raw ?? '')
    .replace(/\r\n/g, '\n')
    .replace(/\r/g, '\n')
  const html = marked.parse(src, { async: false }) as string
  return DOMPurify.sanitize(html, {
    ADD_ATTR: ['target'],
    FORBID_TAGS: ['style'],
  })
}
