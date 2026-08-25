package migrations

import "testing"

func TestCompatible(t *testing.T) {
	for version := 20; version <= 55; version++ {
		if !Compatible(56, version) {
			t.Fatalf("schema 56 should accept supported predecessor %d", version)
		}
	}
	for _, version := range []int{0, 19, 57} {
		if Compatible(56, version) {
			t.Fatalf("schema 56 unexpectedly accepts %d", version)
		}
	}
	if Compatible(57, 56) {
		t.Fatal("a new current schema requires an explicit migration policy")
	}
	if !Compatible(56, 56) {
		t.Fatal("a current database must always remain compatible")
	}
}
