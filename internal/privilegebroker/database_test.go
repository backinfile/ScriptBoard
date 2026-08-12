package privilegebroker

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"scriptboard/internal/auditlog"
)

func TestDatabaseSecurityRequiresCurrentPrivilegedStepUp(t *testing.T) {
	now := time.Unix(1786420000, 0).UTC()
	db := openBrokerDatabase(t)
	security, err := NewDatabaseSecurity(db, auditlog.New(db), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	insertBrokerSession(t, db, "allowed-session-token-0123456789", "maintainer", now.Add(-time.Minute).Unix(), now.Add(time.Hour).Unix())
	actor, err := security.Authorize(context.Background(), AuthorizationRequest{SessionToken: "allowed-session-token-0123456789"})
	if err != nil || actor.Role != "maintainer" || actor.AuthenticationAssurance != 2 {
		t.Fatalf("actor=%+v err=%v", actor, err)
	}

	insertBrokerSession(t, db, "expired-step-up-token-0123456789", "administrator", now.Add(-11*time.Minute).Unix(), now.Add(time.Hour).Unix())
	if _, err := security.Authorize(context.Background(), AuthorizationRequest{SessionToken: "expired-step-up-token-0123456789"}); err == nil {
		t.Fatal("expired step-up authorized")
	}
	insertBrokerSession(t, db, "operator-session-token-0123456789", "operator", now.Unix(), now.Add(time.Hour).Unix())
	if _, err := security.Authorize(context.Background(), AuthorizationRequest{SessionToken: "operator-session-token-0123456789"}); err == nil {
		t.Fatal("operator session authorized for privileged mutation")
	}
	actor, err = security.AuthorizeSession(context.Background(), AuthorizationRequest{SessionToken: "expired-step-up-token-0123456789"})
	if err != nil || actor.Role != "administrator" {
		t.Fatalf("valid session with stale step-up was rejected: actor=%+v err=%v", actor, err)
	}
	actor, err = security.AuthorizeSession(context.Background(), AuthorizationRequest{SessionToken: "operator-session-token-0123456789"})
	if err != nil || actor.Role != "operator" {
		t.Fatalf("valid operator read session was rejected: actor=%+v err=%v", actor, err)
	}
}

func TestDatabaseSecurityWritesIndependentIntentAndResultAudit(t *testing.T) {
	now := time.Unix(1786420000, 0).UTC()
	db := openBrokerDatabase(t)
	security, _ := NewDatabaseSecurity(db, auditlog.New(db), func() time.Time { return now })
	record := AuditRecord{
		OccurredAt: now, RequestID: "request-audit-1", Actor: Actor{UserID: "user-1", Username: "admin", Role: "administrator", AuthenticationAssurance: 2},
		Action: ActionWindowsFirewallDelete, Resource: "rule-1", Revision: "revision-1", ParametersSHA256: strings.Repeat("a", 64), Result: "attempted",
	}
	if err := security.Record(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	var action, result, requestID, assurance, revision, digest string
	if err := db.QueryRow(`SELECT action, result, request_id, authentication_assurance, resource_revision, resource_digest_sha256 FROM audit_events ORDER BY id DESC LIMIT 1`).Scan(&action, &result, &requestID, &assurance, &revision, &digest); err != nil {
		t.Fatal(err)
	}
	if action != "privileged_broker.windows_firewall_delete" || result != "attempted" || requestID != record.RequestID || assurance != "aal2+step-up" || revision != record.Revision || digest != record.ParametersSHA256 {
		t.Fatalf("audit=%q %q %q %q %q %q", action, result, requestID, assurance, revision, digest)
	}
}

func openBrokerDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "broker.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT, role TEXT, auth_version INTEGER, enabled INTEGER)`,
		`CREATE TABLE sessions (token_hash TEXT PRIMARY KEY, user_id TEXT, auth_version INTEGER, authentication_assurance INTEGER, reauthenticated_at INTEGER, last_seen_at INTEGER, expires_at INTEGER)`,
		`CREATE TABLE audit_events (id INTEGER PRIMARY KEY AUTOINCREMENT, occurred_at INTEGER NOT NULL, action TEXT NOT NULL, target TEXT NOT NULL, result TEXT NOT NULL, source_address TEXT NOT NULL, actor_user_id TEXT NOT NULL DEFAULT '', actor_username TEXT NOT NULL DEFAULT '', actor_role TEXT NOT NULL DEFAULT '', request_id TEXT NOT NULL DEFAULT '', authentication_assurance TEXT NOT NULL DEFAULT '', resource_revision TEXT NOT NULL DEFAULT '', resource_digest_sha256 TEXT NOT NULL DEFAULT '', previous_hash TEXT NOT NULL DEFAULT '', event_hash TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE audit_chain_state (id INTEGER PRIMARY KEY, anchor_hash TEXT NOT NULL, tail_hash TEXT NOT NULL)`,
		`INSERT INTO audit_chain_state VALUES (1, '', '')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func insertBrokerSession(t *testing.T, db *sql.DB, token, role string, reauthenticatedAt, expiresAt int64) {
	t.Helper()
	digest := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(digest[:])
	userID := "user-" + role
	if _, err := db.Exec(`INSERT OR REPLACE INTO users VALUES (?, ?, ?, 3, 1)`, userID, role, role); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sessions VALUES (?, ?, 3, 2, ?, ?, ?)`, tokenHash, userID, reauthenticatedAt, reauthenticatedAt, expiresAt); err != nil {
		t.Fatal(err)
	}
}
