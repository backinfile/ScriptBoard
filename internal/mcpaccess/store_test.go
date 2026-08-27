package mcpaccess

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/netip"
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

type fixedCIMDResolver struct{ address netip.Addr }

func (resolver fixedCIMDResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{resolver.address}, nil
}

func TestCIMDUsesSharedOutboundAddressPolicy(t *testing.T) {
	store, _, _ := testStore(t)
	store.cimdResolver = fixedCIMDResolver{address: netip.MustParseAddr("93.184.216.34")}
	if _, err := store.Client(context.Background(), "https://public.example:80/client.json"); err != ErrInvalidClient {
		t.Fatalf("non-443 CIMD error=%v, want ErrInvalidClient", err)
	}
	for _, value := range []string{
		"127.0.0.1", "::1", "10.0.0.2", "172.16.2.2", "192.168.1.2", "169.254.1.1", "0.0.0.0",
		"100.64.0.1", "192.0.0.8", "198.18.0.1", "192.0.2.1", "203.0.113.9", "2001:db8::1",
	} {
		store.cimdResolver = fixedCIMDResolver{address: netip.MustParseAddr(value)}
		if _, err := store.Client(context.Background(), "https://blocked.example/client.json"); err != ErrInvalidClient {
			t.Fatalf("address %s error=%v, want ErrInvalidClient", value, err)
		}
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

func TestTokenRevocationReportsStorageFailureAndRollsBack(t *testing.T) {
	store, db, _ := testStore(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO users VALUES('u1','operator','operator',1,1)`); err != nil {
		t.Fatal(err)
	}
	client, err := store.RegisterClient(ctx, "client", []string{"https://client.example/callback"}, "dcr", "")
	if err != nil {
		t.Fatal(err)
	}
	verifier := "a-valid-verifier-value-with-more-than-forty-three-characters"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	code, err := store.IssueCode(ctx, "u1", client.ClientID, "https://client.example/callback", "https://panel/mcp", challenge, []string{ScopeExecute})
	if err != nil {
		t.Fatal(err)
	}
	set, err := store.ExchangeCode(ctx, code, client.ClientID, "https://client.example/callback", "https://panel/mcp", verifier)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_access_revocation BEFORE UPDATE OF revoked_at ON mcp_oauth_access_tokens
		BEGIN SELECT RAISE(FAIL, 'revocation unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.Revoke(ctx, set.AccessToken); err == nil {
		t.Fatal("revocation storage failure was reported as success")
	}
	if _, err := store.Authenticate(ctx, set.AccessToken, "https://panel/mcp"); err != nil {
		t.Fatalf("failed revocation changed token state: %v", err)
	}
}

func TestNarrowedAuthorizationInvalidatesBroaderTokens(t *testing.T) {
	store, db, _ := testStore(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO users VALUES('u1','operator','operator',1,1)`); err != nil {
		t.Fatal(err)
	}
	client, err := store.RegisterClient(ctx, "client", []string{"https://client.example/callback"}, "dcr", "")
	if err != nil {
		t.Fatal(err)
	}
	verifier := "a-valid-verifier-value-with-more-than-forty-three-characters"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	code, err := store.IssueCode(ctx, "u1", client.ClientID, "https://client.example/callback", "https://panel/mcp", challenge, []string{ScopeExecute})
	if err != nil {
		t.Fatal(err)
	}
	executeTokens, err := store.ExchangeCode(ctx, code, client.ClientID, "https://client.example/callback", "https://panel/mcp", verifier)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.IssueCode(ctx, "u1", client.ClientID, "https://client.example/callback", "https://panel/mcp", challenge, []string{ScopeObserve}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authenticate(ctx, executeTokens.AccessToken, "https://panel/mcp"); err != ErrInvalidToken {
		t.Fatalf("broader access token error=%v, want ErrInvalidToken", err)
	}
	if _, err := store.Refresh(ctx, executeTokens.RefreshToken, client.ClientID, "https://panel/mcp"); err != ErrInvalidGrant {
		t.Fatalf("broader refresh token error=%v, want ErrInvalidGrant", err)
	}
}

func TestCredentialIssuanceFailsClosedWhenEntropyIsUnavailable(t *testing.T) {
	store, db, _ := testStore(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO users VALUES('u1','operator','operator',1,1)`); err != nil {
		t.Fatal(err)
	}
	client, err := store.RegisterClient(ctx, "client", []string{"https://client.example/callback"}, "dcr", "")
	if err != nil {
		t.Fatal(err)
	}
	originalRandom := store.random
	store.random = func(int) (string, error) { return "", errors.New("entropy unavailable") }
	verifier := "a-valid-verifier-value-with-more-than-forty-three-characters"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if _, err := store.IssueCode(ctx, "u1", client.ClientID, "https://client.example/callback", "https://panel/mcp", challenge, []string{ScopeExecute}); err == nil {
		t.Fatal("authorization code was issued without entropy")
	}
	store.random = originalRandom
	code, err := store.IssueCode(ctx, "u1", client.ClientID, "https://client.example/callback", "https://panel/mcp", challenge, []string{ScopeExecute})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	store.random = func(int) (string, error) {
		calls++
		if calls == 1 {
			return "family", nil
		}
		return "", errors.New("entropy unavailable")
	}
	if _, err := store.ExchangeCode(ctx, code, client.ClientID, "https://client.example/callback", "https://panel/mcp", verifier); err == nil {
		t.Fatal("tokens were issued after entropy failure")
	}
	store.random = originalRandom
	set, err := store.ExchangeCode(ctx, code, client.ClientID, "https://client.example/callback", "https://panel/mcp", verifier)
	if err != nil {
		t.Fatalf("failed entropy attempt consumed authorization code: %v", err)
	}
	if set.AccessToken == "" || set.RefreshToken == "" {
		t.Fatal("retry did not issue complete token set")
	}
}
