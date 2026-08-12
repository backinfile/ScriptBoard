package passkey

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"
)

func TestStoreEncryptsCredentialsAndPersistsCounterUpdates(t *testing.T) {
	root := t.TempDir()
	store, err := New(Options{StateRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	credential := webauthn.Credential{ID: []byte("credential-id"), PublicKey: []byte("credential-public-key")}
	credential.Authenticator.SignCount = 1
	if err := store.Add("user-one", "Windows Hello", credential); err != nil {
		t.Fatal(err)
	}
	if err := store.Add("user-two", "duplicate", credential); err != ErrDuplicateCredential {
		t.Fatalf("duplicate error=%v", err)
	}
	views, err := store.List("user-one")
	if err != nil || len(views) != 1 || views[0].Name != "Windows Hello" {
		t.Fatalf("views=%#v err=%v", views, err)
	}
	user, err := store.User("user-one", "alice")
	if err != nil || len(user.Credentials) != 1 || user.Credentials[0].Authenticator.SignCount != 1 {
		t.Fatalf("user=%#v err=%v", user, err)
	}
	credential.Authenticator.SignCount = 9
	if err := store.Update("user-one", credential); err != nil {
		t.Fatal(err)
	}
	user, err = store.User("user-one", "alice")
	if err != nil || user.Credentials[0].Authenticator.SignCount != 9 {
		t.Fatalf("updated user=%#v err=%v", user, err)
	}
	replaced := credential
	replaced.PublicKey = []byte("attacker-controlled-key")
	if err := store.Update("user-one", replaced); err != ErrCredentialIdentityMismatch {
		t.Fatalf("public key replacement error=%v", err)
	}
	user, err = store.User("user-one", "alice")
	if err != nil || !bytes.Equal(user.Credentials[0].PublicKey, credential.PublicKey) {
		t.Fatalf("credential public key changed after rejected update: user=%#v err=%v", user, err)
	}
	replaced = credential
	replaced.Flags.BackupEligible = !credential.Flags.BackupEligible
	if err := store.Update("user-one", replaced); err != ErrCredentialIdentityMismatch {
		t.Fatalf("backup eligibility replacement error=%v", err)
	}

	sealed, err := os.ReadFile(filepath.Join(root, "secrets", "account-passkeys.enc"))
	if err != nil {
		t.Fatal(err)
	}
	for _, plaintext := range [][]byte{credential.ID, credential.PublicKey, []byte("Windows Hello")} {
		if bytes.Contains(sealed, plaintext) {
			t.Fatalf("sealed passkey state contains plaintext %q", plaintext)
		}
	}
	if err := store.Delete("user-one", views[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete("user-one", views[0].ID); err != ErrCredentialNotFound {
		t.Fatalf("missing delete error=%v", err)
	}
}
