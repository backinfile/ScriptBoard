package main

import (
	"path/filepath"
	"testing"
)

func TestPinnedRuntimeLockIsComplete(t *testing.T) {
	lock, err := readRuntimeLock(filepath.Join("..", "..", "runtime", "pi-runtime-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	if lock.Version != "0.83.0" || len(lock.Assets) != 4 {
		t.Fatalf("runtime lock = %#v", lock)
	}
	for _, platform := range [][2]string{{"windows", "amd64"}, {"windows", "arm64"}, {"linux", "amd64"}, {"linux", "arm64"}} {
		if _, ok := lock.assetFor(platform[0], platform[1]); !ok {
			t.Fatalf("missing lock for %s/%s", platform[0], platform[1])
		}
	}
}
