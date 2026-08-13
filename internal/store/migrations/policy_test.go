package migrations

import "testing"

func TestCompatible(t *testing.T) {
	for version := 20; version <= 46; version++ {
		if !Compatible(46, version) {
			t.Fatalf("schema 46 should accept supported predecessor %d", version)
		}
	}
	for _, version := range []int{0, 19, 47} {
		if Compatible(46, version) {
			t.Fatalf("schema 46 unexpectedly accepts %d", version)
		}
	}
	if Compatible(47, 46) {
		t.Fatal("a new current schema requires an explicit migration policy")
	}
	if !Compatible(46, 46) {
		t.Fatal("a current database must always remain compatible")
	}
}
