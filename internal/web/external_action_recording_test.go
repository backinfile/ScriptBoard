package web

import (
	"errors"
	"testing"
)

func TestRecordedExternalActionDoesNotExecuteWhenInitialRecordFails(t *testing.T) {
	executed := 0
	execution := runRecordedExternalAction(
		func() error { return errors.New("database unavailable") },
		func() externalActionResult {
			executed++
			return externalActionResult{status: 202, result: "accepted"}
		},
		func(externalActionResult) error { return nil },
	)
	if execution.Started || executed != 0 || execution.RecordError == nil {
		t.Fatalf("execution=%#v executed=%d, want no action after initial record failure", execution, executed)
	}
}

func TestRecordedExternalActionPreservesResultWhenCompletionRecordFails(t *testing.T) {
	execution := runRecordedExternalAction(
		func() error { return nil },
		func() externalActionResult {
			return externalActionResult{status: 202, result: "accepted", runID: "run-1"}
		},
		func(externalActionResult) error { return errors.New("database unavailable") },
	)
	if !execution.Started || execution.Result.status != 202 || execution.Result.runID != "run-1" || execution.RecordError == nil {
		t.Fatalf("execution=%#v, want accepted result preserved after completion record failure", execution)
	}
}
