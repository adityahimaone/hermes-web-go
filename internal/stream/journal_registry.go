package stream

import (
	"crypto/rand"
	"encoding/hex"
	"sync"

	"hermes-web-go/internal/agentclient"
)

// JournalRegistry stores completed and active journal-backed streams.
type JournalRegistry struct {
	mu      sync.RWMutex
	streams map[string]*Journal
}

func NewJournalRegistry() *JournalRegistry {
	return &JournalRegistry{streams: make(map[string]*Journal)}
}

func newJournalID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func (r *JournalRegistry) Create(retain int) (string, *Journal) {
	id := newJournalID()
	journal := NewJournal(retain)
	r.mu.Lock()
	r.streams[id] = journal
	r.mu.Unlock()
	return id, journal
}

func (r *JournalRegistry) Get(id string) (*Journal, bool) {
	r.mu.RLock()
	journal, ok := r.streams[id]
	r.mu.RUnlock()
	return journal, ok
}

func (r *JournalRegistry) Active(id string) bool {
	journal, ok := r.Get(id)
	if !ok {
		return false
	}
	return journal.Active()
}

func (r *JournalRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := 0
	for _, journal := range r.streams {
		if journal.Active() {
			count++
		}
	}
	return count
}

func (j *Journal) Active() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return !j.terminal
}

func (j *Journal) PublishEvent(ev agentclient.TurnEvent) Event { return j.Publish(ev) }
