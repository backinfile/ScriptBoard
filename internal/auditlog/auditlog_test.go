package auditlog

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"
)

func testStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+strings.ReplaceAll(t.Name(), "/", "-")+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range []string{
		`CREATE TABLE audit_events (id INTEGER PRIMARY KEY AUTOINCREMENT, occurred_at INTEGER NOT NULL, action TEXT NOT NULL, target TEXT NOT NULL, result TEXT NOT NULL, source_address TEXT NOT NULL, actor_user_id TEXT NOT NULL DEFAULT '', actor_username TEXT NOT NULL DEFAULT '', actor_role TEXT NOT NULL DEFAULT '', request_id TEXT NOT NULL DEFAULT '', authentication_assurance TEXT NOT NULL DEFAULT '', previous_hash TEXT NOT NULL DEFAULT '', event_hash TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE audit_chain_state (id INTEGER PRIMARY KEY CHECK(id = 1), anchor_hash TEXT NOT NULL, tail_hash TEXT NOT NULL)`,
		`INSERT INTO audit_chain_state VALUES (1, '', '')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return New(db), db
}

func TestConcurrentAppendsRemainOneLinearChain(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()
	var wait sync.WaitGroup
	errors := make(chan error, 32)
	for index := 0; index < cap(errors); index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := store.Append(ctx, Event{OccurredAt: "1786410000", Action: "concurrent", Target: "resource", Result: "succeeded", SourceAddress: "local"})
			errors <- err
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if verification, err := store.Verify(ctx); err != nil || verification.Count != 32 {
		t.Fatalf("verification=%#v err=%v", verification, err)
	}
}

func TestObserverOnlyReceivesCommittedRedactedEvents(t *testing.T) {
	store, _ := testStore(t)
	var observed []CommittedEvent
	store.SetObserver(func(event CommittedEvent) { observed = append(observed, event) })
	transaction, err := store.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Append(context.Background(), Event{OccurredAt: "1786410000", Action: "login", Target: "Bearer secret-value", Result: "failed", SourceAddress: "local"}); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
	if len(observed) != 0 {
		t.Fatalf("rollback notified observer: %#v", observed)
	}
	if _, err := store.Append(context.Background(), Event{OccurredAt: "1786410000", Action: "login", Target: "Bearer secret-value", Result: "failed", SourceAddress: "local"}); err != nil {
		t.Fatal(err)
	}
	if len(observed) != 1 || observed[0].ID == 0 || observed[0].EventSHA256 == "" || strings.Contains(observed[0].Event.Target, "secret-value") {
		t.Fatalf("observed event=%#v", observed)
	}
}

func TestVerifyDetectsModifiedDeletedAndTruncatedEvents(t *testing.T) {
	store, db := testStore(t)
	ctx := context.Background()
	for _, action := range []string{"first", "second", "third"} {
		if _, err := store.Append(ctx, Event{OccurredAt: "1786410000", Action: action, Target: "resource", Result: "succeeded", SourceAddress: "local"}); err != nil {
			t.Fatal(err)
		}
	}
	if verification, err := store.Verify(ctx); err != nil || verification.Count != 3 {
		t.Fatalf("verification=%#v err=%v", verification, err)
	}
	if _, err := db.Exec("UPDATE audit_events SET target = 'tampered' WHERE action = 'second'"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Verify(ctx); err == nil {
		t.Fatal("modified audit event was not detected")
	}
	if _, err := db.Exec("UPDATE audit_events SET target = 'resource' WHERE action = 'second'"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM audit_events WHERE action = 'second'"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Verify(ctx); err == nil {
		t.Fatal("deleted intermediate audit event was not detected")
	}
}

func TestVerifyAcceptsRetentionAnchorAndDetectsTailDeletion(t *testing.T) {
	store, db := testStore(t)
	ctx := context.Background()
	for _, action := range []string{"first", "second", "third"} {
		if _, err := store.Append(ctx, Event{OccurredAt: "1786410000", Action: action, Target: "resource", Result: "succeeded", SourceAddress: "local"}); err != nil {
			t.Fatal(err)
		}
	}
	var firstHash string
	if err := db.QueryRow("SELECT event_hash FROM audit_events WHERE action = 'first'").Scan(&firstHash); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE audit_chain_state SET anchor_hash = ? WHERE id = 1", firstHash); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM audit_events WHERE action = 'first'"); err != nil {
		t.Fatal(err)
	}
	if verification, err := store.Verify(ctx); err != nil || verification.Count != 2 {
		t.Fatalf("retained verification=%#v err=%v", verification, err)
	}
	if _, err := db.Exec("DELETE FROM audit_events WHERE action = 'third'"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Verify(ctx); err == nil {
		t.Fatal("deleted audit tail was not detected")
	}
}

func TestAppendRedactsSecretsBeforeHashingAndPersistence(t *testing.T) {
	store, db := testStore(t)
	ctx := context.Background()
	const secret = "audit-password-value"
	if _, err := store.Append(ctx, Event{
		OccurredAt: "1786410000", Action: "configure", Target: "password=" + secret,
		Result: "Authorization: Bearer synthetic-audit-token-value", SourceAddress: "local",
	}); err != nil {
		t.Fatal(err)
	}
	var target, result string
	if err := db.QueryRow("SELECT target, result FROM audit_events").Scan(&target, &result); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(target, secret) || strings.Contains(result, "synthetic-audit-token-value") {
		t.Fatalf("audit event retained secret: target=%q result=%q", target, result)
	}
	if verification, err := store.Verify(ctx); err != nil || verification.Count != 1 {
		t.Fatalf("verification=%#v err=%v", verification, err)
	}
}

func TestAppendPersistsRequestCorrelationAndAuthenticationAssuranceInChain(t *testing.T) {
	store, db := testStore(t)
	ctx := context.Background()
	event := Event{
		OccurredAt: "1786410000", Action: "change_role", Target: "user-1", Result: "succeeded", SourceAddress: "local",
		ActorUserID: "actor-1", ActorUsername: "admin", ActorRole: "administrator",
		RequestID: "request-1", AuthenticationAssurance: "aal1+step-up",
	}
	if _, err := store.Append(ctx, event); err != nil {
		t.Fatal(err)
	}
	var requestID, assurance string
	if err := db.QueryRow("SELECT request_id, authentication_assurance FROM audit_events").Scan(&requestID, &assurance); err != nil {
		t.Fatal(err)
	}
	if requestID != event.RequestID || assurance != event.AuthenticationAssurance {
		t.Fatalf("request_id=%q assurance=%q", requestID, assurance)
	}
	if _, err := db.Exec("UPDATE audit_events SET authentication_assurance = 'aal1' WHERE request_id = ?", event.RequestID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Verify(ctx); err == nil {
		t.Fatal("authentication assurance tampering was not detected")
	}
}

func TestVerificationReportsTailEventID(t *testing.T) {
	store, _ := testStore(t)
	first, err := store.Append(context.Background(), Event{OccurredAt: "1", Action: "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Append(context.Background(), Event{OccurredAt: "2", Action: "second"})
	if err != nil {
		t.Fatal(err)
	}
	verification, err := store.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first <= 0 || verification.LastID != second {
		t.Fatalf("first=%d second=%d verification=%#v", first, second, verification)
	}
	if err := store.VerifyCheckpoint(context.Background(), second, verification.LastHash); err != nil {
		t.Fatalf("verify current checkpoint: %v", err)
	}
	if err := store.VerifyCheckpoint(context.Background(), second, strings.Repeat("0", 64)); err == nil {
		t.Fatal("wrong checkpoint hash was accepted")
	}
}
