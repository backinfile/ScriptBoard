package migrations

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// schema 73 之前（版本 57-72）的面板与卡片表结构。
const legacyDashboardSchema57 = `
CREATE TABLE custom_dashboards (
	id TEXT PRIMARY KEY, name TEXT NOT NULL, slug TEXT NOT NULL UNIQUE,
	is_public INTEGER NOT NULL CHECK (is_public IN (0,1)),
	show_as_tab INTEGER NOT NULL DEFAULT 0 CHECK (show_as_tab IN (0,1)), sort_order INTEGER NOT NULL,
	created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE custom_dashboard_cards (
	id TEXT PRIMARY KEY, dashboard_id TEXT NOT NULL REFERENCES custom_dashboards(id) ON DELETE CASCADE,
	name TEXT NOT NULL, type TEXT NOT NULL CHECK(type IN ('number','percentage','quota','key_value','website','registry')),
	source_url TEXT NOT NULL DEFAULT '', headers_json TEXT NOT NULL DEFAULT '{}',
	value_path TEXT NOT NULL DEFAULT '', secondary_path TEXT NOT NULL DEFAULT '', formula TEXT NOT NULL DEFAULT '',
	config_json TEXT NOT NULL DEFAULT '{}', refresh_seconds INTEGER NOT NULL DEFAULT 60,
	sort_order INTEGER NOT NULL, snapshot_json TEXT NOT NULL DEFAULT '{}', last_error TEXT NOT NULL DEFAULT '',
	last_success_at INTEGER NOT NULL DEFAULT 0, last_attempt_at INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE INDEX custom_dashboard_cards_order_idx ON custom_dashboard_cards(dashboard_id, sort_order, created_at);
INSERT INTO custom_dashboards(id,name,slug,is_public,show_as_tab,sort_order,created_at,updated_at)
	VALUES('public-dash','公开','public-dash',1,0,1,1,1),('private-dash','私有','private-dash',0,1,2,1,1);
INSERT INTO custom_dashboard_cards(id,dashboard_id,name,type,config_json,sort_order,created_at,updated_at)
	VALUES('card-1','public-dash','额度','quota','{"note":"保持不变"}',1,1,1),
		('card-2','private-dash','镜像','registry','{"endpoint":"http://registry.lan:5000"}',1,1,1)`

// 面板最早期的表结构（版本 21-33 之前）：无 show_as_tab，卡片仅四种类型。
const legacyDashboardSchema20 = `
CREATE TABLE custom_dashboards (
	id TEXT PRIMARY KEY, name TEXT NOT NULL, slug TEXT NOT NULL UNIQUE,
	is_public INTEGER NOT NULL CHECK (is_public IN (0,1)), sort_order INTEGER NOT NULL,
	created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE custom_dashboard_cards (
	id TEXT PRIMARY KEY, dashboard_id TEXT NOT NULL REFERENCES custom_dashboards(id) ON DELETE CASCADE,
	name TEXT NOT NULL, type TEXT NOT NULL CHECK(type IN ('number','quota','key_value','website')),
	source_url TEXT NOT NULL DEFAULT '', headers_json TEXT NOT NULL DEFAULT '{}',
	value_path TEXT NOT NULL DEFAULT '', secondary_path TEXT NOT NULL DEFAULT '', formula TEXT NOT NULL DEFAULT '',
	config_json TEXT NOT NULL DEFAULT '{}', refresh_seconds INTEGER NOT NULL DEFAULT 60,
	sort_order INTEGER NOT NULL, snapshot_json TEXT NOT NULL DEFAULT '{}', last_error TEXT NOT NULL DEFAULT '',
	last_success_at INTEGER NOT NULL DEFAULT 0, last_attempt_at INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
INSERT INTO custom_dashboards(id,name,slug,is_public,sort_order,created_at,updated_at)
	VALUES('old-public','旧公开','old-public',1,1,1,1),('old-private','旧私有','old-private',0,2,1,1);
INSERT INTO custom_dashboard_cards(id,dashboard_id,name,type,config_json,sort_order,created_at,updated_at)
	VALUES('old-card','old-public','节点','number','{"unit":"个"}',1,1,1)`

func dashboardMigrationOptions() Options {
	return Options{CurrentVersion: 73, RandomToken: func(int) (string, error) { return "token", nil }, HashToken: func(value string) string { return value }, Now: time.Now}
}

func assertDashboardSchema73(t *testing.T, db *sql.DB, publicSlug, privateSlug string) {
	t.Helper()
	var visibility string
	if err := db.QueryRow(`SELECT visibility FROM custom_dashboards WHERE slug=?`, publicSlug).Scan(&visibility); err != nil || visibility != "public_read" {
		t.Fatalf("public dashboard visibility=%q err=%v", visibility, err)
	}
	if err := db.QueryRow(`SELECT visibility FROM custom_dashboards WHERE slug=?`, privateSlug).Scan(&visibility); err != nil || visibility != "private" {
		t.Fatalf("private dashboard visibility=%q err=%v", visibility, err)
	}
	var ciphertext []byte
	var hint string
	if err := db.QueryRow(`SELECT access_key_ciphertext, access_key_hint FROM custom_dashboards WHERE slug=?`, publicSlug).Scan(&ciphertext, &hint); err != nil {
		t.Fatal(err)
	}
	if len(ciphertext) != 0 || hint != "" {
		t.Fatalf("access key columns must default to empty: %x %q", ciphertext, hint)
	}
	if _, err := db.Exec(`INSERT INTO custom_dashboard_cards(id,dashboard_id,name,type,config_json,sort_order,created_at,updated_at)
		VALUES('flow-card',(SELECT id FROM custom_dashboards WHERE slug=?),'流程','flow','{}',9,1,1)`, publicSlug); err != nil {
		t.Fatalf("flow card type rejected: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO custom_dashboard_cards(id,dashboard_id,name,type,config_json,sort_order,created_at,updated_at)
		VALUES('bogus-card',(SELECT id FROM custom_dashboards WHERE slug=?),'未知','bogus','{}',10,1,1)`, publicSlug); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("unknown card type accepted: %v", err)
	}
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 73 {
		t.Fatalf("user_version=%d err=%v", version, err)
	}
}

func TestSchema73FromVersion72MapsVisibilityAndKeepsCards(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(legacyDashboardSchema57); err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, 72, dashboardMigrationOptions()); err != nil {
		t.Fatal(err)
	}
	assertDashboardSchema73(t, db, "public-dash", "private-dash")
	var config string
	if err := db.QueryRow(`SELECT config_json FROM custom_dashboard_cards WHERE id='card-1'`).Scan(&config); err != nil || config != `{"note":"保持不变"}` {
		t.Fatalf("quota card config changed: %s %v", config, err)
	}
	if err := db.QueryRow(`SELECT config_json FROM custom_dashboard_cards WHERE id='card-2'`).Scan(&config); err != nil || config != `{"endpoint":"http://registry.lan:5000"}` {
		t.Fatalf("registry card config changed: %s %v", config, err)
	}
	var isPublic bool
	if err := db.QueryRow(`SELECT is_public FROM custom_dashboards WHERE slug='public-dash'`).Scan(&isPublic); err != nil || !isPublic {
		t.Fatalf("is_public compatibility column lost: %v %v", isPublic, err)
	}
}

func TestSchema73FromMinimumVersionMigratesThroughEveryRebuild(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(legacyDashboardSchema20); err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, 20, dashboardMigrationOptions()); err != nil {
		t.Fatal(err)
	}
	assertDashboardSchema73(t, db, "old-public", "old-private")
	var config string
	if err := db.QueryRow(`SELECT config_json FROM custom_dashboard_cards WHERE id='old-card'`).Scan(&config); err != nil || config != `{"unit":"个"}` {
		t.Fatalf("legacy card config changed: %s %v", config, err)
	}
}
