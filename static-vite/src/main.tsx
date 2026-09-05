import * as React from 'react'
import * as ReactDOM from 'react-dom/client'

// A4 placeholder: the shell imports theme.css so the parity harness has
// computed styles; real App content lands in Phase B.
import './theme.css'

function Shell() {
  return (
    <div id="hermes-vite-shell" data-testid="hermes-vite-shell" />
  )
}

const rootEl = document.getElementById('root')
if (rootEl) {
  ReactDOM.createRoot(rootEl).render(
    <React.StrictMode>
      <Shell />
    </React.StrictMode>,
  )
}
