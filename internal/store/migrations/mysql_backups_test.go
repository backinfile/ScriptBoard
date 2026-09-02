package migrations

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestSchema63BackfillsScheduledBackupSourceNames(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	_, err = database.Exec(`
		CREATE TABLE mysql_instances (id TEXT PRIMARY KEY);
		CREATE TABLE mysql_backup_plans (
			id TEXT PRIMARY KEY, name TEXT NOT NULL, instance_id TEXT NOT NULL, databases_json TEXT NOT NULL,
			expression TEXT NOT NULL, retention_count INTEGER NOT NULL, enabled INTEGER NOT NULL,
			next_fire_at INTEGER NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
		);
		CREATE TABLE mysql_backups (
			id TEXT PRIMARY KEY, instance_id TEXT NOT NULL, database_name TEXT NOT NULL, plan_id TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL, path TEXT NOT NULL UNIQUE, size_bytes INTEGER NOT NULL, sha256 TEXT NOT NULL,
			warning TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, created_by_user_id TEXT NOT NULL DEFAULT '',
			created_by_username TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO mysql_instances(id) VALUES ('instance-one');
		INSERT INTO mysql_backup_plans(id,name,instance_id,databases_json,expression,retention_count,enabled,next_fire_at,created_at,updated_at)
		VALUES ('plan-one','Nightly inventory','instance-one','["inventory"]','0 2 * * *',7,1,1,1,1);
		INSERT INTO mysql_backups(id,instance_id,database_name,plan_id,kind,path,size_bytes,sha256,created_at)
		VALUES ('backup-one','instance-one','inventory','plan-one','scheduled','backup.sql.gz',1,'hash',1);
		PRAGMA user_version=62;
	`)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(database, 62, Options{
		CurrentVersion: 63,
		RandomToken:    func(int) (string, error) { return "token", nil },
		HashToken:      func(value string) string { return value },
		Now:            time.Now,
	}); err != nil {
		t.Fatal(err)
	}
	var sourceName string
	if err := database.QueryRow("SELECT source_name FROM mysql_backups WHERE id='backup-one'").Scan(&sourceName); err != nil {
		t.Fatal(err)
	}
	if sourceName != "Nightly inventory" {
		t.Fatalf("migrated scheduled backup source name = %q", sourceName)
	}
}
