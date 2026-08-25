package runmanager

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"scriptboard/internal/auditlog"
	"scriptboard/internal/diskspace"
	"scriptboard/internal/hostfiles"
	"scriptboard/internal/secretredaction"
)

type StartRequest struct {
	ScriptPath        string
	ExpectedDigest    string
	DisallowOverlap   bool
	ArgumentsTemplate string
	SourceType        string
	SourceName        string
	SourceID          string
	TimeoutSeconds    int
	Variables         map[string]string
	InitiatorUserID   string
	InitiatorUsername string
	PreparedScript    *hostfiles.Script
	PreparedDirectory *hostfiles.PreparedDirectory
}

type OneTimeStartRequest struct {
	WorkingDirectory  string
	Extension         string
	Source            string
	ArgumentsTemplate string
	TimeoutSeconds    int
	Variables         map[string]string
	AuditSource       string
	InitiatorUserID   string
	InitiatorUsername string
	InitiatorRole     string
	PreparedDirectory *hostfiles.PreparedDirectory
}

type Run struct {
	ID                 string
	ScriptPath         string
	ScriptDigest       string
	ArgumentsTemplate  string
	Arguments          []string
	TemplateArguments  []string
	Executor           string
	SourceType         string
	SourceName         string
	SourceID           string
	RuntimeIdentity    string
	Status             string
	CreatedAt          time.Time
	StartedAt          *time.Time
	FinishedAt         *time.Time
	ExitCode           *int
	Error              string
	TimeoutSeconds     int
	Events             []Event
	LogExpired         bool
	LogIncomplete      bool
	LogTruncated       bool
	DroppedBytes       int64
	ScriptKind         string
	WorkingDirectory   string
	SourceFilename     string
	SourceExpired      bool
	SourceAuditEventID int64
	InitiatorUserID    string
	InitiatorUsername  string
}

type Filter struct {
	Query                    string
	ScheduleID               string
	QuickRunID               string
	CreatedFromUnixNano      int64
	CreatedBeforeUnixNano    int64
	HasCreatedFromBoundary   bool
	HasCreatedBeforeBoundary bool
}

type Event struct {
	Sequence      int64     `json:"sequence"`
	Time          time.Time `json:"time"`
	Source        string    `json:"source"`
	Data          string    `json:"text"`
	EncodingError bool      `json:"encodingError,omitempty"`
}

type EventPage struct {
	Events  []Event `json:"events"`
	Before  int64   `json:"before,omitempty"`
	HasMore bool    `json:"hasMore"`
}

type persistedEvent struct {
	Sequence int64  `json:"sequence"`
	Time     int64  `json:"time"`
	Source   string `json:"source"`
	Data     []byte `json:"data"`
}

type executorCandidate struct {
	path   string
	prefix []string
	batch  bool
}

type activeRun struct {
	process              ManagedProcess
	terminal             string
	changed              chan struct{}
	scriptPath           string
	timeoutTimer         *time.Timer
	cleanup              func()
	fileInfo             os.FileInfo
	workingDirectory     string
	workingDirectoryInfo os.FileInfo
	oneTime              bool
	leaseID              string
}

func (r *activeRun) signalChanged() {
	if r == nil || r.changed == nil {
		return
	}
	select {
	case r.changed <- struct{}{}:
	default:
	}
}

type Manager struct {
	db                  *sql.DB
	auditLog            *auditlog.Store
	files               *hostfiles.Manager
	stateRoot           string
	mu                  sync.Mutex
	active              map[string]*activeRun
	wg                  sync.WaitGroup
	timeoutGrace        time.Duration
	executorChains      map[string][]string
	launcher            ProcessLauncher
	startMu             sync.Mutex
	accepting           bool
	persistenceStop     chan struct{}
	persistenceStopOnce sync.Once
}

func New(db *sql.DB, files *hostfiles.Manager, stateRoot string, timeoutGrace time.Duration, executorChains map[string][]string, auditStores ...*auditlog.Store) *Manager {
	return NewWithLauncher(db, files, stateRoot, timeoutGrace, executorChains, nil, auditStores...)
}

func NewWithLauncher(db *sql.DB, files *hostfiles.Manager, stateRoot string, timeoutGrace time.Duration, executorChains map[string][]string, launcher ProcessLauncher, auditStores ...*auditlog.Store) *Manager {
	if launcher == nil {
		launcher = NewLocalProcessLauncher(executorChains)
	}
	manager := &Manager{
		db: db, files: files, stateRoot: stateRoot, active: make(map[string]*activeRun), timeoutGrace: timeoutGrace,
		executorChains: executorChains, launcher: launcher, accepting: true, persistenceStop: make(chan struct{}),
	}
	if len(auditStores) > 0 {
		manager.auditLog = auditStores[0]
	}
	return manager
}

var ErrMaintenance = errors.New("ScriptBoard is entering update maintenance mode")
var ErrSourceExpired = errors.New("one-time source has expired")
var ErrSourceUnavailable = errors.New("one-time source is unavailable")
var ErrRunOverlap = errors.New("the published Run already has an active execution")

func prepareArguments(argumentsTemplate string, variables map[string]string) ([]string, []string, error) {
	if len([]byte(argumentsTemplate)) > 16<<10 {
		return nil, nil, fmt.Errorf("参数模板超过 16 KiB")
	}
	templateArguments, err := ParseArguments(argumentsTemplate)
	if err != nil {
		return nil, nil, err
	}
	if len(templateArguments) > 256 {
		return nil, nil, fmt.Errorf("参数数量超过 256 个")
	}
	arguments, err := resolveVariables(templateArguments, variables)
	if err != nil {
		return nil, nil, err
	}
	return templateArguments, arguments, nil
}

func ValidateArgumentsTemplate(argumentsTemplate string, variables map[string]string) error {
	_, _, err := prepareArguments(argumentsTemplate, variables)
	return err
}

func (m *Manager) ValidateExecutor(extension string) error {
	_, err := resolveExecutors(extension, m.executorChains)
	return err
}

func (m *Manager) Start(request StartRequest) (string, error) {
	m.startMu.Lock()
	defer m.startMu.Unlock()
	if !m.accepting {
		return "", ErrMaintenance
	}
	if err := diskspace.Require(m.stateRoot, diskspace.MinimumWritableBytes); err != nil {
		return "", err
	}
	templateArguments, arguments, err := prepareArguments(request.ArgumentsTemplate, request.Variables)
	if err != nil {
		return "", err
	}
	var script hostfiles.Script
	if request.PreparedScript != nil {
		script = *request.PreparedScript
		if script.Path != request.ScriptPath || script.Digest == "" || script.Directory != filepath.Dir(script.Path) {
			return "", errors.New("prepared script does not match the Run request")
		}
	} else {
		script, err = m.files.PrepareScript(request.ScriptPath)
		if err != nil {
			return "", fmt.Errorf("脚本不可执行: %w", err)
		}
	}
	if request.ExpectedDigest != "" && subtle.ConstantTimeCompare([]byte(script.Digest), []byte(request.ExpectedDigest)) != 1 {
		return "", errors.New("script digest no longer matches the published Run configuration")
	}
	if request.DisallowOverlap && m.IsActiveScript(script.Path) {
		return "", ErrRunOverlap
	}
	executors, err := resolveExecutors(hostfiles.Extension(script.Path), m.executorChains)
	if err != nil {
		return "", err
	}
	var workingDirectory hostfiles.PreparedDirectory
	if request.PreparedDirectory != nil {
		workingDirectory = *request.PreparedDirectory
		if workingDirectory.Path != script.Directory {
			return "", errors.New("prepared working directory does not match the script")
		}
	} else {
		workingDirectory, err = m.files.PrepareDirectory(script.Directory)
		if err != nil {
			return "", fmt.Errorf("脚本工作目录不可用: %w", err)
		}
	}
	id, err := randomID()
	if err != nil {
		return "", err
	}
	return m.startPrepared(preparedStart{
		id: id, displayPath: script.Path, script: script, workingDirectory: workingDirectory,
		scriptKind: "host_file", executors: executors, templateArguments: templateArguments, arguments: arguments,
		argumentsTemplate: request.ArgumentsTemplate, sourceType: request.SourceType, sourceName: request.SourceName,
		sourceID: request.SourceID, timeoutSeconds: request.TimeoutSeconds,
		initiatorUserID: request.InitiatorUserID, initiatorUsername: request.InitiatorUsername,
	})
}

type preparedStart struct {
	id                string
	displayPath       string
	script            hostfiles.Script
	workingDirectory  hostfiles.PreparedDirectory
	scriptKind        string
	sourceFilename    string
	executors         []executorCandidate
	templateArguments []string
	arguments         []string
	argumentsTemplate string
	sourceType        string
	sourceName        string
	sourceID          string
	timeoutSeconds    int
	auditSource       string
	initiatorUserID   string
	initiatorUsername string
	initiatorRole     string
}

func (m *Manager) StartOneTime(request OneTimeStartRequest) (string, error) {
	m.startMu.Lock()
	defer m.startMu.Unlock()
	if !m.accepting {
		return "", ErrMaintenance
	}
	if err := diskspace.Require(m.stateRoot, diskspace.MinimumWritableBytes); err != nil {
		return "", err
	}
	if len([]byte(request.Source)) == 0 || len([]byte(request.Source)) > 1<<20 || !utf8.ValidString(request.Source) || strings.ContainsRune(request.Source, 0) {
		return "", errors.New("one-time source must be valid UTF-8 without NUL bytes and no larger than 1 MiB")
	}
	extension := strings.ToLower(request.Extension)
	switch extension {
	case ".cmd", ".ps1", ".py", ".sh":
	default:
		return "", errors.New("one-time source extension is not supported")
	}
	templateArguments, arguments, err := prepareArguments(request.ArgumentsTemplate, request.Variables)
	if err != nil {
		return "", err
	}
	executors, err := resolveExecutors(extension, m.executorChains)
	if err != nil {
		return "", err
	}
	var workingDirectory hostfiles.PreparedDirectory
	if request.PreparedDirectory != nil {
		workingDirectory = *request.PreparedDirectory
		if workingDirectory.Path != request.WorkingDirectory {
			return "", errors.New("prepared working directory does not match the one-time Run request")
		}
	} else {
		workingDirectory, err = m.files.PrepareDirectory(request.WorkingDirectory)
		if err != nil {
			return "", fmt.Errorf("working directory is invalid: %w", err)
		}
	}
	id, err := randomID()
	if err != nil {
		return "", err
	}
	runRoot := filepath.Join(m.stateRoot, "runs", id)
	if err := os.MkdirAll(filepath.Dir(runRoot), 0o700); err != nil {
		return "", fmt.Errorf("create Run root: %w", err)
	}
	if err := os.Mkdir(runRoot, 0o700); err != nil {
		return "", fmt.Errorf("create private Run directory: %w", err)
	}
	if err := protectOneTimeRunDirectory(runRoot); err != nil {
		_ = os.Remove(runRoot)
		return "", fmt.Errorf("protect one-time Run directory: %w", err)
	}
	sourceFilename := "source" + extension
	sourcePath := filepath.Join(runRoot, sourceFilename)
	sourceFile, err := os.OpenFile(sourcePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		_ = os.Remove(runRoot)
		return "", fmt.Errorf("create one-time source: %w", err)
	}
	_, writeErr := io.WriteString(sourceFile, request.Source)
	if writeErr == nil {
		writeErr = sourceFile.Sync()
	}
	if closeErr := sourceFile.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr == nil {
		writeErr = protectOneTimeSourceForRunner(sourcePath)
	}
	if writeErr != nil {
		_ = os.RemoveAll(runRoot)
		return "", fmt.Errorf("write one-time source: %w", writeErr)
	}
	sourceInfo, err := os.Lstat(sourcePath)
	if err != nil || !sourceInfo.Mode().IsRegular() || sourceInfo.Mode()&os.ModeSymlink != 0 {
		_ = os.RemoveAll(runRoot)
		return "", errors.New("one-time source is not a regular file")
	}
	digest := sha256.Sum256([]byte(request.Source))
	runID, err := m.startPrepared(preparedStart{
		id: id, displayPath: sourceFilename,
		script:           hostfiles.Script{Path: sourcePath, Digest: fmt.Sprintf("%x", digest[:]), Info: sourceInfo},
		workingDirectory: workingDirectory, scriptKind: "one_time", sourceFilename: sourceFilename,
		executors: executors, templateArguments: templateArguments, arguments: arguments,
		argumentsTemplate: request.ArgumentsTemplate, sourceType: "one_time", sourceName: "one-time",
		timeoutSeconds: request.TimeoutSeconds, auditSource: request.AuditSource,
		initiatorUserID: request.InitiatorUserID, initiatorUsername: request.InitiatorUsername, initiatorRole: request.InitiatorRole,
	})
	if err != nil {
		_ = os.RemoveAll(runRoot)
	}
	return runID, err
}

func (m *Manager) startPrepared(prepared preparedStart) (string, error) {
	id := prepared.id
	leaseID := "run:" + id
	leasePaths := []string{prepared.script.Path}
	if prepared.scriptKind == "one_time" {
		leasePaths = []string{prepared.workingDirectory.Path}
	}
	if err := m.files.AcquireLease(leaseID, leasePaths...); err != nil {
		return "", fmt.Errorf("acquire Run path lease: %w", err)
	}
	leaseOwned := true
	defer func() {
		if leaseOwned {
			m.files.ReleaseLease(leaseID)
		}
	}()
	if prepared.scriptKind == "host_file" && prepared.script.Info != nil {
		current, err := m.files.PrepareScript(prepared.script.Path)
		if err != nil || current.Digest != prepared.script.Digest || !os.SameFile(current.Info, prepared.script.Info) {
			return "", errors.New("script changed before its Run lease was acquired")
		}
	}
	logRoot := filepath.Join(m.stateRoot, "runs", id)
	if err := os.MkdirAll(logRoot, 0o700); err != nil {
		return "", fmt.Errorf("创建 Run 日志目录: %w", err)
	}
	logPath := filepath.Join(logRoot, "events.jsonl")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return "", fmt.Errorf("创建 Run Log: %w", err)
	}
	argumentJSON, _ := json.Marshal(prepared.arguments)
	templateArgumentJSON, _ := json.Marshal(prepared.templateArguments)
	now := time.Now().UTC()
	runtimeIdentity := m.launcher.RuntimeIdentity()
	var transaction *sql.Tx
	var auditTransaction *auditlog.Transaction
	if prepared.auditSource != "" && m.auditLog != nil {
		auditTransaction, err = m.auditLog.Begin(context.Background())
		if err == nil {
			transaction = auditTransaction.SQL()
		}
	} else {
		transaction, err = m.db.Begin()
	}
	if err != nil {
		_ = logFile.Close()
		return "", fmt.Errorf("begin Run record: %w", err)
	}
	if auditTransaction != nil {
		defer auditTransaction.Rollback()
	} else {
		defer transaction.Rollback()
	}
	var auditID any
	if prepared.auditSource != "" {
		var value int64
		var auditErr error
		if auditTransaction != nil {
			value, auditErr = auditTransaction.Append(context.Background(), auditlog.Event{
				OccurredAt: strconv.FormatInt(now.Unix(), 10), Action: "start_one_time_run", Target: id, Result: "accepted",
				SourceAddress: prepared.auditSource, ActorUserID: prepared.initiatorUserID,
				ActorUsername: prepared.initiatorUsername, ActorRole: prepared.initiatorRole,
				ResourceDigestSHA256: prepared.script.Digest,
			})
		} else {
			var result sql.Result
			result, auditErr = transaction.Exec(`INSERT INTO audit_events
				(occurred_at, action, target, result, source_address, actor_user_id, actor_username, actor_role)
				VALUES (?, 'start_one_time_run', ?, 'accepted', ?, ?, ?, ?)`,
				now.Unix(), id, prepared.auditSource, prepared.initiatorUserID, prepared.initiatorUsername, prepared.initiatorRole)
			if auditErr == nil {
				value, auditErr = result.LastInsertId()
			}
		}
		if auditErr != nil {
			_ = logFile.Close()
			return "", fmt.Errorf("record one-time Run audit: %w", auditErr)
		}
		auditID = value
	}
	if _, err := transaction.Exec(`INSERT INTO runs
		(id, script_path, script_path_key, script_sha256, arguments_template, template_arguments_json, arguments_json, executor,
		source_type, source_name, source_id, runtime_identity, status, created_at, timeout_seconds, log_path,
		script_kind, working_directory, working_directory_key, source_filename, source_audit_event_id, initiated_by_user_id, initiated_by_username)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'starting', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, prepared.displayPath, hostfiles.ComparisonKey(prepared.displayPath), prepared.script.Digest, prepared.argumentsTemplate, string(templateArgumentJSON), string(argumentJSON), prepared.executors[0].path,
		prepared.sourceType, prepared.sourceName, prepared.sourceID, runtimeIdentity, now.UnixNano(), prepared.timeoutSeconds, logPath,
		prepared.scriptKind, prepared.workingDirectory.Path, hostfiles.ComparisonKey(prepared.workingDirectory.Path), prepared.sourceFilename, auditID,
		prepared.initiatorUserID, prepared.initiatorUsername,
	); err != nil {
		_ = logFile.Close()
		return "", fmt.Errorf("create Run: %w", err)
	}
	if auditTransaction != nil {
		err = auditTransaction.Commit()
	} else {
		err = transaction.Commit()
	}
	if err != nil {
		_ = logFile.Close()
		return "", fmt.Errorf("commit Run: %w", err)
	}

	if prepared.workingDirectory.Info != nil {
		currentDirectoryInfo, directoryErr := os.Lstat(prepared.workingDirectory.Path)
		if directoryErr != nil || !currentDirectoryInfo.IsDir() || currentDirectoryInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(currentDirectoryInfo, prepared.workingDirectory.Info) {
			_ = logFile.Close()
			m.failStart(id, errors.New("working directory changed before execution"))
			return id, nil
		}
	}
	process, executorPath, err := m.launcher.Launch(context.Background(), LaunchRequest{
		RunID: id, ScriptPath: prepared.script.Path, ScriptDigest: prepared.script.Digest,
		WorkingDirectory: prepared.workingDirectory.Path, Arguments: prepared.arguments,
	})
	if err != nil {
		_ = logFile.Close()
		m.failStart(id, err)
		return id, nil
	}
	started := time.Now().UTC()
	runningUpdate, err := m.db.Exec("UPDATE runs SET executor = ?, status = 'running', started_at = ? WHERE id = ? AND status = 'starting'", executorPath, started.UnixNano(), id)
	if err == nil {
		var affected int64
		affected, err = runningUpdate.RowsAffected()
		if err == nil && affected != 1 {
			err = errors.New("run no longer has starting state")
		}
	}
	if err != nil {
		_ = process.Terminate(true)
		_ = process.Wait()
		_ = process.Close()
		_ = logFile.Close()
		m.failStart(id, fmt.Errorf("persist running state: %w", err))
		return id, nil
	}
	m.mu.Lock()
	active := &activeRun{
		process: process, cleanup: func() { _ = process.Close() }, fileInfo: prepared.script.Info, changed: make(chan struct{}, 1),
		scriptPath:           normalizeHostPath(prepared.displayPath),
		workingDirectory:     normalizeHostPath(prepared.workingDirectory.Path),
		workingDirectoryInfo: prepared.workingDirectory.Info,
		oneTime:              prepared.scriptKind == "one_time",
		leaseID:              leaseID,
	}
	m.active[id] = active
	if prepared.timeoutSeconds > 0 {
		active.timeoutTimer = time.AfterFunc(time.Duration(prepared.timeoutSeconds)*time.Second, func() { m.timeout(id) })
	}
	m.mu.Unlock()
	leaseOwned = false
	m.wg.Add(1)
	go m.supervise(id, process, process.Stdout(), process.Stderr(), logFile, active)
	return id, nil
}

func (m *Manager) EnterMaintenance() (int, bool) {
	m.startMu.Lock()
	defer m.startMu.Unlock()
	m.mu.Lock()
	active := len(m.active)
	m.mu.Unlock()
	if active != 0 {
		return active, false
	}
	m.accepting = false
	return 0, true
}

func (m *Manager) LeaveMaintenance() {
	m.startMu.Lock()
	m.accepting = true
	m.startMu.Unlock()
}

func (m *Manager) Accepting() bool {
	m.startMu.Lock()
	defer m.startMu.Unlock()
	return m.accepting
}

func (m *Manager) ConflictsPath(path string) bool {
	if m.files.LeaseConflicts(path) {
		return true
	}
	candidate := normalizeHostPath(path)
	candidateInfo, _ := m.files.Info(path)
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, active := range m.active {
		if active.oneTime && (active.workingDirectory == "" || hostfiles.Contains(active.workingDirectory, candidate)) {
			return true
		}
		if active.scriptPath == candidate || hostfiles.Contains(candidate, active.scriptPath) || (candidateInfo != nil && active.fileInfo != nil && os.SameFile(candidateInfo, active.fileInfo)) {
			return true
		}
	}
	return false
}

func (m *Manager) IsActiveScript(path string) bool {
	candidate := normalizeHostPath(path)
	candidateInfo, _ := m.files.Info(path)
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, active := range m.active {
		if active.scriptPath == candidate || (candidateInfo != nil && active.fileInfo != nil && os.SameFile(candidateInfo, active.fileInfo)) {
			return true
		}
	}
	return false
}

func (m *Manager) HasActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.active) != 0
}

func (m *Manager) ActiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.active)
}

func normalizeHostPath(path string) string {
	return hostfiles.ComparisonKey(path)
}

func (m *Manager) failStart(id string, startErr error) {
	now := time.Now().UTC().UnixNano()
	logBytes := int64(-1)
	var logPath string
	if m.db.QueryRow("SELECT log_path FROM runs WHERE id = ?", id).Scan(&logPath) == nil {
		if info, err := os.Stat(logPath); err == nil {
			logBytes = info.Size()
		}
	}
	write := func() error {
		result, err := m.db.Exec("UPDATE runs SET status = 'failed', finished_at = ?, error = ?, log_bytes = ? WHERE id = ?", now, secretredaction.String(startErr.Error()), logBytes, id)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return errors.New("failed run state target is missing")
		}
		return nil
	}
	if err := write(); err == nil {
		m.recordTerminalAudit(id, "failed")
		return
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		if retryRunStateWrite(m.persistenceStop, write) {
			m.recordTerminalAudit(id, "failed")
		}
	}()
}

func (m *Manager) supervise(id string, process ManagedProcess, stdout, stderr io.ReadCloser, logFile *os.File, activeRun *activeRun) {
	defer m.wg.Done()
	var eventMu sync.Mutex
	var sequence int64
	var written, tailBytes, droppedBytes int64
	logIncomplete := false
	const headLogBytes = int64(5 << 20)
	const tailLogBytes = int64(95 << 20)
	const markerReserve = int64(4 << 10)
	type tailChunk struct {
		path          string
		size          int64
		dataBytes     int64
		firstSequence int64
	}
	var tail []tailChunk
	var tailFile *os.File
	tailIndex := 0
	const tailChunkBytes = int64(1 << 20)
	var readers sync.WaitGroup
	writeEvents := func(source string, reader io.Reader) {
		defer readers.Done()
		buffered := bufio.NewReaderSize(reader, 32<<10)
		buffer := make([]byte, 32<<10)
		for {
			count, readErr := buffered.Read(buffer)
			if count > 0 {
				eventMu.Lock()
				sequence++
				encoded, _ := json.Marshal(persistedEvent{Sequence: sequence, Time: time.Now().UTC().UnixNano(), Source: source, Data: secretredaction.Bytes(buffer[:count])})
				line := append(encoded, '\n')
				if !logIncomplete && written+int64(len(line)) <= headLogBytes {
					countWritten, writeErr := logFile.Write(line)
					written += int64(countWritten)
					if writeErr != nil || countWritten != len(line) {
						logIncomplete = true
					}
				} else if !logIncomplete {
					if tailFile == nil || tail[len(tail)-1].size+int64(len(line)) > tailChunkBytes {
						if tailFile != nil {
							_ = tailFile.Close()
						}
						tailIndex++
						chunkPath := fmt.Sprintf("%s.tail-%04d", logFile.Name(), tailIndex)
						var openErr error
						tailFile, openErr = os.OpenFile(chunkPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
						if openErr != nil {
							logIncomplete = true
						} else {
							tail = append(tail, tailChunk{path: chunkPath, firstSequence: sequence})
						}
					}
					if !logIncomplete {
						countWritten, writeErr := tailFile.Write(line)
						if writeErr != nil || countWritten != len(line) {
							logIncomplete = true
						} else {
							last := &tail[len(tail)-1]
							last.size += int64(countWritten)
							last.dataBytes += int64(count)
							tailBytes += int64(countWritten)
						}
					}
					for tailBytes > tailLogBytes-markerReserve && len(tail) > 1 {
						droppedBytes += tail[0].dataBytes
						tailBytes -= tail[0].size
						_ = os.Remove(tail[0].path)
						tail = tail[1:]
					}
				}
				eventMu.Unlock()
				activeRun.signalChanged()
			}
			if readErr != nil {
				return
			}
		}
	}
	readers.Add(2)
	go writeEvents("stdout", stdout)
	go writeEvents("stderr", stderr)
	readers.Wait()
	waitErr := process.Wait()
	if activeCleanup := func() func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if active := m.active[id]; active != nil {
			return active.cleanup
		}
		return nil
	}(); activeCleanup != nil {
		activeCleanup()
	}
	if tailFile != nil {
		_ = tailFile.Sync()
		_ = tailFile.Close()
	}
	if droppedBytes > 0 && !logIncomplete {
		markerSequence := int64(1)
		if len(tail) > 0 {
			markerSequence = tail[0].firstSequence - 1
		}
		marker, _ := json.Marshal(persistedEvent{Sequence: markerSequence, Time: time.Now().UTC().UnixNano(), Source: "system", Data: []byte(fmt.Sprintf("[日志已截断，丢弃 %d 字节输出；以下为保留的尾部]", droppedBytes))})
		if _, err := logFile.Write(append(marker, '\n')); err != nil {
			logIncomplete = true
		}
	}
	for _, chunk := range tail {
		if !logIncomplete {
			chunkFile, openErr := os.Open(chunk.path)
			if openErr != nil {
				logIncomplete = true
			} else {
				copied, copyErr := io.Copy(logFile, chunkFile)
				_ = chunkFile.Close()
				if copyErr != nil || copied != chunk.size {
					logIncomplete = true
				}
			}
		}
		_ = os.Remove(chunk.path)
	}
	_ = logFile.Sync()
	_ = logFile.Close()
	logBytes := int64(-1)
	if info, err := os.Stat(logFile.Name()); err == nil {
		logBytes = info.Size()
	}
	finished := time.Now().UTC()
	m.mu.Lock()
	active := m.active[id]
	if active != nil && active.timeoutTimer != nil {
		active.timeoutTimer.Stop()
	}
	terminal := ""
	if active != nil {
		terminal = active.terminal
	}
	m.mu.Unlock()
	status := "succeeded"
	exitCode := 0
	errorText := ""
	if terminal != "" {
		status = terminal
		if waitErr != nil {
			errorText = secretredaction.String(waitErr.Error())
		}
	} else if waitErr != nil {
		status = "failed"
		errorText = secretredaction.String(waitErr.Error())
		var exitError *exec.ExitError
		if errors.As(waitErr, &exitError) {
			exitCode = exitError.ExitCode()
		} else {
			exitCode = -1
		}
	}
	if retryRunStateWrite(m.persistenceStop, func() error {
		result, err := m.db.Exec("UPDATE runs SET status = ?, finished_at = ?, exit_code = ?, error = ?, log_incomplete = ?, log_truncated = ?, dropped_bytes = ?, log_bytes = ? WHERE id = ?", status, finished.UnixNano(), exitCode, errorText, logIncomplete, droppedBytes > 0, droppedBytes, logBytes, id)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return errors.New("run terminal state target is missing")
		}
		return nil
	}) {
		m.recordTerminalAudit(id, status)
	}
	activeRun.signalChanged()
	m.mu.Lock()
	delete(m.active, id)
	m.mu.Unlock()
	m.files.ReleaseLease(activeRun.leaseID)
}

func (m *Manager) recordTerminalAudit(id, status string) {
	if m.auditLog == nil {
		return
	}
	var userID, username, digest string
	if err := m.db.QueryRow(`SELECT initiated_by_user_id, initiated_by_username, script_sha256 FROM runs WHERE id = ?`, id).Scan(&userID, &username, &digest); err != nil {
		return
	}
	_, _ = m.auditLog.Append(context.Background(), auditlog.Event{
		OccurredAt: strconv.FormatInt(time.Now().UTC().Unix(), 10), Action: "run_completed", Target: id, Result: status,
		SourceAddress: "runmanager", ActorUserID: userID, ActorUsername: username, ResourceDigestSHA256: digest,
	})
}

func (m *Manager) timeout(id string) {
	m.mu.Lock()
	active := m.active[id]
	if active == nil || active.terminal != "" {
		m.mu.Unlock()
		return
	}
	active.terminal = "timed_out"
	process := active.process
	m.mu.Unlock()
	_, _ = m.db.Exec("UPDATE runs SET status = 'timing_out' WHERE id = ? AND status = 'running'", id)
	active.signalChanged()
	_ = process.Terminate(false)
	time.AfterFunc(m.timeoutGrace, func() {
		m.mu.Lock()
		stillActive := m.active[id]
		var forceProcess ManagedProcess
		if stillActive != nil && stillActive.terminal == "timed_out" {
			forceProcess = stillActive.process
		}
		m.mu.Unlock()
		if forceProcess != nil {
			_ = forceProcess.Terminate(true)
		}
	})
}

func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	active, exists := m.active[id]
	if !exists {
		m.mu.Unlock()
		var status string
		if err := m.db.QueryRow("SELECT status FROM runs WHERE id = ?", id).Scan(&status); err == nil && status == "cancelled" {
			return nil
		}
		return fmt.Errorf("Run 当前不活动")
	}
	force := active.terminal == "cancelled"
	active.terminal = "cancelled"
	process := active.process
	m.mu.Unlock()
	if !force {
		_, _ = m.db.Exec("UPDATE runs SET status = 'stopping' WHERE id = ? AND status = 'running'", id)
	}
	active.signalChanged()
	if err := process.Terminate(force); err != nil {
		if force {
			return nil
		}
		return fmt.Errorf("停止进程树: %w", err)
	}
	return nil
}

const runMetadataColumns = `id, script_path, script_sha256, arguments_template, template_arguments_json, arguments_json, executor, source_type, source_name, source_id, runtime_identity,
	status, created_at, started_at, finished_at, exit_code, error, timeout_seconds, log_path, log_expired, log_incomplete, log_truncated, dropped_bytes,
	script_kind, working_directory, source_filename, source_expired, source_audit_event_id, initiated_by_user_id, initiated_by_username`

type runScanner interface {
	Scan(...any) error
}

func scanRunMetadata(scanner runScanner) (Run, string, error) {
	var result Run
	var argumentJSON, templateArgumentJSON, logPath string
	var createdAt int64
	var startedAt, finishedAt, exitCode sql.NullInt64
	var sourceAuditEventID sql.NullInt64
	err := scanner.Scan(
		&result.ID, &result.ScriptPath, &result.ScriptDigest, &result.ArgumentsTemplate, &templateArgumentJSON, &argumentJSON, &result.Executor, &result.SourceType, &result.SourceName, &result.SourceID, &result.RuntimeIdentity,
		&result.Status, &createdAt, &startedAt, &finishedAt, &exitCode, &result.Error, &result.TimeoutSeconds, &logPath, &result.LogExpired, &result.LogIncomplete, &result.LogTruncated, &result.DroppedBytes,
		&result.ScriptKind, &result.WorkingDirectory, &result.SourceFilename, &result.SourceExpired, &sourceAuditEventID,
		&result.InitiatorUserID, &result.InitiatorUsername,
	)
	if err != nil {
		return Run{}, "", err
	}
	result.CreatedAt = time.Unix(0, createdAt).UTC()
	if startedAt.Valid {
		value := time.Unix(0, startedAt.Int64).UTC()
		result.StartedAt = &value
	}
	if finishedAt.Valid {
		value := time.Unix(0, finishedAt.Int64).UTC()
		result.FinishedAt = &value
	}
	if exitCode.Valid {
		value := int(exitCode.Int64)
		result.ExitCode = &value
	}
	if sourceAuditEventID.Valid {
		result.SourceAuditEventID = sourceAuditEventID.Int64
	}
	_ = json.Unmarshal([]byte(argumentJSON), &result.Arguments)
	_ = json.Unmarshal([]byte(templateArgumentJSON), &result.TemplateArguments)
	return result, logPath, nil
}

func (m *Manager) getMetadata(id string) (Run, string, error) {
	return scanRunMetadata(m.db.QueryRow(`SELECT `+runMetadataColumns+` FROM runs WHERE id = ?`, id))
}

func (m *Manager) GetMetadata(id string) (Run, error) {
	result, _, err := m.getMetadata(id)
	return result, err
}

func (m *Manager) Get(id string) (Run, error) {
	result, logPath, err := m.getMetadata(id)
	if err != nil {
		return Run{}, err
	}
	if result.LogExpired {
		return result, nil
	}
	result.Events, err = readEvents(logPath)
	if err != nil {
		return Run{}, fmt.Errorf("read Run log: %w", err)
	}
	return result, nil
}

// StreamEvents emits the persisted Run log in sequence without retaining the
// complete log in memory. The callback is not invoked if the log cannot be
// opened, allowing callers to choose an error response before writing output.
func (m *Manager) StreamEvents(id string, emit func(Event) error) error {
	run, logPath, err := m.getMetadata(id)
	if err != nil {
		return err
	}
	if run.LogExpired {
		return nil
	}
	_, err = scanEvents(logPath, 0, 0, emit)
	if err != nil {
		return fmt.Errorf("read Run log: %w", err)
	}
	return nil
}

// EventPage returns a bounded tail page before the exclusive sequence cursor.
// A zero cursor selects the newest available events.
func (m *Manager) EventPage(id string, beforeSequence int64, limit int) (EventPage, error) {
	if beforeSequence < 0 || limit < 1 || limit > 1000 {
		return EventPage{}, errors.New("invalid Run event page")
	}
	run, logPath, err := m.getMetadata(id)
	if err != nil {
		return EventPage{}, err
	}
	if run.LogExpired {
		return EventPage{Events: []Event{}}, nil
	}

	const maximumPageBytes = 128 << 10
	page := EventPage{Events: make([]Event, 0, limit)}
	pageBytes := 0
	_, err = scanEvents(logPath, 0, 0, func(event Event) error {
		if beforeSequence > 0 && event.Sequence >= beforeSequence {
			return nil
		}
		page.Events = append(page.Events, event)
		pageBytes += len(event.Data)
		for len(page.Events) > limit || pageBytes > maximumPageBytes && len(page.Events) > 1 {
			pageBytes -= len(page.Events[0].Data)
			page.Events = page.Events[1:]
			page.HasMore = true
		}
		return nil
	})
	if err != nil {
		return EventPage{}, fmt.Errorf("read Run log: %w", err)
	}
	if page.HasMore && len(page.Events) > 0 {
		page.Before = page.Events[0].Sequence
	}
	return page, nil
}

func (m *Manager) FollowEvents(ctx context.Context, id string, afterSequence int64, emit func(Event) error) (string, error) {
	run, logPath, err := m.getMetadata(id)
	if err != nil {
		return "", err
	}
	status := run.Status
	var offset int64
	for {
		events, nextOffset, readErr := readEventsAfter(logPath, offset, afterSequence)
		if readErr != nil {
			return "", readErr
		}
		offset = nextOffset
		for _, event := range events {
			if err := emit(event); err != nil {
				return "", err
			}
			afterSequence = event.Sequence
		}

		if err := m.db.QueryRow("SELECT status FROM runs WHERE id = ?", id).Scan(&status); err != nil {
			return "", err
		}
		if !runStatusIsActive(status) {
			events, _, readErr = readEventsAfter(logPath, offset, afterSequence)
			if readErr != nil {
				return "", readErr
			}
			for _, event := range events {
				if err := emit(event); err != nil {
					return "", err
				}
			}
			return status, nil
		}
		if err := m.waitForRunChange(ctx, id); err != nil {
			return "", err
		}
	}
}

func runStatusIsActive(status string) bool {
	switch status {
	case "starting", "running", "stopping", "timing_out":
		return true
	default:
		return false
	}
}

func (m *Manager) waitForRunChange(ctx context.Context, id string) error {
	m.mu.Lock()
	active := m.active[id]
	m.mu.Unlock()
	if active != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-active.changed:
			return nil
		}
	}

	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (m *Manager) ReadSource(id string) ([]byte, error) {
	var storedID, kind, filename, digest string
	var expired bool
	if err := m.db.QueryRow(`SELECT id, script_kind, source_filename, source_expired, script_sha256
		FROM runs WHERE id = ?`, id).Scan(&storedID, &kind, &filename, &expired, &digest); err != nil {
		return nil, err
	}
	if kind != "one_time" || filename == "" {
		return nil, ErrSourceUnavailable
	}
	if expired {
		return nil, ErrSourceExpired
	}
	if storedID != filepath.Base(storedID) || filename != filepath.Base(filename) || strings.ContainsAny(storedID+filename, `/\`) {
		return nil, ErrSourceUnavailable
	}
	path := filepath.Join(m.stateRoot, "runs", storedID, filename)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrSourceUnavailable
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, ErrSourceUnavailable
	}
	defer file.Close()
	source, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(source) > 1<<20 {
		return nil, ErrSourceUnavailable
	}
	hash := sha256.Sum256(source)
	if !strings.EqualFold(fmt.Sprintf("%x", hash[:]), digest) {
		return nil, ErrSourceUnavailable
	}
	return source, nil
}

func (m *Manager) List(limit int) ([]Run, error) {
	return m.ListPage(limit, 0)
}

func (m *Manager) Count() (int, error) {
	return m.CountFiltered(Filter{})
}

func (m *Manager) CountFiltered(filter Filter) (int, error) {
	var count int
	like := "%" + filter.Query + "%"
	err := m.db.QueryRow(`SELECT COUNT(*) FROM runs
		WHERE (? = '' OR (source_id = ? AND source_type IN ('scheduler', 'admin/schedule-now')))
		AND (? = '' OR (source_id = ? AND source_type IN ('admin/quick-run', 'quick_run')))
		AND (? = '' OR id LIKE ? OR script_path LIKE ? OR source_type LIKE ? OR source_name LIKE ? OR status LIKE ? OR executor LIKE ? OR initiated_by_username LIKE ?)
		AND (? = 0 OR created_at >= ?)
		AND (? = 0 OR created_at < ?)`,
		filter.ScheduleID, filter.ScheduleID,
		filter.QuickRunID, filter.QuickRunID,
		filter.Query, like, like, like, like, like, like, like,
		filter.HasCreatedFromBoundary, filter.CreatedFromUnixNano,
		filter.HasCreatedBeforeBoundary, filter.CreatedBeforeUnixNano).Scan(&count)
	return count, err
}

func (m *Manager) ListPage(limit, offset int) ([]Run, error) {
	return m.ListPageFiltered(Filter{}, limit, offset)
}

func (m *Manager) ListPageFiltered(filter Filter, limit, offset int) ([]Run, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	like := "%" + filter.Query + "%"
	rows, err := m.db.Query(`SELECT `+runMetadataColumns+` FROM runs
		WHERE (? = '' OR (source_id = ? AND source_type IN ('scheduler', 'admin/schedule-now')))
		AND (? = '' OR (source_id = ? AND source_type IN ('admin/quick-run', 'quick_run')))
		AND (? = '' OR id LIKE ? OR script_path LIKE ? OR source_type LIKE ? OR source_name LIKE ? OR status LIKE ? OR executor LIKE ? OR initiated_by_username LIKE ?)
		AND (? = 0 OR created_at >= ?)
		AND (? = 0 OR created_at < ?)
		ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		filter.ScheduleID, filter.ScheduleID,
		filter.QuickRunID, filter.QuickRunID,
		filter.Query, like, like, like, like, like, like, like,
		filter.HasCreatedFromBoundary, filter.CreatedFromUnixNano,
		filter.HasCreatedBeforeBoundary, filter.CreatedBeforeUnixNano,
		limit, offset)
	if err != nil {
		return nil, err
	}
	runs := make([]Run, 0, limit)
	for rows.Next() {
		run, _, err := scanRunMetadata(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return runs, nil
}

func (m *Manager) CleanupLogs(retention time.Duration, maxBytes int64) (int, error) {
	rows, err := m.db.Query(`SELECT id, created_at, log_path, log_bytes FROM runs WHERE log_expired = 0 AND status NOT IN ('starting','running','stopping','timing_out') ORDER BY created_at`)
	if err != nil {
		return 0, err
	}
	type candidate struct {
		id, path      string
		created, size int64
	}
	var candidates []candidate
	backfill := make(map[string]int64)
	var total int64
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.created, &item.path, &item.size); err != nil {
			_ = rows.Close()
			return 0, err
		}
		if item.size < 0 {
			item.size = 0
			if info, statErr := os.Stat(item.path); statErr == nil {
				item.size = info.Size()
			}
			backfill[item.id] = item.size
			if relative, relErr := filepath.Rel(m.stateRoot, item.path); relErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				if staleTail, globErr := filepath.Glob(item.path + ".tail-*"); globErr == nil {
					for _, stalePath := range staleTail {
						_ = os.Remove(stalePath)
					}
				}
			}
		}
		total += item.size
		candidates = append(candidates, item)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if len(backfill) > 0 {
		transaction, err := m.db.Begin()
		if err != nil {
			return 0, err
		}
		for id, size := range backfill {
			if _, err := transaction.Exec("UPDATE runs SET log_bytes = ? WHERE id = ? AND log_bytes < 0", size, id); err != nil {
				_ = transaction.Rollback()
				return 0, err
			}
		}
		if err := transaction.Commit(); err != nil {
			return 0, err
		}
	}
	cutoff := time.Now().Add(-retention).UnixNano()
	cleaned := 0
	for _, item := range candidates {
		if item.created >= cutoff && total <= maxBytes {
			continue
		}
		if info, statErr := os.Stat(item.path); statErr == nil && info.Size() != item.size {
			total += info.Size() - item.size
			item.size = info.Size()
		}
		relative, relErr := filepath.Rel(m.stateRoot, item.path)
		if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return cleaned, fmt.Errorf("拒绝清理 State Root 外的 Run Log")
		}
		if err := os.Remove(item.path); err != nil && !os.IsNotExist(err) {
			return cleaned, err
		}
		if _, err := m.db.Exec("UPDATE runs SET log_expired = 1 WHERE id = ?", item.id); err != nil {
			return cleaned, err
		}
		total -= item.size
		cleaned++
	}
	return cleaned, nil
}

var variableReference = regexp.MustCompile(`^\{\{([A-Z][A-Z0-9_]{0,63})\}\}$`)

func resolveVariables(arguments []string, variables map[string]string) ([]string, error) {
	resolved := make([]string, len(arguments))
	for index, argument := range arguments {
		match := variableReference.FindStringSubmatch(argument)
		if len(match) == 2 {
			value, exists := variables[match[1]]
			if !exists {
				return nil, fmt.Errorf("Variable %s 不存在", match[1])
			}
			resolved[index] = value
			continue
		}
		if strings.Contains(argument, "{{") || strings.Contains(argument, "}}") {
			return nil, fmt.Errorf("Variable 引用必须独占一个参数")
		}
		resolved[index] = argument
	}
	return resolved, nil
}

func readEvents(path string) ([]Event, error) {
	events, _, err := readEventsAfter(path, 0, 0)
	return events, err
}

func readEventsAfter(path string, offset, afterSequence int64) ([]Event, int64, error) {
	var events []Event
	nextOffset, err := scanEvents(path, offset, afterSequence, func(event Event) error {
		events = append(events, event)
		return nil
	})
	return events, nextOffset, err
}

func scanEvents(path string, offset, afterSequence int64, emit func(Event) error) (int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return offset, err
	}
	defer file.Close()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return offset, err
	}
	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 64<<10)
	scanner.Buffer(buffer, 1<<20)
	for scanner.Scan() {
		lineOffset := offset
		offset += int64(len(scanner.Bytes()) + 1)
		var persisted persistedEvent
		if err := json.Unmarshal(scanner.Bytes(), &persisted); err != nil {
			return lineOffset, fmt.Errorf("decode Run log at byte %d: %w", lineOffset, err)
		}
		if persisted.Sequence > afterSequence {
			text, encodingError := decodeOutput(persisted.Data)
			if err := emit(Event{Sequence: persisted.Sequence, Time: time.Unix(0, persisted.Time).UTC(), Source: persisted.Source, Data: text, EncodingError: encodingError}); err != nil {
				return offset, err
			}
		}
	}
	return offset, scanner.Err()
}

func (m *Manager) Close() {
	m.mu.Lock()
	processes := make([]ManagedProcess, 0, len(m.active))
	for _, active := range m.active {
		active.terminal = "cancelled"
		processes = append(processes, active.process)
	}
	m.mu.Unlock()
	for _, process := range processes {
		if err := process.Terminate(false); err != nil {
			_ = process.Terminate(true)
		}
	}
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		m.persistenceStopOnce.Do(func() { close(m.persistenceStop) })
		return
	case <-time.After(30 * time.Second):
	}
	for _, process := range processes {
		_ = process.Terminate(true)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		m.persistenceStopOnce.Do(func() { close(m.persistenceStop) })
		<-done
	}
	m.persistenceStopOnce.Do(func() { close(m.persistenceStop) })
}

const runStateRetryDelay = 100 * time.Millisecond

func retryRunStateWrite(stop <-chan struct{}, write func() error) bool {
	for {
		if err := write(); err == nil {
			return true
		}
		timer := time.NewTimer(runStateRetryDelay)
		select {
		case <-timer.C:
		case <-stop:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return false
		}
	}
}

func randomID() (string, error) {
	value := make([]byte, 18)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func resolveExecutors(extension string, overrides map[string][]string) ([]executorCandidate, error) {
	extension = strings.ToLower(extension)
	type configuredCandidate struct {
		name   string
		prefix []string
	}
	var candidates []configuredCandidate
	configured := overrides[strings.ToLower(extension)]
	if len(configured) > 0 {
		for _, executable := range configured {
			if !filepath.IsAbs(executable) {
				return nil, fmt.Errorf("配置的执行器必须使用绝对路径: %s", executable)
			}
			candidates = append(candidates, configuredCandidate{name: executable, prefix: executorPrefix(extension)})
		}
	}
	if len(candidates) == 0 && runtime.GOOS == "windows" {
		switch extension {
		case ".cmd", ".bat":
			candidates = []configuredCandidate{{name: "cmd.exe", prefix: []string{"/D", "/S", "/V:OFF", "/C"}}}
		case ".ps1":
			candidates = []configuredCandidate{{name: "pwsh.exe", prefix: []string{"-File"}}, {name: "powershell.exe", prefix: []string{"-NoProfile", "-File"}}}
		case ".py":
			candidates = []configuredCandidate{{name: "py.exe", prefix: []string{"-3"}}, {name: "python.exe"}}
		case ".sh":
			candidates = []configuredCandidate{{name: "bash.exe"}}
		}
	} else if len(candidates) == 0 {
		switch extension {
		case ".sh":
			candidates = []configuredCandidate{{name: "bash"}, {name: "sh"}}
		case ".py":
			candidates = []configuredCandidate{{name: "python3"}, {name: "python"}}
		case ".ps1":
			candidates = []configuredCandidate{{name: "pwsh", prefix: []string{"-File"}}}
		}
	}
	resolved := make([]executorCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		path := candidate.name
		if !filepath.IsAbs(path) {
			lookedUp, lookupErr := exec.LookPath(path)
			if lookupErr != nil {
				continue
			}
			path = lookedUp
		}
		path, err := validateExecutorTrust(path)
		if err != nil {
			continue
		}
		resolved = append(resolved, executorCandidate{
			path: path, prefix: candidate.prefix,
			batch: extension == ".cmd" || extension == ".bat",
		})
	}
	if len(resolved) == 0 {
		return nil, fmt.Errorf("宿主机没有可用于 %s 的执行器", extension)
	}
	return resolved, nil
}

func executorPrefix(extension string) []string {
	if runtime.GOOS == "windows" {
		switch extension {
		case ".cmd", ".bat":
			return []string{"/D", "/S", "/V:OFF", "/C"}
		case ".ps1":
			return []string{"-NoProfile", "-File"}
		case ".py":
			return nil
		}
	}
	if extension == ".ps1" {
		return []string{"-File"}
	}
	return nil
}

func ParseArguments(input string) ([]string, error) {
	if err := validateProcessArgument(input); err != nil {
		return nil, err
	}
	var arguments []string
	var current strings.Builder
	var quote rune
	escaped := false
	hasToken := false
	flush := func() {
		arguments = append(arguments, current.String())
		current.Reset()
		hasToken = false
	}
	for _, character := range input {
		if escaped {
			current.WriteRune(character)
			escaped = false
			hasToken = true
			continue
		}
		if character == '\\' {
			escaped = true
			hasToken = true
			continue
		}
		if quote != 0 {
			if character == quote {
				quote = 0
			} else {
				current.WriteRune(character)
			}
			hasToken = true
			continue
		}
		if character == '\'' || character == '"' {
			quote = character
			hasToken = true
			continue
		}
		if unicode.IsSpace(character) {
			if hasToken {
				flush()
			}
			continue
		}
		current.WriteRune(character)
		hasToken = true
	}
	if escaped || quote != 0 {
		return nil, fmt.Errorf("参数包含未闭合的转义或引号")
	}
	if hasToken {
		flush()
	}
	for _, argument := range arguments {
		if err := validateProcessArgument(argument); err != nil {
			return nil, err
		}
		if len(argument) > 32<<10 {
			return nil, fmt.Errorf("单个参数过长: %s", strconv.Quote(argument[:min(len(argument), 32)]))
		}
	}
	return arguments, nil
}
