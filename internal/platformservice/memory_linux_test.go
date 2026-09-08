//go:build linux

package platformservice

import (
	"scriptboard/internal/resourcelimits"
	"strings"
	"testing"
)

func TestRunnerMemoryUnitUpgradePreservesPolicy(t *testing.T) {
	old := []byte("[Service]\nUser=scriptboard-runner\nProtectControlGroups=true\nReadWritePaths=/custom/path\nMemoryMax=2G\nMemorySwapMax=0\nTasksMax=64\n")
	memory := resourcelimits.Defaults()
	memory.Total = "8GiB"
	memory.Swap = "512MiB"
	updated := runnerMemoryUnitText(old, memory)
	for _, wanted := range []string{"MemoryMax=8G", "MemorySwapMax=536870912", "TasksMax=64", "ProtectControlGroups=true", "ReadWritePaths=/custom/path", "Delegate=memory"} {
		if !strings.Contains(updated, wanted) {
			t.Fatalf("missing %s: %s", wanted, updated)
		}
	}
	if again := runnerMemoryUnitText([]byte(updated), memory); again != updated {
		t.Fatal("repeated restart rewrites policy")
	}
	memory.Total = "unlimited"
	memory.Swap = "unlimited"
	updated = runnerMemoryUnitText([]byte(updated), memory)
	if !strings.Contains(updated, "MemoryMax=infinity") || !strings.Contains(updated, "MemorySwapMax=infinity") {
		t.Fatal(updated)
	}
}
