package memorysettings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
	"scriptboard/internal/resourcelimits"
)

var ErrConflict = errors.New("memory settings changed; reload before saving")

type Snapshot struct {
	Memory   resourcelimits.Memory
	Revision string
}

func Path(root string) string { return filepath.Join(root, "runner-memory.db") }

func open(root string, writable bool) (*sql.DB, error) {
	if root == "" {
		return nil, errors.New("memory state root unavailable")
	}
	path := Path(root)
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return nil, errors.New("memory state must be a regular file")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	mode := "ro"
	if writable {
		mode = "rwc"
	}
	address := "file:" + strings.NewReplacer("%", "%25", "#", "%23", "?", "%3F").Replace(filepath.ToSlash(path)) + "?mode=" + mode + "&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", address)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err = db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
func Read(root string) (Snapshot, error) {
	if root == "" {
		return Snapshot{}, errors.New("memory state root unavailable")
	}
	if _, err := os.Lstat(Path(root)); os.IsNotExist(err) {
		return Snapshot{Memory: resourcelimits.Defaults(), Revision: "0"}, nil
	} else if err != nil {
		return Snapshot{}, err
	}
	db, err := open(root, false)
	if err != nil {
		return Snapshot{}, err
	}
	defer db.Close()
	var s Snapshot
	var revision int64
	err = db.QueryRow("SELECT total, per_run, process, swap, revision FROM memory_settings WHERE singleton=1").Scan(&s.Memory.Total, &s.Memory.PerRun, &s.Memory.Process, &s.Memory.Swap, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{Memory: resourcelimits.Defaults(), Revision: "0"}, nil
	}
	if err != nil {
		return Snapshot{}, err
	}
	s.Revision = strconv.FormatInt(revision, 10)
	if err = s.Memory.Validate(); err != nil {
		return Snapshot{}, fmt.Errorf("stored memory settings: %w", err)
	}
	return s, nil
}

// Ensure creates the durable policy once; later starts always use the stored values.
func Ensure(root string, seed resourcelimits.Memory) (Snapshot, error) {
	if _, err := os.Lstat(Path(root)); os.IsNotExist(err) {
		if err = os.MkdirAll(root, 0700); err != nil {
			return Snapshot{}, err
		}
		if err = Save(root, "0", seed.Resolved()); err != nil && !errors.Is(err, ErrConflict) {
			return Snapshot{}, err
		}
	} else if err != nil {
		return Snapshot{}, err
	}
	if err := shareWithRunner(root); err != nil {
		return Snapshot{}, err
	}
	return Read(root)
}

func Save(root, revision string, memory resourcelimits.Memory) error {
	if err := memory.Validate(); err != nil {
		return err
	}
	memory = memory.Resolved()
	expected, err := strconv.ParseInt(revision, 10, 64)
	if err != nil || expected < 0 {
		return ErrConflict
	}
	db, err := open(root, true)
	if err != nil {
		return err
	}
	defer db.Close()
	// DELETE journaling lets the isolated Runner read the policy without write access to SQLite sidecars.
	if _, err = db.Exec("PRAGMA journal_mode=DELETE"); err != nil {
		return err
	}
	if _, err = db.Exec("CREATE TABLE IF NOT EXISTS memory_settings (singleton INTEGER PRIMARY KEY CHECK(singleton=1), total TEXT NOT NULL, per_run TEXT NOT NULL, process TEXT NOT NULL, swap TEXT NOT NULL, revision INTEGER NOT NULL)"); err != nil {
		return err
	}
	if err := shareWithRunner(root); err != nil {
		return err
	}
	var result sql.Result
	if expected == 0 {
		result, err = db.Exec("INSERT OR IGNORE INTO memory_settings(singleton,total,per_run,process,swap,revision) VALUES(1,?,?,?,?,1)", memory.Total, memory.PerRun, memory.Process, memory.Swap)
	} else {
		result, err = db.Exec("UPDATE memory_settings SET total=?,per_run=?,process=?,swap=?,revision=revision+1 WHERE singleton=1 AND revision=?", memory.Total, memory.PerRun, memory.Process, memory.Swap, expected)
	}
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrConflict
	}
	return nil
}

// Backup copies a consistent SQLite snapshot; the policy has no credentials or user records.
func Backup(ctx context.Context, root, destination string) (bool, error) {
	if _, err := os.Lstat(Path(root)); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	db, err := open(root, false)
	if err != nil {
		return false, err
	}
	defer db.Close()
	_, err = db.ExecContext(ctx, "VACUUM INTO ?", destination)
	return err == nil, err
}
