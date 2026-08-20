package migrations

import "testing"

func TestCompatible(t *testing.T) {
	for version := 20; version <= 52; version++ {
		if !Compatible(53, version) {
			t.Fatalf("schema 53 should accept supported predecessor %d", version)
		}
	}
	for _, version := range []int{0, 19, 54} {
		if Compatible(53, version) {
			t.Fatalf("schema 53 unexpectedly accepts %d", version)
		}
	}
	if Compatible(54, 53) {
		t.Fatal("a new current schema requires an explicit migration policy")
	}
	if !Compatible(53, 53) {
		t.Fatal("a current database must always remain compatible")
	}
}
