package agentclient

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	agentpb "hermes-web-go/internal/agentpb"
)

type TransportMode string

const (
	TransportAuto TransportMode = "auto"
	TransportGRPC TransportMode = "grpc"
	TransportHTTP TransportMode = "http"
)

type TransportConfig struct {
	Mode         TransportMode
	SocketPath   string
	ProbeTimeout time.Duration
}

func grpcDialContext(ctx context.Context, socketPath string) (*grpc.ClientConn, error) {
	if socketPath == "" {
		return nil, fmt.Errorf("agentclient: empty socket path")
	}
	return grpc.DialContext(ctx, "passthrough:///agent", grpc.WithBlock(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socketPath)
	}))
}

func probeGRPC(ctx context.Context, socketPath string, timeout time.Duration) (*grpc.ClientConn, error) {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := grpcDialContext(probeCtx, socketPath)
	if err != nil {
		return nil, err
	}
	if _, err := agentpb.NewAgentClient(conn).Ping(probeCtx, &agentpb.PingRequest{}); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("agentclient: grpc ping failed: %w", err)
	}
	return conn, nil
}

func NewBestClient(ctx context.Context, cfg TransportConfig, fallback *HTTPClient) (AgentClient, error) {
	if fallback == nil {
		return nil, fmt.Errorf("agentclient: nil HTTP fallback")
	}
	timeout := cfg.ProbeTimeout
	if timeout <= 0 {
		timeout = 500 * time.Millisecond
	}
	switch cfg.Mode {
	case TransportHTTP:
		return fallback, nil
	case TransportGRPC:
		conn, err := probeGRPC(ctx, cfg.SocketPath, timeout)
		if err != nil {
			return nil, fmt.Errorf("agent transport: grpc forced but unavailable: %w", err)
		}
		return NewGRPCClient(conn, fallback), nil
	case TransportAuto, "":
		conn, err := probeGRPC(ctx, cfg.SocketPath, timeout)
		if err != nil {
			log.Printf("agent transport: using HTTP fallback (grpc socket unavailable): %v", err)
			return fallback, nil
		}
		return NewGRPCClient(conn, fallback), nil
	default:
		return nil, fmt.Errorf("agent transport: unknown mode %q (want auto|grpc|http)", cfg.Mode)
	}
}
