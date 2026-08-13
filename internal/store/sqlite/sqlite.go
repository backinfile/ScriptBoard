package sqlite

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	_ "modernc.org/sqlite"
)

// Compatibility decides whether an existing schema may be opened for
// migration. It is evaluated from the SQLite header before a writable
// connection is created.
type Compatibility func(version int) bool

// OpenSQLite performs the safety-sensitive part of opening the application
// database. It returns the connected database and the schema version observed
// before migrations run.
func OpenSQLite(path string, currentVersion int, compatible Compatibility) (*sql.DB, int, error) {
	if currentVersion < 0 {
		return nil, 0, errors.New("current SQLite schema version cannot be negative")
	}
	if compatible == nil {
		return nil, 0, errors.New("SQLite compatibility policy is required")
	}
	info, statErr := os.Stat(path)
	if statErr != nil && !os.IsNotExist(statErr) {
		return nil, 0, fmt.Errorf("inspect SQLite database: %w", statErr)
	}
	if statErr == nil && info.Size() > 0 {
		version, err := HeaderUserVersion(path)
		if err != nil {
			return nil, 0, fmt.Errorf("inspect existing SQLite schema without modifying it: %w", err)
		}
		if !compatible(version) {
			return nil, 0, fmt.Errorf("database schema version %d is incompatible with schema %d; use a new State Root", version, currentVersion)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, 0, fmt.Errorf("open SQLite: %w", err)
	}
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=FULL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			return nil, 0, fmt.Errorf("configure SQLite: %w", err)
		}
	}
	var integrity string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		_ = db.Close()
		return nil, 0, fmt.Errorf("SQLite integrity check failed: result=%q error=%v", integrity, err)
	}
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		_ = db.Close()
		return nil, 0, fmt.Errorf("read SQLite schema version: %w", err)
	}
	return db, version, nil
}

// Checkpoint persists page one so the next startup can reject an incompatible
// database by inspecting its header without opening it in writable mode.
func Checkpoint(db *sql.DB) error {
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("checkpoint SQLite schema: %w", err)
	}
	return nil
}

// ColumnExists probes a migration transaction without exposing PRAGMA row
// parsing to domain migration code.
func ColumnExists(transaction *sql.Tx, table, column string) (bool, error) {
	if !sqliteIdentifier(table) || !sqliteIdentifier(column) {
		return false, errors.New("invalid SQLite identifier")
	}
	rows, err := transaction.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var position, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&position, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// HeaderUserVersion reads SQLite's user_version field without acquiring a
// database connection or allowing SQLite to modify the file.
func HeaderUserVersion(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	header := make([]byte, 100)
	if _, err := io.ReadFull(file, header); err != nil {
		return 0, fmt.Errorf("read SQLite header: %w", err)
	}
	if !bytes.Equal(header[:16], []byte("SQLite format 3\x00")) {
		return 0, errors.New("file does not contain a SQLite 3 header")
	}
	return int(binary.BigEndian.Uint32(header[60:64])), nil
}

func sqliteIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character == '_' || index > 0 && character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return true
}
