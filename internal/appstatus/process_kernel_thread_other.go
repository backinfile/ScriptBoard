//go:build !linux

package appstatus

import "context"

func processIsKernelThread(context.Context, int32) bool {
	return false
}
