package migrations

import (
	"database/sql"
	"path/filepath"
	"scriptboard/internal/mysqlmanager"
	"strings"
	"testing"
	"time"
)

func TestSchema72PreservesAndBackfillsPlanExecutionHistory(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range mysqlmanager.SchemaStatements {
		statement = strings.ReplaceAll(statement, "plan_id TEXT NOT NULL DEFAULT '',\n\t\ttarget_database", "target_database")
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	_, err = db.Exec("INSERT INTO mysql_backups (id,instance_id,database_name,plan_id,kind,path,size_bytes,sha256,created_at) VALUES ('backup','instance','inventory','plan','scheduled','fixture.sql.gz',1,'hash',1)")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"known", "unknown"} {
		backupID := ""
		if id == "known" {
			backupID = "backup"
		}
		if _, err := db.Exec("INSERT INTO mysql_operations (id,kind,instance_id,database_name,backup_id,phase,created_at,updated_at) VALUES (?,'backup','instance','inventory',?,'completed',1,1)", id, backupID); err != nil {
			t.Fatal(err)
		}
	}
	if err := Apply(db, 71, Options{CurrentVersion: 72, RandomToken: func(int) (string, error) { return "fixture", nil }, HashToken: func(v string) string { return v }, Now: time.Now}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"known", "unknown"} {
		var plan string
		if err := db.QueryRow("SELECT plan_id FROM mysql_operations WHERE id=?", id).Scan(&plan); err != nil {
			t.Fatal(err)
		}
		if (id == "known" && plan != "plan") || (id == "unknown" && plan != "") {
			t.Fatalf("%s: %q", id, plan)
		}
	}
	if !Compatible(72, 71) {
		t.Fatal("schema 71 must remain upgradeable")
	}
}
