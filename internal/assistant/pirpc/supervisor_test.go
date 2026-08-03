package pirpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSupervisorStopsOnlyTheSelectedManagedProcess(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	supervisor := NewSupervisor(2)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = supervisor.Close(ctx)
	})
	newSpec := func(directory string) LaunchSpec {
		return LaunchSpec{
			Executable: executable,
			Args:       []string{"-test.run=TestPiRPCSupervisorHelperProcess", "--"},
			Env:        append(os.Environ(), "SCRIPTBOARD_PI_RPC_HELPER=1"),
			Workspace:  directory,
		}
	}
	first, err := supervisor.Start("first", newSpec(filepath.Join(t.TempDir(), "first")))
	if err != nil {
		t.Fatal(err)
	}
	second, err := supervisor.Start("second", newSpec(filepath.Join(t.TempDir(), "second")))
	if err != nil {
		t.Fatal(err)
	}
	if supervisor.Active() != 2 {
		t.Fatalf("active = %d", supervisor.Active())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := first.Client().Prompt(ctx, "prompt-first", "first"); err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Stop(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	if supervisor.Active() != 1 {
		t.Fatalf("active after first stop = %d", supervisor.Active())
	}
	if _, err := second.Client().Prompt(ctx, "prompt-second", "second"); err != nil {
		t.Fatalf("second process was affected: %v", err)
	}
	if err := supervisor.Stop(ctx, "second"); err != nil {
		t.Fatal(err)
	}
	if supervisor.Active() != 0 {
		t.Fatalf("active after stop = %d", supervisor.Active())
	}
}

func TestSupervisorEnforcesCapacityAndDuplicateKeys(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	supervisor := NewSupervisor(1)
	t.Cleanup(func() {
		closeContext, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer closeCancel()
		_ = supervisor.Close(closeContext)
	})
	spec := LaunchSpec{Executable: executable, Args: []string{"-test.run=TestPiRPCSupervisorHelperProcess", "--"}, Env: append(os.Environ(), "SCRIPTBOARD_PI_RPC_HELPER=1"), Workspace: filepath.Dir(executable)}
	if _, err := supervisor.Start("one", spec); err != nil {
		t.Fatal(err)
	}
	if _, err := supervisor.Start("one", spec); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("duplicate error = %v", err)
	}
	if _, err := supervisor.Start("two", spec); !errors.Is(err, ErrCapacity) {
		t.Fatalf("capacity error = %v", err)
	}
}

func TestPiRPCSupervisorHelperProcess(t *testing.T) {
	if os.Getenv("SCRIPTBOARD_PI_RPC_HELPER") != "1" {
		return
	}
	reader := bufio.NewScanner(os.Stdin)
	for reader.Scan() {
		var command Command
		if err := json.Unmarshal(reader.Bytes(), &command); err != nil {
			os.Exit(2)
		}
		switch command.Type {
		case "prompt":
			_, _ = fmt.Fprintf(os.Stdout, "{\"id\":%q,\"type\":\"response\",\"command\":\"prompt\",\"success\":true}\n", command.ID)
			_, _ = fmt.Fprintln(os.Stdout, "{\"type\":\"agent_settled\"}")
		case "abort":
			_, _ = fmt.Fprintf(os.Stdout, "{\"id\":%q,\"type\":\"response\",\"command\":\"abort\",\"success\":true}\n", command.ID)
			os.Exit(0)
		default:
			_, _ = fmt.Fprintf(os.Stdout, "{\"id\":%q,\"type\":\"response\",\"command\":%q,\"success\":false}\n", command.ID, command.Type)
		}
	}
	os.Exit(0)
}
