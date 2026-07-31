package pathsecurity

import "testing"

func TestUnsafeWindowsComponent(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"NUL", "con.txt", "COM1.log", "LPT9", "CLOCK$", "CONIN$.txt",
		"file.txt:stream", `bad"name`, "question?.txt", "trailing.", "trailing ", "line\nbreak", "NUL .txt",
	} {
		if !UnsafeWindowsComponent(name) {
			t.Errorf("unsafe component %q was accepted", name)
		}
	}
	for _, name := range []string{"scriptboard.exe", "console.txt", "com10.log", "report final.txt"} {
		if UnsafeWindowsComponent(name) {
			t.Errorf("safe component %q was rejected", name)
		}
	}
}
