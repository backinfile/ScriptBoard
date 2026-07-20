package gitprotect

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const managedBranch = "scriptboard-managed"

type State struct {
	Status          string
	Enabled         bool
	LastCommit      string
	AbnormalReason  string
	RepositoryBytes int64
	StorageWarning  bool
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
	state, err := m.State()
	if err != nil || !state.Enabled {
		return "Version Protection 已停用"
	}
	if size > m.maxFileBytes {
		return "未保护：超过 10 MiB"
	}
	err = m.command("check-ignore", "--quiet", "--", filepath.ToSlash(relative)).Run()
	if err == nil {
		return "未保护：被 .gitignore 排除"
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
		return "已受保护"
	}
	return "保护状态未知"
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
	if _, err := m.raw("init", "--initial-branch="+managedBranch, m.root); err != nil {
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
	if clean == "." || strings.HasPrefix(clean, "../") || filepath.IsAbs(clean) {
		return nil, fmt.Errorf("历史路径无效")
	}
	output, err := m.command("log", "--format=%H%x09%cI%x09%s", "--", clean).Output()
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
	trackedOutput, _ := m.command("ls-files", "-z").Output()
	for _, tracked := range strings.Split(string(trackedOutput), "\x00") {
		if tracked == "" {
			continue
		}
		if _, keep := eligible[tracked]; keep {
			continue
		}
		if info, statErr := os.Lstat(filepath.Join(m.root, filepath.FromSlash(tracked))); statErr == nil && info.Mode().IsRegular() {
			if output, err := m.command("rm", "--cached", "--ignore-unmatch", "--", tracked).CombinedOutput(); err != nil {
				return fmt.Errorf("Git 停止跟踪不符合资格的文件失败: %w: %s", err, output)
			}
		}
	}
	if m.command("rev-parse", "--verify", "HEAD").Run() == nil {
		if output, err := m.command("add", "-u", "--", ".").CombinedOutput(); err != nil {
			return fmt.Errorf("Git add -u 失败: %w: %s", err, output)
		}
	}
	for start := 0; start < len(paths); start += 100 {
		end := min(start+100, len(paths))
		arguments := append([]string{"add", "--"}, paths[start:end]...)
		if _, err := m.command(arguments...).CombinedOutput(); err != nil {
			return fmt.Errorf("Git add 失败: %w", err)
		}
	}
	command := m.command("-c", "user.name=ScriptBoard", "-c", "user.email=scriptboard@localhost", "commit", "--allow-empty", "-m", message)
	if output, err := command.CombinedOutput(); err != nil {
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
	bare, err := m.output("rev-parse", "--is-bare-repository")
	if err != nil || strings.TrimSpace(bare) != "false" {
		return fmt.Errorf("不能接管 bare 仓库")
	}
	status, err := m.output("status", "--porcelain=v1", "--untracked-files=all")
	if err != nil || strings.TrimSpace(status) != "" {
		return fmt.Errorf("接管前仓库必须完全 clean")
	}
	if output, err := m.command("fsck", "--full", "--no-dangling").CombinedOutput(); err != nil {
		return fmt.Errorf("Git fsck 失败: %w: %s", err, output)
	}
	if err := m.validateSafeRepository(); err != nil {
		return err
	}
	if m.command("show-ref", "--verify", "--quiet", "refs/heads/"+managedBranch).Run() == nil {
		return fmt.Errorf("已有同名专用分支，拒绝自动接管")
	}
	if output, err := m.command("switch", "-c", managedBranch).CombinedOutput(); err != nil {
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
	if _, err := os.Lstat(filepath.Join(m.root, ".gitmodules")); err == nil {
		return fmt.Errorf("仓库包含子模块配置")
	}
	configBytes, err := os.ReadFile(filepath.Join(m.root, ".git", "config"))
	if err == nil {
		lower := strings.ToLower(string(configBytes))
		for _, forbidden := range []string{"[submodule ", "[filter ", "credential.helper", "fsmonitor", "diff.external"} {
			if strings.Contains(lower, forbidden) {
				return fmt.Errorf("Git 配置包含禁止项 %s", forbidden)
			}
		}
	}
	return filepath.WalkDir(m.root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if entry.Name() != ".gitattributes" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lower := strings.ToLower(string(content))
		if strings.Contains(lower, "filter=") || strings.Contains(lower, "diff=") || strings.Contains(lower, "textconv") {
			return fmt.Errorf(".gitattributes 包含可执行 filter/diff 配置")
		}
		return nil
	})
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
	if err := m.Checkpoint("ScriptBoard pre-restore checkpoint\n\nScriptBoard-Operation: pre-restore"); err != nil {
		return err
	}
	slash := filepath.ToSlash(clean)
	content, err := m.command("show", commit+":"+slash).Output()
	if err != nil {
		return fmt.Errorf("读取历史文件失败: %w", err)
	}
	target := filepath.Join(m.root, clean)
	parent := filepath.Dir(target)
	if info, err := os.Lstat(parent); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("恢复目标父目录无效")
	}
	temporary, err := os.CreateTemp(parent, ".scriptboard-upload-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	mode := os.FileMode(0o644)
	if info, err := os.Lstat(target); err == nil {
		if !info.Mode().IsRegular() {
			_ = temporary.Close()
			return fmt.Errorf("恢复目标不是普通文件")
		}
		mode = info.Mode().Perm()
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
	backup := target + ".scriptboard-restore-backup"
	hadTarget := false
	if _, err := os.Lstat(target); err == nil {
		_ = os.Remove(backup)
		if err := os.Rename(target, backup); err != nil {
			return err
		}
		hadTarget = true
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		if hadTarget {
			_ = os.Rename(backup, target)
		}
		return err
	}
	if hadTarget {
		_ = os.Remove(backup)
	}
	return m.Checkpoint("ScriptBoard restore file\n\nScriptBoard-Operation: restore\nScriptBoard-Path: " + slash)
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
		ignored := m.command("check-ignore", "-q", "--", slash).Run() == nil
		if !ignored {
			paths = append(paths, slash)
		}
		return nil
	})
	return paths, err
}

func (m *Manager) writeMandatoryExcludes() error {
	path := filepath.Join(m.root, ".git", "info", "exclude")
	content := "\n# ScriptBoard mandatory exclusions\n.scriptboard-trash/\n.scriptboard-upload-*\n"
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
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
	base := []string{"-C", m.root, "-c", "core.hooksPath=" + m.emptyHooks, "-c", "credential.helper=", "-c", "core.fsmonitor=false", "-c", "diff.external="}
	return exec.Command(m.gitExecutable, append(base, arguments...)...)
}

func (m *Manager) output(arguments ...string) (string, error) {
	output, err := m.command(arguments...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(arguments, " "), err, output)
	}
	return string(output), nil
}

func (m *Manager) raw(arguments ...string) (string, error) {
	output, err := exec.Command(m.gitExecutable, arguments...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(arguments, " "), err, output)
	}
	return string(output), nil
}
