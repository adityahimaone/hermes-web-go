// Chat message list — C5. Renders settled history from chatReducer state.messages.
// Uses renderMd for block content; thinking content goes in <details>.
// Tool cards render whenever toolCalls exist (live turn may have a running
// tool before any token arrives), messages render when present.

import * as React from 'react'
import { renderMd } from '../../lib/markdown'
import type { ChatMessage, ToolCall } from '../../state/types'
import 'katex/dist/katex.min.css'

function ToolCard({ tc }: { tc: ToolCall }) {
  const running = tc.state === 'start'
  const name = String(tc.name || 'tool')
  const args = typeof tc.args === 'string' ? tc.args : tc.args != null ? JSON.stringify(tc.args, null, 2) : ''
  const result = typeof tc.result === 'string' ? tc.result : tc.result != null ? JSON.stringify(tc.result, null, 2) : ''
  return (
    <div className={`tool-card${running ? ' tool-card-running' : ''}`} data-tool-worklog-group="1" data-tool={name}>
      <div className="tool-card-head">
        <span className="tool-card-name">{name}</span>
        {running ? (
          <span className="tool-card-badge tool-card-badge--running">running</span>
        ) : tc.is_error ? (
          <span className="tool-card-badge tool-card-badge--error">error</span>
        ) : (
          <span className="tool-card-badge tool-card-badge--done">done</span>
        )}
      </div>
      {args ? (
        <details className="tool-card-details">
          <summary className="tool-card-summary">args</summary>
          <pre className="tool-card-args">{args}</pre>
        </details>
      ) : null}
      {result ? <pre className="tool-card-result">{result}</pre> : null}
    </div>
  )
}

function MessageBody({ m, idx }: { m: ChatMessage; idx: number }) {
  if (m.role === 'user') {
    const text = typeof m.content === 'string' ? m.content : JSON.stringify(m.content)
    return (
      <div className="msg-row" data-role="user" data-msg-idx={idx} id={`msg-user-${idx}`}>
        <div className="msg-body" dangerouslySetInnerHTML={{ __html: renderMd(text) }} />
        {Array.isArray(m.attachments) && m.attachments.length ? (
          <div className="msg-attachments">{m.attachments.join(', ')}</div>
        ) : null}
      </div>
    )
  }
  if (m.role === 'assistant') {
    const visible = typeof m.content === 'string' ? m.content : m.content != null ? JSON.stringify(m.content) : ''
    const reasoning = m.reasoning ? String(m.reasoning) : ''
    const turnUsage = m._turnUsage as { input_tokens?: number; output_tokens?: number } | undefined
    const hasReasoning = !!reasoning.trim()
    return (
      <div className="assistant-turn" data-role="assistant" data-msg-idx={idx}>
        <div className="assistant-turn-blocks">
          {hasReasoning ? (
            <details className="assistant-thinking" data-thinking="1">
              <summary className="assistant-thinking-summary">Thinking</summary>
              <div className="assistant-thinking-body">{reasoning}</div>
            </details>
          ) : null}
          <div className="msg-body assistant-body" dangerouslySetInnerHTML={{ __html: renderMd(visible) }} />
          {turnUsage ? (
            <div className="assistant-turn-footer">
              <span className="assistant-turn-tokens">
                {turnUsage.input_tokens != null ? `${turnUsage.input_tokens} in · ` : ''}
                {turnUsage.output_tokens != null ? `${turnUsage.output_tokens} out` : ''}
              </span>
            </div>
          ) : null}
          {m.provider_details ? (
            <details className="assistant-error-details">
              <summary>Details</summary>
              <pre>{m.provider_details}</pre>
            </details>
          ) : null}
        </div>
      </div>
    )
  }
  // system / tool role (rare in this UI) — plain row
  const text = typeof m.content === 'string' ? m.content : JSON.stringify(m.content)
  return (
    <div className="msg-row" data-role={String(m.role)} data-msg-idx={idx}>
      <div className="msg-body" dangerouslySetInnerHTML={{ __html: renderMd(text) }} />
    </div>
  )
}

export function MessageList({
  messages,
  toolCalls,
}: {
  messages: ChatMessage[]
  toolCalls: ToolCall[]
}) {
  return (
    <div className="messages-inner" id="msgInner">
      {messages.map((m, idx) => (
        <MessageBody key={`${m.role}-${idx}`} m={m} idx={idx} />
      ))}
      {toolCalls.length ? (
        <div className="tool-call-group" data-tool-worklog-group="1">
          {toolCalls.map((tc, j) => (
            <ToolCard key={`tc-${tc.id ?? j}-${tc.name}`} tc={tc} />
          ))}
        </div>
      ) : null}
    </div>
  )
}
