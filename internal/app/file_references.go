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

func (a *App) countExternalFileReferences(root string) (int, error) {
	rows, err := a.db.Query("SELECT target FROM external_trigger_entries WHERE action_type IN ('log', 'upload')")
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
			query := "UPDATE " + table + " SET script_path = ?, script_path_key = ? WHERE id = ?"
			if table == "quick_runs" {
				query = "UPDATE quick_runs SET script_path = ?, script_path_key = ?, revision = revision + 1, updated_at = unixepoch() WHERE id = ?"
			}
			if _, err := transaction.Exec(query, movedPath, hostfiles.ComparisonKey(movedPath), reference.id); err != nil {
				return fmt.Errorf("update %s file reference: %w", table, err)
			}
		}
	}
	rows, err := transaction.Query("SELECT id, action_type, target, config_json FROM external_trigger_entries WHERE action_type IN ('log', 'upload')")
	if err != nil {
		return err
	}
	type externalFileReference struct{ id, actionType, target, configJSON string }
	var references []externalFileReference
	for rows.Next() {
		var reference externalFileReference
		if err := rows.Scan(&reference.id, &reference.actionType, &reference.target, &reference.configJSON); err != nil {
			_ = rows.Close()
			return err
		}
		if hostfiles.Contains(source, reference.target) {
			references = append(references, reference)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, reference := range references {
		movedPath, err := hostfiles.Rebase(source, destination, reference.target)
		if err != nil {
			return err
		}
		var normalized any
		switch reference.actionType {
		case string(externaltrigger.ActionUpload):
			var config externaltrigger.UploadConfig
			if err := json.Unmarshal([]byte(reference.configJSON), &config); err != nil {
				return err
			}
			config.Directory = movedPath
			normalized = config
		case string(externaltrigger.ActionLog):
			var config externaltrigger.LogConfig
			if err := json.Unmarshal([]byte(reference.configJSON), &config); err != nil {
				return err
			}
			config.File = movedPath
			normalized = config
		default:
			continue
		}
		encoded, err := json.Marshal(normalized)
		if err != nil {
			return err
		}
		if _, err := transaction.Exec("UPDATE external_trigger_entries SET target = ?, config_json = ?, updated_at = unixepoch() WHERE id = ?", movedPath, string(encoded), reference.id); err != nil {
			return fmt.Errorf("update external file reference: %w", err)
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
