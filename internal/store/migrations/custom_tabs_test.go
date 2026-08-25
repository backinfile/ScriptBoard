package migrations

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestSchema57UpgradesFeatureLineCustomTabs(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE custom_tabs (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, target_url TEXT NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 0, credential_mode TEXT NOT NULL DEFAULT 'isolated',
		key_name TEXT NOT NULL DEFAULT '', key_ciphertext BLOB NOT NULL DEFAULT X'',
		sort_order INTEGER NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, 55, Options{CurrentVersion: 57, RandomToken: func(int) (string, error) { return "token", nil }, HashToken: func(value string) string { return value }, Now: time.Now}); err != nil {
		t.Fatal(err)
	}
	var roles string
	if _, err := db.Exec(`INSERT INTO custom_tabs(id,name,target_url,sort_order,created_at,updated_at) VALUES('one','One','https://example.test',1,1,1)`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT visibility_roles FROM custom_tabs WHERE id='one'`).Scan(&roles); err != nil {
		t.Fatal(err)
	}
	if roles != "administrator,maintainer,operator,viewer" {
		t.Fatalf("visibility default=%q", roles)
	}
}

func TestSchema57ReconcilesFeatureLineWithDashboardTabs(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE custom_dashboards (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, slug TEXT NOT NULL UNIQUE,
		is_public INTEGER NOT NULL CHECK (is_public IN (0,1)), sort_order INTEGER NOT NULL,
		created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, 56, Options{CurrentVersion: 57, RandomToken: func(int) (string, error) { return "token", nil }, HashToken: func(value string) string { return value }, Now: time.Now}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO custom_dashboards(id,name,slug,is_public,sort_order,created_at,updated_at) VALUES('one','One','one',0,1,1,1)`); err != nil {
		t.Fatal(err)
	}
	var showAsTab int
	if err := db.QueryRow(`SELECT show_as_tab FROM custom_dashboards WHERE id='one'`).Scan(&showAsTab); err != nil {
		t.Fatal(err)
	}
	if showAsTab != 0 {
		t.Fatalf("show_as_tab default=%d", showAsTab)
	}
}
