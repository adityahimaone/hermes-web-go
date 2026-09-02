package agentclient

import (
	"context"
	"log"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// NewBestClient selects configured transport. auto probes gRPC, then falls back
// to HTTP. Fallback keeps existing deployments working until shim rollout.
func NewBestClient(baseURL, apiKey, transport, socket string) AgentClient {
	if transport == "grpc" || (transport == "auto" && socket != "") {
		if c, err := NewGRPCClient(socket); err == nil {
			return c
		} else {
			log.Printf("agent transport: using HTTP fallback (grpc socket unavailable): %v", err)
		}
	}
	return NewHTTPClient(baseURL, apiKey)
}

func grpcDialContext(ctx context.Context, network, socket string) (*grpc.ClientConn, error) {
	return grpc.DialContext(ctx, "passthrough:///agent", grpc.WithBlock(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, socket)
	}))
}

func probeGRPC(socket string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	conn, err := grpcDialContext(ctx, "unix", socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	return nil
}

var _ = probeGRPC
