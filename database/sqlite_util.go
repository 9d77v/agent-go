package database

import (
	"database/sql"
	"fmt"
)

// Checkpoint performs a WAL checkpoint (TRUNCATE mode) on a SQLite database.
func Checkpoint(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}
	_, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}

// Vacuum reclaims unused space in a SQLite database.
func Vacuum(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}
	_, err := db.Exec("VACUUM")
	return err
}
