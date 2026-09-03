package agentclient

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"

	agentpb "hermes-web-go/internal/agentpb"
)

type mockAgentServer struct {
	agentpb.UnimplementedAgentServer
	pingErr error
	evs     []*agentpb.TurnEvent
}

func (m *mockAgentServer) Ping(ctx context.Context, _ *agentpb.PingRequest) (*agentpb.PingResponse, error) {
	if m.pingErr != nil {
		return nil, m.pingErr
	}
	return &agentpb.PingResponse{Version: "mock"}, nil
}

func (m *mockAgentServer) RunTurn(req *agentpb.RunTurnRequest, s agentpb.Agent_RunTurnServer) error {
	for _, ev := range m.evs {
		if err := s.Send(ev); err != nil {
			return err
		}
	}
	return nil
}

func (m *mockAgentServer) Cancel(ctx context.Context, _ *agentpb.CancelRequest) (*agentpb.CancelResponse, error) {
	return &agentpb.CancelResponse{}, nil
}

func startMockAgentServer(t *testing.T) string {
	t.Helper()
	sock := filepath.Join(os.TempDir(), "agmock.sock")
	_ = os.Remove(sock)
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	mock := &mockAgentServer{evs: []*agentpb.TurnEvent{
		{Type: "token", Text: "hello"},
		{Type: "done"},
	}}
	srv := grpc.NewServer()
	agentpb.RegisterAgentServer(srv, mock)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); lis.Close(); _ = os.Remove(sock) })
	return sock
}

func startMockAgentServerWithEvents(t *testing.T, evs []*agentpb.TurnEvent) string {
	t.Helper()
	sock := filepath.Join(os.TempDir(), "agmock-seq.sock")
	_ = os.Remove(sock)
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	mock := &mockAgentServer{evs: evs}
	srv := grpc.NewServer()
	agentpb.RegisterAgentServer(srv, mock)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); lis.Close(); _ = os.Remove(sock) })
	return sock
}

func fakeHTTPFallback(t *testing.T) *HTTPClient {
	t.Helper()
	ts, _ := newRunnerShim(t, func(w http.ResponseWriter) {
		if f, ok := w.(http.Flusher); ok {
			_, _ = w.Write([]byte("event: token\ndata: {\"text\":\"http-fallback\"}\n\n"))
			f.Flush()
		}
	})
	return NewHTTPClient(ts, "")
}

func TestNewBestClient_NoSocketFallsBackToHTTP(t *testing.T) {
	cfg := TransportConfig{Mode: TransportAuto, SocketPath: "/tmp/does-not-exist.sock"}
	client, err := NewBestClient(context.Background(), cfg, fakeHTTPFallback(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := client.(*HTTPClient); !ok {
		t.Fatalf("expected HTTPClient fallback, got %T", client)
	}
}

func TestNewBestClient_ForcedGRPCUnavailableErrors(t *testing.T) {
	cfg := TransportConfig{Mode: TransportGRPC, SocketPath: "/tmp/does-not-exist.sock"}
	_, err := NewBestClient(context.Background(), cfg, fakeHTTPFallback(t))
	if err == nil {
		t.Fatal("expected error when grpc is forced but socket is unavailable")
	}
}

func TestNewBestClient_AutoPrefersGRPCWhenAvailable(t *testing.T) {
	sock := startMockAgentServer(t)
	cfg := TransportConfig{Mode: TransportAuto, SocketPath: sock}
	client, err := NewBestClient(context.Background(), cfg, fakeHTTPFallback(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := client.(*GRPCClient); !ok {
		t.Fatalf("expected GRPCClient when socket is healthy, got %T", client)
	}
}

func TestGRPCAndHTTPProduceSameTurnEventSequence(t *testing.T) {
	grpcSocket := startMockAgentServerWithEvents(t, []*agentpb.TurnEvent{
		{Type: "tool", Name: "shell", Preview: "pwd", DataJson: `{"event":"tool","name":"shell","preview":"pwd"}`},
		{Type: "token", Text: "hello", DataJson: `{"event":"token","text":"hello"}`},
		{Type: "done", DataJson: `{"event":"done"}`},
	})
	grpcClient, err := NewBestClient(context.Background(), TransportConfig{Mode: TransportGRPC, SocketPath: grpcSocket}, fakeHTTPFallback(t))
	if err != nil {
		t.Fatalf("grpc client: %v", err)
	}
	defer grpcClient.(*GRPCClient).Close()
	grpcEvents, err := grpcClient.RunTurn(context.Background(), TurnRequest{SessionID: "s1", TaskID: "t1", Message: "hi"})
	if err != nil {
		t.Fatalf("grpc turn: %v", err)
	}
	gotGRPC := collectTurnEvents(grpcEvents)

	httpBase, _ := newRunnerShim(t, func(w http.ResponseWriter) {
		f := w.(http.Flusher)
		_, _ = w.Write([]byte(`data: {"event":"tool","name":"shell","preview":"pwd"}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"event":"message.delta","delta":"hello"}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"event":"done"}` + "\n\n"))
		f.Flush()
	})
	httpEvents, err := NewHTTPClient(httpBase, "").RunTurn(context.Background(), TurnRequest{SessionID: "s1", TaskID: "t1", Message: "hi"})
	if err != nil {
		t.Fatalf("http turn: %v", err)
	}
	gotHTTP := collectTurnEvents(httpEvents)
	if len(gotGRPC) != len(gotHTTP) {
		t.Fatalf("event counts differ: grpc=%+v http=%+v", gotGRPC, gotHTTP)
	}
	for i := range gotGRPC {
		if gotGRPC[i].Type != gotHTTP[i].Type || gotGRPC[i].Text != gotHTTP[i].Text || gotGRPC[i].Name != gotHTTP[i].Name || gotGRPC[i].Preview != gotHTTP[i].Preview || gotGRPC[i].Error != gotHTTP[i].Error {
			t.Fatalf("event %d differs: grpc=%+v http=%+v", i, gotGRPC[i], gotHTTP[i])
		}
	}
}

func collectTurnEvents(ch <-chan TurnEvent) []TurnEvent {
	var events []TurnEvent
	for ev := range ch {
		events = append(events, ev)
	}
	return events
}

func TestGRPCFallbackOnCrash(t *testing.T) {
	sock := startMockAgentServer(t)
	cfg := TransportConfig{Mode: TransportAuto, SocketPath: sock}
	fallback := fakeHTTPFallback(t)
	client, err := NewBestClient(context.Background(), cfg, fallback)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gc, ok := client.(*GRPCClient)
	if !ok {
		t.Fatalf("expected GRPCClient, got %T", client)
	}
	// Simulate the shim dying: close the underlying connection so the next
	// RunTurn cannot establish a stream and must fall back to HTTP.
	if err := gc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	events, err := gc.RunTurn(context.Background(), TurnRequest{SessionID: "s1", Message: "hi"})
	if err != nil {
		t.Fatalf("expected transparent fallback, got error: %v", err)
	}
	got := make([]TurnEvent, 0)
	for ev := range events {
		got = append(got, ev)
	}
	if len(got) == 0 || got[0].Text != "http-fallback" {
		t.Fatalf("expected fallback events, got %+v", got)
	}
}
