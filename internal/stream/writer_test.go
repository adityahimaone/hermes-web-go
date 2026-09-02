package stream

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"hermes-web-go/internal/agentclient"
)

// bufFlusher is a fake http.ResponseWriter that records writes and counts
// flushes. It is mutex-protected on Write (the writer goroutine) and String
// (the test goroutine) so -race stays clean.
type bufFlusher struct {
	http.ResponseWriter
	mu      sync.Mutex
	buf     bytes.Buffer
	flushes int
}

func (b *bufFlusher) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}
func (b *bufFlusher) Flush() {
	b.mu.Lock()
	b.flushes++
	b.mu.Unlock()
}
func (b *bufFlusher) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
func (b *bufFlusher) flushCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.flushes
}

func TestWriterTokenAndDone(t *testing.T) {
	ch := make(chan agentclient.TurnEvent, 4)
	ch <- agentclient.TurnEvent{Type: agentclient.EventToken, Text: "hello"}
	ch <- agentclient.TurnEvent{Type: agentclient.EventDone}
	close(ch)

	w := &bufFlusher{ResponseWriter: httptest.NewRecorder()}
	done := make(chan bool, 1)
	go func() { done <- WriteSSE(w, ch) }()

	select {
	case ok := <-done:
		if !ok {
			t.Fatal("writer returned false")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("writer hung")
	}

	out := w.String()
	if !strings.Contains(out, "event: token\n") || !strings.Contains(out, `"text":"hello"`) {
		t.Fatalf("token frame missing: %q", out)
	}
	if !strings.Contains(out, "event: done\n") {
		t.Fatalf("done frame missing: %q", out)
	}
	if w.flushCount() < 2 {
		t.Fatalf("expected >=2 flushes, got %d", w.flushCount())
	}
}

func TestWriterToolEvent(t *testing.T) {
	ch := make(chan agentclient.TurnEvent, 2)
	ch <- agentclient.TurnEvent{Type: agentclient.EventTool, Name: "bash", Preview: "ls -la"}
	close(ch)

	w := &bufFlusher{ResponseWriter: httptest.NewRecorder()}
	go WriteSSE(w, ch)
	time.Sleep(100 * time.Millisecond)
	out := w.String()
	if !strings.Contains(out, "event: tool\n") || !strings.Contains(out, `"name":"bash"`) {
		t.Fatalf("tool frame: %q", out)
	}
}

func TestWriterHeartbeat(t *testing.T) {
	ch := make(chan agentclient.TurnEvent, 1)
	w := &bufFlusher{ResponseWriter: httptest.NewRecorder()}

	done := make(chan bool, 1)
	go func() { done <- writeSSE(w, ch, 20*time.Millisecond) }()

	time.Sleep(70 * time.Millisecond)
	close(ch)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("writer hung")
	}
	if !strings.Contains(w.String(), ": heartbeat") {
		t.Fatalf("heartbeat missing: %q", w.String())
	}
}
