package web

import (
	"testing"
	"time"

	"scriptboard/internal/hostfiles"
)

func TestPrepareFileListingSortsKnownCreationTimesBeforeUnavailableValues(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, time.September, 1, 8, 0, 0, 0, time.UTC)
	entries := []hostfiles.Entry{
		{Name: "unknown.txt", Path: "/unknown.txt", Kind: hostfiles.Regular},
		{Name: "newer.txt", Path: "/newer.txt", Kind: hostfiles.Regular, CreatedAt: base.Add(time.Hour)},
		{Name: "older.txt", Path: "/older.txt", Kind: hostfiles.Regular, CreatedAt: base},
	}

	listing := prepareFileListingWithContent(entries, "", "created", "asc", true, nil)
	want := []string{"older.txt", "newer.txt", "unknown.txt"}
	for index, name := range want {
		if listing[index].Name != name {
			t.Fatalf("created sort[%d]=%q, want %q; listing=%#v", index, listing[index].Name, name, listing)
		}
	}

	listing = prepareFileListingWithContent(entries, "", "created", "desc", true, nil)
	want = []string{"newer.txt", "older.txt", "unknown.txt"}
	for index, name := range want {
		if listing[index].Name != name {
			t.Fatalf("descending created sort[%d]=%q, want %q; listing=%#v", index, listing[index].Name, name, listing)
		}
	}
}

func TestNormalizeFileSortAcceptsCreationTime(t *testing.T) {
	t.Parallel()

	field, direction := normalizeFileSort("created", "desc")
	if field != "created" || direction != "desc" {
		t.Fatalf("normalizeFileSort(created, desc)=(%q, %q), want (created, desc)", field, direction)
	}
}
