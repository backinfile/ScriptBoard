package clusterstatus

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

type Options struct {
	DB       *sql.DB
	Factory  Factory
	Interval time.Duration
	Now      func() time.Time
}

type Manager struct {
	db       *sql.DB
	factory  Factory
	interval time.Duration
	now      func() time.Time

	mu sync.RWMutex
	// operationMu serializes production-client use with connection replacement
	// so an old cluster client cannot write a snapshot after its fingerprint has
	// been replaced in the database.
	operationMu sync.Mutex
	client      Client
	current     Snapshot
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	closed      bool
}

func New(options Options) (*Manager, error) {
	if options.DB == nil || options.Factory == nil {
		return nil, errors.New("Kubernetes database and client factory are required")
	}
	if options.Interval <= 0 {
		options.Interval = 10 * time.Second
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Manager{db: options.DB, factory: options.Factory, interval: options.Interval, now: options.Now}, nil
}

func normalizeConnection(connection Connection) (Connection, error) {
	connection.Name = strings.TrimSpace(connection.Name)
	connection.KubeconfigPath = strings.TrimSpace(connection.KubeconfigPath)
	connection.Context = strings.TrimSpace(connection.Context)
	if connection.Name == "" {
		return Connection{}, errors.New("connection name is required")
	}
	if connection.KubeconfigPath == "" {
		return Connection{}, errors.New("kubeconfig path is required")
	}
	if connection.Mode == "" {
		connection.Mode = ModeObserve
	}
	if connection.Mode != ModeObserve && connection.Mode != ModeLimited {
		return Connection{}, errors.New("operation mode must be observe or limited")
	}
	return connection, nil
}

// TestConnection validates a candidate without changing the configured
// connection or any cluster-scoped history.
func (manager *Manager) TestConnection(ctx context.Context, connection Connection) (ConnectionStatus, error) {
	connection, err := normalizeConnection(connection)
	if err != nil {
		return ConnectionStatus{}, err
	}
	client, err := manager.factory.Open(ctx, connection)
	if err != nil {
		return ConnectionStatus{}, err
	}
	defer client.Close()
	capabilities, err := client.Capabilities(ctx)
	if err != nil {
		return ConnectionStatus{}, err
	}
	if !capabilities.Workloads {
		return ConnectionStatus{}, errors.New("Kubernetes credentials cannot list workloads")
	}
	if connection.Mode == ModeObserve {
		capabilities.Redeploy = false
		capabilities.Scale = false
		capabilities.RunCron = false
	}
	return ConnectionStatus{
		Connection:   connection,
		Connected:    true,
		Fingerprint:  client.Fingerprint(),
		Capabilities: capabilities,
		TestedAt:     manager.now().UTC(),
	}, nil
}

func (manager *Manager) SaveConnection(ctx context.Context, connection Connection) (ConnectionStatus, error) {
	connection, err := normalizeConnection(connection)
	if err != nil {
		return ConnectionStatus{}, err
	}
	client, err := manager.factory.Open(ctx, connection)
	if err != nil {
		return ConnectionStatus{}, err
	}
	capabilities, err := client.Capabilities(ctx)
	if err != nil {
		_ = client.Close()
		return ConnectionStatus{}, err
	}
	if connection.Mode == ModeObserve {
		capabilities.Redeploy = false
		capabilities.Scale = false
		capabilities.RunCron = false
	}
	encoded, err := json.Marshal(capabilities)
	if err != nil {
		_ = client.Close()
		return ConnectionStatus{}, err
	}
	manager.operationMu.Lock()
	defer manager.operationMu.Unlock()
	now := manager.now().UTC()
	transaction, err := manager.db.BeginTx(ctx, nil)
	if err != nil {
		_ = client.Close()
		return ConnectionStatus{}, err
	}
	defer transaction.Rollback()
	var previousFingerprint string
	if scanErr := transaction.QueryRowContext(ctx, `SELECT fingerprint FROM kubernetes_connection WHERE singleton=1`).Scan(&previousFingerprint); scanErr != nil && !errors.Is(scanErr, sql.ErrNoRows) {
		_ = client.Close()
		return ConnectionStatus{}, scanErr
	}
	if previousFingerprint != "" && previousFingerprint != client.Fingerprint() {
		for _, table := range []string{"kubernetes_pins", "kubernetes_versions", "kubernetes_metric_minutes"} {
			if _, err := transaction.ExecContext(ctx, "DELETE FROM "+table); err != nil {
				_ = client.Close()
				return ConnectionStatus{}, err
			}
		}
	}
	_, err = transaction.ExecContext(ctx, `INSERT INTO kubernetes_connection
		(singleton, name, kubeconfig_path, context_name, operation_mode, fingerprint, capabilities_json, last_tested_at, last_error, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, '', ?)
		ON CONFLICT(singleton) DO UPDATE SET name=excluded.name, kubeconfig_path=excluded.kubeconfig_path,
		context_name=excluded.context_name, operation_mode=excluded.operation_mode, fingerprint=excluded.fingerprint,
		capabilities_json=excluded.capabilities_json, last_tested_at=excluded.last_tested_at,
		last_error='', updated_at=excluded.updated_at`, connection.Name, connection.KubeconfigPath, connection.Context,
		connection.Mode, client.Fingerprint(), string(encoded), now.UnixNano(), now.UnixNano())
	if err != nil {
		_ = client.Close()
		return ConnectionStatus{}, err
	}
	if err := transaction.Commit(); err != nil {
		_ = client.Close()
		return ConnectionStatus{}, err
	}
	manager.mu.Lock()
	previous := manager.client
	manager.client = client
	if previousFingerprint != "" && previousFingerprint != client.Fingerprint() {
		manager.current = Snapshot{}
	}
	manager.mu.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
	return ConnectionStatus{Connection: connection, Connected: true, Fingerprint: client.Fingerprint(), Capabilities: capabilities, TestedAt: now}, nil
}

func (manager *Manager) ConnectionStatus(ctx context.Context) (ConnectionStatus, bool, error) {
	var status ConnectionStatus
	var encoded string
	var testedAt int64
	err := manager.db.QueryRowContext(ctx, `SELECT name, kubeconfig_path, context_name, operation_mode,
		fingerprint, capabilities_json, last_tested_at, last_error FROM kubernetes_connection WHERE singleton=1`).Scan(
		&status.Name, &status.KubeconfigPath, &status.Context, &status.Mode, &status.Fingerprint, &encoded, &testedAt, &status.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return ConnectionStatus{}, false, nil
	}
	if err != nil {
		return ConnectionStatus{}, false, err
	}
	_ = json.Unmarshal([]byte(encoded), &status.Capabilities)
	status.TestedAt = time.Unix(0, testedAt).UTC()
	status.Connected = status.Error == ""
	return status, true, nil
}

func (manager *Manager) ensureClient(ctx context.Context) (Client, error) {
	manager.mu.RLock()
	client := manager.client
	manager.mu.RUnlock()
	if client != nil {
		return client, nil
	}
	connection, ok, err := manager.Connection(ctx)
	if err != nil || !ok {
		if err == nil {
			err = errors.New("Kubernetes connection is not configured")
		}
		return nil, err
	}
	client, err = manager.factory.Open(ctx, connection)
	if err != nil {
		return nil, err
	}
	manager.mu.Lock()
	if manager.client == nil {
		manager.client = client
	} else {
		_ = client.Close()
		client = manager.client
	}
	manager.mu.Unlock()
	return client, nil
}

func (manager *Manager) Refresh(ctx context.Context) error {
	manager.operationMu.Lock()
	defer manager.operationMu.Unlock()
	client, err := manager.ensureClient(ctx)
	if err != nil {
		return err
	}
	snapshot, err := client.Snapshot(ctx)
	if err != nil {
		_, _ = manager.db.ExecContext(ctx, `UPDATE kubernetes_connection SET last_error=? WHERE singleton=1`, err.Error())
		return err
	}
	if snapshot.CollectedAt.IsZero() {
		snapshot.CollectedAt = manager.now().UTC()
	}
	if err := manager.persistSnapshot(ctx, snapshot); err != nil {
		return err
	}
	manager.mu.Lock()
	manager.current = snapshot
	manager.mu.Unlock()
	_, _ = manager.db.ExecContext(ctx, `UPDATE kubernetes_connection SET last_error='' WHERE singleton=1`)
	return nil
}

func (manager *Manager) Start(parent context.Context) {
	manager.mu.Lock()
	if manager.cancel != nil || manager.closed {
		manager.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	manager.cancel = cancel
	manager.wg.Add(1)
	manager.mu.Unlock()
	go func() {
		defer manager.wg.Done()
		_, configured, _ := manager.Connection(ctx)
		if configured {
			_ = manager.Refresh(ctx)
		}
		ticker := time.NewTicker(manager.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, configured, _ := manager.Connection(ctx)
				if configured {
					_ = manager.Refresh(ctx)
				}
			}
		}
	}()
}

func (manager *Manager) View(ctx context.Context, query Query) (View, error) {
	status, configured, err := manager.ConnectionStatus(ctx)
	if err != nil {
		return View{}, err
	}
	// The monitor view is readable by observers; connection filesystem paths
	// and cluster fingerprints remain confined to the connection task.
	status.KubeconfigPath = ""
	status.Fingerprint = ""
	view := View{Connection: status}
	if !configured {
		return view, nil
	}
	manager.mu.RLock()
	snapshot := manager.current
	manager.mu.RUnlock()
	view.CollectedAt, view.ServerVersion, view.Nodes = snapshot.CollectedAt, snapshot.ServerVersion, append([]Node(nil), snapshot.Nodes...)
	view.PodsReady, view.PodsTotal, view.Namespaces, view.MetricsAvailable, view.Errors = snapshot.PodsReady, snapshot.PodsTotal, snapshot.Namespaces, snapshot.MetricsAvailable, cloneStrings(snapshot.Errors)
	workloads := append([]Workload(nil), snapshot.Workloads...)
	pins, err := manager.loadPins(ctx, workloads)
	if err != nil {
		return View{}, err
	}
	view.Pinned = pins
	pinnedKeys := make(map[string]struct{}, len(pins))
	for _, workload := range pins {
		pinnedKeys[workload.Key] = struct{}{}
	}
	for position := range workloads {
		_, workloads[position].Pinned = pinnedKeys[workloads[position].Key]
	}
	view.Total = len(workloads)
	namespaceSet := make(map[string]struct{})
	for _, workload := range workloads {
		namespaceSet[workload.Namespace] = struct{}{}
		switch workload.Status {
		case "ready":
			view.Ready++
		case "progressing":
			view.Progressing++
		default:
			view.Degraded++
		}
	}
	view.AvailableNamespaces = make([]string, 0, len(namespaceSet))
	for namespace := range namespaceSet {
		view.AvailableNamespaces = append(view.AvailableNamespaces, namespace)
	}
	sort.Strings(view.AvailableNamespaces)
	search := strings.ToLower(strings.TrimSpace(query.Search))
	filtered := workloads[:0]
	for _, workload := range workloads {
		if query.Status != "" && query.Status != "all" && workload.Status != query.Status {
			continue
		}
		if query.Namespace != "" && query.Namespace != "all" && workload.Namespace != query.Namespace {
			continue
		}
		if query.Kind != "" && query.Kind != "all" && workload.Kind != query.Kind {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(workload.Name+"\x00"+workload.Namespace+"\x00"+workload.Image), search) {
			continue
		}
		filtered = append(filtered, workload)
	}
	view.Matched = len(filtered)
	sortWorkloads(filtered, query.Sort, query.Direction)
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	view.Workloads = filtered
	return view, nil
}

func (manager *Manager) Pin(ctx context.Context, key string) error {
	manager.mu.RLock()
	var selected Workload
	for _, workload := range manager.current.Workloads {
		if workload.Key == key {
			selected = workload
			break
		}
	}
	manager.mu.RUnlock()
	if selected.Key == "" {
		return errors.New("workload is not in the current snapshot")
	}
	transaction, err := manager.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	now := manager.now().UTC().UnixNano()
	var order int
	if err := transaction.QueryRowContext(ctx, `SELECT COALESCE(MAX(sort_order),0)+1 FROM kubernetes_pins`).Scan(&order); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO kubernetes_pins
		(workload_key, namespace, kind, name, sort_order, created_at, updated_at) VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(workload_key) DO UPDATE SET updated_at=excluded.updated_at`, selected.Key, selected.Namespace, selected.Kind, selected.Name, order, now, now); err != nil {
		return err
	}
	if err := insertVersionIfChanged(ctx, transaction, selected, now); err != nil {
		return err
	}
	if err := upsertMetric(ctx, transaction, selected, manager.now().UTC()); err != nil {
		return err
	}
	return transaction.Commit()
}

func (manager *Manager) Unpin(ctx context.Context, key string) error {
	result, err := manager.db.ExecContext(ctx, `DELETE FROM kubernetes_pins WHERE workload_key=?`, key)
	if err != nil {
		return err
	}
	removed, _ := result.RowsAffected()
	if removed == 0 {
		return errors.New("workload is not pinned")
	}
	return nil
}

func (manager *Manager) MovePin(ctx context.Context, key, direction string) error {
	direction = strings.ToLower(strings.TrimSpace(direction))
	if direction != "up" && direction != "down" && direction != "top" {
		return errors.New("pin direction must be top, up, or down")
	}
	rows, err := manager.db.QueryContext(ctx, `SELECT workload_key FROM kubernetes_pins ORDER BY sort_order, created_at`)
	if err != nil {
		return err
	}
	var keys []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, value)
	}
	_ = rows.Close()
	index := -1
	for position, value := range keys {
		if value == key {
			index = position
			break
		}
	}
	if index < 0 {
		return errors.New("workload is not pinned")
	}
	if direction == "top" {
		value := keys[index]
		copy(keys[1:index+1], keys[:index])
		keys[0] = value
	} else {
		target := index - 1
		if direction == "down" {
			target = index + 1
		}
		if target < 0 || target >= len(keys) {
			return errors.New("workload pin is already at the requested edge")
		}
		keys[index], keys[target] = keys[target], keys[index]
	}
	transaction, err := manager.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	for position, value := range keys {
		if _, err := transaction.ExecContext(ctx, `UPDATE kubernetes_pins SET sort_order=?,updated_at=? WHERE workload_key=?`, position+1, manager.now().UTC().UnixNano(), value); err != nil {
			return err
		}
	}
	return transaction.Commit()
}

func (manager *Manager) Detail(ctx context.Context, key string) (Detail, error) {
	manager.mu.RLock()
	var selected Workload
	for _, workload := range manager.current.Workloads {
		if workload.Key == key {
			selected = workload
			break
		}
	}
	manager.mu.RUnlock()
	if selected.Key == "" {
		return Detail{}, errors.New("workload not found")
	}
	manager.operationMu.Lock()
	defer manager.operationMu.Unlock()
	client, err := manager.ensureClient(ctx)
	if err != nil {
		return Detail{}, err
	}
	detail, err := client.Detail(ctx, key)
	if err != nil {
		return Detail{}, err
	}
	detail.Workload = selected
	detail.Versions, err = manager.versions(ctx, key)
	if err != nil {
		return Detail{}, err
	}
	detail.Metrics, err = manager.metrics(ctx, key)
	return detail, err
}

func (manager *Manager) Logs(ctx context.Context, key string, limit int) ([]LogLine, error) {
	manager.mu.RLock()
	found := false
	for _, workload := range manager.current.Workloads {
		if workload.Key == key {
			found = true
			break
		}
	}
	manager.mu.RUnlock()
	if !found {
		return nil, errors.New("workload not found")
	}
	status, ok, err := manager.ConnectionStatus(ctx)
	if err != nil || !ok {
		return nil, err
	}
	if !status.Capabilities.Logs {
		return nil, errors.New("Kubernetes credentials cannot read Pod logs")
	}
	manager.operationMu.Lock()
	defer manager.operationMu.Unlock()
	client, err := manager.ensureClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.Logs(ctx, key, limit)
}

func (manager *Manager) Operate(ctx context.Context, operation Operation) error {
	status, ok, err := manager.ConnectionStatus(ctx)
	if err != nil {
		return err
	}
	if !ok || status.Mode != ModeLimited {
		return errors.New("Kubernetes connection is configured for observation only")
	}
	manager.mu.RLock()
	var selected Workload
	for _, workload := range manager.current.Workloads {
		if workload.Key == operation.WorkloadKey {
			selected = workload
			break
		}
	}
	manager.mu.RUnlock()
	if selected.Key == "" {
		return errors.New("workload not found")
	}
	switch operation.Kind {
	case OperationRedeploy:
		if !status.Capabilities.Redeploy || selected.Kind == "CronJob" {
			return errors.New("Kubernetes credentials cannot redeploy this workload")
		}
	case OperationScale:
		if !status.Capabilities.Scale || selected.Kind != "Deployment" && selected.Kind != "StatefulSet" {
			return errors.New("Kubernetes credentials cannot scale this workload")
		}
		difference := operation.Replicas - selected.Desired
		if difference != 1 && difference != -1 {
			return errors.New("replicas can only be adjusted by one")
		}
	case OperationRunCron:
		if !status.Capabilities.RunCron || selected.Kind != "CronJob" {
			return errors.New("Kubernetes credentials cannot run this CronJob")
		}
	default:
		return errors.New("unsupported Kubernetes operation")
	}
	manager.operationMu.Lock()
	client, err := manager.ensureClient(ctx)
	if err != nil {
		manager.operationMu.Unlock()
		return err
	}
	if err := client.Operate(ctx, operation); err != nil {
		manager.operationMu.Unlock()
		return err
	}
	manager.operationMu.Unlock()
	return manager.Refresh(ctx)
}

func (manager *Manager) persistSnapshot(ctx context.Context, snapshot Snapshot) error {
	rows, err := manager.db.QueryContext(ctx, `SELECT workload_key FROM kubernetes_pins`)
	if err != nil {
		return err
	}
	pinned := make(map[string]struct{})
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return err
		}
		pinned[key] = struct{}{}
	}
	_ = rows.Close()
	transaction, err := manager.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	for _, workload := range snapshot.Workloads {
		if _, ok := pinned[workload.Key]; !ok {
			continue
		}
		if err := insertVersionIfChanged(ctx, transaction, workload, snapshot.CollectedAt.UnixNano()); err != nil {
			return err
		}
		if err := upsertMetric(ctx, transaction, workload, snapshot.CollectedAt); err != nil {
			return err
		}
	}
	_, _ = transaction.ExecContext(ctx, `DELETE FROM kubernetes_metric_minutes WHERE bucket_at < ?`, manager.now().UTC().Add(-24*time.Hour).UnixNano())
	return transaction.Commit()
}

func insertVersionIfChanged(ctx context.Context, transaction *sql.Tx, workload Workload, observedAt int64) error {
	var image, revision string
	err := transaction.QueryRowContext(ctx, `SELECT image, revision FROM kubernetes_versions WHERE workload_key=? ORDER BY observed_at DESC LIMIT 1`, workload.Key).Scan(&image, &revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && image == workload.Image && revision == workload.Revision {
		return nil
	}
	if _, err = transaction.ExecContext(ctx, `INSERT INTO kubernetes_versions (workload_key, observed_at, image, revision) VALUES (?,?,?,?)`, workload.Key, observedAt, workload.Image, workload.Revision); err != nil {
		return err
	}
	_, err = transaction.ExecContext(ctx, `DELETE FROM kubernetes_versions
		WHERE workload_key=? AND observed_at NOT IN (
			SELECT observed_at FROM kubernetes_versions WHERE workload_key=? ORDER BY observed_at DESC LIMIT 100
		)`, workload.Key, workload.Key)
	return err
}

func upsertMetric(ctx context.Context, transaction *sql.Tx, workload Workload, at time.Time) error {
	bucket := at.UTC().Truncate(time.Minute).UnixNano()
	_, err := transaction.ExecContext(ctx, `INSERT INTO kubernetes_metric_minutes
		(workload_key,bucket_at,cpu_millicores,memory_bytes,ready,desired,restarts) VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(workload_key,bucket_at) DO UPDATE SET cpu_millicores=excluded.cpu_millicores,memory_bytes=excluded.memory_bytes,
		ready=excluded.ready,desired=excluded.desired,restarts=excluded.restarts`, workload.Key, bucket, workload.CPUMillicores, workload.MemoryBytes, workload.Ready, workload.Desired, workload.Restarts)
	return err
}

func (manager *Manager) loadPins(ctx context.Context, workloads []Workload) ([]Workload, error) {
	index := make(map[string]int, len(workloads))
	for position := range workloads {
		index[workloads[position].Key] = position
	}
	rows, err := manager.db.QueryContext(ctx, `SELECT workload_key,namespace,kind,name FROM kubernetes_pins ORDER BY sort_order,created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pinned []Workload
	for rows.Next() {
		var stored Workload
		if err := rows.Scan(&stored.Key, &stored.Namespace, &stored.Kind, &stored.Name); err != nil {
			return nil, err
		}
		stored.Pinned = true
		stored.Status, stored.StatusLabel = "missing", "Missing"
		if position, ok := index[stored.Key]; ok {
			workloads[position].Pinned = true
			stored = workloads[position]
		}
		pinned = append(pinned, stored)
	}
	for position := range pinned {
		pinned[position].CanMoveUp = position > 0
		pinned[position].CanMoveDown = position+1 < len(pinned)
	}
	return pinned, rows.Err()
}

func (manager *Manager) versions(ctx context.Context, key string) ([]Version, error) {
	rows, err := manager.db.QueryContext(ctx, `SELECT observed_at,image,revision FROM kubernetes_versions WHERE workload_key=? ORDER BY observed_at DESC LIMIT 100`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Version
	for rows.Next() {
		var item Version
		var at int64
		if err := rows.Scan(&at, &item.Image, &item.Revision); err != nil {
			return nil, err
		}
		item.ObservedAt = time.Unix(0, at).UTC()
		result = append(result, item)
	}
	return result, rows.Err()
}

func (manager *Manager) metrics(ctx context.Context, key string) ([]MetricSample, error) {
	rows, err := manager.db.QueryContext(ctx, `SELECT bucket_at,cpu_millicores,memory_bytes,ready,desired,restarts FROM kubernetes_metric_minutes WHERE workload_key=? ORDER BY bucket_at`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []MetricSample
	for rows.Next() {
		var item MetricSample
		var at int64
		if err := rows.Scan(&at, &item.CPUMillicores, &item.MemoryBytes, &item.Ready, &item.Desired, &item.Restarts); err != nil {
			return nil, err
		}
		item.At = time.Unix(0, at).UTC()
		result = append(result, item)
	}
	return result, rows.Err()
}

func sortWorkloads(workloads []Workload, field, direction string) {
	if field == "" {
		field = "status"
	}
	descending := direction == "desc"
	sort.SliceStable(workloads, func(i, j int) bool {
		left, right := workloads[i], workloads[j]
		var comparison int
		switch field {
		case "name":
			comparison = strings.Compare(strings.ToLower(left.Name), strings.ToLower(right.Name))
		case "ready":
			comparison = left.Ready - right.Ready
		case "cpu":
			comparison = int(left.CPUMillicores - right.CPUMillicores)
		case "memory":
			if left.MemoryBytes < right.MemoryBytes {
				comparison = -1
			} else if left.MemoryBytes > right.MemoryBytes {
				comparison = 1
			}
		case "restarts":
			comparison = left.Restarts - right.Restarts
		case "namespace":
			comparison = strings.Compare(left.Namespace, right.Namespace)
		default:
			comparison = strings.Compare(left.Status, right.Status)
		}
		if comparison == 0 {
			comparison = strings.Compare(left.Key, right.Key)
		}
		if descending {
			return comparison > 0
		}
		return comparison < 0
	})
}

func cloneStrings(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func (manager *Manager) Connection(ctx context.Context) (Connection, bool, error) {
	var connection Connection
	err := manager.db.QueryRowContext(ctx, `SELECT name, kubeconfig_path, context_name, operation_mode
		FROM kubernetes_connection WHERE singleton=1`).Scan(&connection.Name, &connection.KubeconfigPath, &connection.Context, &connection.Mode)
	if errors.Is(err, sql.ErrNoRows) {
		return Connection{}, false, nil
	}
	return connection, err == nil, err
}

func (manager *Manager) Close() {
	manager.mu.RLock()
	cancel := manager.cancel
	manager.mu.RUnlock()
	if cancel != nil {
		cancel()
		manager.wg.Wait()
	}
	manager.operationMu.Lock()
	defer manager.operationMu.Unlock()
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return
	}
	manager.closed = true
	client := manager.client
	manager.client = nil
	manager.mu.Unlock()
	if client != nil {
		_ = client.Close()
	}
}
