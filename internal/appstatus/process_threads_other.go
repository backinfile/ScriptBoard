//go:build !windows

package appstatus

import "context"

func snapshotThreadCounts(context.Context) (map[int32]int32, error) {
	return nil, nil
}
