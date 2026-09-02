package approval

import "testing"

func TestApprovalStoreQueueAndChoices(t *testing.T) {
	s := NewStore()
	entry := PendingApproval{ID: "a1", SessionID: "s1", Command: "rm x", Description: "delete", PatternKeys: []string{"rm", "delete"}}
	if !s.Submit(entry) {
		t.Fatal("submit rejected")
	}
	pending, ok := s.Pending("s1")
	if !ok || pending.ID != "a1" || pending.Command != "rm x" {
		t.Fatalf("pending = %+v, ok=%v", pending, ok)
	}
	if count := s.Count("s1"); count != 1 {
		t.Fatalf("count = %d", count)
	}
	if !s.Respond("s1", "a1", "session") {
		t.Fatal("respond rejected")
	}
	if _, ok := s.Pending("s1"); ok {
		t.Fatal("pending entry not removed")
	}
	for _, key := range entry.PatternKeys {
		if !s.IsApproved("s1", key) {
			t.Fatalf("session approval missing for %q", key)
		}
	}
}

func TestApprovalStoreAlwaysPersistsAllPatternKeys(t *testing.T) {
	s := NewStore()
	s.Submit(PendingApproval{ID: "a2", SessionID: "s2", PatternKeys: []string{"one", "two"}})
	if !s.Respond("s2", "a2", "always") {
		t.Fatal("respond rejected")
	}
	for _, key := range []string{"one", "two"} {
		if !s.IsPermanentlyApproved(key) {
			t.Fatalf("permanent approval missing for %q", key)
		}
	}
}

func TestApprovalStoreRejectsInvalidChoice(t *testing.T) {
	s := NewStore()
	s.Submit(PendingApproval{ID: "a3", SessionID: "s3"})
	if s.Respond("s3", "a3", "invalid") {
		t.Fatal("invalid choice accepted")
	}
	if _, ok := s.Pending("s3"); !ok {
		t.Fatal("invalid choice removed pending entry")
	}
}
