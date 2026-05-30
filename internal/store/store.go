// Package store provides Toolmux's local SQLite-backed history: a persistent
// record of tool calls made through `toolmux mcp serve`, designed so workflow
// executions (runs and per-step input/output) can be recorded in the same
// database later without a schema redesign.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // pure-Go driver registered as "sqlite"
)

// Store is a handle to the Toolmux history database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if necessary) the SQLite database at path, applies the
// connection pragmas, and runs migrations. The parent directory is created if
// missing.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// A single connection avoids in-process "database is locked" contention;
	// WAL + busy_timeout let separate Toolmux processes share the file safely.
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("apply %q: %w", pragma, err)
		}
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}
