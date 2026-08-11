//go:build linux

package appstatus

import "context"

func processIsKernelThread(ctx context.Context, pid int32) bool {
	return linuxProcessIsKernelThread(ctx, pid)
}
