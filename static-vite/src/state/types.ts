// Shared chat types — field names verbatim from the server contract
// (api/models.py Usage; static/ui.js S shape; messages.js stream handlers).
// Do not rename server fields.

export interface Usage {
  input_tokens?: number
  output_tokens?: number
  estimated_cost?: number
  cache_read_tokens?: number
  cache_write_tokens?: number
  context_length?: number
  threshold_tokens?: number
  last_prompt_tokens?: number
  post_compression_context_tokens_estimate?: number
  turn_cache_hit_percent?: number
  duration_seconds?: number
  tps?: number
  gateway_routing?: unknown
}

export interface ToolCall {
  id?: string
  name: string
  state: 'start' | 'complete'
  args?: unknown
  result?: unknown
  is_error?: boolean
  started_at?: number
  completed_at?: number
  _createdByComplete?: boolean
  done?: boolean
  [k: string]: unknown
}

export interface ChatMessage {
  role: 'user' | 'assistant' | 'system' | 'tool'
  content: string | Array<{ type: string; [k: string]: unknown }> | null
  reasoning?: string
  timestamp?: number
  _ts?: number
  attachments?: string[]
  provider_details?: string
  provider_details_label?: string
  _turnUsage?: Usage
  _turnDuration?: number
  _turnTps?: number
  _gatewayRouting?: unknown
  [k: string]: unknown
}

export interface SessionMeta {
  session_id: string
  title?: string
  model?: string
  model_provider?: string | null
  message_count?: number
  input_tokens?: number
  output_tokens?: number
  estimated_cost?: number
  cache_read_tokens?: number
  cache_write_tokens?: number
  context_length?: number
  threshold_tokens?: number
  last_prompt_tokens?: number
  post_compression_context_tokens_estimate?: number
  gateway_routing?: unknown
  gateway_routing_history?: unknown[]
  _messages_truncated?: boolean
  _messages_offset?: number
  [k: string]: unknown
}

export interface TodoItem {
  [k: string]: unknown
}

export interface TodoStateMeta {
  ts: number
  source: string
  version: number
}

/** App state — mirrors vanilla `S` (static/ui.js:8) plus rev guard. */
export interface AppState {
  session: SessionMeta | null
  messages: ChatMessage[]
  busy: boolean
  pendingFiles: string[]
  toolCalls: ToolCall[]
  activeStreamId: string | null
  /** streaming-only transient: set while activeStreamId != null */
  liveTps?: number | null
  /** deterministic synthetic field mirror for rev filtering */
  rev: number
  currentDir: string
  activeProfile: string
  activeProfileIsDefault: boolean
  showHiddenWorkspaceFiles: boolean
  todos: TodoItem[]
  todoStateMeta: TodoStateMeta | null
  /** Last usage merged for ctx indicator (vanilla S.lastUsage). */
  lastUsage: Usage | null
  /** internal pending reasoning buffer (vanilla reasoningText accumulator) */
  _pendingReasoning?: string
}

export function initialAppState(): AppState {
  return {
    session: null,
    messages: [],
    busy: false,
    pendingFiles: [],
    toolCalls: [],
    activeStreamId: null,
    currentDir: '.',
    activeProfile: 'default',
    activeProfileIsDefault: true,
    showHiddenWorkspaceFiles: false,
    todos: [],
    todoStateMeta: null,
    lastUsage: null,
    rev: 0,
  }
}

/** SSE event payloads (data-attribute shapes from messages.js _wireSSE). */
export type ChatEvent =
  | { type: 'token'; text: string }
  | { type: 'interim_assistant'; text: string; already_streamed?: boolean; reasoning_echo?: boolean }
  | { type: 'reasoning'; text: string }
  | { type: 'tool'; id?: string; name: string; args?: unknown; [k: string]: unknown }
  | { type: 'tool_complete'; id?: string; name: string; is_error?: boolean; result?: unknown; [k: string]: unknown }
  | { type: 'todo_state'; todos: TodoItem[]; ts?: number; source?: string; version?: number; session_id?: string }
  | { type: 'done'; status?: string; usage?: Usage | null; created_at?: number | null; session?: SessionMeta & { messages?: ChatMessage[] }; stream_id?: string }
  | { type: 'metering'; session_id?: string; estimated?: boolean; tps_available?: boolean; tps?: number; usage?: Usage }
  | { type: 'apperror'; kind?: string; message?: string; hint?: string; details?: string; session?: SessionMeta & { messages?: ChatMessage[] }; [k: string]: unknown }
  | { type: 'stream_end'; session_id?: string }
