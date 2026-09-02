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
	var eventType EventType
	var data strings.Builder
	flush := func() {
		if eventType == "" || data.Len() == 0 {
			eventType = ""
			data.Reset()
			return
		}
		ev := TurnEvent{Type: eventType}
		var payload map[string]any
		if json.Unmarshal([]byte(data.String()), &payload) == nil {
			ev.Data = payload
			switch eventType {
			case EventToken:
				ev.Text, _ = payload["text"].(string)
			case EventTool:
				ev.Name, _ = payload["name"].(string)
				ev.Preview, _ = payload["preview"].(string)
			case EventError:
				ev.Error, _ = payload["message"].(string)
			}
		} else {
			ev.Error = data.String()
		}
		select {
		case out <- ev:
		case <-ctx.Done():
		}
		eventType = ""
		data.Reset()
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			eventType = EventType(strings.TrimSpace(strings.TrimPrefix(line, "event: ")))
		case strings.HasPrefix(line, "data: "):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(line, "data: "))
		case line == "":
			flush()
		}
	}
	flush()
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		out <- TurnEvent{Type: EventError, Error: err.Error()}
	}
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
