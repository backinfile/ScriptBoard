package appstatus

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shirou/gopsutil/v4/common"
)

func TestLinuxProcessStatusMarksOnlyKernelThreads(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status string
		want   bool
	}{
		{name: "kernel thread", status: "Name:\tkworker/R-crypt\nKthread:\t1\n", want: true},
		{name: "user process", status: "Name:\trestricted-agent\nKthread:\t0\n", want: false},
		{name: "older kernel without field", status: "Name:\trestricted-agent\nThreads:\t2\n", want: false},
		{name: "malformed field", status: "Name:\tkworker/R-crypt\nKthread:\tunknown\n", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := linuxProcessStatusIsKernelThread([]byte(test.status)); got != test.want {
				t.Fatalf("kernel thread = %t, want %t", got, test.want)
			}
		})
	}
}

func TestLinuxProcessKernelThreadReadsTheConfiguredProcRoot(t *testing.T) {
	t.Parallel()

	procRoot := t.TempDir()
	processRoot := filepath.Join(procRoot, "17")
	if err := os.MkdirAll(processRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(processRoot, "status"),
		[]byte("Name:\tkworker/R-crypt\nKthread:\t1\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), common.EnvKey, common.EnvMap{
		common.HostProcEnvKey: procRoot,
	})

	if !linuxProcessIsKernelThread(ctx, 17) {
		t.Fatal("kernel thread in configured proc root was not identified")
	}
	if linuxProcessIsKernelThread(ctx, 18) {
		t.Fatal("unreadable process status was classified as a kernel thread")
	}
}
