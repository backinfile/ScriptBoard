package runmanager

import (
	"bufio"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"scriptboard/internal/diskspace"
	"scriptboard/internal/managedfiles"
)

type StartRequest struct {
	ScriptPath        string
	ArgumentsTemplate string
	SourceType        string
	SourceName        string
	TimeoutSeconds    int
	Variables         map[string]string
}

type Run struct {
	ID                string
	ScriptPath        string
	ScriptDigest      string
	ArgumentsTemplate string
	Arguments         []string
	TemplateArguments []string
	Executor          string
	SourceType        string
	SourceName        string
	RuntimeIdentity   string
	Status            string
	CreatedAt         time.Time
	StartedAt         *time.Time
	FinishedAt        *time.Time
	ExitCode          *int
	Error             string
	TimeoutSeconds    int
	Events            []Event
	LogExpired        bool
	LogIncomplete     bool
	LogTruncated      bool
	DroppedBytes      int64
}

type Event struct {
	Sequence      int64
	Time          time.Time
	Source        string
	Data          string
	EncodingError bool
}

type Lifecycle interface {
	BeginRun(id string) error
	EndRun(id string)
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
}

type activeRun struct {
	command      *exec.Cmd
	terminal     string
	scriptPath   string
	timeoutTimer *time.Timer
	cleanup      func()
	fileInfo     os.FileInfo
}

type Manager struct {
	db             *sql.DB
	managed        *managedfiles.Store
	stateRoot      string
	mu             sync.Mutex
	active         map[string]*activeRun
	wg             sync.WaitGroup
	timeoutGrace   time.Duration
	lifecycle      Lifecycle
	executorChains map[string][]string
}

func New(db *sql.DB, managed *managedfiles.Store, stateRoot string, timeoutGrace time.Duration, executorChains map[string][]string) *Manager {
	return &Manager{db: db, managed: managed, stateRoot: stateRoot, active: make(map[string]*activeRun), timeoutGrace: timeoutGrace, executorChains: executorChains}
}

func (m *Manager) SetLifecycle(lifecycle Lifecycle) {
	m.lifecycle = lifecycle
}

func (m *Manager) Start(request StartRequest) (string, error) {
	if err := diskspace.Require(m.stateRoot, diskspace.MinimumWritableBytes); err != nil {
		return "", err
	}
	if len([]byte(request.ArgumentsTemplate)) > 16<<10 {
		return "", fmt.Errorf("参数模板超过 16 KiB")
	}
	templateArguments, err := ParseArguments(request.ArgumentsTemplate)
	if err != nil {
		return "", err
	}
	if len(templateArguments) > 256 {
		return "", fmt.Errorf("参数数量超过 256 个")
	}
	arguments, err := resolveVariables(templateArguments, request.Variables)
	if err != nil {
		return "", err
	}
	script, err := m.managed.PrepareScript(request.ScriptPath)
	if err != nil {
		return "", fmt.Errorf("脚本不可执行: %w", err)
	}
	executors, err := resolveExecutors(filepath.Ext(script.Path), m.executorChains)
	if err != nil {
		return "", err
	}
	id, err := randomID()
	if err != nil {
		return "", err
	}
	lifecycleBegun := false
	if m.lifecycle != nil {
		if err := m.lifecycle.BeginRun(id); err != nil {
			return "", err
		}
		lifecycleBegun = true
	}
	defer func() {
		if lifecycleBegun && m.lifecycle != nil {
			m.lifecycle.EndRun(id)
		}
	}()
	logRoot := filepath.Join(m.stateRoot, "runs", id)
	if err := os.MkdirAll(logRoot, 0o700); err != nil {
		return "", fmt.Errorf("创建 Run 日志目录: %w", err)
	}
	logPath := filepath.Join(logRoot, "events.jsonl")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return "", fmt.Errorf("创建 Run Log: %w", err)
	}
	argumentJSON, _ := json.Marshal(arguments)
	templateArgumentJSON, _ := json.Marshal(templateArguments)
	now := time.Now().UTC()
	runtimeIdentity := "unknown"
	if currentUser, userErr := user.Current(); userErr == nil {
		runtimeIdentity = currentUser.Username
	}
	if _, err := m.db.Exec(`INSERT INTO runs
		(id, script_path, script_sha256, arguments_template, template_arguments_json, arguments_json, executor, source_type, source_name, runtime_identity, status, created_at, timeout_seconds, log_path)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'starting', ?, ?, ?)`,
		id, request.ScriptPath, script.Digest, request.ArgumentsTemplate, string(templateArgumentJSON), string(argumentJSON), executors[0].path, request.SourceType, request.SourceName, runtimeIdentity, now.UnixNano(), request.TimeoutSeconds, logPath,
	); err != nil {
		_ = logFile.Close()
		return "", fmt.Errorf("创建 Run: %w", err)
	}

	var command *exec.Cmd
	var stdout, stderr io.ReadCloser
	var startErrors []string
	for _, executor := range executors {
		commandArguments := append(append([]string{}, executor.prefix...), script.Path)
		commandArguments = append(commandArguments, arguments...)
		candidate := exec.Command(executor.path, commandArguments...)
		candidate.Dir = filepath.Dir(script.Path)
		candidate.Env = append(os.Environ(), "SCRIPTBOARD_RUN_ID="+id, "SCRIPTBOARD_SCRIPT_PATH="+request.ScriptPath)
		configureProcess(candidate)
		candidateStdout, pipeErr := candidate.StdoutPipe()
		if pipeErr != nil {
			startErrors = append(startErrors, executor.path+": "+pipeErr.Error())
			continue
		}
		candidateStderr, pipeErr := candidate.StderrPipe()
		if pipeErr != nil {
			_ = candidateStdout.Close()
			startErrors = append(startErrors, executor.path+": "+pipeErr.Error())
			continue
		}
		if startErr := candidate.Start(); startErr != nil {
			_ = candidateStdout.Close()
			_ = candidateStderr.Close()
			startErrors = append(startErrors, executor.path+": "+startErr.Error())
			continue
		}
		command, stdout, stderr = candidate, candidateStdout, candidateStderr
		_, _ = m.db.Exec("UPDATE runs SET executor = ? WHERE id = ?", executor.path, id)
		break
	}
	if command == nil {
		_ = logFile.Close()
		m.failStart(id, fmt.Errorf("所有执行器均无法启动: %s", strings.Join(startErrors, "; ")))
		return id, nil
	}
	cleanup, err := attachProcess(command.Process)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		_ = logFile.Close()
		m.failStart(id, err)
		return id, nil
	}
	started := time.Now().UTC()
	_, _ = m.db.Exec("UPDATE runs SET status = 'running', started_at = ? WHERE id = ?", started.UnixNano(), id)
	m.mu.Lock()
	active := &activeRun{command: command, scriptPath: normalizeManagedPath(request.ScriptPath), cleanup: cleanup, fileInfo: script.Info}
	m.active[id] = active
	if request.TimeoutSeconds > 0 {
		active.timeoutTimer = time.AfterFunc(time.Duration(request.TimeoutSeconds)*time.Second, func() { m.timeout(id) })
	}
	m.mu.Unlock()
	m.wg.Add(1)
	go m.supervise(id, command, stdout, stderr, logFile)
	lifecycleBegun = false
	return id, nil
}

func (m *Manager) ConflictsPath(relative string) bool {
	candidate := normalizeManagedPath(relative)
	candidateInfo, _ := m.managed.Info(relative)
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, active := range m.active {
		if active.scriptPath == candidate || strings.HasPrefix(active.scriptPath, candidate+"/") || (candidateInfo != nil && active.fileInfo != nil && os.SameFile(candidateInfo, active.fileInfo)) {
			return true
		}
	}
	return false
}

func (m *Manager) IsActiveScript(relative string) bool {
	candidate := normalizeManagedPath(relative)
	candidateInfo, _ := m.managed.Info(relative)
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

func normalizeManagedPath(relative string) string {
	value := strings.Trim(filepath.ToSlash(filepath.Clean(filepath.FromSlash(relative))), "/")
	if runtime.GOOS == "windows" {
		value = strings.ToLower(value)
	}
	return value
}

func (m *Manager) failStart(id string, startErr error) {
	now := time.Now().UTC().UnixNano()
	_, _ = m.db.Exec("UPDATE runs SET status = 'failed', finished_at = ?, error = ? WHERE id = ?", now, startErr.Error(), id)
}

func (m *Manager) supervise(id string, command *exec.Cmd, stdout, stderr io.ReadCloser, logFile *os.File) {
	defer m.wg.Done()
	if m.lifecycle != nil {
		defer m.lifecycle.EndRun(id)
	}
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
				encoded, _ := json.Marshal(persistedEvent{Sequence: sequence, Time: time.Now().UTC().UnixNano(), Source: source, Data: append([]byte(nil), buffer[:count]...)})
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
			}
			if readErr != nil {
				return
			}
		}
	}
	readers.Add(2)
	go writeEvents("stdout", stdout)
	go writeEvents("stderr", stderr)
	waitErr := command.Wait()
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
	readers.Wait()
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
	finished := time.Now().UTC()
	m.mu.Lock()
	active := m.active[id]
	if active != nil && active.timeoutTimer != nil {
		active.timeoutTimer.Stop()
	}
	m.mu.Unlock()
	status := "succeeded"
	exitCode := 0
	errorText := ""
	if active != nil && active.terminal != "" {
		status = active.terminal
		if waitErr != nil {
			errorText = waitErr.Error()
		}
	} else if waitErr != nil {
		status = "failed"
		errorText = waitErr.Error()
		var exitError *exec.ExitError
		if errors.As(waitErr, &exitError) {
			exitCode = exitError.ExitCode()
		} else {
			exitCode = -1
		}
	}
	_, _ = m.db.Exec("UPDATE runs SET status = ?, finished_at = ?, exit_code = ?, error = ?, log_incomplete = ?, log_truncated = ?, dropped_bytes = ? WHERE id = ?", status, finished.UnixNano(), exitCode, errorText, logIncomplete, droppedBytes > 0, droppedBytes, id)
	m.mu.Lock()
	delete(m.active, id)
	m.mu.Unlock()
}

func (m *Manager) timeout(id string) {
	m.mu.Lock()
	active := m.active[id]
	if active == nil || active.terminal != "" {
		m.mu.Unlock()
		return
	}
	active.terminal = "timed_out"
	process := active.command.Process
	m.mu.Unlock()
	_, _ = m.db.Exec("UPDATE runs SET status = 'timing_out' WHERE id = ? AND status = 'running'", id)
	_ = terminateProcess(process, false)
	time.AfterFunc(m.timeoutGrace, func() {
		m.mu.Lock()
		stillActive := m.active[id]
		m.mu.Unlock()
		if stillActive != nil && stillActive.terminal == "timed_out" {
			_ = terminateProcess(stillActive.command.Process, true)
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
	process := active.command.Process
	m.mu.Unlock()
	if !force {
		_, _ = m.db.Exec("UPDATE runs SET status = 'stopping' WHERE id = ? AND status = 'running'", id)
	}
	if err := terminateProcess(process, force); err != nil {
		if force {
			return nil
		}
		return fmt.Errorf("停止进程树: %w", err)
	}
	return nil
}

func (m *Manager) Get(id string) (Run, error) {
	var result Run
	var argumentJSON, templateArgumentJSON, logPath string
	var createdAt int64
	var startedAt, finishedAt, exitCode sql.NullInt64
	err := m.db.QueryRow(`SELECT id, script_path, script_sha256, arguments_template, template_arguments_json, arguments_json, executor, source_type, source_name, runtime_identity,
		status, created_at, started_at, finished_at, exit_code, error, timeout_seconds, log_path, log_expired, log_incomplete, log_truncated, dropped_bytes FROM runs WHERE id = ?`, id).Scan(
		&result.ID, &result.ScriptPath, &result.ScriptDigest, &result.ArgumentsTemplate, &templateArgumentJSON, &argumentJSON, &result.Executor, &result.SourceType, &result.SourceName, &result.RuntimeIdentity,
		&result.Status, &createdAt, &startedAt, &finishedAt, &exitCode, &result.Error, &result.TimeoutSeconds, &logPath, &result.LogExpired, &result.LogIncomplete, &result.LogTruncated, &result.DroppedBytes,
	)
	if err != nil {
		return Run{}, err
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
	_ = json.Unmarshal([]byte(argumentJSON), &result.Arguments)
	_ = json.Unmarshal([]byte(templateArgumentJSON), &result.TemplateArguments)
	result.Events, _ = readEvents(logPath)
	return result, nil
}

func (m *Manager) List(limit int) ([]Run, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := m.db.Query("SELECT id FROM runs ORDER BY created_at DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	runs := make([]Run, 0, len(ids))
	for _, id := range ids {
		run, err := m.Get(id)
		if err != nil {
			return nil, err
		}
		run.Events = nil
		runs = append(runs, run)
	}
	return runs, nil
}

func (m *Manager) CleanupLogs(retention time.Duration, maxBytes int64) (int, error) {
	rows, err := m.db.Query(`SELECT id, created_at, log_path FROM runs WHERE log_expired = 0 AND status NOT IN ('starting','running','stopping','timing_out') ORDER BY created_at`)
	if err != nil {
		return 0, err
	}
	type candidate struct {
		id, path      string
		created, size int64
	}
	var candidates []candidate
	var total int64
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.created, &item.path); err != nil {
			_ = rows.Close()
			return 0, err
		}
		if info, statErr := os.Stat(item.path); statErr == nil {
			item.size = info.Size()
			total += item.size
		}
		if relative, relErr := filepath.Rel(m.stateRoot, item.path); relErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			if staleTail, globErr := filepath.Glob(item.path + ".tail-*"); globErr == nil {
				for _, stalePath := range staleTail {
					_ = os.Remove(stalePath)
				}
			}
		}
		candidates = append(candidates, item)
	}
	_ = rows.Close()
	cutoff := time.Now().Add(-retention).UnixNano()
	cleaned := 0
	for _, item := range candidates {
		if item.created >= cutoff && total <= maxBytes {
			continue
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
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()
	var events []Event
	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 64<<10)
	scanner.Buffer(buffer, 1<<20)
	for scanner.Scan() {
		var persisted persistedEvent
		if json.Unmarshal(scanner.Bytes(), &persisted) == nil {
			raw := string(persisted.Data)
			events = append(events, Event{Sequence: persisted.Sequence, Time: time.Unix(0, persisted.Time).UTC(), Source: persisted.Source, Data: strings.ToValidUTF8(raw, "�"), EncodingError: !utf8.ValidString(raw)})
		}
	}
	return events, scanner.Err()
}

func (m *Manager) Close() {
	m.mu.Lock()
	processes := make([]*os.Process, 0, len(m.active))
	for _, active := range m.active {
		active.terminal = "cancelled"
		processes = append(processes, active.command.Process)
	}
	m.mu.Unlock()
	for _, process := range processes {
		if err := terminateProcess(process, false); err != nil {
			_ = terminateProcess(process, true)
		}
	}
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return
	case <-time.After(30 * time.Second):
	}
	for _, process := range processes {
		_ = terminateProcess(process, true)
	}
	<-done
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
			candidates = []configuredCandidate{{name: "cmd.exe", prefix: []string{"/D", "/S", "/C"}}}
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
		resolved = append(resolved, executorCandidate{path: path, prefix: candidate.prefix})
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
			return []string{"/D", "/S", "/C"}
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
		if len(argument) > 32<<10 {
			return nil, fmt.Errorf("单个参数过长: %s", strconv.Quote(argument[:min(len(argument), 32)]))
		}
	}
	return arguments, nil
}
