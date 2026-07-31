package app

import (
	"database/sql"
	"fmt"

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
