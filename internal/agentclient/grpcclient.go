package agentclient

import (
	"context"
	"encoding/json"
	"io"

	"google.golang.org/grpc"

	agentpb "hermes-web-go/internal/agentpb"
)

// GRPCClient implements AgentClient over a gRPC stream. It always holds a
// non-nil fallback (the http AgentClient) and falls back whenever the stream
// cannot be established — never mid-stream (see doc note).
type GRPCClient struct {
	conn     *grpc.ClientConn
	client   agentpb.AgentClient
	fallback AgentClient
}

func NewGRPCClient(conn *grpc.ClientConn, fallback AgentClient) *GRPCClient {
	return &GRPCClient{conn: conn, client: agentpb.NewAgentClient(conn), fallback: fallback}
}

func (g *GRPCClient) Close() error { return g.conn.Close() }

func (g *GRPCClient) RunTurn(ctx context.Context, req TurnRequest) (<-chan TurnEvent, error) {
	history, err := json.Marshal(req.History)
	if err != nil {
		return nil, err
	}
	stream, err := g.client.RunTurn(ctx, &agentpb.RunTurnRequest{
		SessionId:   req.SessionID,
		TaskId:      req.TaskID,
		Message:     req.Message,
		Workspace:   req.Workspace,
		Model:       req.Model,
		Provider:    req.Provider,
		HistoryJson: string(history),
		Attachments: req.Attachments,
	})
	if err != nil {
		return g.fallback.RunTurn(ctx, req)
	}
	out := make(chan TurnEvent, 16)
	go func() {
		defer close(out)
		for {
			ev, err := stream.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				select {
				case out <- TurnEvent{Type: EventError, Error: "agent stream interrupted: " + err.Error()}:
				case <-ctx.Done():
				}
				return
			}
			data := map[string]any{}
			if ev.DataJson != "" {
				_ = json.Unmarshal([]byte(ev.DataJson), &data)
			}
			select {
			case out <- TurnEvent{Type: EventType(ev.Type), Text: ev.Text, Name: ev.Name, Preview: ev.Preview, Data: data, Error: ev.Error}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (g *GRPCClient) Cancel(ctx context.Context, sessionID string) error {
	if _, err := g.client.Cancel(ctx, &agentpb.CancelRequest{SessionId: sessionID}); err != nil {
		return g.fallback.Cancel(ctx, sessionID)
	}
	return nil
}

var _ AgentClient = (*GRPCClient)(nil)
