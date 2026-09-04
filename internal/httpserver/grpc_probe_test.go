package httpserver

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	agentpb "hermes-web-go/internal/agentpb"
)

// TestGrpcShimPing probes the real launchd-managed agent shim socket.
// Skipped when the socket is absent (CI or shim not deployed).
func TestGrpcShimPing(t *testing.T) {
	sock := "/Users/adityahimawan/.hermes/webui/agent.sock"
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, "passthrough:///agent", grpc.WithBlock(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}))
	if err != nil {
		t.Skipf("shim socket unavailable: %v", err)
	}
	defer conn.Close()
	c := agentpb.NewAgentClient(conn)
	resp, err := c.Ping(ctx, &agentpb.PingRequest{})
	if err != nil {
		t.Fatalf("grpc ping failed: %v", err)
	}
	t.Logf("shim version: %s", resp.Version)
}
