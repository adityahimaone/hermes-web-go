// Login route component (login.js port) — rendered by the SPA when the
// pathname is /login. Reuses the same auth contract as src-login (POST
// /api/auth/login, ?next= with open-redirect guards).

import * as React from 'react'

function safeNextPath(): string {
  try {
    const raw = new URL(window.location.href).searchParams.get('next')
    if (!raw) return './'
    if (raw.charAt(0) !== '/') return './'
    if (raw.charAt(1) === '/' || raw.charAt(1) === '\\') return './'
    if (/[\x00-\x1f\x7f]/.test(raw)) return './'
    if (/^[a-zA-Z][a-zA-Z\d+\-.]*:/.test(raw)) return './'
    return raw
  } catch {
    return './'
  }
}

export default function LoginRoute() {
  const [pw, setPw] = React.useState('')
  const [err, setErr] = React.useState('')
  const [busy, setBusy] = React.useState(false)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setErr('')
    try {
      const r = await fetch('/api/auth/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ password: pw }),
      })
      if (r.ok) {
        window.location.href = safeNextPath()
        return
      }
      setErr(r.status === 401 ? 'Invalid password' : 'Connection failed')
    } catch {
      setErr('Connection failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="login-wrap">
      <form id="login-form" onSubmit={submit}>
        <h1>Hermes</h1>
        <input
          id="pw"
          type="password"
          value={pw}
          onChange={(e) => setPw(e.target.value)}
          placeholder="Password"
          autoComplete="current-password"
          autoFocus
        />
        <button id="loginBtn" type="submit" disabled={busy || !pw}>
          {busy ? '…' : 'Sign in'}
        </button>
        <div id="err" role="alert" style={{ display: err ? 'block' : 'none' }}>
          {err}
        </div>
      </form>
    </div>
  )
}
