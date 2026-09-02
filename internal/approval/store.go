// Package approval implements the per-session approval state machine
// (01-architecture-design.md §5). It holds pending approvals in memory and
// records session-scoped and permanent approvals. Permanent approvals are
// additionally persisted to the SQLite settings table by the caller.
package approval

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

// PendingApproval is one queued tool approval awaiting a user response.
type PendingApproval struct {
	ID          string // stable approval_id (uuid hex) — target of /respond
	SessionID   string // session_key the approval belongs to
	Command     string // the shell command / tool invocation preview
	Description string
	PatternKeys []string // one or more pattern_keys (Rule #9)
	RunID       string   // optional gateway run_id mirror
}

// Store is the Go equivalent of Python's `_pending`/`_lock`/`_permanent_approved`.
type Store struct {
	mu        sync.Mutex
	pending   map[string][]PendingApproval // keyed by session, ordered queue (head first)
	approved  map[string]map[string]bool   // session -> pattern_key -> true (session scope)
	permanent map[string]bool              // pattern_key -> true (always scope)
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// NewStore creates an empty approval store.
func NewStore() *Store {
	return &Store{
		pending:   make(map[string][]PendingApproval),
		approved:  make(map[string]map[string]bool),
		permanent: make(map[string]bool),
	}
}

// Submit appends a pending approval to the session's queue. A missing ID is
// minted as a fresh uuid4 hex so /respond can target it. Returns false if an
// entry with the same ID is already pending.
func (s *Store) Submit(entry PendingApproval) bool {
	if entry.ID == "" {
		entry.ID = newID()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.pending[entry.SessionID] {
		if e.ID == entry.ID {
			return false
		}
	}
	s.pending[entry.SessionID] = append(s.pending[entry.SessionID], entry)
	return true
}

// Pending returns the head of the session's queue (the one the UI displays).
func (s *Store) Pending(sessionID string) (PendingApproval, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.pending[sessionID]
	if len(q) == 0 {
		return PendingApproval{}, false
	}
	return q[0], true
}

// Count returns the number of pending approvals for a session.
func (s *Store) Count(sessionID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending[sessionID])
}

// Respond resolves the head-matching approval for the session with the given
// choice ("once", "session", "always", "deny"). It returns false if the
// approval id is not pending or the choice is invalid. "always" records a
// permanent approval for every pattern_key (Rule #9).
func (s *Store) Respond(sessionID, approvalID, choice string) bool {
	switch choice {
	case "once", "session", "always", "deny":
	default:
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.pending[sessionID]
	idx := -1
	for i, e := range q {
		if e.ID == approvalID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false
	}
	entry := q[idx]
	q = append(q[:idx], q[idx+1:]...)
	if len(q) == 0 {
		delete(s.pending, sessionID)
	} else {
		s.pending[sessionID] = q
	}
	if choice == "session" {
		if s.approved[sessionID] == nil {
			s.approved[sessionID] = make(map[string]bool)
		}
		for _, key := range entry.PatternKeys {
			s.approved[sessionID][key] = true
		}
	} else if choice == "always" {
		for _, key := range entry.PatternKeys {
			s.permanent[key] = true
		}
	}
	return true
}

// IsApproved reports whether a pattern_key is approved for the session
// (session-scope or permanent).
func (s *Store) IsApproved(sessionID, patternKey string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.permanent[patternKey] {
		return true
	}
	return s.approved[sessionID][patternKey]
}

// IsPermanentlyApproved reports whether a pattern_key is in the permanent set.
func (s *Store) IsPermanentlyApproved(patternKey string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.permanent[patternKey]
}
