// Package stream owns the SSE stream registry (01-architecture-design.md §4).
// A registry is the Go equivalent of Python's STREAMS dict + STREAMS_LOCK: a
// mutex-protected map keyed by stream_id, each carrying a buffered channel of
// TurnEvents that the agent/generator drains and the SSE writer reads.
package stream

import (
	"crypto/rand"
	"encoding/hex"
	"sync"

	"hermes-web-go/internal/agentclient"
)

func newStreamID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// Registry maps stream_id -> per-stream event channel.
type Registry struct {
	mu     sync.RWMutex
	m      map[string]chan agentclient.TurnEvent
	closed map[string]bool
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{m: make(map[string]chan agentclient.TurnEvent), closed: make(map[string]bool)}
}

// Create adds a new stream with a fresh 64-capacity buffered channel and
// returns its id. id is an opaque hex string.
func (r *Registry) Create() (string, chan agentclient.TurnEvent) {
	id := newStreamID()
	ch := make(chan agentclient.TurnEvent, 64)
	r.mu.Lock()
	r.m[id] = ch
	r.mu.Unlock()
	return id, ch
}

// Get returns the channel for id, or false if the stream does not exist.
func (r *Registry) Get(id string) (chan agentclient.TurnEvent, bool) {
	r.mu.RLock()
	ch, ok := r.m[id]
	r.mu.RUnlock()
	return ch, ok
}

// Publish sends an event to the named stream. Returns false if the stream
// does not exist or is already closed. Non-blocking: drops the event if the
// channel is full.
func (r *Registry) Publish(id string, ev agentclient.TurnEvent) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ch, ok := r.m[id]
	if !ok || r.closed[id] {
		return false
	}
	select {
	case ch <- ev:
	default:
	}
	return true
}

// Close closes the channel for id and marks the stream as closed. Returns
// false if the stream does not exist. Idempotent: a second call returns
// false.
func (r *Registry) Close(id string) bool {
	r.mu.Lock()
	ch, ok := r.m[id]
	if !ok || r.closed[id] {
		r.mu.Unlock()
		return false
	}
	r.closed[id] = true
	r.mu.Unlock()
	close(ch)
	return true
}

// Active reports whether the stream exists and is not yet closed.
func (r *Registry) Active(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.m[id]
	if !ok {
		return false
	}
	return !r.closed[id]
}

// Delete removes the stream entry and its closed marker. Returns false if
// the stream does not exist.
func (r *Registry) Delete(id string) bool {
	r.mu.Lock()
	_, ok := r.m[id]
	if !ok {
		r.mu.Unlock()
		return false
	}
	delete(r.m, id)
	delete(r.closed, id)
	r.mu.Unlock()
	return true
}
