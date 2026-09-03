package stream

import (
	"sync"
	"time"

	"hermes-web-go/internal/agentclient"
)

// Event is one journaled SSE event with a monotonic sequence.
type Event struct {
	Seq   uint64
	Event agentclient.TurnEvent
	At    time.Time
}

// Journal is a journaled broadcast hub. Subscribers receive a replay of
// retained events plus a live broadcast. After Finish the journal becomes
// terminal and no further publishes are accepted.
type Journal struct {
	mu       sync.Mutex
	events   []Event
	subs     map[uint64]chan Event
	nextID   uint64
	seq      uint64
	terminal bool
	retain   int
}

func NewJournal(retain int) *Journal {
	return &Journal{
		subs:   make(map[uint64]chan Event),
		retain: retain,
	}
}

func (j *Journal) Publish(ev agentclient.TurnEvent) Event {
	j.mu.Lock()
	if j.terminal {
		j.mu.Unlock()
		return Event{}
	}
	j.seq++
	je := Event{Seq: j.seq, Event: ev, At: time.Now()}
	j.events = append(j.events, je)
	if j.retain > 0 && len(j.events) > j.retain {
		j.events = append([]Event(nil), j.events[len(j.events)-j.retain:]...)
	}
	subs := make([]chan Event, 0, len(j.subs))
	for _, ch := range j.subs {
		subs = append(subs, ch)
	}
	j.mu.Unlock()
	for _, ch := range subs {
		ch <- je
	}
	return je
}

func (j *Journal) Finish(ev agentclient.TurnEvent) Event {
	j.mu.Lock()
	if j.terminal {
		j.mu.Unlock()
		return Event{}
	}
	j.seq++
	je := Event{Seq: j.seq, Event: ev, At: time.Now()}
	j.events = append(j.events, je)
	if j.retain > 0 && len(j.events) > j.retain {
		j.events = append([]Event(nil), j.events[len(j.events)-j.retain:]...)
	}
	subs := make([]chan Event, 0, len(j.subs))
	for _, ch := range j.subs {
		subs = append(subs, ch)
	}
	j.terminal = true
	j.mu.Unlock()
	for _, ch := range subs {
		ch <- je
		close(ch)
	}
	return je
}

func (j *Journal) Subscribe(after uint64) (replay []Event, live <-chan Event, cancel func()) {
	j.mu.Lock()
	defer j.mu.Unlock()
	replay = make([]Event, 0)
	for _, e := range j.events {
		if e.Seq > after {
			replay = append(replay, e)
		}
	}
	if j.terminal {
		cp := append([]Event(nil), replay...)
		return cp, nil, func() {}
	}
	ch := make(chan Event, 64)
	id := j.nextID
	j.nextID++
	j.subs[id] = ch
	cancel = func() {
		j.mu.Lock()
		defer j.mu.Unlock()
		delete(j.subs, id)
	}
	cp := append([]Event(nil), replay...)
	return cp, ch, cancel
}

func (j *Journal) SnapshotAfter(after uint64) []Event {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]Event, 0)
	for _, e := range j.events {
		if e.Seq > after {
			out = append(out, e)
		}
	}
	return out
}
