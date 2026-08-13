package uploadinbox

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestReceiveStagesOpaquePrivatePayloadAndMetadata(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "inbox")
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := store.Receive(Input{
		EntryID: "entry-one", OriginalName: "release.txt", TargetDirectory: `C:\scripts`, ConflictPolicy: "rename",
	}, bytes.NewBufferString("release payload"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if pending.ID == "" || pending.SHA256 == "" || pending.Size != int64(len("release payload")) {
		t.Fatalf("pending = %#v", pending)
	}
	entries, err := os.ReadDir(filepath.Join(root, pending.ID))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != "" {
			t.Fatalf("staged file exposes an executable/content extension: %s", entry.Name())
		}
	}
	loaded, payload, err := store.Open(pending.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer payload.Close()
	content, _ := io.ReadAll(payload)
	if loaded.OriginalName != "release.txt" || string(content) != "release payload" {
		t.Fatalf("loaded=%#v content=%q", loaded, content)
	}
}

func TestReceiveRejectsOversizeAndUnsafeMetadata(t *testing.T) {
	t.Parallel()
	store, err := New(filepath.Join(t.TempDir(), "inbox"))
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []Input{
		{EntryID: "entry", OriginalName: "../payload.txt", TargetDirectory: "/srv/inbox", ConflictPolicy: "reject"},
		{EntryID: "entry", OriginalName: "CON.txt", TargetDirectory: "/srv/inbox", ConflictPolicy: "reject"},
		{EntryID: "entry\nnext", OriginalName: "payload.txt", TargetDirectory: "/srv/inbox", ConflictPolicy: "reject"},
		{EntryID: "entry", OriginalName: "payload.txt", TargetDirectory: "/srv/inbox", ConflictPolicy: "overwrite"},
	} {
		if _, err := store.Receive(input, bytes.NewBufferString("ok"), 10); err == nil {
			t.Fatalf("unsafe metadata accepted: %#v", input)
		}
	}
	if _, err := store.Receive(Input{EntryID: "entry", OriginalName: "payload.txt", TargetDirectory: "/srv/inbox", ConflictPolicy: "reject"}, bytes.NewBufferString("too large"), 3); err == nil {
		t.Fatal("oversize payload accepted")
	}
}

func TestRemoveRejectsUntrustedIdentifier(t *testing.T) {
	t.Parallel()
	store, err := New(filepath.Join(t.TempDir(), "inbox"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Remove("../escape"); err == nil {
		t.Fatal("path-like inbox identifier accepted")
	}
}

func TestClaimAllowsOnlyOnePublisherAndCanBeReleased(t *testing.T) {
	t.Parallel()
	store, err := New(filepath.Join(t.TempDir(), "inbox"))
	if err != nil {
		t.Fatal(err)
	}
	pending, err := store.Receive(Input{EntryID: "entry", OriginalName: "payload.txt", TargetDirectory: "/srv/inbox", ConflictPolicy: "reject"}, bytes.NewBufferString("payload"), 32)
	if err != nil {
		t.Fatal(err)
	}
	_, firstPayload, claim, err := store.Claim(pending.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := store.Claim(pending.ID); err == nil {
		t.Fatal("a second publisher claimed the same upload")
	}
	_ = firstPayload.Close()
	if err := claim.Release(); err != nil {
		t.Fatal(err)
	}
	_, secondPayload, secondClaim, err := store.Claim(pending.ID)
	if err != nil {
		t.Fatalf("released upload could not be reclaimed: %v", err)
	}
	_ = secondPayload.Close()
	if err := secondClaim.Complete(); err != nil {
		t.Fatal(err)
	}
}

func TestSealedClaimCannotBeReleasedAndIsRemovedDuringRecovery(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "inbox")
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := store.Receive(Input{EntryID: "entry", OriginalName: "payload.txt", TargetDirectory: "/srv/inbox", ConflictPolicy: "reject"}, bytes.NewBufferString("payload"), 32)
	if err != nil {
		t.Fatal(err)
	}
	_, payload, claim, err := store.Claim(pending.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = payload.Close()
	if err := claim.BeginPublication(); err != nil {
		t.Fatal(err)
	}
	if err := claim.Release(); err == nil {
		t.Fatal("sealed publication claim was released for duplicate publication")
	}
	if _, err := New(root); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Open(pending.ID); !os.IsNotExist(err) {
		t.Fatalf("sealed upload was restored after recovery: %v", err)
	}
}

func TestCancelledPublicationCanBeClaimedAgain(t *testing.T) {
	t.Parallel()
	store, err := New(filepath.Join(t.TempDir(), "inbox"))
	if err != nil {
		t.Fatal(err)
	}
	pending, err := store.Receive(Input{EntryID: "entry", OriginalName: "payload.txt", TargetDirectory: "/srv/inbox", ConflictPolicy: "reject"}, bytes.NewBufferString("payload"), 32)
	if err != nil {
		t.Fatal(err)
	}
	_, payload, claim, err := store.Claim(pending.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = payload.Close()
	if err := claim.BeginPublication(); err != nil {
		t.Fatal(err)
	}
	if err := claim.CancelPublication(); err != nil {
		t.Fatal(err)
	}
	_, payload, claim, err = store.Claim(pending.ID)
	if err != nil {
		t.Fatalf("cancelled publication could not be claimed again: %v", err)
	}
	_ = payload.Close()
	if err := claim.Complete(); err != nil {
		t.Fatal(err)
	}
}
