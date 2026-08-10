package app

import "testing"

func TestHumanBytesAcceptsDatabaseSignedSizes(t *testing.T) {
	if got := humanBytes(int64(1586)); got != "1.5 KiB" {
		t.Fatalf("humanBytes(int64(1586)) = %q", got)
	}
	if got := humanBytes(int64(-1)); got != "0 B" {
		t.Fatalf("humanBytes(int64(-1)) = %q", got)
	}
}
