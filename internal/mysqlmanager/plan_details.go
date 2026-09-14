package mysqlmanager

import (
	"context"
	"time"
)

// PlanNextFive uses the same parser and server clock as automatic backups.
func (m *Manager) PlanNextFive(plan Plan) ([]time.Time, error) {
	schedule, err := planParser.Parse(plan.Expression)
	if err != nil {
		return nil, err
	}
	cursor := m.now()
	result := make([]time.Time, 0, 5)
	for range 5 {
		next := schedule.Next(cursor)
		if !next.After(cursor) {
			break
		}
		result = append(result, next)
		cursor = next
	}
	return result, nil
}

// PlanOperationsPage retains execution history independently of backup file rotation.
func (m *Manager) PlanOperationsPage(ctx context.Context, planID string, limit, offset int) ([]Operation, int, error) {
	var total int
	if err := m.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM mysql_operations WHERE plan_id=?", planID).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit = max(1, min(limit, 100))
	rows, err := m.db.QueryContext(ctx, "SELECT id FROM mysql_operations WHERE plan_id=? ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?", planID, limit, max(offset, 0))
	if err != nil {
		return nil, 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	result := make([]Operation, 0, len(ids))
	for _, id := range ids {
		operation, err := m.Operation(ctx, id)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, operation)
	}
	return result, total, nil
}
