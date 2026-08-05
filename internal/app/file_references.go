package app

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"scriptboard/internal/externaltrigger"
	"scriptboard/internal/hostfiles"
)

type databaseQueryer interface {
	Query(string, ...any) (*sql.Rows, error)
}

type scriptReference struct {
	id   string
	path string
}

func scriptReferences(queryer databaseQueryer, table, root string, activeOnly bool) ([]scriptReference, error) {
	query := "SELECT id, script_path FROM " + table
	if activeOnly && table == "schedules" {
		query += " WHERE deleted = 0"
	}
	rows, err := queryer.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []scriptReference
	for rows.Next() {
		var reference scriptReference
		if err := rows.Scan(&reference.id, &reference.path); err != nil {
			return nil, err
		}
		if hostfiles.Contains(root, reference.path) {
			result = append(result, reference)
		}
	}
	return result, rows.Err()
}

func (a *App) countScriptReferences(root string) (int, int, error) {
	quick, err := scriptReferences(a.db, "quick_runs", root, false)
	if err != nil {
		return 0, 0, err
	}
	schedules, err := scriptReferences(a.db, "schedules", root, true)
	return len(quick), len(schedules), err
}

func (a *App) countExternalUploadReferences(root string) (int, error) {
	rows, err := a.db.Query("SELECT target FROM external_trigger_entries WHERE action_type = 'upload'")
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var target string
		if err := rows.Scan(&target); err != nil {
			return 0, err
		}
		if hostfiles.Contains(root, target) {
			count++
		}
	}
	return count, rows.Err()
}

func updateMovedScriptReferences(transaction *sql.Tx, source, destination string) error {
	for _, table := range []string{"quick_runs", "schedules"} {
		references, err := scriptReferences(transaction, table, source, table == "schedules")
		if err != nil {
			return err
		}
		for _, reference := range references {
			movedPath, err := hostfiles.Rebase(source, destination, reference.path)
			if err != nil {
				return err
			}
			if _, err := transaction.Exec("UPDATE "+table+" SET script_path = ?, script_path_key = ? WHERE id = ?",
				movedPath, hostfiles.ComparisonKey(movedPath), reference.id); err != nil {
				return fmt.Errorf("update %s file reference: %w", table, err)
			}
		}
	}
	rows, err := transaction.Query("SELECT id, target, config_json FROM external_trigger_entries WHERE action_type = 'upload'")
	if err != nil {
		return err
	}
	type uploadReference struct{ id, target, configJSON string }
	var uploads []uploadReference
	for rows.Next() {
		var reference uploadReference
		if err := rows.Scan(&reference.id, &reference.target, &reference.configJSON); err != nil {
			_ = rows.Close()
			return err
		}
		if hostfiles.Contains(source, reference.target) {
			uploads = append(uploads, reference)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, reference := range uploads {
		movedPath, err := hostfiles.Rebase(source, destination, reference.target)
		if err != nil {
			return err
		}
		var config externaltrigger.UploadConfig
		if err := json.Unmarshal([]byte(reference.configJSON), &config); err != nil {
			return err
		}
		config.Directory = movedPath
		encoded, err := json.Marshal(config)
		if err != nil {
			return err
		}
		if _, err := transaction.Exec("UPDATE external_trigger_entries SET target = ?, config_json = ?, updated_at = unixepoch() WHERE id = ?", movedPath, string(encoded), reference.id); err != nil {
			return fmt.Errorf("update external upload file reference: %w", err)
		}
	}
	return nil
}

func disableScheduleReferences(transaction *sql.Tx, root string, updatedAt int64) error {
	references, err := scriptReferences(transaction, "schedules", root, true)
	if err != nil {
		return err
	}
	for _, reference := range references {
		if _, err := transaction.Exec("UPDATE schedules SET enabled = 0, updated_at = ? WHERE id = ?", updatedAt, reference.id); err != nil {
			return err
		}
	}
	return nil
}
