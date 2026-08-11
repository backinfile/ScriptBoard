package runmanager

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExecutorTrustRequiresCanonicalRegularFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	executable := filepath.Join(root, "executor")
	if err := os.WriteFile(executable, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	protectExecutorFixture(t, executable)
	resolved, err := validateExecutorTrust(executable)
	if err != nil {
		t.Fatalf("trusted executor rejected: %v", err)
	}
	if !filepath.IsAbs(resolved) {
		t.Fatalf("resolved executor is not absolute: %s", resolved)
	}
	if _, err := validateExecutorTrust(root); err == nil {
		t.Fatal("directory was accepted as an executor")
	}
}

func TestArgumentsRejectControlCharactersWithoutBlockingScriptOptions(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"safe\n--next", "safe\r--next", "safe\x00--next", "safe\t--next"} {
		if _, err := ParseArguments(input); err == nil {
			t.Fatalf("control-bearing arguments accepted: %q", input)
		}
	}
	arguments, err := ParseArguments(`--mode safe`)
	if err != nil {
		t.Fatalf("ordinary script options rejected: %v", err)
	}
	if len(arguments) != 2 || arguments[0] != "--mode" {
		t.Fatalf("arguments = %#v", arguments)
	}
}

func FuzzParseArgumentsRejectsControlCharacters(f *testing.F) {
	for _, seed := range []string{"--mode safe", `"two words" --flag`, "safe\n--next", "safe\x00next"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		arguments, err := ParseArguments(input)
		if err != nil {
			return
		}
		for _, argument := range arguments {
			if err := validateProcessArgument(argument); err != nil {
				t.Fatalf("parser returned an unsafe argument %q", argument)
			}
		}
	})
}
