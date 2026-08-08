package platformservice

import (
	"errors"
	"testing"
)

func TestUninstallDoesNotRemoveDefinitionWhenStopFails(t *testing.T) {
	t.Parallel()

	stopErr := errors.New("service did not stop")
	removed := false
	reloaded := false
	err := uninstallService(
		func() error { return stopErr },
		func() error { removed = true; return nil },
		func() error { reloaded = true; return nil },
	)
	if !errors.Is(err, stopErr) {
		t.Fatalf("uninstall error = %v, want %v", err, stopErr)
	}
	if removed || reloaded {
		t.Fatalf("remove = %t, reload = %t after stop failure; want both false", removed, reloaded)
	}
}
