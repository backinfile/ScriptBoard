package migrations

import "testing"

func TestCompatible(t *testing.T) {
	for version := 20; version <= 51; version++ {
		if !Compatible(52, version) {
			t.Fatalf("schema 52 should accept supported predecessor %d", version)
		}
	}
	for _, version := range []int{0, 19, 53} {
		if Compatible(52, version) {
			t.Fatalf("schema 52 unexpectedly accepts %d", version)
		}
	}
	if Compatible(53, 52) {
		t.Fatal("a new current schema requires an explicit migration policy")
	}
	if !Compatible(52, 52) {
		t.Fatal("a current database must always remain compatible")
	}
}
