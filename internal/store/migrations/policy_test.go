package migrations

import "testing"

func TestCompatible(t *testing.T) {
	for version := 20; version <= 45; version++ {
		if !Compatible(45, version) {
			t.Fatalf("schema 45 should accept supported predecessor %d", version)
		}
	}
	for _, version := range []int{0, 19, 46} {
		if Compatible(45, version) {
			t.Fatalf("schema 45 unexpectedly accepts %d", version)
		}
	}
	if Compatible(46, 45) {
		t.Fatal("a new current schema requires an explicit migration policy")
	}
	if !Compatible(46, 46) {
		t.Fatal("a current database must always remain compatible")
	}
}
