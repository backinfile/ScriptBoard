//go:build !windows

package update

import "context"

func snapshotDatabase(_ context.Context, database, snapshot string) error {
	return copyFileSync(database, snapshot, 0o600)
}
