package migrations

import "testing"

func TestCompatible(t *testing.T) {
	for version := 20; version <= 53; version++ {
		if !Compatible(54, version) {
			t.Fatalf("schema 54 should accept supported predecessor %d", version)
		}
	}
	for _, version := range []int{0, 19, 55} {
		if Compatible(54, version) {
			t.Fatalf("schema 54 unexpectedly accepts %d", version)
		}
	}
	if Compatible(55, 54) {
		t.Fatal("a new current schema requires an explicit migration policy")
	}
	if !Compatible(54, 54) {
		t.Fatal("a current database must always remain compatible")
	}
}
