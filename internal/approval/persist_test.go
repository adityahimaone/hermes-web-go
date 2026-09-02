package approval

import (
	"database/sql"
	"path/filepath"
	"testing"

	"hermes-web-go/internal/store"
)

// newPersistDB opens a fresh sqlite db with the store schema (settings table).
func newPersistDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "webui.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestStorePersistsPermanentToDB(t *testing.T) {
	db := newPersistDB(t)
	p := NewSQLitePersistence(db)

	s := NewStoreP(p)
	s.Submit(PendingApproval{ID: "a1", SessionID: "s1", PatternKeys: []string{"curl", "bash"}})
	if !s.Respond("s1", "a1", "always") {
		t.Fatal("respond rejected")
	}

	// Load into a fresh store and assert the permanent keys survived.
	s2 := NewStoreP(NewSQLitePersistence(db))
	for _, key := range []string{"curl", "bash"} {
		if !s2.IsPermanentlyApproved(key) {
			t.Fatalf("permanent key %q not loaded from db", key)
		}
	}
}

func TestStoreLoadsPermanentsOnBoot(t *testing.T) {
	db := newPersistDB(t)
	p := NewSQLitePersistence(db)
	if err := p.SavePermanent([]string{"git", "docker"}); err != nil {
		t.Fatal(err)
	}
	s := NewStoreP(NewSQLitePersistence(db))
	if !s.IsPermanentlyApproved("git") || !s.IsPermanentlyApproved("docker") {
		t.Fatal("boot-load failed")
	}
}

func TestSQLitePersistenceRoundTrip(t *testing.T) {
	db := newPersistDB(t)
	p := NewSQLitePersistence(db)
	if err := p.SavePermanent([]string{"one", "two", "three"}); err != nil {
		t.Fatal(err)
	}
	keys, err := p.LoadPermanent()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 3 {
		t.Fatalf("loaded %d keys, want 3: %v", len(keys), keys)
	}
}
