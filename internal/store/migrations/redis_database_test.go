package migrations

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestSchema64RemovesRedisDatabaseIndexFromConnectionMetadata(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	_, err = database.Exec(`
		CREATE TABLE redis_instances (
			id TEXT PRIMARY KEY, name TEXT NOT NULL, environment TEXT NOT NULL, host TEXT NOT NULL,
			port INTEGER NOT NULL, username TEXT NOT NULL, database_index INTEGER NOT NULL DEFAULT 0 CHECK(database_index >= 0),
			tls_mode TEXT NOT NULL, ca_path TEXT NOT NULL, credential_configured INTEGER NOT NULL,
			connection_state TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
		);
		INSERT INTO redis_instances VALUES ('redis-one','Cache','production','redis.internal',6379,'default',7,'disabled','',1,'connected',1,1);
		PRAGMA user_version=63;
	`)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(database, 63, Options{
		CurrentVersion: 64,
		RandomToken:    func(int) (string, error) { return "token", nil },
		HashToken:      func(value string) string { return value },
		Now:            time.Now,
	}); err != nil {
		t.Fatal(err)
	}
	var databaseIndexColumns int
	if err := database.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('redis_instances') WHERE name='database_index'`).Scan(&databaseIndexColumns); err != nil {
		t.Fatal(err)
	}
	if databaseIndexColumns != 0 {
		t.Fatal("Redis database index remains part of connection metadata")
	}
	var name, host string
	if err := database.QueryRow(`SELECT name,host FROM redis_instances WHERE id='redis-one'`).Scan(&name, &host); err != nil {
		t.Fatal(err)
	}
	if name != "Cache" || host != "redis.internal" {
		t.Fatalf("Redis connection was not preserved: name=%q host=%q", name, host)
	}
}
