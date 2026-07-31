package gitprotect

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const managedBranch = "scriptboard-managed"
const maxRepositoryMetadataBytes int64 = 1 << 20
const maxGitCommandOutputBytes int64 = 16 << 20
const maxGitHistoryOutputBytes int64 = 4 << 20

var forbiddenGitConfigSection = regexp.MustCompile(`(?im)^[\t ]*\[(?:submodule|filter|diff|include|includeif|credential|gpg|gc|maintenance)(?:[\t ]|"|\.|\])`)
var forbiddenGitConfigVariable = regexp.MustCompile(`(?im)^[\t ]*(?:gpgsign|showsignature|signingkey|program|helper|hookspath|fsmonitor|external|textconv|attributesfile|excludesfile|sshcommand|gitproxy|alternaterefscommand|recentobjectshook|skiplist|worktreeconfig)(?:[\t ]*=|[\t ]*(?:[#;].*)?$)`)

type State struct {
	Status          string
	Enabled         bool
	LastCommit      string
	AbnormalReason  string
	RepositoryBytes int64
	StorageWarning  bool
}

type File struct {
	Path string
	Size int64
}

type Description struct {
	State   State
	Reasons map[string]string
}

type Manager struct {
	db            *sql.DB
	root          string
	gitExecutable string
	emptyHooks    string
	maxFileBytes  int64
	mu            sync.Mutex
	activeRuns    map[string]struct{}
	batchRunIDs   []string
}

func New(db *sql.DB, root, gitExecutable, stateRoot string) (*Manager, error) {
	if gitExecutable == "" {
		resolved, err := exec.LookPath("git")
		if err == nil {
			gitExecutable = resolved
		}
	}
	emptyHooks := filepath.Join(stateRoot, "git-hooks-disabled")
	if err := os.MkdirAll(emptyHooks, 0o700); err != nil {
		return nil, err
	}
	return &Manager{db: db, root: root, gitExecutable: gitExecutable, emptyHooks: emptyHooks, maxFileBytes: 10 << 20, activeRuns: make(map[string]struct{})}, nil
}

func (m *Manager) BeginRun(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.State()
	if err != nil {
		return err
	}
	if !state.Enabled {
		return nil
	}
	if state.Status != "healthy" {
		return fmt.Errorf("Version Protection 处于 %s 状态，拒绝新 Run", state.Status)
	}
	if len(m.activeRuns) == 0 {
		if err := m.Checkpoint("ScriptBoard pre-run checkpoint\n\nScriptBoard-Operation: pre-run"); err != nil {
			return err
		}
		m.batchRunIDs = nil
	}
	m.activeRuns[id] = struct{}{}
	m.batchRunIDs = append(m.batchRunIDs, id)
	return nil
}

func (m *Manager) EndRun(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.activeRuns[id]; !exists {
		return
	}
	delete(m.activeRuns, id)
	if len(m.activeRuns) != 0 {
		return
	}
	message := "ScriptBoard post-run checkpoint\n\nScriptBoard-Operation: post-run"
	for _, runID := range m.batchRunIDs {
		message += "\nScriptBoard-Run-ID: " + runID
	}
	_ = m.Checkpoint(message)
}

func (m *Manager) State() (State, error) {
	var state State
	err := m.db.QueryRow("SELECT status, enabled, last_commit, abnormal_reason FROM git_state WHERE id = 1").Scan(
		&state.Status, &state.Enabled, &state.LastCommit, &state.AbnormalReason,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return State{Status: "disabled"}, nil
	}
	if err == nil {
		state.RepositoryBytes, _ = directorySize(filepath.Join(m.root, ".git"))
		state.StorageWarning = state.RepositoryBytes >= 4<<30
	}
	return state, err
}

func (m *Manager) ProtectionReason(relative string, size int64) string {
	description, err := m.DescribeEntries([]File{{Path: relative, Size: size}})
	if err != nil {
		return "Version Protection 已停用"
	}
	return description.Reasons[relative]
}

func (m *Manager) DescribeEntries(files []File) (Description, error) {
	state, err := m.State()
	if err != nil {
		return Description{}, err
	}
	description := Description{
		State:   state,
		Reasons: make(map[string]string, len(files)),
	}
	if !state.Enabled {
		for _, file := range files {
			description.Reasons[file.Path] = "Version Protection 已停用"
		}
		return description, nil
	}
	if err := m.validateSafeRepository(); err != nil {
		return Description{}, err
	}

	type candidate struct {
		original   string
		normalized string
	}
	eligible := make([]candidate, 0, len(files))
	for _, file := range files {
		if file.Size > m.maxFileBytes {
			description.Reasons[file.Path] = "未保护：超过 10 MiB"
			continue
		}
		eligible = append(eligible, candidate{original: file.Path, normalized: filepath.ToSlash(file.Path)})
	}
	if len(eligible) == 0 {
		return description, nil
	}

	paths := make([]string, len(eligible))
	for index := range eligible {
		paths[index] = eligible[index].normalized
	}
	command := m.command("check-ignore", "--stdin", "-z")
	command.Stdin = strings.NewReader(strings.Join(paths, "\x00") + "\x00")
	output, commandErr := runCommandOutput(command, maxGitCommandOutputBytes)
	known := commandErr == nil
	if commandErr != nil {
		var exitError *exec.ExitError
		known = errors.As(commandErr, &exitError) && exitError.ExitCode() == 1
	}
	ignored := make(map[string]struct{})
	if known {
		for _, path := range strings.Split(string(output), "\x00") {
			if path != "" {
				ignored[path] = struct{}{}
			}
		}
	}
	for _, file := range eligible {
		_, isIgnored := ignored[file.normalized]
		switch {
		case !known:
			description.Reasons[file.original] = "保护状态未知"
		case isIgnored:
			description.Reasons[file.original] = "未保护：被 .gitignore 排除"
		default:
			description.Reasons[file.original] = "已受保护"
		}
	}
	return description, nil
}

func (m *Manager) Enable() error {
	if m.gitExecutable == "" {
		return fmt.Errorf("未找到系统 Git CLI")
	}
	gitPath := filepath.Join(m.root, ".git")
	if info, err := os.Lstat(gitPath); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf(".git 必须是真实本地目录")
		}
		if err := m.validateSafeRepository(); err != nil {
			return err
		}
		state, stateErr := m.State()
		if stateErr != nil || state.LastCommit == "" {
			return fmt.Errorf("已有 Git 仓库的接管需要单独确认")
		}
		branch, branchErr := m.output("branch", "--show-current")
		if branchErr != nil || strings.TrimSpace(branch) != managedBranch {
			return fmt.Errorf("现有 ScriptBoard 仓库不在专用分支")
		}
		if err := m.checkpoint("ScriptBoard re-enable checkpoint\n\nScriptBoard-Operation: re-enable"); err != nil {
			return m.abnormal(err.Error())
		}
		head, err := m.output("rev-parse", "HEAD")
		if err != nil {
			return m.abnormal(err.Error())
		}
		_, err = m.db.Exec("UPDATE git_state SET status = 'healthy', enabled = 1, last_commit = ?, abnormal_reason = '', updated_at = ? WHERE id = 1", strings.TrimSpace(head), time.Now().UTC().Unix())
		return err
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := m.raw("init", "--template=", "--initial-branch="+managedBranch, m.root); err != nil {
		return m.abnormal("初始化仓库失败: " + err.Error())
	}
	if err := m.writeMandatoryExcludes(); err != nil {
		return m.abnormal(err.Error())
	}
	if err := m.checkpoint("ScriptBoard baseline\n\nScriptBoard-Operation: baseline"); err != nil {
		return m.abnormal(err.Error())
	}
	head, err := m.output("rev-parse", "HEAD")
	if err != nil {
		return m.abnormal(err.Error())
	}
	_, err = m.db.Exec(`INSERT INTO git_state (id, status, enabled, branch, git_executable, max_tracked_file_bytes, max_repository_bytes, last_commit, abnormal_reason, updated_at)
		VALUES (1, 'healthy', 1, ?, ?, ?, ?, ?, '', ?)
		ON CONFLICT(id) DO UPDATE SET status='healthy', enabled=1, branch=excluded.branch, git_executable=excluded.git_executable,
		last_commit=excluded.last_commit, abnormal_reason='', updated_at=excluded.updated_at`,
		managedBranch, m.gitExecutable, m.maxFileBytes, int64(5<<30), strings.TrimSpace(head), time.Now().UTC().Unix(),
	)
	return err
}

func (m *Manager) Disable() error {
	state, err := m.State()
	if err != nil {
		return err
	}
	if !state.Enabled {
		return nil
	}
	_, err = m.db.Exec("UPDATE git_state SET status = 'disabled', enabled = 0, updated_at = ? WHERE id = 1", time.Now().UTC().Unix())
	return err
}

type Commit struct {
	Hash, Time, Subject string
}

func (m *Manager) History(relative string) ([]Commit, error) {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(relative)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || filepath.IsAbs(clean) {
		return nil, fmt.Errorf("历史路径无效")
	}
	if err := m.validateSafeRepository(); err != nil {
		return nil, err
	}
	output, err := runCommandOutput(m.command("log", "--format=%H%x09%cI%x09%s", "--", clean), maxGitHistoryOutputBytes)
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) == 3 {
			commits = append(commits, Commit{Hash: parts[0], Time: parts[1], Subject: parts[2]})
		}
	}
	return commits, nil
}

func (m *Manager) checkpoint(message string) error {
	if err := m.validateSafeRepository(); err != nil {
		return err
	}
	if size, _ := directorySize(filepath.Join(m.root, ".git")); size >= 5<<30 {
		return fmt.Errorf("Git 仓库已达到 5 GiB 上限")
	}
	branch, err := m.output("branch", "--show-current")
	if err != nil || strings.TrimSpace(branch) != managedBranch {
		return fmt.Errorf("Git HEAD 不在专用分支 %s", managedBranch)
	}
	paths, err := m.eligiblePaths()
	if err != nil {
		return err
	}
	eligible := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		eligible[path] = struct{}{}
	}
	trackedOutput, err := runCommandOutput(m.command("ls-files", "-z"), maxGitCommandOutputBytes)
	if err != nil {
		return fmt.Errorf("Git ls-files 失败: %w", err)
	}
	for _, tracked := range strings.Split(string(trackedOutput), "\x00") {
		if tracked == "" {
			continue
		}
		if _, keep := eligible[tracked]; keep {
			continue
		}
		if _, statErr := os.Lstat(filepath.Join(m.root, filepath.FromSlash(tracked))); statErr == nil {
			if output, err := runCommandOutput(m.command("rm", "--cached", "--ignore-unmatch", "--", tracked), maxGitCommandOutputBytes); err != nil {
				return fmt.Errorf("Git 停止跟踪不符合资格的文件失败: %w: %s", err, output)
			}
		}
	}
	if m.command("rev-parse", "--verify", "HEAD").Run() == nil {
		if output, err := runCommandOutput(m.command("add", "-u", "--", "."), maxGitCommandOutputBytes); err != nil {
			return fmt.Errorf("Git add -u 失败: %w: %s", err, output)
		}
	}
	for start := 0; start < len(paths); start += 100 {
		end := min(start+100, len(paths))
		arguments := append([]string{"add", "--"}, paths[start:end]...)
		if output, err := runCommandOutput(m.command(arguments...), maxGitCommandOutputBytes); err != nil {
			return fmt.Errorf("Git add 失败: %w: %s", err, output)
		}
	}
	command := m.command("-c", "user.name=ScriptBoard", "-c", "user.email=scriptboard@localhost", "commit", "--allow-empty", "-m", message)
	if output, err := runCommandOutput(command, maxGitCommandOutputBytes); err != nil {
		return fmt.Errorf("Git commit 失败: %w: %s", err, output)
	}
	return nil
}

func (m *Manager) Adopt() error {
	if m.gitExecutable == "" {
		return fmt.Errorf("未找到系统 Git CLI")
	}
	gitPath := filepath.Join(m.root, ".git")
	info, err := os.Lstat(gitPath)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("只能接管 .git 为真实目录的非 bare 仓库")
	}
	if err := m.validateSafeRepository(); err != nil {
		return err
	}
	bare, err := m.output("rev-parse", "--is-bare-repository")
	if err != nil || strings.TrimSpace(bare) != "false" {
		return fmt.Errorf("不能接管 bare 仓库")
	}
	status, err := m.output("status", "--porcelain=v1", "--untracked-files=all")
	if err != nil || strings.TrimSpace(status) != "" {
		return fmt.Errorf("接管前仓库必须完全 clean")
	}
	if output, err := runCommandOutput(m.command("fsck", "--full", "--no-dangling"), maxGitCommandOutputBytes); err != nil {
		return fmt.Errorf("Git fsck 失败: %w: %s", err, output)
	}
	if err := m.validateSafeRepository(); err != nil {
		return err
	}
	if m.command("show-ref", "--verify", "--quiet", "refs/heads/"+managedBranch).Run() == nil {
		return fmt.Errorf("已有同名专用分支，拒绝自动接管")
	}
	if output, err := runCommandOutput(m.command("switch", "-c", managedBranch), maxGitCommandOutputBytes); err != nil {
		return fmt.Errorf("创建专用分支失败: %w: %s", err, output)
	}
	if err := m.writeMandatoryExcludes(); err != nil {
		return err
	}
	if err := m.checkpoint("ScriptBoard adoption checkpoint\n\nScriptBoard-Operation: adopt"); err != nil {
		return m.abnormal(err.Error())
	}
	head, err := m.output("rev-parse", "HEAD")
	if err != nil {
		return err
	}
	_, err = m.db.Exec(`INSERT INTO git_state (id, status, enabled, branch, git_executable, max_tracked_file_bytes, max_repository_bytes, last_commit, abnormal_reason, updated_at)
		VALUES (1, 'healthy', 1, ?, ?, ?, ?, ?, '', ?)
		ON CONFLICT(id) DO UPDATE SET status='healthy', enabled=1, branch=excluded.branch, git_executable=excluded.git_executable, last_commit=excluded.last_commit, abnormal_reason='', updated_at=excluded.updated_at`,
		managedBranch, m.gitExecutable, m.maxFileBytes, int64(5<<30), strings.TrimSpace(head), time.Now().UTC().Unix())
	return err
}

func (m *Manager) validateSafeRepository() error {
	repositoryRoot, err := os.OpenRoot(m.root)
	if err != nil {
		return fmt.Errorf("打开受管根目录: %w", err)
	}
	defer repositoryRoot.Close()

	if _, err := repositoryRoot.Lstat(".gitmodules"); err == nil {
		return fmt.Errorf("仓库包含子模块配置")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("检查 .gitmodules: %w", err)
	}
	for _, path := range []string{".git/commondir", ".git/info/grafts", ".git/objects/info/alternates", ".git/objects/info/http-alternates"} {
		if _, err := repositoryRoot.Lstat(path); err == nil {
			return fmt.Errorf("Git 仓库包含禁止的外部对象或工作树元数据 %s", path)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("检查 %s: %w", path, err)
		}
	}
	if err := validateGitConfigFile(repositoryRoot, ".git/config", false); err != nil {
		return err
	}
	if err := validateGitConfigFile(repositoryRoot, ".git/config.worktree", true); err != nil {
		return err
	}

	if err := validateAttributesFile(repositoryRoot, ".git/info/attributes", true); err != nil {
		return err
	}
	return fs.WalkDir(repositoryRoot.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return fs.SkipDir
		}
		if entry.Name() != ".gitattributes" {
			return nil
		}
		return validateAttributesFile(repositoryRoot, path, false)
	})
}

func validateGitConfigFile(root *os.Root, path string, optional bool) error {
	info, err := root.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) && optional {
		return nil
	}
	if err != nil {
		return fmt.Errorf("检查 %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s 必须是普通文件", path)
	}
	content, err := readRootFileLimited(root, path, maxRepositoryMetadataBytes)
	if err != nil {
		return fmt.Errorf("读取 %s: %w", path, err)
	}
	if forbiddenGitConfigSection.Match(content) || forbiddenGitConfigVariable.Match(content) {
		return fmt.Errorf("%s 包含可能启动外部程序或加载外部配置的设置", path)
	}
	return nil
}

func validateAttributesFile(root *os.Root, path string, optional bool) error {
	info, err := root.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) && optional {
		return nil
	}
	if err != nil {
		return fmt.Errorf("检查 %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s 必须是普通文件", path)
	}
	content, err := readRootFileLimited(root, path, maxRepositoryMetadataBytes)
	if err != nil {
		return fmt.Errorf("读取 %s: %w", path, err)
	}
	for _, line := range strings.Split(strings.ToLower(string(content)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		for _, field := range fields[1:] {
			name := strings.TrimLeft(field, "-!")
			if name == "filter" || name == "diff" || name == "merge" ||
				strings.HasPrefix(name, "filter=") || strings.HasPrefix(name, "diff=") || strings.HasPrefix(name, "merge=") {
				return fmt.Errorf("%s 包含可执行 filter/diff/merge 配置", path)
			}
		}
	}
	return nil
}

func readRootFileLimited(root *os.Root, path string, maximum int64) ([]byte, error) {
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maximum {
		return nil, fmt.Errorf("文件超过 %d 字节上限", maximum)
	}
	return content, nil
}

func directorySize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if !entry.IsDir() {
			if info, err := entry.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, err
}

func (m *Manager) Checkpoint(message string) error {
	state, err := m.State()
	if err != nil {
		return err
	}
	if !state.Enabled || state.Status != "healthy" {
		return fmt.Errorf("Version Protection 未处于 healthy 状态")
	}
	if err := m.checkpoint(message); err != nil {
		return m.abnormal(err.Error())
	}
	head, err := m.output("rev-parse", "HEAD")
	if err != nil {
		return m.abnormal(err.Error())
	}
	_, err = m.db.Exec("UPDATE git_state SET last_commit = ?, updated_at = ? WHERE id = 1", strings.TrimSpace(head), time.Now().UTC().Unix())
	return err
}

func (m *Manager) RestoreFile(relative, commit string) error {
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == "." || clean == ".." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("恢复路径无效")
	}
	for _, part := range strings.Split(filepath.ToSlash(clean), "/") {
		if part == ".git" || part == ".scriptboard-trash" || strings.HasPrefix(part, ".scriptboard-upload-") {
			return fmt.Errorf("不能恢复 ScriptBoard 保留路径")
		}
	}
	if !validCommitID(commit) {
		return fmt.Errorf("恢复提交标识无效")
	}
	if err := m.Checkpoint("ScriptBoard pre-restore checkpoint\n\nScriptBoard-Operation: pre-restore"); err != nil {
		return err
	}
	slash := filepath.ToSlash(clean)
	content, err := runCommandOutput(m.command("show", commit+":"+slash), m.maxFileBytes)
	if err != nil {
		return fmt.Errorf("读取历史文件失败: %w", err)
	}
	repositoryRoot, err := os.OpenRoot(m.root)
	if err != nil {
		return fmt.Errorf("打开受管根目录: %w", err)
	}
	defer repositoryRoot.Close()
	parent := filepath.Dir(clean)
	parentRoot, err := repositoryRoot.OpenRoot(parent)
	if err != nil {
		return fmt.Errorf("恢复目标父目录无效")
	}
	_ = parentRoot.Close()
	temporary, temporaryPath, err := createRootTemp(repositoryRoot, parent, ".scriptboard-upload-")
	if err != nil {
		return err
	}
	defer repositoryRoot.Remove(temporaryPath)
	mode := os.FileMode(0o644)
	if info, err := repositoryRoot.Lstat(clean); err == nil {
		if !info.Mode().IsRegular() {
			_ = temporary.Close()
			return fmt.Errorf("恢复目标不是普通文件")
		}
		mode = info.Mode().Perm()
	} else if !errors.Is(err, fs.ErrNotExist) {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	backup, err := unusedRootName(repositoryRoot, parent, ".scriptboard-restore-backup-")
	if err != nil {
		return err
	}
	hadTarget := false
	if _, err := repositoryRoot.Lstat(clean); err == nil {
		if err := repositoryRoot.Rename(clean, backup); err != nil {
			return err
		}
		hadTarget = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := repositoryRoot.Rename(temporaryPath, clean); err != nil {
		if hadTarget {
			_ = repositoryRoot.Rename(backup, clean)
		}
		return err
	}
	if hadTarget {
		_ = repositoryRoot.Remove(backup)
	}
	return m.Checkpoint("ScriptBoard restore file\n\nScriptBoard-Operation: restore\nScriptBoard-Path: " + slash)
}

func createRootTemp(root *os.Root, parent, prefix string) (*os.File, string, error) {
	for range 32 {
		name, err := randomRootName(parent, prefix)
		if err != nil {
			return nil, "", err
		}
		file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		return file, name, nil
	}
	return nil, "", errors.New("无法创建唯一恢复临时文件")
}

func unusedRootName(root *os.Root, parent, prefix string) (string, error) {
	for range 32 {
		name, err := randomRootName(parent, prefix)
		if err != nil {
			return "", err
		}
		if _, err := root.Lstat(name); errors.Is(err, fs.ErrNotExist) {
			return name, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", errors.New("无法创建唯一恢复备份名称")
}

func randomRootName(parent, prefix string) (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return filepath.Join(parent, prefix+hex.EncodeToString(random[:])), nil
}

func validCommitID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') &&
			(character < 'A' || character > 'F') {
			return false
		}
	}
	return true
}

func (m *Manager) eligiblePaths() ([]string, error) {
	var paths []string
	err := filepath.WalkDir(m.root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(m.root, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		slash := filepath.ToSlash(relative)
		if entry.IsDir() && (slash == ".git" || slash == ".scriptboard-trash") {
			return filepath.SkipDir
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > m.maxFileBytes || strings.HasPrefix(entry.Name(), ".scriptboard-upload-") {
			return nil
		}
		check := m.command("check-ignore", "--stdin", "-z")
		check.Stdin = strings.NewReader(slash + "\x00")
		output, checkErr := runCommandOutput(check, maxGitCommandOutputBytes)
		ignored := checkErr == nil && len(output) != 0
		if checkErr != nil {
			var exitError *exec.ExitError
			if !errors.As(checkErr, &exitError) || exitError.ExitCode() != 1 {
				return fmt.Errorf("Git check-ignore 失败: %w: %s", checkErr, output)
			}
		}
		if !ignored {
			paths = append(paths, slash)
		}
		return nil
	})
	return paths, err
}

func (m *Manager) writeMandatoryExcludes() error {
	gitRoot, err := os.OpenRoot(filepath.Join(m.root, ".git"))
	if err != nil {
		return err
	}
	defer gitRoot.Close()
	if err := gitRoot.MkdirAll("info", 0o700); err != nil {
		return err
	}
	content := "\n# ScriptBoard mandatory exclusions\n.scriptboard-trash/\n.scriptboard-upload-*\n"
	file, err := gitRoot.OpenFile("info/exclude", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(content)
	return err
}

func (m *Manager) abnormal(reason string) error {
	_, _ = m.db.Exec(`INSERT INTO git_state (id, status, enabled, branch, git_executable, max_tracked_file_bytes, max_repository_bytes, last_commit, abnormal_reason, updated_at)
		VALUES (1, 'abnormal', 1, ?, ?, ?, ?, '', ?, ?)
		ON CONFLICT(id) DO UPDATE SET status='abnormal', enabled=1, abnormal_reason=excluded.abnormal_reason, updated_at=excluded.updated_at`,
		managedBranch, m.gitExecutable, m.maxFileBytes, int64(5<<30), reason, time.Now().UTC().Unix(),
	)
	return errors.New(reason)
}

func (m *Manager) command(arguments ...string) *exec.Cmd {
	base := []string{"--no-pager", "--no-replace-objects"}
	if len(arguments) == 0 || arguments[0] != "check-ignore" {
		base = append(base, "--literal-pathspecs")
	}
	base = append(base,
		"--git-dir="+filepath.Join(m.root, ".git"),
		"--work-tree="+m.root,
		"-c", "core.hooksPath="+m.emptyHooks,
		"-c", "core.attributesFile="+os.DevNull,
		"-c", "core.excludesFile="+os.DevNull,
		"-c", "credential.helper=",
		"-c", "core.fsmonitor=false",
		"-c", "diff.external=",
		"-c", "commit.gpgSign=false",
		"-c", "tag.gpgSign=false",
		"-c", "log.showSignature=false",
		"-c", "gc.auto=0",
		"-c", "maintenance.auto=false",
	)
	command := exec.Command(m.gitExecutable, append(base, arguments...)...)
	command.Dir = m.root
	command.Env = safeGitEnvironment()
	return command
}

func (m *Manager) output(arguments ...string) (string, error) {
	output, err := runCommandOutput(m.command(arguments...), maxGitCommandOutputBytes)
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(arguments, " "), err, output)
	}
	return string(output), nil
}

func (m *Manager) raw(arguments ...string) (string, error) {
	command := exec.Command(m.gitExecutable, arguments...)
	command.Env = safeGitEnvironment()
	output, err := runCommandOutput(command, maxGitCommandOutputBytes)
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(arguments, " "), err, output)
	}
	return string(output), nil
}

type boundedOutput struct {
	mu       sync.Mutex
	content  bytes.Buffer
	limit    int64
	exceeded bool
}

func (output *boundedOutput) Write(value []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	remaining := output.limit - int64(output.content.Len())
	if remaining > 0 {
		kept := int64(len(value))
		if kept > remaining {
			kept = remaining
		}
		_, _ = output.content.Write(value[:kept])
	}
	if int64(len(value)) > remaining {
		output.exceeded = true
	}
	return len(value), nil
}

func (output *boundedOutput) result() ([]byte, bool) {
	output.mu.Lock()
	defer output.mu.Unlock()
	return append([]byte(nil), output.content.Bytes()...), output.exceeded
}

func runCommandOutput(command *exec.Cmd, maximum int64) ([]byte, error) {
	output := &boundedOutput{limit: maximum}
	command.Stdout = output
	command.Stderr = output
	commandErr := command.Run()
	content, exceeded := output.result()
	if exceeded {
		return content, fmt.Errorf("Git 输出超过 %d 字节上限", maximum)
	}
	return content, commandErr
}

func safeGitEnvironment() []string {
	environment := os.Environ()
	safe := make([]string, 0, len(environment)+5)
	for _, value := range environment {
		key, _, _ := strings.Cut(value, "=")
		if strings.HasPrefix(strings.ToUpper(key), "GIT_") {
			continue
		}
		safe = append(safe, value)
	}
	return append(safe,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_COUNT=0",
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_TERMINAL_PROMPT=0",
	)
}
