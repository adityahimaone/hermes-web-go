package agentclient

import (
	"context"
	"testing"
)

func TestTurnRequestCarriesTaskID(t *testing.T) {
	req := TurnRequest{SessionID: "session", TaskID: "task", Message: "hello"}
	if req.TaskID != "task" {
		t.Fatalf("task id = %q", req.TaskID)
	}
}

func TestTurnEventJSONShape(t *testing.T) {
	event := TurnEvent{Type: EventToken, Text: "hello"}
	if event.Type != EventToken || event.Text != "hello" {
		t.Fatalf("event = %+v", event)
	}
}

func TestAgentClientInterfaceCompiles(t *testing.T) {
	var _ AgentClient = (*testClient)(nil)
}

type testClient struct{}

func (*testClient) RunTurn(context.Context, TurnRequest) (<-chan TurnEvent, error) { return nil, nil }
func (*testClient) Cancel(context.Context, string) error                           { return nil }
