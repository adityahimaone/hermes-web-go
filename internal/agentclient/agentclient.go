// Package agentclient defines the transport contract between the WebUI and
// whatever executes an agent turn (a Hermes agent process over the gateway
// HTTP API, or the agent shim). Per 01-architecture-design.md §2b, both
// transports produce identical TurnEvent streams and the SSE layer never
// knows which transport is in use.
package agentclient

import "context"

// EventType is the category of a TurnEvent. These map 1:1 onto the SSE event
// names the frontend already parses (token/tool/approval/done/error).
type EventType string

const (
	EventToken    EventType = "token"
	EventTool     EventType = "tool"
	EventApproval EventType = "approval"
	EventDone     EventType = "done"
	EventError    EventType = "error"
)

// TurnRequest is the per-turn execution contract. TaskID (not SessionID) is
// what the agent side keys on — Critical Rule #3.
type TurnRequest struct {
	SessionID   string
	TaskID      string
	Message     string
	Workspace   string
	Model       string
	Provider    string
	History     []map[string]any // OpenAI-format messages (role/content), sent to the agent
	Attachments []string
}

// TurnEvent is one item streamed from the agent back to the SSE writer.
// Text is only meaningful for EventToken; Name/Preview only for EventTool.
// The channel is closed by the transport when the turn is fully done — that
// close (not a sentinel event) is the end-of-stream signal, so an empty
// token string is never ambiguous (Critical Rule #4).
type TurnEvent struct {
	Type    EventType
	Text    string
	Name    string
	Preview string
	Data    map[string]any // arbitrary extra payload (approval shape, done payload)
	Error   string
}

// AgentClient is the seam between the HTTP/SSE surface and the agent. See
// 01-architecture-design.md §2b for the two implementations behind it.
type AgentClient interface {
	// RunTurn executes one agent turn and streams events back. The returned
	// channel is closed when the turn completes (or ctx is cancelled).
	RunTurn(ctx context.Context, req TurnRequest) (<-chan TurnEvent, error)
	// Cancel stops an in-flight turn. sessionID is the WebUI session id.
	Cancel(ctx context.Context, sessionID string) error
}

// CronMutator forwards scheduler ownership to the agent gateway.
type CronMutator interface {
	CronMutation(ctx context.Context, action, jobID, profile string, payload []byte) (int, []byte, error)
}
