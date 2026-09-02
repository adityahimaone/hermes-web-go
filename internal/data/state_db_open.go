package data

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// OpenStateDB opens Hermes state.db read-only. It never creates or mutates the
// source database.
func OpenStateDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro", path))
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
