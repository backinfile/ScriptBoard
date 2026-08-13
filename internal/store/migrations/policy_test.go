package migrations

import "testing"

func TestCompatible(t *testing.T) {
	for version := 20; version <= 47; version++ {
		if !Compatible(47, version) {
			t.Fatalf("schema 47 should accept supported predecessor %d", version)
		}
	}
	for _, version := range []int{0, 19, 48} {
		if Compatible(47, version) {
			t.Fatalf("schema 47 unexpectedly accepts %d", version)
		}
	}
	if Compatible(48, 47) {
		t.Fatal("a new current schema requires an explicit migration policy")
	}
	if !Compatible(47, 47) {
		t.Fatal("a current database must always remain compatible")
	}
}
