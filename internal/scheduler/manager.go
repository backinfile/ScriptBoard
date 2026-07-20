package scheduler

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"scriptboard/internal/runmanager"
)

type VariableLoader func() (map[string]string, error)

type CreateRequest struct {
	Name              string
	ScriptPath        string
	ArgumentsTemplate string
	Expression        string
	TimeoutSeconds    int
	AllowOverlap      bool
}

type Schedule struct {
	ID                string
	Name              string
	ScriptPath        string
	ArgumentsTemplate string
	Expression        string
	TimeoutSeconds    int
	Enabled           bool
	AllowOverlap      bool
	NextFireAt        time.Time
	LastResult        string
	LastRunID         string
}

type Manager struct {
	db            *sql.DB
	runs          *runmanager.Manager
	loadVariables VariableLoader
	parser        cron.Parser
	now           func() time.Time
	tick          time.Duration
	stop          chan struct{}
	done          chan struct{}
	closeOnce     sync.Once
}

func New(db *sql.DB, runs *runmanager.Manager, loadVariables VariableLoader, now func() time.Time, tick time.Duration) *Manager {
	if now == nil {
		now = time.Now
	}
	if tick <= 0 {
		tick = time.Second
	}
	manager := &Manager{
		db: db, runs: runs, loadVariables: loadVariables,
		parser: cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
		now:    now, tick: tick, stop: make(chan struct{}), done: make(chan struct{}),
	}
	manager.reconcileMissed()
	go manager.loop()
	return manager
}

func (m *Manager) Update(id string, request CreateRequest) error {
	spec, err := m.parser.Parse(request.Expression)
	if err != nil {
		return fmt.Errorf("五段 cron 无效: %w", err)
	}
	result, err := m.db.Exec(`UPDATE schedules SET name = ?, script_path = ?, arguments_template = ?, expression = ?, timeout_seconds = ?, allow_overlap = ?, next_fire_at = ?, updated_at = ? WHERE id = ? AND deleted = 0`,
		request.Name, request.ScriptPath, request.ArgumentsTemplate, request.Expression, request.TimeoutSeconds, request.AllowOverlap,
		spec.Next(m.now()).UnixNano(), m.now().UnixNano(), id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (m *Manager) SetEnabled(id string, enabled bool) error {
	result, err := m.db.Exec("UPDATE schedules SET enabled = ?, updated_at = ? WHERE id = ? AND deleted = 0", enabled, m.now().UnixNano(), id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (m *Manager) Delete(id string) error {
	result, err := m.db.Exec("UPDATE schedules SET enabled = 0, deleted = 1, updated_at = ? WHERE id = ? AND deleted = 0", m.now().UnixNano(), id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (m *Manager) reconcileMissed() {
	now := m.now()
	rows, err := m.db.Query("SELECT id, expression, next_fire_at FROM schedules WHERE enabled = 1 AND deleted = 0 AND next_fire_at < ?", now.UnixNano())
	if err != nil {
		return
	}
	type missed struct {
		id, expression string
		scheduledFor   int64
	}
	var items []missed
	for rows.Next() {
		var item missed
		if rows.Scan(&item.id, &item.expression, &item.scheduledFor) == nil {
			items = append(items, item)
		}
	}
	_ = rows.Close()
	for _, item := range items {
		spec, err := m.parser.Parse(item.expression)
		if err != nil {
			continue
		}
		next := spec.Next(now)
		triggerID, _ := randomID()
		_, _ = m.db.Exec("UPDATE schedules SET next_fire_at = ?, updated_at = ? WHERE id = ?", next.UnixNano(), now.UnixNano(), item.id)
		_, _ = m.db.Exec("INSERT INTO schedule_triggers (id, schedule_id, scheduled_for, result, run_id, error) VALUES (?, ?, ?, 'missed', '', 'service was not running')", triggerID, item.id, item.scheduledFor)
	}
}

func (m *Manager) Create(request CreateRequest) (string, error) {
	spec, err := m.parser.Parse(request.Expression)
	if err != nil {
		return "", fmt.Errorf("五段 cron 无效: %w", err)
	}
	next := spec.Next(m.now())
	id, err := randomID()
	if err != nil {
		return "", err
	}
	_, err = m.db.Exec(`INSERT INTO schedules
		(id, name, script_path, arguments_template, expression, timeout_seconds, enabled, allow_overlap, next_fire_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?)`,
		id, request.Name, request.ScriptPath, request.ArgumentsTemplate, request.Expression, request.TimeoutSeconds,
		request.AllowOverlap, next.UnixNano(), m.now().UnixNano(), m.now().UnixNano(),
	)
	if err != nil {
		return "", fmt.Errorf("保存 Schedule: %w", err)
	}
	return id, nil
}

func (m *Manager) List() ([]Schedule, error) {
	rows, err := m.db.Query(`SELECT s.id, s.name, s.script_path, s.arguments_template, s.expression, s.timeout_seconds,
		s.enabled, s.allow_overlap, s.next_fire_at,
		COALESCE((SELECT result FROM schedule_triggers t WHERE t.schedule_id = s.id ORDER BY t.scheduled_for DESC LIMIT 1), ''),
		COALESCE((SELECT run_id FROM schedule_triggers t WHERE t.schedule_id = s.id ORDER BY t.scheduled_for DESC LIMIT 1), '')
		FROM schedules s WHERE s.deleted = 0 ORDER BY s.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var schedules []Schedule
	for rows.Next() {
		var schedule Schedule
		var next int64
		if err := rows.Scan(&schedule.ID, &schedule.Name, &schedule.ScriptPath, &schedule.ArgumentsTemplate, &schedule.Expression,
			&schedule.TimeoutSeconds, &schedule.Enabled, &schedule.AllowOverlap, &next, &schedule.LastResult, &schedule.LastRunID); err != nil {
			return nil, err
		}
		schedule.NextFireAt = time.Unix(0, next)
		schedules = append(schedules, schedule)
	}
	return schedules, rows.Err()
}

func (m *Manager) Preview(expression string, count int) ([]time.Time, error) {
	spec, err := m.parser.Parse(expression)
	if err != nil {
		return nil, err
	}
	result := make([]time.Time, 0, count)
	next := m.now()
	for range count {
		next = spec.Next(next)
		result = append(result, next)
	}
	return result, nil
}

func (m *Manager) loop() {
	defer close(m.done)
	ticker := time.NewTicker(m.tick)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.fireDue()
		}
	}
}

func (m *Manager) fireDue() {
	now := m.now()
	rows, err := m.db.Query(`SELECT id, name, script_path, arguments_template, expression, timeout_seconds, allow_overlap, next_fire_at
		FROM schedules WHERE enabled = 1 AND deleted = 0 AND next_fire_at <= ? ORDER BY next_fire_at`, now.UnixNano())
	if err != nil {
		return
	}
	type due struct {
		id, name, scriptPath, arguments, expression string
		timeout                                     int
		allowOverlap                                bool
		scheduledFor                                int64
	}
	var dueSchedules []due
	for rows.Next() {
		var item due
		if rows.Scan(&item.id, &item.name, &item.scriptPath, &item.arguments, &item.expression, &item.timeout, &item.allowOverlap, &item.scheduledFor) == nil {
			dueSchedules = append(dueSchedules, item)
		}
	}
	_ = rows.Close()
	for _, item := range dueSchedules {
		spec, err := m.parser.Parse(item.expression)
		if err != nil {
			continue
		}
		next := spec.Next(now)
		_, _ = m.db.Exec("UPDATE schedules SET next_fire_at = ?, updated_at = ? WHERE id = ?", next.UnixNano(), now.UnixNano(), item.id)
		triggerID, _ := randomID()
		if !item.allowOverlap && m.runs.IsActiveScript(item.scriptPath) {
			_, _ = m.db.Exec("INSERT INTO schedule_triggers (id, schedule_id, scheduled_for, result, run_id, error) VALUES (?, ?, ?, 'skipped', '', '')", triggerID, item.id, item.scheduledFor)
			continue
		}
		variables, loadErr := m.loadVariables()
		if loadErr != nil {
			_, _ = m.db.Exec("INSERT INTO schedule_triggers (id, schedule_id, scheduled_for, result, run_id, error) VALUES (?, ?, ?, 'rejected', '', ?)", triggerID, item.id, item.scheduledFor, loadErr.Error())
			continue
		}
		runID, startErr := m.runs.Start(runmanager.StartRequest{
			ScriptPath: item.scriptPath, ArgumentsTemplate: item.arguments, TimeoutSeconds: item.timeout,
			SourceType: "scheduler", SourceName: item.name, Variables: variables,
		})
		if startErr != nil {
			_, _ = m.db.Exec("INSERT INTO schedule_triggers (id, schedule_id, scheduled_for, result, run_id, error) VALUES (?, ?, ?, 'rejected', '', ?)", triggerID, item.id, item.scheduledFor, startErr.Error())
			continue
		}
		_, _ = m.db.Exec("INSERT INTO schedule_triggers (id, schedule_id, scheduled_for, result, run_id, error) VALUES (?, ?, ?, 'created', ?, '')", triggerID, item.id, item.scheduledFor, runID)
	}
}

func (m *Manager) Close() {
	m.closeOnce.Do(func() { close(m.stop) })
	<-m.done
}

func randomID() (string, error) {
	value := make([]byte, 18)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
