import * as React from 'react'
import * as ReactDOM from 'react-dom/client'
import './theme.css'
import { AppTitlebar } from './components/layout/app-titlebar'
import { NavRail } from './components/layout/nav-rail'
import { Sidebar } from './components/layout/sidebar'
import { MainView } from './components/layout/main-view'
import { loadLocale } from './i18n'
import { useChatStream } from './hooks/useChatStream'

loadLocale()

function Shell() {
  const chat = useChatStream()
  return (
    <>
      <AppTitlebar />
      <div className="layout">
        <NavRail />
        <Sidebar />
        <MainView state={chat.state} onSend={(text) => void chat.send(text)} />
      </div>
    </>
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
