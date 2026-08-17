package web

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"

	"scriptboard/internal/auditlog"
	"scriptboard/internal/identity"
	"scriptboard/internal/privilegebroker"

	_ "modernc.org/sqlite"
)

func TestMySQLBackgroundOperationKeepsAuthorizationAndRecordsAcceptance(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	for _, statement := range []string{
		`CREATE TABLE audit_events (id INTEGER PRIMARY KEY AUTOINCREMENT, occurred_at INTEGER NOT NULL, action TEXT NOT NULL, target TEXT NOT NULL, result TEXT NOT NULL, source_address TEXT NOT NULL, actor_user_id TEXT NOT NULL DEFAULT '', actor_username TEXT NOT NULL DEFAULT '', actor_role TEXT NOT NULL DEFAULT '', request_id TEXT NOT NULL DEFAULT '', authentication_assurance TEXT NOT NULL DEFAULT '', resource_revision TEXT NOT NULL DEFAULT '', resource_digest_sha256 TEXT NOT NULL DEFAULT '', previous_hash TEXT NOT NULL DEFAULT '', event_hash TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE audit_chain_state (id INTEGER PRIMARY KEY CHECK(id = 1), anchor_hash TEXT NOT NULL, tail_hash TEXT NOT NULL)`,
		`INSERT INTO audit_chain_state (id, anchor_hash, tail_hash) VALUES (1, '', '')`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	audited := make(chan auditlog.CommittedEvent, 1)
	store := auditlog.New(database)
	store.SetObserver(func(event auditlog.CommittedEvent) { audited <- event })
	application := &App{mysqlContext: context.Background(), auditLog: store}

	requestContext, cancelRequest := context.WithCancel(context.Background())
	authorization := privilegebroker.Authorization{SessionToken: strings.Repeat("s", 32), RequestID: "mysql-background-test"}
	requestContext = privilegebroker.WithAuthorization(requestContext, authorization)
	requestContext = context.WithValue(requestContext, sessionContextKey, session{
		userID: "user-1", username: "admin", role: identity.RoleAdministrator,
	})
	request := httptest.NewRequest("POST", "/resources/databases", nil).WithContext(requestContext)

	started := make(chan context.Context, 1)
	release := make(chan struct{})
	application.startMySQLBackgroundOperation(request, "start_mysql_backup", "instance/database", func(ctx context.Context) {
		started <- ctx
		<-release
	})
	cancelRequest()

	operationContext := <-started
	if operationContext.Err() != nil {
		t.Fatalf("background context followed request cancellation: %v", operationContext.Err())
	}
	if got, ok := privilegebroker.AuthorizationFromContext(operationContext); !ok || got != authorization {
		t.Fatalf("background authorization = %#v, %t; want %#v", got, ok, authorization)
	}
	close(release)
	application.mysqlWG.Wait()

	event := <-audited
	if event.Event.Action != "start_mysql_backup" || event.Event.Target != "instance/database" || event.Event.Result != "accepted" {
		t.Fatalf("audit event = %#v", event.Event)
	}
}
