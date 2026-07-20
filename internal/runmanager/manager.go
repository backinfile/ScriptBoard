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
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"scriptboard/internal/managedfiles"
)

type StartRequest struct {
	ScriptPath        string
	ArgumentsTemplate string
	SourceType        string
}

type Run struct {
	ID                string
	ScriptPath        string
	ScriptDigest      string
	ArgumentsTemplate string
	Arguments         []string
	Executor          string
	Status            string
	CreatedAt         time.Time
	StartedAt         *time.Time
	FinishedAt        *time.Time
	ExitCode          *int
	Error             string
	Events            []Event
}

type Event struct {
	Sequence int64
	Time     time.Time
	Source   string
	Data     string
}

type persistedEvent struct {
	Sequence int64  `json:"sequence"`
	Time     int64  `json:"time"`
	Source   string `json:"source"`
	Data     []byte `json:"data"`
}

type activeRun struct {
	command    *exec.Cmd
	terminal   string
	scriptPath string
}

type Manager struct {
	db        *sql.DB
	managed   *managedfiles.Store
	stateRoot string
	mu        sync.Mutex
	active    map[string]*activeRun
	wg        sync.WaitGroup
}

func New(db *sql.DB, managed *managedfiles.Store, stateRoot string) *Manager {
	return &Manager{db: db, managed: managed, stateRoot: stateRoot, active: make(map[string]*activeRun)}
}

func (m *Manager) Start(request StartRequest) (string, error) {
	if len([]byte(request.ArgumentsTemplate)) > 16<<10 {
		return "", fmt.Errorf("参数模板超过 16 KiB")
	}
	arguments, err := ParseArguments(request.ArgumentsTemplate)
	if err != nil {
		return "", err
	}
	if len(arguments) > 256 {
		return "", fmt.Errorf("参数数量超过 256 个")
	}
	script, err := m.managed.PrepareScript(request.ScriptPath)
	if err != nil {
		return "", fmt.Errorf("脚本不可执行: %w", err)
	}
	executor, prefix, err := resolveExecutor(filepath.Ext(script.Path))
	if err != nil {
		return "", err
	}
	id, err := randomID()
	if err != nil {
		return "", err
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
	argumentJSON, _ := json.Marshal(arguments)
	now := time.Now().UTC()
	if _, err := m.db.Exec(`INSERT INTO runs
		(id, script_path, script_sha256, arguments_template, arguments_json, executor, source_type, status, created_at, log_path)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'starting', ?, ?)`,
		id, request.ScriptPath, script.Digest, request.ArgumentsTemplate, string(argumentJSON), executor, request.SourceType, now.UnixNano(), logPath,
	); err != nil {
		_ = logFile.Close()
		return "", fmt.Errorf("创建 Run: %w", err)
	}

	commandArguments := append(append([]string{}, prefix...), script.Path)
	commandArguments = append(commandArguments, arguments...)
	command := exec.Command(executor, commandArguments...)
	command.Dir = filepath.Dir(script.Path)
	command.Env = append(os.Environ(), "SCRIPTBOARD_RUN_ID="+id, "SCRIPTBOARD_SCRIPT_PATH="+request.ScriptPath)
	configureProcess(command)
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = logFile.Close()
		m.failStart(id, err)
		return id, nil
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		_ = logFile.Close()
		m.failStart(id, err)
		return id, nil
	}
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		m.failStart(id, err)
		return id, nil
	}
	started := time.Now().UTC()
	_, _ = m.db.Exec("UPDATE runs SET status = 'running', started_at = ? WHERE id = ?", started.UnixNano(), id)
	m.mu.Lock()
	m.active[id] = &activeRun{command: command, scriptPath: normalizeManagedPath(request.ScriptPath)}
	m.mu.Unlock()
	m.wg.Add(1)
	go m.supervise(id, command, stdout, stderr, logFile)
	return id, nil
}

func (m *Manager) ConflictsPath(relative string) bool {
	candidate := normalizeManagedPath(relative)
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, active := range m.active {
		if active.scriptPath == candidate || strings.HasPrefix(active.scriptPath, candidate+"/") {
			return true
		}
	}
	return false
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
	var eventMu sync.Mutex
	var sequence int64
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
				_, _ = logFile.Write(append(encoded, '\n'))
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
	readers.Wait()
	_ = logFile.Sync()
	_ = logFile.Close()
	finished := time.Now().UTC()
	m.mu.Lock()
	active := m.active[id]
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
	_, _ = m.db.Exec("UPDATE runs SET status = ?, finished_at = ?, exit_code = ?, error = ? WHERE id = ?", status, finished.UnixNano(), exitCode, errorText, id)
	m.mu.Lock()
	delete(m.active, id)
	m.mu.Unlock()
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
	var argumentJSON, logPath string
	var createdAt int64
	var startedAt, finishedAt, exitCode sql.NullInt64
	err := m.db.QueryRow(`SELECT id, script_path, script_sha256, arguments_template, arguments_json, executor,
		status, created_at, started_at, finished_at, exit_code, error, log_path FROM runs WHERE id = ?`, id).Scan(
		&result.ID, &result.ScriptPath, &result.ScriptDigest, &result.ArgumentsTemplate, &argumentJSON, &result.Executor,
		&result.Status, &createdAt, &startedAt, &finishedAt, &exitCode, &result.Error, &logPath,
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
	result.Events, _ = readEvents(logPath)
	return result, nil
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
			events = append(events, Event{Sequence: persisted.Sequence, Time: time.Unix(0, persisted.Time).UTC(), Source: persisted.Source, Data: strings.ToValidUTF8(string(persisted.Data), "�")})
		}
	}
	return events, scanner.Err()
}

func (m *Manager) Close() {
	m.mu.Lock()
	for _, active := range m.active {
		active.terminal = "cancelled"
		_ = terminateProcess(active.command.Process, true)
	}
	m.mu.Unlock()
	m.wg.Wait()
}

func randomID() (string, error) {
	value := make([]byte, 18)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func resolveExecutor(extension string) (string, []string, error) {
	extension = strings.ToLower(extension)
	type candidate struct {
		name   string
		prefix []string
	}
	var candidates []candidate
	if runtime.GOOS == "windows" {
		switch extension {
		case ".cmd", ".bat":
			candidates = []candidate{{name: "cmd.exe", prefix: []string{"/D", "/S", "/C"}}}
		case ".ps1":
			candidates = []candidate{{name: "pwsh.exe", prefix: []string{"-File"}}, {name: "powershell.exe", prefix: []string{"-NoProfile", "-File"}}}
		case ".py":
			candidates = []candidate{{name: "py.exe", prefix: []string{"-3"}}, {name: "python.exe"}}
		case ".sh":
			candidates = []candidate{{name: "bash.exe"}}
		}
	} else {
		switch extension {
		case ".sh":
			candidates = []candidate{{name: "bash"}, {name: "sh"}}
		case ".py":
			candidates = []candidate{{name: "python3"}, {name: "python"}}
		case ".ps1":
			candidates = []candidate{{name: "pwsh", prefix: []string{"-File"}}}
		}
	}
	for _, candidate := range candidates {
		path, err := exec.LookPath(candidate.name)
		if err == nil {
			return path, candidate.prefix, nil
		}
	}
	return "", nil, fmt.Errorf("宿主机没有可用于 %s 的执行器", extension)
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
