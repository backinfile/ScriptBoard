package externalapproval

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStagedUploadCanBeClaimedOnlyOnce(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id := "abcdefghijklmnopqrstuvwx"
	staged, err := store.Stage(id, strings.NewReader("approved payload"), 64)
	if err != nil || staged.Size != 16 || len(staged.SHA256) != 64 {
		t.Fatalf("staged=%#v error=%v", staged, err)
	}
	payload, claim, err := store.Claim(id)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := io.ReadAll(payload)
	_ = payload.Close()
	if string(content) != "approved payload" {
		t.Fatalf("payload=%q", content)
	}
	if _, _, err := store.Claim(id); err == nil {
		t.Fatal("staged upload could be claimed twice")
	}
	if err := claim.Complete(); err != nil {
		t.Fatal(err)
	}
}

func TestPendingUploadCanBePreviewedWithoutClaimingIt(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id := "previewpendingupload1234"
	if _, err := store.Stage(id, strings.NewReader("first line\nsecond line\n"), 128); err != nil {
		t.Fatal(err)
	}
	preview, truncated, err := store.Preview(id, 10)
	if err != nil {
		t.Fatal(err)
	}
	if string(preview) != "first line" || !truncated {
		t.Fatalf("preview=%q truncated=%v", preview, truncated)
	}
	payload, claim, err := store.Claim(id)
	if err != nil {
		t.Fatalf("preview claimed or removed payload: %v", err)
	}
	_ = payload.Close()
	_ = claim.Complete()
	if _, _, err := store.Preview("../outside", 10); err == nil {
		t.Fatal("path traversal identifier was accepted")
	}
}

func TestPendingUploadCanBeOpenedForDownloadWithoutClaimingIt(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id := "downloadpendingupload1"
	if _, err := store.Stage(id, strings.NewReader("download body"), 128); err != nil {
		t.Fatal(err)
	}
	payload, err := store.Open(id)
	if err != nil {
		t.Fatal(err)
	}
	downloaded, readErr := io.ReadAll(payload)
	closeErr := payload.Close()
	if readErr != nil || closeErr != nil || string(downloaded) != "download body" {
		t.Fatalf("download=%q read=%v close=%v", downloaded, readErr, closeErr)
	}
	claimed, claim, err := store.Claim(id)
	if err != nil {
		t.Fatalf("download claimed or removed payload: %v", err)
	}
	_ = claimed.Close()
	_ = claim.Complete()
}

func TestRetainRemovesOrphanedPayloadsAfterRecovery(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	keepID, removeID := "abcdefghijklmnopqrstuvwx", "zyxwvutsrqponmlkjihgfedc"
	for _, id := range []string{keepID, removeID} {
		if _, err := store.Stage(id, strings.NewReader(id), 64); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Retain(map[string]bool{keepID: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, keepID, "payload")); err != nil {
		t.Fatalf("pending payload removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, removeID)); !os.IsNotExist(err) {
		t.Fatalf("orphaned payload remains: %v", err)
	}
}
