package agentclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// HTTPClient speaks the runner's JSON + SSE HTTP contract.
type HTTPClient struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

func NewHTTPClient(baseURL, apiKey string) *HTTPClient {
	return &HTTPClient{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, client: &http.Client{}}
}

func (c *HTTPClient) request(ctx context.Context, method, path string, body any, accept string) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	return c.client.Do(req)
}

func (c *HTTPClient) RunTurn(ctx context.Context, req TurnRequest) (<-chan TurnEvent, error) {
	payload := map[string]any{
		"session_id":  req.SessionID,
		"message":     req.Message,
		"attachments": req.Attachments,
		"workspace":   req.Workspace,
		"provider":    req.Provider,
		"model":       req.Model,
		"history":     req.History,
		"source":      "webui",
	}
	resp, err := c.request(ctx, http.MethodPost, "/v1/runs", payload, "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("runner start returned HTTP %d", resp.StatusCode)
	}
	var started struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&started); err != nil {
		return nil, fmt.Errorf("runner start response: %w", err)
	}
	if started.RunID == "" {
		return nil, fmt.Errorf("runner start response missing run_id")
	}

	out := make(chan TurnEvent, 16)
	go c.readEvents(ctx, started.RunID, out)
	return out, nil
}

func (c *HTTPClient) readEvents(ctx context.Context, runID string, out chan<- TurnEvent) {
	defer close(out)
	resp, err := c.request(ctx, http.MethodGet, "/v1/runs/"+runID+"/events", nil, "text/event-stream")
	if err != nil {
		out <- TurnEvent{Type: EventError, Error: err.Error()}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		out <- TurnEvent{Type: EventError, Error: fmt.Sprintf("runner events returned HTTP %d", resp.StatusCode)}
		return
	}

	scanner := bufio.NewScanner(resp.Body)
	// A done event carries a full session snapshot and can exceed Scanner's
	// 64KiB default on long conversations.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var pendingEventType string
	var data strings.Builder
	flush := func() {
		if data.Len() == 0 {
			pendingEventType = ""
			return
		}
		var payload map[string]any
		if json.Unmarshal([]byte(data.String()), &payload) != nil {
			data.Reset()
			pendingEventType = ""
			return
		}
		// The gateway's SSE frames are `data: {"event":...,...}` — the event
		// name is a field INSIDE the JSON payload, not a standalone SSE-level
		// `event:` line. Fall back to it whenever no `event:` line preceded.
		eventType := pendingEventType
		if eventType == "" {
			eventType, _ = payload["event"].(string)
		}
		if ev, ok := translateGatewayEvent(eventType, payload); ok {
			select {
			case out <- ev:
			case <-ctx.Done():
			}
		}
		data.Reset()
		pendingEventType = ""
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "event:"):
			pendingEventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	flush()
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		out <- TurnEvent{Type: EventError, Error: err.Error()}
	}
}

func translateGatewayEvent(eventType string, payload map[string]any) (TurnEvent, bool) {
	switch eventType {
	case "message.delta":
		text, _ := payload["delta"].(string)
		return TurnEvent{Type: EventToken, Text: text, Data: payload}, true
	case "token":
		text, _ := payload["text"].(string)
		return TurnEvent{Type: EventToken, Text: text, Data: payload}, true
	case "reasoning", "reasoning.available":
		text, _ := payload["text"].(string)
		if text == "" {
			text, _ = payload["delta"].(string)
		}
		if text == "" {
			text, _ = payload["content"].(string)
		}
		return TurnEvent{Type: EventReasoning, Text: text, Data: payload}, text != ""
	case "run.completed":
		// Informational only; `done` is the single completion signal.
		return TurnEvent{}, false
	case "done":
		return TurnEvent{Type: EventDone, Data: payload}, true
	case "tool", "tool.started":
		name, _ := payload["name"].(string)
		preview, _ := payload["preview"].(string)
		// frontend listens for SSE event "tool"
		return TurnEvent{Type: EventTool, Name: name, Preview: preview, Data: payload}, true
	case "tool_complete", "tool.completed":
		name, _ := payload["name"].(string)
		preview, _ := payload["preview"].(string)
		return TurnEvent{Type: EventType("tool_complete"), Name: name, Preview: preview, Data: payload}, true
	case "interim_assistant":
		text, _ := payload["text"].(string)
		return TurnEvent{Type: EventType("interim_assistant"), Text: text, Data: payload}, true
	case "approval":
		return TurnEvent{Type: EventApproval, Data: payload}, true
	case "error":
		msg, _ := payload["message"].(string)
		return TurnEvent{Type: EventError, Error: msg, Data: payload}, true
	default:
		// Pass through all other gateway events (metering, context_status,
		// title, todo_state, compressing, etc.) so the frontend's
		// addEventListener handlers fire. Without this, the Go relay drops
		// UI-critical events and the WebUI shows a degraded "sudden reply"
		// with no thinking/tool/context updates (image 1 vs 3 gap).
		if eventType == "" {
			return TurnEvent{}, false
		}
		return TurnEvent{Type: EventType(eventType), Data: payload}, true
	}
}

func (c *HTTPClient) CronMutation(ctx context.Context, action, jobID, profile string, payload []byte) (int, []byte, error) {
	path := "/api/jobs"
	method := http.MethodPost
	var body any
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &body); err != nil {
			return 0, nil, fmt.Errorf("invalid cron payload: %w", err)
		}
		// gateway create/update expects "prompt"; WebUI form sends "command".
		if m, ok := body.(map[string]any); ok && (action == "create" || action == "update") {
			if cmd, has := m["command"]; has {
				if _, hasPrompt := m["prompt"]; !hasPrompt {
					m["prompt"] = cmd
				}
				delete(m, "command")
			}
		}
	}
	if profile != "" && profile != "default" {
		path = "/p/" + profile + path
	}
	if action == "update" {
		path += "/" + jobID
		method = http.MethodPatch
	} else if action == "delete" {
		path += "/" + jobID
		method = http.MethodDelete
	} else if action != "create" {
		path += "/" + jobID + "/" + action
	}
	resp, err := c.request(ctx, method, path, body, "application/json")
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return resp.StatusCode, data, err
}

func (c *HTTPClient) Cancel(ctx context.Context, sessionID string) error {
	resp, err := c.request(ctx, http.MethodPost, "/v1/runs/"+sessionID+"/cancel", map[string]any{}, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("runner cancel returned HTTP %d", resp.StatusCode)
	}
	return nil
}
