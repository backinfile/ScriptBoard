package privilegebroker

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/auditlog"
	"scriptboard/internal/gitproject"
	"scriptboard/internal/hostfiles"
)

func TestGitProjectBrokerSeparatesReadAndDestructivePermissions(t *testing.T) {
	root := t.TempDir()
	files, err := hostfiles.Open(hostfiles.Options{Topology: fixtureHostFilesTopology{root: root}})
	if err != nil {
		t.Fatal(err)
	}
	db := openBrokerDatabase(t)
	for _, statement := range gitproject.SchemaStatements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(root, "checkout")
	project := gitproject.Project{ID: "project", Name: "Example", Remote: "https://git.example.invalid/repo.git",
		Directory: target, PathKey: hostfiles.ComparisonKey(target)}
	if err := gitproject.Add(t.Context(), db, project); err != nil {
		t.Fatal(err)
	}
	service := newBrokerHostFilesService(files, "", nil, t.Context(), db)
	service.gitProjects = gitproject.NewCheckout(files, t.TempDir())
	now := time.Now()
	security, err := NewDatabaseSecurity(db, auditlog.New(db), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("x", 32)
	insertBrokerSession(t, db, token, "maintainer", now.Add(-4*time.Hour).Unix(), now.Add(time.Hour).Unix())
	backend, closeServer := hostFilesTestBackendWithAuthorizer(t, service, security)
	defer closeServer()

	ctx := WithAuthorization(t.Context(), Authorization{SessionToken: token, RequestID: "git-project-policy"})
	result, err := backend.GitProject(ctx, gitproject.Request{ProjectID: project.ID, Action: gitproject.Overview})
	if err != nil || result.After.Cloned {
		t.Fatalf("inspect pending Git checkout = %+v, %v", result, err)
	}
	_, err = backend.GitProject(ctx, gitproject.Request{ProjectID: project.ID, Action: gitproject.Force, Revision: gitproject.UnclonedRevision})
	if err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("destructive Git operation with a stale login = %v", err)
	}
}
