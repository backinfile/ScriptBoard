package web

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestRecordNotesSurviveSupportedUpgrades(t *testing.T) {
	for _, version := range []int{20, 36, 43, 64, 72} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "notes.db")
			db, err := openDatabase(path)
			if err != nil {
				t.Fatal(err)
			}
			tables := []string{"users", "quick_run_groups", "quick_runs", "schedule_groups", "schedules", "file_quick_access_pins", "website_monitors", "external_trigger_groups", "external_trigger_keys", "external_trigger_entries", "fleet_peers", "mysql_instances", "mysql_backup_plans", "redis_instances", "custom_dashboards", "custom_dashboard_cards", "custom_tabs", "kubernetes_connection"}
			for _, table := range tables {
				if _, err := db.Exec("ALTER TABLE " + table + " DROP COLUMN note"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version=%d", version)); err != nil {
				t.Fatal(err)
			}
			db.Close()
			db, err = openDatabase(path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for _, table := range tables {
				var n int
				if err := db.QueryRow("SELECT count(*) FROM pragma_table_info(?) WHERE name='note'", table).Scan(&n); err != nil || n != 1 {
					t.Fatalf("%s note count=%d error=%v", table, n, err)
				}
			}
		})
	}
}

func TestSchema73DashboardRebuildPreservesNotes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.db")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{"INSERT INTO custom_dashboards(id,name,note,slug,is_public,sort_order,created_at,updated_at) VALUES('d','dashboard','private dashboard','notes',0,1,1,1)", "INSERT INTO custom_dashboard_cards(id,dashboard_id,name,note,type,sort_order,created_at,updated_at) VALUES('c','d','card','private card','number',1,1,1)", "PRAGMA user_version=73"} {
		if _, err := db.Exec(sql); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	db, err = openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for table, want := range map[string]string{"custom_dashboards": "private dashboard", "custom_dashboard_cards": "private card"} {
		var note string
		if err := db.QueryRow("SELECT note FROM " + table).Scan(&note); err != nil || note != want {
			t.Fatalf("%s note=%q err=%v", table, note, err)
		}
	}
}
