package migrations

import "testing"

func TestCompatible(t *testing.T) {
	for version := 20; version <= 56; version++ {
		if !Compatible(57, version) {
			t.Fatalf("schema 57 should accept supported predecessor %d", version)
		}
	}
	for _, version := range []int{0, 19, 58} {
		if Compatible(57, version) {
			t.Fatalf("schema 57 unexpectedly accepts %d", version)
		}
	}
	if Compatible(58, 57) {
		t.Fatal("a new current schema requires an explicit migration policy")
	}
	if !Compatible(57, 57) {
		t.Fatal("a current database must always remain compatible")
	}
}
