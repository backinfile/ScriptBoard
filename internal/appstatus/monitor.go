package appstatus

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"path"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Kind string

const (
	KindHost   Kind = "host"
	KindDocker Kind = "docker"
)

type RawProcess struct {
	PID, ParentPID        int32
	CreatedAt             time.Time
	Name, ExecutablePath  string
	KernelThread          bool
	CPUSeconds            float64
	ResidentMemoryBytes   uint64
	Threads               int32
	ReadBytes, WriteBytes uint64
}

type RawContainer struct {
	ID, Name, Image                         string
	State, Status, Health                   string
	ComposeProject, ComposeService          string
	PublishedPorts                          []string
	CPUPercent                              float64
	MemoryBytes, MemoryLimitBytes           uint64
	ReadBytesPerSecond, WriteBytesPerSecond float64
	ProcessCount                            int
}

type RawSnapshot struct {
	CollectedAt      time.Time
	LogicalCores     int
	TotalMemoryBytes uint64
	Processes        []RawProcess
	Containers       []RawContainer
	DockerAvailable  bool
	Errors           map[string]string
}

type Probe interface {
	Snapshot(context.Context) RawSnapshot
}

type Options struct {
	HostOS   string
	Interval time.Duration
	Now      func() time.Time
}

type Application struct {
	ID                  string  `json:"id"`
	Name                string  `json:"name"`
	Identity            string  `json:"identity"`
	Technical           string  `json:"technical"`
	Kind                Kind    `json:"kind"`
	Pinnable            bool    `json:"pinnable"`
	Pinned              bool    `json:"pinned"`
	Running             bool    `json:"running"`
	RateAvailable       bool    `json:"rateAvailable"`
	CPUPercent          float64 `json:"cpuPercent"`
	MemoryPercent       float64 `json:"memoryPercent"`
	MemoryBytes         uint64  `json:"memoryBytes"`
	MemoryLimitBytes    uint64  `json:"memoryLimitBytes"`
	ReadBytesPerSecond  float64 `json:"readBytesPerSecond"`
	WriteBytesPerSecond float64 `json:"writeBytesPerSecond"`
	ProcessCount        int     `json:"processCount"`
	ThreadCount         int     `json:"threadCount"`
	CanMoveUp           bool    `json:"canMoveUp"`
	CanMoveDown         bool    `json:"canMoveDown"`
}

type Query struct {
	Search, Sort, Direction string
	Kind                    Kind
	Limit                   int
}

type View struct {
	Applications    []Application     `json:"applications"`
	Pinned          []Application     `json:"pinned"`
	CollectedAt     time.Time         `json:"collectedAt"`
	HostCount       int               `json:"hostCount"`
	DockerCount     int               `json:"dockerCount"`
	Total           int               `json:"total"`
	Matched         int               `json:"matched"`
	Truncated       bool              `json:"truncated"`
	DockerAvailable bool              `json:"dockerAvailable"`
	Errors          map[string]string `json:"errors,omitempty"`
}

type processIdentity struct {
	PID       int32
	CreatedAt int64
}

type Monitor struct {
	db      *sql.DB
	probe   Probe
	options Options

	refreshMu       sync.Mutex
	mu              sync.RWMutex
	current         RawSnapshot
	previous        map[processIdentity]RawProcess
	apps            []Application
	metricCleanupAt time.Time
	cancel          context.CancelFunc
	done            chan struct{}
	close           sync.Once
}

func New(db *sql.DB, probe Probe, options Options) (*Monitor, error) {
	if db == nil || probe == nil {
		return nil, errors.New("application status database and probe are required")
	}
	if options.HostOS == "" {
		options.HostOS = runtime.GOOS
	}
	if options.Interval <= 0 {
		options.Interval = 5 * time.Second
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	monitor := &Monitor{db: db, probe: probe, options: options, done: make(chan struct{})}
	if err := monitor.migrateHostPinsToApplicationNames(context.Background()); err != nil {
		return nil, err
	}
	return monitor, nil
}

func (m *Monitor) Refresh(ctx context.Context) error {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	raw := m.probe.Snapshot(ctx)
	if raw.CollectedAt.IsZero() {
		raw.CollectedAt = m.options.Now().UTC()
	}
	if raw.LogicalCores < 1 {
		raw.LogicalCores = 1
	}

	m.mu.Lock()
	apps := deriveApplications(raw, m.previous, m.current.CollectedAt, m.options.HostOS)
	previous := make(map[processIdentity]RawProcess, len(raw.Processes))
	for _, process := range raw.Processes {
		previous[identityForProcess(process)] = process
	}
	m.current = raw
	m.previous = previous
	m.apps = apps
	m.mu.Unlock()
	if err := m.persistMetricSamples(ctx, raw.CollectedAt, apps); err != nil {
		return err
	}
	if err := m.persistContainerVersions(ctx, raw.CollectedAt, apps, raw.Containers); err != nil {
		return err
	}
	now := m.options.Now().UTC()
	if m.metricCleanupAt.IsZero() || now.Sub(m.metricCleanupAt) >= time.Hour {
		if err := m.cleanupMetricSamples(ctx); err != nil {
			return err
		}
		m.metricCleanupAt = now
	}
	return nil
}

func (m *Monitor) Start(parent context.Context) {
	m.mu.Lock()
	if m.cancel != nil {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	m.mu.Unlock()
	go func() {
		defer close(m.done)
		_ = m.Refresh(ctx)
		ticker := time.NewTicker(m.options.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = m.Refresh(ctx)
			}
		}
	}()
}

func (m *Monitor) Close() {
	m.mu.RLock()
	cancel := m.cancel
	m.mu.RUnlock()
	if cancel != nil {
		cancel()
		<-m.done
	}
	m.close.Do(func() {
		m.refreshMu.Lock()
		defer m.refreshMu.Unlock()
		if closer, ok := m.probe.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	})
}

func (m *Monitor) View(ctx context.Context, query Query) (View, error) {
	m.mu.RLock()
	raw := m.current
	apps := append([]Application(nil), m.apps...)
	m.mu.RUnlock()

	pinned, err := m.loadPins(ctx, apps)
	if err != nil {
		return View{}, err
	}
	view := View{
		CollectedAt: raw.CollectedAt, DockerAvailable: raw.DockerAvailable,
		Errors: cloneErrors(raw.Errors), Total: len(apps), Pinned: pinned,
	}
	for _, application := range apps {
		switch application.Kind {
		case KindHost:
			view.HostCount++
		case KindDocker:
			view.DockerCount++
		}
	}

	search := strings.ToLower(strings.TrimSpace(query.Search))
	filtered := apps[:0]
	for _, application := range apps {
		if query.Kind != "" && application.Kind != query.Kind {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(application.Name+"\x00"+application.Technical), search) {
			continue
		}
		filtered = append(filtered, application)
	}
	view.Matched = len(filtered)
	sortApplications(filtered, query.Sort, query.Direction)
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	if len(filtered) > limit {
		view.Truncated = true
		filtered = filtered[:limit]
	}
	view.Applications = filtered
	return view, nil
}

func (m *Monitor) Pin(ctx context.Context, id string) error {
	m.mu.RLock()
	var selected Application
	for _, application := range m.apps {
		if application.ID == id {
			selected = application
			break
		}
	}
	m.mu.RUnlock()
	if selected.ID == "" {
		return errors.New("application is not in the current snapshot")
	}
	if !selected.Pinnable {
		return errors.New("application identity is restricted")
	}

	transaction, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	var exists int
	if err := transaction.QueryRowContext(ctx, "SELECT COUNT(*) FROM application_pins WHERE id = ?", selected.ID).Scan(&exists); err != nil {
		return err
	}
	now := m.options.Now().UTC().UnixNano()
	if exists == 0 {
		var sortOrder int
		if err := transaction.QueryRowContext(ctx, "SELECT COALESCE(MAX(sort_order), 0) + 1 FROM application_pins").Scan(&sortOrder); err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, `INSERT INTO application_pins
			(id, kind, identity, name, technical, sort_order, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			selected.ID, selected.Kind, selected.Identity, selected.Name, selected.Technical, sortOrder, now, now); err != nil {
			return err
		}
	} else if _, err := transaction.ExecContext(ctx, `UPDATE application_pins
		SET name = ?, technical = ?, updated_at = ? WHERE id = ?`,
		selected.Name, selected.Technical, now, selected.ID); err != nil {
		return err
	}
	return transaction.Commit()
}

func (m *Monitor) Unpin(ctx context.Context, id string) error {
	result, err := m.db.ExecContext(ctx, "DELETE FROM application_pins WHERE id = ?", id)
	if err != nil {
		return err
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if removed == 0 {
		return errors.New("application is not pinned")
	}
	return nil
}

func (m *Monitor) MovePin(ctx context.Context, id, direction string) error {
	id = strings.TrimSpace(id)
	direction = strings.ToLower(strings.TrimSpace(direction))
	if id == "" {
		return errors.New("application id is required")
	}
	if direction != "top" && direction != "up" && direction != "down" {
		return errors.New("pin direction must be top, up, or down")
	}
	transaction, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	rows, err := transaction.QueryContext(ctx, `SELECT id, sort_order
		FROM application_pins ORDER BY sort_order, created_at, id`)
	if err != nil {
		return err
	}
	type pinPosition struct {
		id    string
		order int
	}
	var positions []pinPosition
	for rows.Next() {
		var position pinPosition
		if err := rows.Scan(&position.id, &position.order); err != nil {
			rows.Close()
			return err
		}
		positions = append(positions, position)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	index := -1
	for position := range positions {
		if positions[position].id == id {
			index = position
			break
		}
	}
	if index < 0 {
		return errors.New("application is not pinned")
	}
	if (direction == "top" || direction == "up") && index == 0 ||
		direction == "down" && index == len(positions)-1 {
		return errors.New("application pin is already at the requested edge")
	}
	switch direction {
	case "top":
		moved := positions[index]
		copy(positions[1:index+1], positions[:index])
		positions[0] = moved
	case "up":
		positions[index], positions[index-1] = positions[index-1], positions[index]
	case "down":
		positions[index], positions[index+1] = positions[index+1], positions[index]
	}
	now := m.options.Now().UTC().UnixNano()
	for position, pin := range positions {
		if _, err := transaction.ExecContext(ctx,
			"UPDATE application_pins SET sort_order = ?, updated_at = ? WHERE id = ?",
			position+1, now, pin.id,
		); err != nil {
			return err
		}
	}
	return transaction.Commit()
}

func (m *Monitor) loadPins(ctx context.Context, applications []Application) ([]Application, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT id, kind, identity, name, technical
		FROM application_pins ORDER BY sort_order, created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	index := make(map[string]int, len(applications))
	for position := range applications {
		index[applications[position].ID] = position
	}
	var result []Application
	for rows.Next() {
		var stored Application
		if err := rows.Scan(&stored.ID, &stored.Kind, &stored.Identity, &stored.Name, &stored.Technical); err != nil {
			return nil, err
		}
		stored.Pinnable = true
		stored.Pinned = true
		if position, ok := index[stored.ID]; ok {
			applications[position].Pinned = true
			result = append(result, applications[position])
		} else {
			result = append(result, stored)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for position := range result {
		result[position].CanMoveUp = position > 0
		result[position].CanMoveDown = position+1 < len(result)
		if applicationPosition, ok := index[result[position].ID]; ok {
			applications[applicationPosition].CanMoveUp = result[position].CanMoveUp
			applications[applicationPosition].CanMoveDown = result[position].CanMoveDown
		}
	}
	return result, nil
}

func (m *Monitor) migrateHostPinsToApplicationNames(ctx context.Context) error {
	transaction, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	rows, err := transaction.QueryContext(ctx, `SELECT id, identity, name, technical, sort_order, created_at, updated_at
		FROM application_pins WHERE kind = ? ORDER BY sort_order, created_at, id`, KindHost)
	if err != nil {
		return err
	}
	type storedPin struct {
		id, identity, name, technical string
		sortOrder                     int
		createdAt, updatedAt          int64
	}
	var pins []storedPin
	needsMigration := false
	seen := make(map[string]struct{})
	for rows.Next() {
		var pin storedPin
		if err := rows.Scan(&pin.id, &pin.identity, &pin.name, &pin.technical, &pin.sortOrder, &pin.createdAt, &pin.updatedAt); err != nil {
			rows.Close()
			return err
		}
		identity := normalizeApplicationName(pin.name)
		if identity == "" {
			continue
		}
		id := stableID(KindHost, identity)
		if pin.id != id || pin.identity != identity {
			needsMigration = true
		}
		if _, duplicate := seen[id]; duplicate {
			needsMigration = true
			continue
		}
		seen[id] = struct{}{}
		pin.id = id
		pin.identity = identity
		pins = append(pins, pin)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !needsMigration {
		return transaction.Commit()
	}
	if _, err := transaction.ExecContext(ctx, "DELETE FROM application_pins WHERE kind = ?", KindHost); err != nil {
		return err
	}
	for _, pin := range pins {
		if _, err := transaction.ExecContext(ctx, `INSERT INTO application_pins
			(id, kind, identity, name, technical, sort_order, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			pin.id, KindHost, pin.identity, pin.name, pin.technical,
			pin.sortOrder, pin.createdAt, pin.updatedAt,
		); err != nil {
			return err
		}
	}
	return transaction.Commit()
}

func deriveApplications(raw RawSnapshot, previous map[processIdentity]RawProcess, previousAt time.Time, hostOS string) []Application {
	type aggregate struct {
		application Application
		cpuSeconds  float64
		readBytes   float64
		writeBytes  float64
		rateCount   int
	}
	byIdentity := make(map[string]*aggregate)
	elapsed := raw.CollectedAt.Sub(previousAt).Seconds()
	for _, process := range raw.Processes {
		if process.KernelThread {
			continue
		}
		pathIdentity := normalizeExecutablePath(process.ExecutablePath, hostOS)
		identity := normalizeApplicationName(process.Name)
		pinnable := identity != "" && pathIdentity != ""
		if !pinnable {
			identity = restrictedIdentity(process)
		}
		value := byIdentity[identity]
		if value == nil {
			technical := process.ExecutablePath
			if technical == "" {
				technical = process.Name
			}
			value = &aggregate{application: Application{
				ID: stableID(KindHost, identity), Kind: KindHost, Name: process.Name,
				Identity: identity, Technical: technical, Pinnable: pinnable, Running: true,
			}}
			byIdentity[identity] = value
		}
		value.application.ProcessCount++
		value.application.ThreadCount += int(process.Threads)
		value.application.MemoryBytes += process.ResidentMemoryBytes
		if raw.TotalMemoryBytes > 0 {
			value.application.MemoryPercent = float64(value.application.MemoryBytes) / float64(raw.TotalMemoryBytes) * 100
		}
		if process.CreatedAt.IsZero() {
			continue
		}
		before, ok := previous[identityForProcess(process)]
		if !ok || elapsed <= 0 || process.CPUSeconds < before.CPUSeconds ||
			process.ReadBytes < before.ReadBytes || process.WriteBytes < before.WriteBytes {
			continue
		}
		value.cpuSeconds += process.CPUSeconds - before.CPUSeconds
		value.readBytes += float64(process.ReadBytes - before.ReadBytes)
		value.writeBytes += float64(process.WriteBytes - before.WriteBytes)
		value.rateCount++
	}

	applications := make([]Application, 0, len(byIdentity)+len(raw.Containers))
	for _, value := range byIdentity {
		if elapsed > 0 && value.rateCount > 0 {
			value.application.RateAvailable = true
			value.application.CPUPercent = min(100, value.cpuSeconds/elapsed/float64(raw.LogicalCores)*100)
			value.application.ReadBytesPerSecond = value.readBytes / elapsed
			value.application.WriteBytesPerSecond = value.writeBytes / elapsed
		}
		applications = append(applications, value.application)
	}
	for _, container := range raw.Containers {
		identity := normalizeContainerName(container.Name)
		if identity == "" {
			continue
		}
		memoryPercent := 0.0
		if container.MemoryLimitBytes > 0 {
			memoryPercent = float64(container.MemoryBytes) / float64(container.MemoryLimitBytes) * 100
		}
		applications = append(applications, Application{
			ID: stableID(KindDocker, identity), Kind: KindDocker, Name: container.Name,
			Identity: identity, Technical: container.Image, Pinnable: true,
			Running: containerStateRunning(container.State), RateAvailable: containerStateRunning(container.State),
			CPUPercent: min(100, max(0, container.CPUPercent)), MemoryBytes: container.MemoryBytes,
			MemoryPercent: memoryPercent, MemoryLimitBytes: container.MemoryLimitBytes,
			ReadBytesPerSecond:  container.ReadBytesPerSecond,
			WriteBytesPerSecond: container.WriteBytesPerSecond, ProcessCount: container.ProcessCount,
		})
	}
	return applications
}

func sortApplications(applications []Application, field, direction string) {
	if field == "" {
		field = "cpu"
	}
	if direction == "" {
		direction = "desc"
	}
	descending := direction == "desc"
	sort.SliceStable(applications, func(i, j int) bool {
		left, right := applications[i], applications[j]
		comparison := 0
		switch field {
		case "name":
			comparison = strings.Compare(strings.ToLower(left.Name), strings.ToLower(right.Name))
		case "memory":
			comparison = compareOrdered(left.MemoryBytes, right.MemoryBytes)
		case "read":
			comparison = compareOrdered(left.ReadBytesPerSecond, right.ReadBytesPerSecond)
		case "write":
			comparison = compareOrdered(left.WriteBytesPerSecond, right.WriteBytesPerSecond)
		case "processes":
			comparison = compareOrdered(left.ProcessCount, right.ProcessCount)
		case "pinned":
			if left.Pinned != right.Pinned {
				if left.Pinned {
					comparison = 1
				} else {
					comparison = -1
				}
			}
		default:
			comparison = compareOrdered(left.CPUPercent, right.CPUPercent)
		}
		if comparison == 0 {
			nameComparison := strings.Compare(strings.ToLower(left.Name), strings.ToLower(right.Name))
			if nameComparison != 0 {
				return nameComparison < 0
			}
			return left.ID < right.ID
		}
		if descending {
			return comparison > 0
		}
		return comparison < 0
	})
}

func compareOrdered[T ~int | ~uint64 | ~float64](left, right T) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func identityForProcess(process RawProcess) processIdentity {
	return processIdentity{PID: process.PID, CreatedAt: process.CreatedAt.UnixNano()}
}

func normalizeExecutablePath(value, hostOS string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if hostOS == "windows" {
		value = strings.ReplaceAll(value, `\`, "/")
		value = strings.ToLower(path.Clean(value))
		return value
	}
	return path.Clean(value)
}

func normalizeContainerName(value string) string {
	return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(value, "/")))
}

func normalizeApplicationName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func restrictedIdentity(process RawProcess) string {
	return "restricted:" + process.Name + ":" + strconv.FormatInt(int64(process.PID), 10) + ":" + time.Unix(0, process.CreatedAt.UnixNano()).UTC().Format(time.RFC3339Nano)
}

func stableID(kind Kind, identity string) string {
	digest := sha256.Sum256([]byte(string(kind) + "\x00" + identity))
	return hex.EncodeToString(digest[:16])
}

func cloneErrors(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
