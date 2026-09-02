package approval

import (
	"database/sql"
	"encoding/json"
)

const permanentSettingsKey = "approval_permanent"

// SQLitePersistence stores permanent approvals in the settings table as a
// JSON array, replacing Python's flat-file allowlist. One row keeps the
// schema unchanged and makes import/backup trivial.
type SQLitePersistence struct {
	db *sql.DB
}

// NewSQLitePersistence wraps a *sql.DB for permanent-approval persistence.
func NewSQLitePersistence(db *sql.DB) *SQLitePersistence {
	return &SQLitePersistence{db: db}
}

// SavePermanent merges keys into the persisted union, then writes it back.
func (p *SQLitePersistence) SavePermanent(keys []string) error {
	if p == nil || p.db == nil || len(keys) == 0 {
		return nil
	}
	existing, err := p.LoadPermanent()
	if err != nil {
		existing = nil
	}
	seen := make(map[string]bool, len(existing)+len(keys))
	for _, k := range existing {
		seen[k] = true
	}
	for _, k := range keys {
		if k != "" {
			seen[k] = true
		}
	}
	merged := make([]string, 0, len(seen))
	for k := range seen {
		merged = append(merged, k)
	}
	b, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	_, err = p.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, permanentSettingsKey, string(b))
	return err
}

// LoadPermanent returns the persisted permanent approval keys.
func (p *SQLitePersistence) LoadPermanent() ([]string, error) {
	if p == nil || p.db == nil {
		return nil, nil
	}
	var raw string
	err := p.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, permanentSettingsKey).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var keys []string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return nil, err
	}
	return keys, nil
}
