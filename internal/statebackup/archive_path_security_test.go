package statebackup

import (
	"scriptboard/internal/testsupport/securitycorpus"
	"testing"
)

func TestStateBackupRejectsSharedUnsafeArchivePathCorpus(t *testing.T) {
	t.Parallel()
	for _, name := range securitycorpus.UnsafeArchivePaths() {
		if _, err := validateArchivePath(name); err == nil {
			t.Fatalf("unsafe state backup path %q was accepted", name)
		}
	}
}
