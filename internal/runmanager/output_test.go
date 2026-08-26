package runmanager

import (
	"bufio"
	"strings"
	"testing"
)

func TestReadRunOutputKeepsLogLinesInSeparateEvents(t *testing.T) {
	t.Parallel()

	reader := bufio.NewReaderSize(strings.NewReader("WARNING cache nearing capacity\nERROR fixture error marker\n"), 32<<10)
	var events []string
	for {
		output, done := readRunOutputChunk(reader)
		if len(output) > 0 {
			events = append(events, string(output))
		}
		if done {
			break
		}
	}

	want := []string{"WARNING cache nearing capacity\n", "ERROR fixture error marker\n"}
	if len(events) != len(want) {
		t.Fatalf("events = %q, want %q", events, want)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Fatalf("event %d = %q, want %q", index, events[index], want[index])
		}
	}
}
