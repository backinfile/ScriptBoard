package migrations

import "testing"

func TestCompatible(t *testing.T) {
	for version := 20; version <= 49; version++ {
		if !Compatible(50, version) {
			t.Fatalf("schema 50 should accept supported predecessor %d", version)
		}
	}
	for _, version := range []int{0, 19, 51} {
		if Compatible(50, version) {
			t.Fatalf("schema 50 unexpectedly accepts %d", version)
		}
	}
	if Compatible(51, 50) {
		t.Fatal("a new current schema requires an explicit migration policy")
	}
	if !Compatible(50, 50) {
		t.Fatal("a current database must always remain compatible")
	}
}
