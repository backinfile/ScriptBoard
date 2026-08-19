package migrations

import "testing"

func TestCompatible(t *testing.T) {
	for version := 20; version <= 50; version++ {
		if !Compatible(51, version) {
			t.Fatalf("schema 51 should accept supported predecessor %d", version)
		}
	}
	for _, version := range []int{0, 19, 52} {
		if Compatible(51, version) {
			t.Fatalf("schema 51 unexpectedly accepts %d", version)
		}
	}
	if Compatible(52, 51) {
		t.Fatal("a new current schema requires an explicit migration policy")
	}
	if !Compatible(51, 51) {
		t.Fatal("a current database must always remain compatible")
	}
}
