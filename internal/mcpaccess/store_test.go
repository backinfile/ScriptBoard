package mcpaccess

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"net"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func testStore(t *testing.T) (*Store, *sql.DB, *time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE users(id TEXT PRIMARY KEY,username TEXT NOT NULL,role TEXT NOT NULL,enabled INTEGER NOT NULL,auth_version INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range SchemaStatements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	return NewStore(db, func() time.Time { return now }), db, &now
}

func TestAuthorizationCodeAndRefreshRotation(t *testing.T) {
	store, db, now := testStore(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO users VALUES('u1','operator','operator',1,7)`); err != nil {
		t.Fatal(err)
	}
	client, err := store.RegisterClient(ctx, "Codex", []string{"http://127.0.0.1/callback"}, "dcr", "")
	if err != nil {
		t.Fatal(err)
	}
	verifier := "a-valid-verifier-value-with-more-than-forty-three-characters"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	resource := "https://panel.example/mcp"
	code, err := store.IssueCode(ctx, "u1", client.ClientID, "http://127.0.0.1:49152/callback", resource, challenge, []string{ScopeExecute})
	if err != nil {
		t.Fatal(err)
	}
	set, err := store.ExchangeCode(ctx, code, client.ClientID, "http://127.0.0.1:49152/callback", resource, verifier)
	if err != nil {
		t.Fatal(err)
	}
	if set.AccessToken == "" || set.RefreshToken == "" {
		t.Fatal("tokens were not issued")
	}
	if _, err := store.ExchangeCode(ctx, code, client.ClientID, "http://127.0.0.1:49152/callback", resource, verifier); err != ErrInvalidGrant {
		t.Fatalf("code replay error=%v", err)
	}
	principal, err := store.Authenticate(ctx, set.AccessToken, resource)
	if err != nil || !principal.Scopes[ScopeExecute] || !principal.Scopes[ScopeObserve] {
		t.Fatalf("principal=%+v err=%v", principal, err)
	}
	*now = now.Add(time.Minute)
	rotated, err := store.Refresh(ctx, set.RefreshToken, client.ClientID, resource)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.RefreshToken == set.RefreshToken {
		t.Fatal("refresh token was not rotated")
	}
	if _, err := store.Refresh(ctx, set.RefreshToken, client.ClientID, resource); err != ErrInvalidGrant {
		t.Fatalf("refresh reuse error=%v", err)
	}
	if _, err := store.Authenticate(ctx, rotated.AccessToken, resource); err != ErrInvalidToken {
		t.Fatalf("family reuse did not revoke access: %v", err)
	}
	var leaked int
	if err := db.QueryRow(`SELECT COUNT(*) FROM mcp_oauth_access_tokens WHERE token_hash=? OR token_hint=?`, set.AccessToken, set.AccessToken).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatal("complete token was persisted")
	}
}

func TestRedirectURIMatching(t *testing.T) {
	if !ValidateRedirectURI("http://127.0.0.1/callback", "http://127.0.0.1:54321/callback") {
		t.Fatal("loopback port variation rejected")
	}
	for _, requested := range []string{"http://127.0.0.2/callback", "http://127.0.0.1/other", "http://127.0.0.1/callback?extra=1", "https://client.example/callback/"} {
		if ValidateRedirectURI("https://client.example/callback", requested) {
			t.Fatalf("accepted redirect %q", requested)
		}
	}
}

func TestCIMDRejectsNonPublicAddresses(t *testing.T) {
	for _, value := range []string{"127.0.0.1", "::1", "10.0.0.2", "172.16.2.2", "192.168.1.2", "169.254.1.1", "0.0.0.0"} {
		if publicIP(net.ParseIP(value)) {
			t.Fatalf("accepted non-public IP %s", value)
		}
	}
	if !publicIP(net.ParseIP("203.0.113.9")) {
		t.Fatal("public documentation address rejected")
	}
}

func TestCurrentUserStateInvalidatesAccessToken(t *testing.T) {
	store, db, _ := testStore(t)
	ctx := context.Background()
	_, _ = db.Exec(`INSERT INTO users VALUES('u1','operator','operator',1,1)`)
	client, _ := store.RegisterClient(ctx, "client", []string{"https://client.example/callback"}, "dcr", "")
	verifier := "a-valid-verifier-value-with-more-than-forty-three-characters"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	code, _ := store.IssueCode(ctx, "u1", client.ClientID, "https://client.example/callback", "https://panel/mcp", challenge, []string{ScopeExecute})
	set, _ := store.ExchangeCode(ctx, code, client.ClientID, "https://client.example/callback", "https://panel/mcp", verifier)
	_, _ = db.Exec(`UPDATE users SET auth_version=2 WHERE id='u1'`)
	if _, err := store.Authenticate(ctx, set.AccessToken, "https://panel/mcp"); err != ErrInvalidToken {
		t.Fatalf("auth_version change error=%v", err)
	}
}
