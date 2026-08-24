package clusterstatus

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
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

type connectionRuntime struct {
	operationMu sync.Mutex
	client      Client
	current     Snapshot
}

type Manager struct {
	db       *sql.DB
	factory  Factory
	interval time.Duration
	now      func() time.Time

	mu       sync.RWMutex
	runtimes map[string]*connectionRuntime
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	closed   bool
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
	return &Manager{db: options.DB, factory: options.Factory, interval: options.Interval, now: options.Now, runtimes: make(map[string]*connectionRuntime)}, nil
}

func normalizeConnection(connection Connection) (Connection, error) {
	connection.ID = strings.TrimSpace(connection.ID)
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

func newConnectionID() (string, error) {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "k8s_" + hex.EncodeToString(raw), nil
}

func effectiveCapabilities(connection Connection, capabilities Capabilities) Capabilities {
	if connection.Mode == ModeObserve {
		capabilities.Redeploy = false
		capabilities.Scale = false
		capabilities.RunCron = false
	}
	return capabilities
}

// TestConnection validates a candidate without changing a saved connection or
// any connection-scoped history.
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
	return ConnectionStatus{Connection: connection, Connected: true, Fingerprint: client.Fingerprint(), Capabilities: effectiveCapabilities(connection, capabilities), TestedAt: manager.now().UTC()}, nil
}

func (manager *Manager) runtime(id string) *connectionRuntime {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	runtime := manager.runtimes[id]
	if runtime == nil {
		runtime = &connectionRuntime{}
		manager.runtimes[id] = runtime
	}
	return runtime
}

func (manager *Manager) SaveConnection(ctx context.Context, connection Connection) (ConnectionStatus, error) {
	connection, err := normalizeConnection(connection)
	if err != nil {
		return ConnectionStatus{}, err
	}
	updating := connection.ID != ""
	if !updating {
		connection.ID, err = newConnectionID()
		if err != nil {
			return ConnectionStatus{}, err
		}
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
	if !capabilities.Workloads {
		_ = client.Close()
		return ConnectionStatus{}, errors.New("Kubernetes credentials cannot list workloads")
	}
	capabilities = effectiveCapabilities(connection, capabilities)
	encoded, err := json.Marshal(capabilities)
	if err != nil {
		_ = client.Close()
		return ConnectionStatus{}, err
	}
	runtime := manager.runtime(connection.ID)
	runtime.operationMu.Lock()
	defer runtime.operationMu.Unlock()
	now := manager.now().UTC()
	transaction, err := manager.db.BeginTx(ctx, nil)
	if err != nil {
		_ = client.Close()
		return ConnectionStatus{}, err
	}
	defer transaction.Rollback()
	var duplicateID string
	if duplicateErr := transaction.QueryRowContext(ctx, `SELECT id FROM kubernetes_connection WHERE name=? AND id<>?`, connection.Name, connection.ID).Scan(&duplicateID); duplicateErr == nil {
		_ = client.Close()
		return ConnectionStatus{}, errors.New("connection name already exists")
	} else if !errors.Is(duplicateErr, sql.ErrNoRows) {
		_ = client.Close()
		return ConnectionStatus{}, duplicateErr
	}
	var previousFingerprint string
	scanErr := transaction.QueryRowContext(ctx, `SELECT fingerprint FROM kubernetes_connection WHERE id=?`, connection.ID).Scan(&previousFingerprint)
	if updating && errors.Is(scanErr, sql.ErrNoRows) {
		_ = client.Close()
		return ConnectionStatus{}, errors.New("Kubernetes connection was not found")
	}
	if scanErr != nil && !errors.Is(scanErr, sql.ErrNoRows) {
		_ = client.Close()
		return ConnectionStatus{}, scanErr
	}
	if previousFingerprint != "" && previousFingerprint != client.Fingerprint() {
		for _, table := range []string{"kubernetes_versions", "kubernetes_metric_minutes"} {
			if _, err := transaction.ExecContext(ctx, "DELETE FROM "+table+" WHERE connection_id=?", connection.ID); err != nil {
				_ = client.Close()
				return ConnectionStatus{}, err
			}
		}
	}
	_, err = transaction.ExecContext(ctx, `INSERT INTO kubernetes_connection
		(id, name, kubeconfig_path, context_name, operation_mode, fingerprint, capabilities_json, last_tested_at, last_error, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, '', ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, kubeconfig_path=excluded.kubeconfig_path,
		context_name=excluded.context_name, operation_mode=excluded.operation_mode, fingerprint=excluded.fingerprint,
		capabilities_json=excluded.capabilities_json, last_tested_at=excluded.last_tested_at,
		last_error='', updated_at=excluded.updated_at`, connection.ID, connection.Name, connection.KubeconfigPath, connection.Context,
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
	previous := runtime.client
	runtime.client = client
	if previousFingerprint != "" && previousFingerprint != client.Fingerprint() {
		runtime.current = Snapshot{}
	}
	manager.mu.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
	return ConnectionStatus{Connection: connection, Connected: true, Fingerprint: client.Fingerprint(), Capabilities: capabilities, TestedAt: now}, nil
}

// DeleteConnection removes the saved connection and its connection-scoped
// history, then closes the live client so deleted credentials are no longer used.
func (manager *Manager) DeleteConnection(ctx context.Context, id string) (bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return false, nil
	}
	runtime := manager.runtime(id)
	runtime.operationMu.Lock()
	defer runtime.operationMu.Unlock()
	transaction, err := manager.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer transaction.Rollback()
	for _, table := range []string{"kubernetes_versions", "kubernetes_metric_minutes"} {
		if _, err := transaction.ExecContext(ctx, "DELETE FROM "+table+" WHERE connection_id=?", id); err != nil {
			return false, err
		}
	}
	result, err := transaction.ExecContext(ctx, `DELETE FROM kubernetes_connection WHERE id=?`, id)
	if err != nil {
		return false, err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if deleted == 0 {
		manager.mu.Lock()
		if manager.runtimes[id] == runtime && runtime.client == nil {
			delete(manager.runtimes, id)
		}
		manager.mu.Unlock()
		return false, nil
	}
	if err := transaction.Commit(); err != nil {
		return false, err
	}
	manager.mu.Lock()
	delete(manager.runtimes, id)
	client := runtime.client
	runtime.client = nil
	runtime.current = Snapshot{}
	manager.mu.Unlock()
	if client != nil {
		_ = client.Close()
	}
	return true, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanConnectionStatus(scanner rowScanner) (ConnectionStatus, error) {
	var status ConnectionStatus
	var encoded string
	var testedAt int64
	err := scanner.Scan(&status.ID, &status.Name, &status.KubeconfigPath, &status.Context, &status.Mode, &status.Fingerprint, &encoded, &testedAt, &status.Error)
	if err != nil {
		return ConnectionStatus{}, err
	}
	_ = json.Unmarshal([]byte(encoded), &status.Capabilities)
	if testedAt > 0 {
		status.TestedAt = time.Unix(0, testedAt).UTC()
	}
	status.Connected = testedAt > 0 && status.Error == ""
	return status, nil
}

const connectionStatusColumns = `id, name, kubeconfig_path, context_name, operation_mode, fingerprint, capabilities_json, last_tested_at, last_error`

func (manager *Manager) ConnectionStatus(ctx context.Context, id string) (ConnectionStatus, bool, error) {
	status, err := scanConnectionStatus(manager.db.QueryRowContext(ctx, `SELECT `+connectionStatusColumns+` FROM kubernetes_connection WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ConnectionStatus{}, false, nil
	}
	return status, err == nil, err
}

func (manager *Manager) Connections(ctx context.Context) ([]ConnectionStatus, error) {
	rows, err := manager.db.QueryContext(ctx, `SELECT `+connectionStatusColumns+` FROM kubernetes_connection ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ConnectionStatus
	for rows.Next() {
		status, err := scanConnectionStatus(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, status)
	}
	return result, rows.Err()
}

func (manager *Manager) ensureClient(ctx context.Context, id string, runtime *connectionRuntime) (Client, error) {
	manager.mu.RLock()
	client := runtime.client
	manager.mu.RUnlock()
	if client != nil {
		return client, nil
	}
	connection, ok, err := manager.Connection(ctx, id)
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
	if runtime.client == nil {
		runtime.client = client
	} else {
		_ = client.Close()
		client = runtime.client
	}
	manager.mu.Unlock()
	return client, nil
}

func (manager *Manager) Refresh(ctx context.Context, id string) error {
	runtime := manager.runtime(id)
	runtime.operationMu.Lock()
	defer runtime.operationMu.Unlock()
	client, err := manager.ensureClient(ctx, id, runtime)
	if err != nil {
		return err
	}
	snapshot, err := client.Snapshot(ctx)
	if err != nil {
		_, _ = manager.db.ExecContext(ctx, `UPDATE kubernetes_connection SET last_error=? WHERE id=?`, err.Error(), id)
		return err
	}
	if snapshot.CollectedAt.IsZero() {
		snapshot.CollectedAt = manager.now().UTC()
	}
	if err := manager.persistSnapshot(ctx, id, snapshot); err != nil {
		return err
	}
	manager.mu.Lock()
	runtime.current = snapshot
	manager.mu.Unlock()
	_, _ = manager.db.ExecContext(ctx, `UPDATE kubernetes_connection SET last_error='' WHERE id=?`, id)
	return nil
}

func (manager *Manager) refreshAll(ctx context.Context) {
	connections, err := manager.Connections(ctx)
	if err != nil {
		return
	}
	var refreshes sync.WaitGroup
	for _, connection := range connections {
		connectionID := connection.ID
		refreshes.Add(1)
		go func() {
			defer refreshes.Done()
			_ = manager.Refresh(ctx, connectionID)
		}()
	}
	refreshes.Wait()
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
		manager.refreshAll(ctx)
		ticker := time.NewTicker(manager.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				manager.refreshAll(ctx)
			}
		}
	}()
}

func (manager *Manager) View(ctx context.Context, id string, query Query) (View, error) {
	status, configured, err := manager.ConnectionStatus(ctx, id)
	if err != nil {
		return View{}, err
	}
	status.KubeconfigPath = ""
	status.Fingerprint = ""
	view := View{Connection: status}
	if !configured {
		return view, nil
	}
	runtime := manager.runtime(id)
	manager.mu.RLock()
	snapshot := runtime.current
	manager.mu.RUnlock()
	view.CollectedAt, view.ServerVersion, view.Nodes = snapshot.CollectedAt, snapshot.ServerVersion, append([]Node(nil), snapshot.Nodes...)
	view.PodsReady, view.PodsTotal, view.Namespaces, view.MetricsAvailable, view.Errors = snapshot.PodsReady, snapshot.PodsTotal, snapshot.Namespaces, snapshot.MetricsAvailable, cloneStrings(snapshot.Errors)
	workloads := append([]Workload(nil), snapshot.Workloads...)
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

func (manager *Manager) Detail(ctx context.Context, id, key string) (Detail, error) {
	runtime := manager.runtime(id)
	manager.mu.RLock()
	var selected Workload
	for _, workload := range runtime.current.Workloads {
		if workload.Key == key {
			selected = workload
			break
		}
	}
	manager.mu.RUnlock()
	if selected.Key == "" {
		return Detail{}, errors.New("workload not found")
	}
	runtime.operationMu.Lock()
	defer runtime.operationMu.Unlock()
	client, err := manager.ensureClient(ctx, id, runtime)
	if err != nil {
		return Detail{}, err
	}
	detail, err := client.Detail(ctx, key)
	if err != nil {
		return Detail{}, err
	}
	detail.Workload = selected
	detail.Versions, err = manager.versions(ctx, id, key)
	if err != nil {
		return Detail{}, err
	}
	detail.Metrics, err = manager.metrics(ctx, id, key)
	return detail, err
}

func (manager *Manager) Logs(ctx context.Context, id, key string, limit int) ([]LogLine, error) {
	runtime := manager.runtime(id)
	manager.mu.RLock()
	found := false
	for _, workload := range runtime.current.Workloads {
		if workload.Key == key {
			found = true
			break
		}
	}
	manager.mu.RUnlock()
	if !found {
		return nil, errors.New("workload not found")
	}
	status, ok, err := manager.ConnectionStatus(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("Kubernetes connection is not configured")
	}
	if !status.Capabilities.Logs {
		return nil, errors.New("Kubernetes credentials cannot read Pod logs")
	}
	runtime.operationMu.Lock()
	defer runtime.operationMu.Unlock()
	client, err := manager.ensureClient(ctx, id, runtime)
	if err != nil {
		return nil, err
	}
	return client.Logs(ctx, key, limit)
}

func (manager *Manager) Operate(ctx context.Context, id string, operation Operation) error {
	status, ok, err := manager.ConnectionStatus(ctx, id)
	if err != nil {
		return err
	}
	if !ok || status.Mode != ModeLimited {
		return errors.New("Kubernetes connection is configured for observation only")
	}
	runtime := manager.runtime(id)
	manager.mu.RLock()
	var selected Workload
	for _, workload := range runtime.current.Workloads {
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
	runtime.operationMu.Lock()
	client, err := manager.ensureClient(ctx, id, runtime)
	if err != nil {
		runtime.operationMu.Unlock()
		return err
	}
	if err := client.Operate(ctx, operation); err != nil {
		runtime.operationMu.Unlock()
		return err
	}
	runtime.operationMu.Unlock()
	return manager.Refresh(ctx, id)
}

func (manager *Manager) persistSnapshot(ctx context.Context, id string, snapshot Snapshot) error {
	transaction, err := manager.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	for _, workload := range snapshot.Workloads {
		if err := insertVersionIfChanged(ctx, transaction, id, workload, snapshot.CollectedAt.UnixNano()); err != nil {
			return err
		}
		if err := upsertMetric(ctx, transaction, id, workload, snapshot.CollectedAt); err != nil {
			return err
		}
	}
	_, _ = transaction.ExecContext(ctx, `DELETE FROM kubernetes_metric_minutes WHERE connection_id=? AND bucket_at < ?`, id, manager.now().UTC().Add(-24*time.Hour).UnixNano())
	return transaction.Commit()
}

func insertVersionIfChanged(ctx context.Context, transaction *sql.Tx, connectionID string, workload Workload, observedAt int64) error {
	var image, revision string
	err := transaction.QueryRowContext(ctx, `SELECT image, revision FROM kubernetes_versions WHERE connection_id=? AND workload_key=? ORDER BY observed_at DESC LIMIT 1`, connectionID, workload.Key).Scan(&image, &revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && image == workload.Image && revision == workload.Revision {
		return nil
	}
	if _, err = transaction.ExecContext(ctx, `INSERT INTO kubernetes_versions (connection_id, workload_key, observed_at, image, revision) VALUES (?,?,?,?,?)`, connectionID, workload.Key, observedAt, workload.Image, workload.Revision); err != nil {
		return err
	}
	_, err = transaction.ExecContext(ctx, `DELETE FROM kubernetes_versions
		WHERE connection_id=? AND workload_key=? AND observed_at NOT IN (
			SELECT observed_at FROM kubernetes_versions WHERE connection_id=? AND workload_key=? ORDER BY observed_at DESC LIMIT 100
		)`, connectionID, workload.Key, connectionID, workload.Key)
	return err
}

func upsertMetric(ctx context.Context, transaction *sql.Tx, connectionID string, workload Workload, at time.Time) error {
	bucket := at.UTC().Truncate(time.Minute).UnixNano()
	_, err := transaction.ExecContext(ctx, `INSERT INTO kubernetes_metric_minutes
		(connection_id,workload_key,bucket_at,cpu_millicores,memory_bytes,ready,desired,restarts) VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(connection_id,workload_key,bucket_at) DO UPDATE SET cpu_millicores=excluded.cpu_millicores,memory_bytes=excluded.memory_bytes,
		ready=excluded.ready,desired=excluded.desired,restarts=excluded.restarts`, connectionID, workload.Key, bucket, workload.CPUMillicores, workload.MemoryBytes, workload.Ready, workload.Desired, workload.Restarts)
	return err
}

func (manager *Manager) versions(ctx context.Context, connectionID, key string) ([]Version, error) {
	rows, err := manager.db.QueryContext(ctx, `SELECT observed_at,image,revision FROM kubernetes_versions WHERE connection_id=? AND workload_key=? ORDER BY observed_at DESC LIMIT 100`, connectionID, key)
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

func (manager *Manager) metrics(ctx context.Context, connectionID, key string) ([]MetricSample, error) {
	rows, err := manager.db.QueryContext(ctx, `SELECT bucket_at,cpu_millicores,memory_bytes,ready,desired,restarts FROM kubernetes_metric_minutes WHERE connection_id=? AND workload_key=? ORDER BY bucket_at`, connectionID, key)
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
		case "node":
			comparison = strings.Compare(strings.ToLower(left.Nodes), strings.ToLower(right.Nodes))
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

func (manager *Manager) Connection(ctx context.Context, id string) (Connection, bool, error) {
	var connection Connection
	err := manager.db.QueryRowContext(ctx, `SELECT id, name, kubeconfig_path, context_name, operation_mode
		FROM kubernetes_connection WHERE id=?`, id).Scan(&connection.ID, &connection.Name, &connection.KubeconfigPath, &connection.Context, &connection.Mode)
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
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return
	}
	manager.closed = true
	runtimes := make([]*connectionRuntime, 0, len(manager.runtimes))
	for _, runtime := range manager.runtimes {
		runtimes = append(runtimes, runtime)
	}
	manager.mu.Unlock()
	for _, runtime := range runtimes {
		runtime.operationMu.Lock()
		manager.mu.Lock()
		client := runtime.client
		runtime.client = nil
		manager.mu.Unlock()
		if client != nil {
			_ = client.Close()
		}
		runtime.operationMu.Unlock()
	}
}
