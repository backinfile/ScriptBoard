//go:build windows

package processlaunch

import (
	"context"
	"testing"
)

func TestPrepareAcceptsWindowsHiddenDriveEnvironmentEntry(t *testing.T) {
	command, err := Prepare(Spec{
		Context: context.Background(), Executable: "powershell.exe",
		Environment: EnvironmentExact,
		Env:         []string{`=C:=C:\workspace`, `SystemRoot=C:\Windows`},
	})
	if err != nil {
		t.Fatalf("Windows process environment rejected an OS-provided drive entry: %v", err)
	}
	if len(command.Env) != 2 || command.Env[0] != `=C:=C:\workspace` {
		t.Fatalf("Windows drive entry was not preserved: %#v", command.Env)
	}
}
