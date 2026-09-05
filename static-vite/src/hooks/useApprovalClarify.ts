// useApprovalClarify — C6: consumes 'approval'/'clarify' raw SSE events,
// keeps the current pending prompt per session, and posts responses to
// /api/approval/respond + /api/clarify/respond (vanilla respondApproval /
// respondClarify semantics: in-flight disable, echo clarify reply as user row).

import { useCallback, useRef, useState } from 'react'
import { api } from './useChatStream'
import type { ApprovalPending, ClarifyPending } from '../components/chat/approval-clarify'

export type { ClarifyPending }

interface ApprovalResponse {
  ok?: boolean
  error?: string
  yolo_enabled?: boolean
  stale_cleared?: boolean
}

export function useApprovalClarify(onClarifyEcho?: (text: string) => void) {
  const [approval, setApproval] = useState<{ pending: ApprovalPending; count: number } | null>(null)
  const [clarify, setClarify] = useState<ClarifyPending | null>(null)
  const [approvalResponding, setApprovalResponding] = useState<string | null>(null)
  const [clarifyResponding, setClarifyResponding] = useState(false)
  const sessionRef = useRef<string | null>(null)

  const setSession = useCallback((sid: string | null) => {
    sessionRef.current = sid
  }, [])

  /** Raw SSE handler — call from wireChatSSE onRaw for approval/clarify. */
  const onRawSSE = useCallback((name: string, data: Record<string, unknown>) => {
    if (name === 'approval') {
      setApproval({ pending: data as ApprovalPending, count: Number(data.pending_count) || 1 })
    } else if (name === 'clarify') {
      setClarify(data as ClarifyPending)
    }
  }, [])

  const clearForSession = useCallback((sid: string | null) => {
    setApproval(null)
    setClarify(null)
    setApprovalResponding(null)
    setClarifyResponding(false)
  }, [])

  const respondApproval = useCallback(
    async (choice: 'once' | 'session' | 'always' | 'deny', pending?: ApprovalPending | null) => {
      const p = pending ?? approval?.pending
      const sid = sessionRef.current
      if (!p || !sid) return false
      setApprovalResponding(choice)
      try {
        const res = await api('/api/approval/respond', {
          method: 'POST',
          body: JSON.stringify({
            session_id: sid,
            choice,
            approval_id: p.approval_id ?? '',
            ...(p.run_id ? { run_id: p.run_id } : {}),
          }),
        })
        const result = (await res.json().catch(() => ({}))) as ApprovalResponse
        if (result.ok) {
          setApproval(null)
          setApprovalResponding(null)
          return true
        }
        setApprovalResponding(null)
        return false
      } catch {
        setApprovalResponding(null)
        return false
      }
    },
    [approval],
  )

  const respondClarify = useCallback(
    async (value: string) => {
      const sid = sessionRef.current
      const p = clarify
      if (!sid || !p || !value) return false
      setClarifyResponding(true)
      try {
        const res = await api('/api/clarify/respond', {
          method: 'POST',
          body: JSON.stringify({
            session_id: sid,
            response: value,
            clarify_id: p.clarify_id ?? '',
          }),
        })
        const result = (await res.json().catch(() => ({}))) as ApprovalResponse
        if (result.ok) {
          setClarify(null)
          setClarifyResponding(false)
          // Echo the user's clarify choice as a visible message (vanilla #2639)
          onClarifyEcho?.(value)
          return true
        }
        // stale/expired — keep card + draft (vanilla keeps draft on failure)
        setClarifyResponding(false)
        return false
      } catch {
        setClarifyResponding(false)
        return false
      }
    },
    [clarify, onClarifyEcho],
  )

  return {
    approval,
    clarify,
    approvalResponding,
    clarifyResponding,
    onRawSSE,
    setSession,
    clearForSession,
    respondApproval,
    respondClarify,
  }
}
