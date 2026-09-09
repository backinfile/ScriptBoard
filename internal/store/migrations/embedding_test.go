package migrations

import (
	"database/sql"
	_ "modernc.org/sqlite"
	"testing"
	"time"
)

func TestSchema70EmbeddingSettingsUpgrade(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	options := Options{CurrentVersion: 69, RandomToken: func(int) (string, error) { return "token", nil }, HashToken: func(s string) string { return s }, Now: time.Now}
	if err := Apply(db, 0, options); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE embedding_settings; INSERT INTO instance_settings(singleton,display_name,updated_at) VALUES(1,'Existing name',1)`); err != nil {
		t.Fatal(err)
	}
	options.CurrentVersion = 70
	if !Compatible(70, 69) {
		t.Fatal("69 must migrate")
	}
	if err := Apply(db, 69, options); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM embedding_settings`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("startup config must remain inherited: %d %v", count, err)
	}
	if _, err := db.Exec(`INSERT INTO embedding_settings(singleton,frame_ancestors,updated_at) VALUES(1,'["*"]',1)`); err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, 70, options); err != nil {
		t.Fatal(err)
	}
	var origins, name string
	if err := db.QueryRow(`SELECT frame_ancestors FROM embedding_settings`).Scan(&origins); err != nil || origins != `["*"]` {
		t.Fatalf("saved policy lost: %s %v", origins, err)
	}
	if err := db.QueryRow(`SELECT display_name FROM instance_settings`).Scan(&name); err != nil || name != "Existing name" {
		t.Fatalf("site name lost: %s %v", name, err)
	}
}
