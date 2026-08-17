package migrations

import "testing"

func TestCompatible(t *testing.T) {
	for version := 20; version <= 49; version++ {
		if !Compatible(49, version) {
			t.Fatalf("schema 49 should accept supported predecessor %d", version)
		}
	}
	for _, version := range []int{0, 19, 50} {
		if Compatible(49, version) {
			t.Fatalf("schema 49 unexpectedly accepts %d", version)
		}
	}
	if Compatible(50, 49) {
		t.Fatal("a new current schema requires an explicit migration policy")
	}
	if !Compatible(49, 49) {
		t.Fatal("a current database must always remain compatible")
	}
}
