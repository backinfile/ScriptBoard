package web_test

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"scriptboard/internal/statebackup"
	app "scriptboard/internal/web"
)

func TestStateBackupSettingsUsesStepUpProtectedServiceWithoutRenderingPassphrase(t *testing.T) {
	service := &stateBackupFixtureService{}
	stateRoot := filepath.Join(t.TempDir(), "state")
	client, serverURL := authenticatedClientWithConfig(t, app.Config{StateRoot: stateRoot, StateBackups: service})
	page := getSecurityPage(t, client, serverURL+"/settings/state-backups")
	passphrase := "state backup fixture passphrase"
	destination := filepath.Join(t.TempDir(), "state.sbsb")
	response, err := client.PostForm(serverURL+"/settings/state-backups/create", url.Values{
		"csrf_token": {formToken(t, page)}, "destination": {destination}, "passphrase": {passphrase}, "passphrase_confirm": {passphrase},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/settings/account/mfa" || service.createdPath != "" {
		t.Fatalf("AAL1 create status=%d location=%q service path=%q", response.StatusCode, response.Header.Get("Location"), service.createdPath)
	}
	database, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE sessions SET authentication_assurance = 2`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	_ = database.Close()
	page = getSecurityPage(t, client, serverURL+"/settings/state-backups")
	for _, expected := range [][]byte{[]byte("Private state backups"), []byte("recent AAL2"), []byte("offline restore workflow")} {
		if !bytes.Contains(page, expected) {
			t.Fatalf("state backup page missing %q: %s", expected, page)
		}
	}

	response, body := postStateBackupForm(t, client, serverURL+"/settings/state-backups/create", url.Values{
		"csrf_token": {formToken(t, page)}, "destination": {destination}, "passphrase": {passphrase}, "passphrase_confirm": {passphrase},
	})
	if response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("ABCDEFGHIJKLMNOPQRSTUVWX")) || bytes.Contains(body, []byte(passphrase)) {
		t.Fatalf("create status=%d body=%s", response.StatusCode, body)
	}
	if service.createdPath != destination || string(service.passphrase) != passphrase {
		t.Fatalf("service create path=%q passphrase=%q", service.createdPath, service.passphrase)
	}

	response, body = postStateBackupForm(t, client, serverURL+"/settings/state-backups/stage", url.Values{
		"csrf_token": {formToken(t, body)}, "archive_path": {destination}, "passphrase": {passphrase}, "backup_id": {"ABCDEFGHIJKLMNOPQRSTUVWX"},
	})
	if response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("YZabcdefghijklmnopqrstuv")) || bytes.Contains(body, []byte(passphrase)) {
		t.Fatalf("stage status=%d body=%s", response.StatusCode, body)
	}

	response, body = postStateBackupForm(t, client, serverURL+"/settings/state-backups/stages/YZabcdefghijklmnopqrstuv/discard", url.Values{
		"csrf_token": {formToken(t, body)}, "confirm": {"yes"},
	})
	if response.StatusCode != http.StatusOK || service.discarded != "YZabcdefghijklmnopqrstuv" {
		t.Fatalf("discard status=%d discarded=%q body=%s", response.StatusCode, service.discarded, body)
	}
}

func postStateBackupForm(t *testing.T, client *http.Client, target string, values url.Values) (*http.Response, []byte) {
	t.Helper()
	response, err := client.PostForm(target, values)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return response, body
}

type stateBackupFixtureService struct {
	createdPath string
	passphrase  []byte
	stages      []statebackup.Stage
	discarded   string
}

func (service *stateBackupFixtureService) Create(_ context.Context, destination string, passphrase []byte) (statebackup.Artifact, error) {
	service.createdPath = destination
	service.passphrase = append([]byte(nil), passphrase...)
	manifest := stateBackupFixtureManifest()
	return statebackup.Artifact{Path: destination, Manifest: manifest}, nil
}

func (*stateBackupFixtureService) Inspect(_ context.Context, _ string, _ []byte) (statebackup.Manifest, error) {
	return stateBackupFixtureManifest(), nil
}

func (service *stateBackupFixtureService) Stage(_ context.Context, _ string, _ []byte, _ string) (statebackup.Stage, error) {
	stage := statebackup.Stage{ID: "YZabcdefghijklmnopqrstuv", CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(24 * time.Hour), Manifest: stateBackupFixtureManifest()}
	service.stages = []statebackup.Stage{stage}
	return stage, nil
}

func (service *stateBackupFixtureService) List(_ context.Context) ([]statebackup.Stage, error) {
	return append([]statebackup.Stage(nil), service.stages...), nil
}

func (service *stateBackupFixtureService) Discard(_ context.Context, stageID string) error {
	service.discarded = stageID
	service.stages = nil
	return nil
}

func stateBackupFixtureManifest() statebackup.Manifest {
	return statebackup.Manifest{FormatVersion: 1, ID: "ABCDEFGHIJKLMNOPQRSTUVWX", CreatedAt: "2026-08-12T10:00:00Z", SchemaVersion: 43, Files: []statebackup.FileManifest{{Path: "app.db", Size: 1024, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}
}
