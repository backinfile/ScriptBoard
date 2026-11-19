package update

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func snapshotDatabase(ctx context.Context, database, snapshot string) error {
	state, err := os.OpenRoot(filepath.Dir(database))
	if err != nil {
		return err
	}
	defer state.Close()

	if err := copyDatabaseFile(state, filepath.Base(database), snapshot); err != nil {
		return err
	}
	if err := copyDatabaseFile(state, filepath.Base(database)+"-wal", snapshot+"-wal"); err != nil && !os.IsNotExist(err) {
		return err
	}

	// Replay any SQLite WAL inside protected storage. Only the resulting
	// standalone database is used for rollback.
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(snapshot)+"?mode=rw")
	if err != nil {
		return err
	}
	var busy, frames, checkpointed int
	err = db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &frames, &checkpointed)
	closeErr := db.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if busy != 0 {
		return errors.New("SQLite update snapshot WAL could not be checkpointed")
	}
	return verifyDatabaseSnapshot(snapshot)
}

func copyDatabaseFile(state *os.Root, name, destination string) error {
	info, err := state.Lstat(name)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("update snapshot source is not a regular state file")
	}
	input, err := state.Open(name)
	if err != nil {
		return err
	}
	defer input.Close()
	var handleInfo windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(input.Fd()), &handleInfo); err != nil {
		return err
	}
	openedInfo, err := input.Stat()
	if err != nil {
		return err
	}
	if handleInfo.NumberOfLinks != 1 || !os.SameFile(info, openedInfo) {
		return errors.New("update snapshot source is linked or changed")
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	return syncCopy(input, output)
}
