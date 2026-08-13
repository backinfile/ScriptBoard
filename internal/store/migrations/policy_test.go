package migrations

import "testing"

func TestCompatible(t *testing.T) {
	for version := 20; version <= 48; version++ {
		if !Compatible(48, version) {
			t.Fatalf("schema 48 should accept supported predecessor %d", version)
		}
	}
	for _, version := range []int{0, 19, 49} {
		if Compatible(48, version) {
			t.Fatalf("schema 48 unexpectedly accepts %d", version)
		}
	}
	if Compatible(49, 48) {
		t.Fatal("a new current schema requires an explicit migration policy")
	}
	if !Compatible(48, 48) {
		t.Fatal("a current database must always remain compatible")
	}
}
