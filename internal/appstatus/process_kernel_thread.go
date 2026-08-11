package appstatus

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v4/common"
)

func linuxProcessStatusIsKernelThread(status []byte) bool {
	for _, line := range bytes.Split(status, []byte{'\n'}) {
		field, value, found := bytes.Cut(line, []byte{':'})
		if found && bytes.Equal(bytes.TrimSpace(field), []byte("Kthread")) {
			return bytes.Equal(bytes.TrimSpace(value), []byte("1"))
		}
	}
	return false
}

func linuxProcessIsKernelThread(ctx context.Context, pid int32) bool {
	procRoot := "/proc"
	configured := ""
	if environment, ok := ctx.Value(common.EnvKey).(common.EnvMap); ok {
		configured = strings.TrimSpace(environment[common.HostProcEnvKey])
	}
	if configured == "" {
		configured = strings.TrimSpace(os.Getenv("HOST_PROC"))
	}
	if configured != "" {
		procRoot = configured
	}
	status, err := os.ReadFile(filepath.Join(procRoot, strconv.FormatInt(int64(pid), 10), "status"))
	if err != nil {
		return false
	}
	return linuxProcessStatusIsKernelThread(status)
}
