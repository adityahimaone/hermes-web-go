package agentclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	agentpb "hermes-web-go/internal/agentpb"
)

// GRPCClient speaks Agent service over a Unix socket.
type GRPCClient struct {
	conn   *grpc.ClientConn
	client agentpb.AgentClient
}

func NewGRPCClient(socket string) (*GRPCClient, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	conn, err := grpc.DialContext(ctx, "passthrough:///agent", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socket)
	}), grpc.WithBlock())
	if err != nil {
		return nil, err
	}
	c := &GRPCClient{conn: conn, client: agentpb.NewAgentClient(conn)}
	if _, err = c.client.Ping(ctx, &agentpb.PingRequest{}); err != nil {
		conn.Close()
		return nil, err
	}
	return c, nil
}
func (c *GRPCClient) Close() error { return c.conn.Close() }
func (c *GRPCClient) RunTurn(ctx context.Context, req TurnRequest) (<-chan TurnEvent, error) {
	history, err := json.Marshal(req.History)
	if err != nil {
		return nil, err
	}
	stream, err := c.client.RunTurn(ctx, &agentpb.RunTurnRequest{SessionId: req.SessionID, TaskId: req.TaskID, Message: req.Message, Workspace: req.Workspace, Model: req.Model, Provider: req.Provider, HistoryJson: string(history), Attachments: req.Attachments})
	if err != nil {
		return nil, err
	}
	out := make(chan TurnEvent, 16)
	go func() {
		defer close(out)
		for {
			ev, err := stream.Recv()
			if err != nil {
				if err.Error() != "EOF" {
					out <- TurnEvent{Type: EventError, Error: err.Error()}
				}
				return
			}
			data := map[string]any{}
			if ev.DataJson != "" {
				_ = json.Unmarshal([]byte(ev.DataJson), &data)
			}
			out <- TurnEvent{Type: EventType(ev.Type), Text: ev.Text, Name: ev.Name, Preview: ev.Preview, Data: data, Error: ev.Error}
		}
	}()
	return out, nil
}
func (c *GRPCClient) Cancel(ctx context.Context, sessionID string) error {
	_, err := c.client.Cancel(ctx, &agentpb.CancelRequest{SessionId: sessionID})
	return err
}

var _ AgentClient = (*GRPCClient)(nil)
var _ = fmt.Sprintf
