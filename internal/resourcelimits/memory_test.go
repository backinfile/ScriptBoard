package resourcelimits

import "testing"

func TestMemoryParsing(t *testing.T) {
	for value, want := range map[string]uint64{"512MiB": 512 << 20, "8GiB": 8 << 30, "unlimited": 0, "1024": 1024, "1TiB": 1 << 40} {
		got, err := Parse(value)
		if err != nil || got != want {
			t.Fatalf("%s: %d %v", value, got, err)
		}
	}
	for _, value := range []string{"0", "-1GiB", "1.5GiB", "9223372036854775808", "8388608TiB", "10GB", "infinity", "", "1GiB\nTasksMax=infinity"} {
		if _, err := Parse(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
	if got, err := Task("  "); got != "" || err != nil {
		t.Fatal("inherit rejected")
	}
}
