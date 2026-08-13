package scheduler

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"scriptboard/internal/hostfiles"
	"scriptboard/internal/runmanager"
	"scriptboard/internal/secretredaction"
)

type VariableLoader func() (map[string]string, error)
type AuditRecorder func(action, target, result, source string)
type ScriptPreparer func(scheduleID string) (hostfiles.Script, hostfiles.PreparedDirectory, error)

type CreateRequest struct {
	Name              string
	GroupID           string
	GroupName         string
	ScriptPath        string
	ArgumentsTemplate string
	Expression        string
	TimeoutSeconds    int
	AllowOverlap      bool
}

type Schedule struct {
	ID                string
	Name              string
	GroupID           string
	GroupName         string
	ScriptPath        string
	ArgumentsTemplate string
	Expression        string
	TimeoutSeconds    int
	Enabled           bool
	AllowOverlap      bool
	NextFireAt        time.Time
	LastResult        string
	LastRunID         string
	LastError         string
	NextFive          []time.Time
}

type Manager struct {
	db             *sql.DB
	runs           *runmanager.Manager
	loadVariables  VariableLoader
	now            func() time.Time
	tick           time.Duration
	stop           chan struct{}
	done           chan struct{}
	wake           chan struct{}
	pollClock      bool
	closeOnce      sync.Once
	initializeOnce sync.Once
	pauseMu        sync.Mutex
	fireMu         sync.Mutex
	paused         bool
	audit          AuditRecorder
	prepareMu      sync.RWMutex
	prepareScript  ScriptPreparer
}

func New(db *sql.DB, runs *runmanager.Manager, loadVariables VariableLoader, now func() time.Time, tick time.Duration, audits ...AuditRecorder) *Manager {
	return newManager(db, runs, loadVariables, now, tick, false, audits...)
}

func NewPaused(db *sql.DB, runs *runmanager.Manager, loadVariables VariableLoader, now func() time.Time, tick time.Duration, audits ...AuditRecorder) *Manager {
	return newManager(db, runs, loadVariables, now, tick, true, audits...)
}

func newManager(db *sql.DB, runs *runmanager.Manager, loadVariables VariableLoader, now func() time.Time, tick time.Duration, paused bool, audits ...AuditRecorder) *Manager {
	pollClock := now != nil
	if now == nil {
		now = time.Now
	}
	if tick <= 0 {
		tick = time.Second
	}
	manager := &Manager{
		db: db, runs: runs, loadVariables: loadVariables,
		now: now, tick: tick, stop: make(chan struct{}), done: make(chan struct{}),
		wake: make(chan struct{}, 1), pollClock: pollClock, paused: paused,
	}
	if len(audits) > 0 {
		manager.audit = audits[0]
	}
	if !paused {
		manager.initialize()
	}
	go manager.loop()
	return manager
}

var ErrPaused = errors.New("scheduler is paused for update maintenance")

func (m *Manager) SetScriptPreparer(preparer ScriptPreparer) {
	m.prepareMu.Lock()
	m.prepareScript = preparer
	m.prepareMu.Unlock()
}

func (m *Manager) preparedSchedule(id, path string) (*hostfiles.Script, *hostfiles.PreparedDirectory, error) {
	m.prepareMu.RLock()
	preparer := m.prepareScript
	m.prepareMu.RUnlock()
	if preparer == nil {
		return nil, nil, nil
	}
	script, directory, err := preparer(id)
	if err != nil || script.Path != path || script.Digest == "" || directory.Path != script.Directory {
		if err == nil {
			err = errors.New("Broker returned a mismatched scheduled script")
		}
		return nil, nil, err
	}
	return &script, &directory, nil
}

func (m *Manager) initialize() {
	m.initializeOnce.Do(func() {
		m.disableInvalidSchedules()
		m.aggregateOldTriggers()
		m.reconcileMissed()
	})
}

func (m *Manager) aggregateOldTriggers() {
	cutoff := m.now().AddDate(-1, 0, 0).UnixNano()
	rows, err := m.db.Query("SELECT id, schedule_id, scheduled_for, result FROM schedule_triggers WHERE run_id = '' AND scheduled_for < ? ORDER BY scheduled_for", cutoff)
	if err != nil {
		return
	}
	type aggregateKey struct{ scheduleID, period, result string }
	counts := make(map[aggregateKey]int64)
	var ids []string
	for rows.Next() {
		var id, scheduleID, result string
		var scheduledFor int64
		if rows.Scan(&id, &scheduleID, &scheduledFor, &result) == nil {
			period := time.Unix(0, scheduledFor).UTC().Format("2006-01")
			counts[aggregateKey{scheduleID: scheduleID, period: period, result: result}]++
			ids = append(ids, id)
		}
	}
	_ = rows.Close()
	if len(ids) == 0 {
		return
	}
	transaction, err := m.db.Begin()
	if err != nil {
		return
	}
	defer transaction.Rollback()
	for key, count := range counts {
		if _, err := transaction.Exec(`INSERT INTO schedule_trigger_aggregates (schedule_id, period, result, trigger_count) VALUES (?, ?, ?, ?)
			ON CONFLICT(schedule_id, period, result) DO UPDATE SET trigger_count = trigger_count + excluded.trigger_count`, key.scheduleID, key.period, key.result, count); err != nil {
			return
		}
	}
	for _, id := range ids {
		if _, err := transaction.Exec("DELETE FROM schedule_triggers WHERE id = ?", id); err != nil {
			return
		}
	}
	if transaction.Commit() == nil {
		m.recordAuditSource("aggregate_schedule_triggers", fmt.Sprintf("%d triggers", len(ids)), "succeeded", "system")
	}
}

func (m *Manager) Update(id string, request CreateRequest) error {
	now := m.now()
	preview, err := PreviewExpression(request.Expression, now)
	if err != nil {
		return err
	}
	groupID := sql.NullString{String: request.GroupID, Valid: request.GroupID != ""}
	result, err := m.db.Exec(`UPDATE schedules SET name = ?, group_id = ?, group_name = ?, script_path = ?, script_path_key = ?, arguments_template = ?, expression = ?, timeout_seconds = ?, allow_overlap = ?, next_fire_at = ?, updated_at = ? WHERE id = ? AND deleted = 0`,
		request.Name, groupID, request.GroupName, request.ScriptPath, hostfiles.ComparisonKey(request.ScriptPath), request.ArgumentsTemplate, preview.Expression, request.TimeoutSeconds, request.AllowOverlap,
		preview.NextFive[0].UnixNano(), now.UnixNano(), id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	m.signalWake()
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
	m.signalWake()
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
	m.signalWake()
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
		spec, _, err := parseExpression(item.expression, now)
		if err != nil {
			m.disableInvalidSchedule(item.id, item.expression)
			continue
		}
		missedCount := 1
		cursor := time.Unix(0, item.scheduledFor).In(now.Location())
		for missedCount < 1_000_000 {
			candidate := spec.Next(cursor)
			if candidate.After(now) {
				break
			}
			missedCount++
			cursor = candidate
		}
		next := spec.Next(now)
		triggerID, err := randomID()
		if err != nil {
			continue
		}
		reserved, err := m.advanceWithTrigger(item.id, item.scheduledFor, next.UnixNano(), now.UnixNano(), triggerID, "missed",
			fmt.Sprintf("服务停机期间错过 %d 次触发", missedCount))
		if err != nil || !reserved {
			continue
		}
		m.recordAudit("schedule_trigger", item.id, "missed")
	}
}

func (m *Manager) Create(request CreateRequest) (string, error) {
	now := m.now()
	preview, err := PreviewExpression(request.Expression, now)
	if err != nil {
		return "", err
	}
	id, err := randomID()
	if err != nil {
		return "", err
	}
	groupID := sql.NullString{String: request.GroupID, Valid: request.GroupID != ""}
	_, err = m.db.Exec(`INSERT INTO schedules
		(id, name, group_id, group_name, script_path, script_path_key, arguments_template, expression, timeout_seconds, enabled, allow_overlap, next_fire_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?)`,
		id, request.Name, groupID, request.GroupName, request.ScriptPath, hostfiles.ComparisonKey(request.ScriptPath), request.ArgumentsTemplate, preview.Expression, request.TimeoutSeconds,
		request.AllowOverlap, preview.NextFive[0].UnixNano(), now.UnixNano(), now.UnixNano(),
	)
	if err != nil {
		return "", fmt.Errorf("保存 Schedule: %w", err)
	}
	m.signalWake()
	return id, nil
}

func (m *Manager) List() ([]Schedule, error) {
	return m.ListPage(1000, 0)
}

func (m *Manager) RunNow(id string) (string, error) {
	return m.RunNowAs(id, "", "")
}

func (m *Manager) RunNowAs(id, userID, username string) (string, error) {
	m.pauseMu.Lock()
	paused := m.paused
	m.pauseMu.Unlock()
	if paused {
		return "", ErrPaused
	}
	var schedule Schedule
	if err := m.db.QueryRow(`SELECT id, name, script_path, arguments_template, timeout_seconds, allow_overlap
		FROM schedules WHERE id = ? AND deleted = 0`, id).Scan(
		&schedule.ID, &schedule.Name, &schedule.ScriptPath, &schedule.ArgumentsTemplate, &schedule.TimeoutSeconds, &schedule.AllowOverlap,
	); err != nil {
		return "", err
	}
	if !schedule.AllowOverlap && m.runs.IsActiveScript(schedule.ScriptPath) {
		return "", errors.New("该脚本已有活动运行，计划禁止重叠执行")
	}
	variables, err := m.loadVariables()
	if err != nil {
		return "", err
	}
	triggerID, err := randomID()
	if err != nil {
		return "", err
	}
	if _, err := m.db.Exec("INSERT INTO schedule_triggers (id, schedule_id, scheduled_for, result, run_id, error) VALUES (?, ?, ?, 'pending', '', '')", triggerID, schedule.ID, m.now().UnixNano()); err != nil {
		return "", fmt.Errorf("record pending schedule trigger: %w", err)
	}
	prepared, preparedDirectory, err := m.preparedSchedule(schedule.ID, schedule.ScriptPath)
	if err != nil {
		_ = m.completeTrigger(triggerID, "rejected", "", secretredaction.String(err.Error()))
		m.recordAudit("schedule_run_now", schedule.Name, "rejected")
		return "", err
	}
	runID, err := m.runs.Start(runmanager.StartRequest{
		ScriptPath: schedule.ScriptPath, ArgumentsTemplate: schedule.ArgumentsTemplate, TimeoutSeconds: schedule.TimeoutSeconds,
		SourceType: "admin/schedule-now", SourceName: schedule.Name, SourceID: schedule.ID, Variables: variables,
		InitiatorUserID: userID, InitiatorUsername: username,
		PreparedScript: prepared, PreparedDirectory: preparedDirectory,
	})
	result, errorText := "created", ""
	if err != nil {
		result, errorText = "rejected", secretredaction.String(err.Error())
	}
	if completeErr := m.completeTrigger(triggerID, result, runID, errorText); completeErr != nil {
		m.recordAudit("schedule_trigger_record", schedule.Name, "pending")
	}
	m.recordAudit("schedule_run_now", schedule.Name, result)
	if err != nil {
		return "", err
	}
	return runID, nil
}

func (m *Manager) Count() (int, error) {
	var count int
	err := m.db.QueryRow("SELECT COUNT(*) FROM schedules WHERE deleted = 0").Scan(&count)
	return count, err
}

func (m *Manager) ListPage(limit, offset int) ([]Schedule, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := m.db.Query(`SELECT s.id, s.name, COALESCE(s.group_id, ''), COALESCE(g.name, ''), s.script_path, s.arguments_template, s.expression, s.timeout_seconds,
		s.enabled, s.allow_overlap, s.next_fire_at,
		COALESCE((SELECT result FROM schedule_triggers t WHERE t.schedule_id = s.id ORDER BY t.scheduled_for DESC LIMIT 1), ''),
		COALESCE((SELECT run_id FROM schedule_triggers t WHERE t.schedule_id = s.id ORDER BY t.scheduled_for DESC LIMIT 1), ''),
		COALESCE((SELECT error FROM schedule_triggers t WHERE t.schedule_id = s.id ORDER BY t.scheduled_for DESC LIMIT 1), '')
		FROM schedules s
		LEFT JOIN schedule_groups g ON g.id = s.group_id
		WHERE s.deleted = 0 ORDER BY s.created_at LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var schedules []Schedule
	for rows.Next() {
		var schedule Schedule
		var next int64
		if err := rows.Scan(&schedule.ID, &schedule.Name, &schedule.GroupID, &schedule.GroupName, &schedule.ScriptPath, &schedule.ArgumentsTemplate, &schedule.Expression,
			&schedule.TimeoutSeconds, &schedule.Enabled, &schedule.AllowOverlap, &next, &schedule.LastResult, &schedule.LastRunID, &schedule.LastError); err != nil {
			return nil, err
		}
		schedule.NextFireAt = time.Unix(0, next).In(m.now().Location())
		if preview, parseErr := PreviewExpression(schedule.Expression, m.now()); parseErr == nil {
			schedule.Expression = preview.Expression
			schedule.NextFive = preview.NextFive
		}
		schedules = append(schedules, schedule)
	}
	return schedules, rows.Err()
}

func (m *Manager) Preview(expression string) (ExpressionPreview, error) {
	return PreviewExpression(expression, m.now())
}

func (m *Manager) loop() {
	defer close(m.done)
	for {
		m.pauseMu.Lock()
		paused := m.paused
		m.pauseMu.Unlock()
		if !paused {
			m.fireMu.Lock()
			m.fireDue()
			m.fireMu.Unlock()
		}

		delay, scheduled := m.nextWakeDelay(paused)
		var timer *time.Timer
		var timerChannel <-chan time.Time
		if scheduled {
			timer = time.NewTimer(delay)
			timerChannel = timer.C
		}
		select {
		case <-m.stop:
			if timer != nil {
				timer.Stop()
			}
			return
		case <-m.wake:
			if timer != nil && !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timerChannel:
		}
	}
}

func (m *Manager) nextWakeDelay(paused bool) (time.Duration, bool) {
	if paused {
		return 0, false
	}
	var next sql.NullInt64
	if err := m.db.QueryRow("SELECT MIN(next_fire_at) FROM schedules WHERE enabled = 1 AND deleted = 0").Scan(&next); err != nil {
		return m.tick, true
	}
	if !next.Valid {
		if m.pollClock {
			return m.tick, true
		}
		return 0, false
	}
	delay := time.Unix(0, next.Int64).Sub(m.now())
	if delay < 0 {
		delay = 0
	}
	if m.pollClock && delay > m.tick {
		delay = m.tick
	}
	return delay, true
}

func (m *Manager) signalWake() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) PauseAndWait() {
	m.pauseMu.Lock()
	m.paused = true
	m.pauseMu.Unlock()
	m.signalWake()
	m.fireMu.Lock()
	m.fireMu.Unlock()
}

func (m *Manager) Resume() {
	m.initialize()
	m.pauseMu.Lock()
	m.paused = false
	m.pauseMu.Unlock()
	m.signalWake()
}

func (m *Manager) Paused() bool {
	m.pauseMu.Lock()
	defer m.pauseMu.Unlock()
	return m.paused
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
		spec, _, err := parseExpression(item.expression, now)
		if err != nil {
			m.disableInvalidSchedule(item.id, item.expression)
			continue
		}
		next := spec.Next(now)
		triggerID, err := randomID()
		if err != nil {
			continue
		}
		reserved, err := m.advanceWithTrigger(item.id, item.scheduledFor, next.UnixNano(), now.UnixNano(), triggerID, "pending", "")
		if err != nil || !reserved {
			continue
		}
		if !item.allowOverlap && m.runs.IsActiveScript(item.scriptPath) {
			_ = m.completeTrigger(triggerID, "skipped", "", "")
			m.recordAudit("schedule_trigger", item.name, "skipped")
			continue
		}
		variables, loadErr := m.loadVariables()
		if loadErr != nil {
			_ = m.completeTrigger(triggerID, "rejected", "", secretredaction.String(loadErr.Error()))
			m.recordAudit("schedule_trigger", item.name, "rejected")
			continue
		}
		prepared, preparedDirectory, prepareErr := m.preparedSchedule(item.id, item.scriptPath)
		if prepareErr != nil {
			_ = m.completeTrigger(triggerID, "rejected", "", secretredaction.String(prepareErr.Error()))
			m.recordAudit("schedule_trigger", item.name, "rejected")
			continue
		}
		runID, startErr := m.runs.Start(runmanager.StartRequest{
			ScriptPath: item.scriptPath, ArgumentsTemplate: item.arguments, TimeoutSeconds: item.timeout,
			SourceType: "scheduler", SourceName: item.name, SourceID: item.id, Variables: variables,
			PreparedScript: prepared, PreparedDirectory: preparedDirectory,
		})
		if startErr != nil {
			_ = m.completeTrigger(triggerID, "rejected", "", secretredaction.String(startErr.Error()))
			m.recordAudit("schedule_trigger", item.name, "rejected")
			continue
		}
		_ = m.completeTrigger(triggerID, "created", runID, "")
		m.recordAudit("schedule_trigger", item.name, "created")
	}
}

func (m *Manager) advanceWithTrigger(scheduleID string, scheduledFor, nextFireAt, updatedAt int64, triggerID, result, errorText string) (bool, error) {
	transaction, err := m.db.Begin()
	if err != nil {
		return false, err
	}
	defer transaction.Rollback()
	advance, err := transaction.Exec(`UPDATE schedules SET next_fire_at = ?, updated_at = ?
		WHERE id = ? AND enabled = 1 AND deleted = 0 AND next_fire_at = ?`, nextFireAt, updatedAt, scheduleID, scheduledFor)
	if err != nil {
		return false, err
	}
	affected, err := advance.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	if _, err := transaction.Exec(`INSERT INTO schedule_triggers
		(id, schedule_id, scheduled_for, result, run_id, error) VALUES (?, ?, ?, ?, '', ?)`,
		triggerID, scheduleID, scheduledFor, result, errorText); err != nil {
		return false, err
	}
	if err := transaction.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (m *Manager) completeTrigger(triggerID, result, runID, errorText string) error {
	updated, err := m.db.Exec(`UPDATE schedule_triggers SET result = ?, run_id = ?, error = ? WHERE id = ?`, result, runID, errorText, triggerID)
	if err != nil {
		return err
	}
	affected, err := updated.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (m *Manager) disableInvalidSchedules() {
	rows, err := m.db.Query("SELECT id, expression FROM schedules WHERE enabled = 1 AND deleted = 0")
	if err != nil {
		return
	}
	var schedules [][2]string
	for rows.Next() {
		var id, expression string
		if rows.Scan(&id, &expression) == nil {
			schedules = append(schedules, [2]string{id, expression})
		}
	}
	_ = rows.Close()
	for _, schedule := range schedules {
		if _, err := PreviewExpression(schedule[1], m.now()); err != nil {
			m.disableInvalidSchedule(schedule[0], schedule[1])
		}
	}
}

func (m *Manager) disableInvalidSchedule(id, expression string) {
	now := m.now()
	result, err := m.db.Exec("UPDATE schedules SET enabled = 0, updated_at = ? WHERE id = ? AND enabled = 1 AND deleted = 0", now.UnixNano(), id)
	if err != nil {
		return
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return
	}
	m.recordAudit("disable_invalid_schedule", expression, "failed")
}

func (m *Manager) recordAudit(action, target, result string) {
	m.recordAuditSource(action, target, result, "scheduler")
}

func (m *Manager) recordAuditSource(action, target, result, source string) {
	if m.audit != nil {
		m.audit(action, target, result, source)
		return
	}
	_, _ = m.db.Exec("INSERT INTO audit_events (occurred_at, action, target, result, source_address) VALUES (?, ?, ?, ?, ?)", m.now().UTC().Unix(), action, target, result, source)
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
