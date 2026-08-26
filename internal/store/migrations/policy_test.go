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
	if !Compatible(58, 57) {
		t.Fatal("schema 58 should accept schema 57 after the approval migration was declared")
	}
	for version := 20; version <= 58; version++ {
		if !Compatible(59, version) {
			t.Fatalf("schema 59 should accept supported predecessor %d", version)
		}
	}
	if !Compatible(57, 57) {
		t.Fatal("a current database must always remain compatible")
	}
}
