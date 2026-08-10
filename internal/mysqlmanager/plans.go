package mysqlmanager

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

type Plan struct {
	ID, Name, InstanceID, Expression string
	Databases                        []string
	RetentionCount                   int
	Enabled                          bool
	NextFireAt, CreatedAt, UpdatedAt time.Time
}

type PlanInput struct {
	ID, Name, InstanceID, Expression string
	Databases                        []string
	RetentionCount                   int
	Enabled                          bool
}

var planParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

func (m *Manager) SavePlan(ctx context.Context, input PlanInput) (Plan, error) {
	input.Name, input.InstanceID, input.Expression = strings.TrimSpace(input.Name), strings.TrimSpace(input.InstanceID), strings.TrimSpace(input.Expression)
	if input.Name == "" || input.InstanceID == "" || input.RetentionCount < 1 || input.RetentionCount > 365 {
		return Plan{}, errors.New("plan name, instance, and retention from 1 to 365 are required")
	}
	if _, err := m.Instance(ctx, input.InstanceID); err != nil {
		return Plan{}, err
	}
	deduplicated := make([]string, 0, len(input.Databases))
	seen := make(map[string]bool)
	for _, name := range input.Databases {
		name = strings.TrimSpace(name)
		if name == "" || IsSystemDatabase(name) || seen[name] {
			continue
		}
		seen[name] = true
		deduplicated = append(deduplicated, name)
	}
	if len(deduplicated) == 0 {
		return Plan{}, errors.New("at least one non-system database is required")
	}
	schedule, err := planParser.Parse(input.Expression)
	if err != nil {
		return Plan{}, fmt.Errorf("invalid five-field MySQL backup schedule: %w", err)
	}
	now := m.now()
	next := schedule.Next(now)
	databasesJSON, _ := json.Marshal(deduplicated)
	id := strings.TrimSpace(input.ID)
	if id == "" {
		id = randomID()
		_, err = m.db.ExecContext(ctx, `INSERT INTO mysql_backup_plans
			(id,name,instance_id,databases_json,expression,retention_count,enabled,next_fire_at,created_at,updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?)`, id, input.Name, input.InstanceID, databasesJSON, input.Expression, input.RetentionCount,
			input.Enabled, next.UnixNano(), now.UTC().UnixNano(), now.UTC().UnixNano())
	} else {
		_, err = m.db.ExecContext(ctx, `UPDATE mysql_backup_plans SET name=?,instance_id=?,databases_json=?,expression=?,retention_count=?,enabled=?,next_fire_at=?,updated_at=? WHERE id=?`,
			input.Name, input.InstanceID, databasesJSON, input.Expression, input.RetentionCount, input.Enabled, next.UnixNano(), now.UTC().UnixNano(), id)
	}
	if err != nil {
		return Plan{}, err
	}
	return m.Plan(ctx, id)
}

func (m *Manager) Plan(ctx context.Context, id string) (Plan, error) {
	var item Plan
	var databasesJSON string
	var next, created, updated int64
	err := m.db.QueryRowContext(ctx, `SELECT id,name,instance_id,databases_json,expression,retention_count,enabled,next_fire_at,created_at,updated_at
		FROM mysql_backup_plans WHERE id=?`, id).Scan(&item.ID, &item.Name, &item.InstanceID, &databasesJSON, &item.Expression,
		&item.RetentionCount, &item.Enabled, &next, &created, &updated)
	if err != nil {
		return Plan{}, err
	}
	if err := json.Unmarshal([]byte(databasesJSON), &item.Databases); err != nil {
		return Plan{}, err
	}
	item.NextFireAt, item.CreatedAt, item.UpdatedAt = time.Unix(0, next), time.Unix(0, created).UTC(), time.Unix(0, updated).UTC()
	return item, nil
}

func (m *Manager) Plans(ctx context.Context) ([]Plan, error) {
	rows, err := m.db.QueryContext(ctx, "SELECT id FROM mysql_backup_plans ORDER BY name COLLATE NOCASE")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	var result []Plan
	for _, id := range ids {
		item, err := m.Plan(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func (m *Manager) DeletePlan(ctx context.Context, id string) error {
	_, err := m.db.ExecContext(ctx, "DELETE FROM mysql_backup_plans WHERE id=?", id)
	return err
}

// ReconcilePlans advances stale schedules without replaying work missed while
// ScriptBoard was stopped.
func (m *Manager) ReconcilePlans(ctx context.Context) error {
	rows, err := m.db.QueryContext(ctx, "SELECT id, expression FROM mysql_backup_plans WHERE enabled=1 AND next_fire_at < ?", m.now().UnixNano())
	if err != nil {
		return err
	}
	defer rows.Close()
	type stale struct{ id, expression string }
	var values []stale
	for rows.Next() {
		var value stale
		if rows.Scan(&value.id, &value.expression) == nil {
			values = append(values, value)
		}
	}
	for _, value := range values {
		schedule, parseErr := planParser.Parse(value.expression)
		if parseErr != nil {
			_, _ = m.db.ExecContext(ctx, "UPDATE mysql_backup_plans SET enabled=0, updated_at=? WHERE id=?", m.now().UnixNano(), value.id)
			continue
		}
		_, err = m.db.ExecContext(ctx, "UPDATE mysql_backup_plans SET next_fire_at=?, updated_at=? WHERE id=?", schedule.Next(m.now()).UnixNano(), m.now().UnixNano(), value.id)
	}
	return err
}

// RunDuePlans executes due plans. Callers provide the cadence; each plan is
// advanced before work begins so a crash cannot replay the same occurrence.
func (m *Manager) RunDuePlans(ctx context.Context) error {
	rows, err := m.db.QueryContext(ctx, "SELECT id FROM mysql_backup_plans WHERE enabled=1 AND next_fire_at <= ? ORDER BY next_fire_at", m.now().UnixNano())
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	_ = rows.Close()
	for _, id := range ids {
		plan, err := m.Plan(ctx, id)
		if err != nil {
			continue
		}
		schedule, err := planParser.Parse(plan.Expression)
		if err != nil {
			continue
		}
		_, _ = m.db.ExecContext(ctx, "UPDATE mysql_backup_plans SET next_fire_at=?, updated_at=? WHERE id=?", schedule.Next(m.now()).UnixNano(), m.now().UnixNano(), plan.ID)
		for _, database := range plan.Databases {
			_, err := m.Backup(ctx, BackupRequest{InstanceID: plan.InstanceID, Database: database, PlanID: plan.ID, Kind: BackupScheduled, ActorUsername: "system"})
			if err != nil {
				if strings.Contains(err.Error(), "another MySQL operation") {
					_ = m.recordSkippedOperation(ctx, plan.InstanceID, database)
				}
				continue
			}
			_ = m.applyRetention(ctx, plan.ID, database, plan.RetentionCount)
		}
	}
	return nil
}

func (m *Manager) applyRetention(ctx context.Context, planID, database string, keep int) error {
	rows, err := m.db.QueryContext(ctx, `SELECT id,path FROM mysql_backups WHERE plan_id=? AND database_name=? AND kind='scheduled'
		ORDER BY created_at DESC LIMIT -1 OFFSET ?`, planID, database, keep)
	if err != nil {
		return err
	}
	type expired struct{ id, path string }
	var values []expired
	for rows.Next() {
		var value expired
		if rows.Scan(&value.id, &value.path) == nil {
			values = append(values, value)
		}
	}
	_ = rows.Close()
	for _, value := range values {
		if pathWithin(m.BackupRoot(), value.path) {
			if err := os.Remove(value.path); err != nil && !os.IsNotExist(err) {
				continue
			}
			_, _ = m.db.ExecContext(ctx, "DELETE FROM mysql_backups WHERE id=?", value.id)
			m.recordAudit(AuditEvent{Action: "mysql_backup_rotation", Target: value.id, Result: "succeeded", Actor: Actor{Username: "system"}})
		}
	}
	return nil
}

func (m *Manager) recordSkippedOperation(ctx context.Context, instanceID, database string) error {
	now := m.now().UTC().UnixNano()
	_, err := m.db.ExecContext(ctx, `INSERT INTO mysql_operations
		(id,kind,instance_id,database_name,phase,created_at,updated_at,actor_username)
		VALUES (?,'scheduled_backup',?,?,'skipped_overlap',?,?, 'system')`, randomID(), instanceID, database, now, now)
	return err
}

var _ = sql.ErrNoRows
