package flowbuiltin

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"scriptboard/internal/processlaunch"
	"strconv"
	"strings"
	"time"
)

// Builtins execute under the Runner identity. Relative paths remain inside the
// card workspace after resolving existing symlinks; production does not enable absolute paths.

// BuiltinInput 是内置节点声明的一个 with 输入。
type BuiltinInput struct {
	Name     string
	Required bool
}

// BuiltinNodeSpec 是内置节点的校验视图。
type BuiltinNodeSpec struct {
	Inputs []BuiltinInput
	// AllowHeaderKeys 允许 header_<名> 形式的动态请求头输入（http-request）。
	AllowHeaderKeys bool
}

type builtinNode struct {
	spec BuiltinNodeSpec
	run  func(ctx context.Context, runner *BuiltinRunner, workspace string, with map[string]string) (string, error)
}

var builtinNodes = map[string]builtinNode{
	"git-sync":     {spec: BuiltinNodeSpec{Inputs: []BuiltinInput{{Name: "repo", Required: true}, {Name: "dest", Required: true}, {Name: "branch"}, {Name: "depth"}}}, run: runBuiltinGitSync},
	"copy-files":   {spec: BuiltinNodeSpec{Inputs: []BuiltinInput{{Name: "from", Required: true}, {Name: "to", Required: true}}}, run: runBuiltinCopyFiles},
	"write-file":   {spec: BuiltinNodeSpec{Inputs: []BuiltinInput{{Name: "path", Required: true}, {Name: "content", Required: true}, {Name: "append"}}}, run: runBuiltinWriteFile},
	"make-dir":     {spec: BuiltinNodeSpec{Inputs: []BuiltinInput{{Name: "path", Required: true}}}, run: runBuiltinMakeDir},
	"remove-files": {spec: BuiltinNodeSpec{Inputs: []BuiltinInput{{Name: "path", Required: true}}}, run: runBuiltinRemoveFiles},
	"http-request": {spec: BuiltinNodeSpec{Inputs: []BuiltinInput{{Name: "url", Required: true}, {Name: "method"}, {Name: "body"}, {Name: "insecure_skip_verify"}}, AllowHeaderKeys: true}, run: runBuiltinHTTPRequest},
	"sleep":        {spec: BuiltinNodeSpec{Inputs: []BuiltinInput{{Name: "seconds", Required: true}}}, run: runBuiltinSleep},
}

// LookupBuiltinNode 按名称查内置节点声明，供 flow 校验使用。
func LookupBuiltinNode(uses string) (BuiltinNodeSpec, bool) {
	node, ok := builtinNodes[uses]
	return node.spec, ok
}

// BuiltinRunnerOptions 注入内置节点执行依赖；WorkspaceRoot 下每卡片一个工作区。
type BuiltinRunnerOptions struct {
	RunCommand    func(context.Context, string, []string) ([]byte, error)
	WorkspaceRoot string
	ValidatePath  func(path string) error
	// HTTPClient 仅供测试替换；默认 30 秒超时的普通客户端。
	// 不复用 outboundpolicy：其仅放行 80/443 公网地址，内部自动化常需访问内网。
	HTTPClient *http.Client
}

type BuiltinRunner struct {
	runCommand    func(context.Context, string, []string) ([]byte, error)
	workspaceRoot string
	validatePath  func(path string) error
	httpClient    *http.Client
}

func NewBuiltinRunner(options BuiltinRunnerOptions) *BuiltinRunner {
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	runCommand := options.RunCommand
	if runCommand == nil {
		runCommand = defaultBuiltinCommand
	}
	return &BuiltinRunner{runCommand: runCommand, workspaceRoot: options.WorkspaceRoot, validatePath: options.ValidatePath, httpClient: client}
}

// Run 执行一个内置节点，返回简短结果说明（写入节点状态展示）。
func (r *BuiltinRunner) Run(ctx context.Context, cardID, uses string, with map[string]string) (string, error) {
	node, ok := builtinNodes[uses]
	if !ok {
		return "", fmt.Errorf("内置节点 %q 未注册", uses)
	}
	if r.workspaceRoot == "" {
		return "", errors.New("内置节点工作区未配置")
	}
	if cardID == "" || cardID == "." || cardID == ".." || strings.ContainsAny(cardID, "/\\") {
		return "", errors.New("invalid workspace ID")
	}
	workspace := filepath.Join(r.workspaceRoot, cardID)
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return "", fmt.Errorf("创建工作区失败：%w", err)
	}
	return node.run(ctx, r, workspace, with)
}

// resolvePath 把 with 中的路径解析为可操作的绝对路径。
func (r *BuiltinRunner) resolvePath(workspace, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("路径不能为空")
	}
	if filepath.IsAbs(raw) {
		if r.validatePath == nil {
			return "", errors.New("绝对路径校验未配置")
		}
		if err := r.validatePath(raw); err != nil {
			return "", err
		}
		return filepath.Clean(raw), nil
	}
	root, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", fmt.Errorf("解析工作区失败：%w", err)
	}
	target := resolveExistingAncestor(filepath.Join(root, raw))
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("路径 %q 逃逸出卡片工作区", raw)
	}
	return target, nil
}

// resolveExistingAncestor 从最近的已存在祖先解符号链接后再拼接剩余部分。
func resolveExistingAncestor(path string) string {
	current := path
	var rest []string
	for {
		if evaluated, err := filepath.EvalSymlinks(current); err == nil {
			for index := len(rest) - 1; index >= 0; index-- {
				evaluated = filepath.Join(evaluated, rest[index])
			}
			return evaluated
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path
		}
		rest = append(rest, filepath.Base(current))
		current = parent
	}
}

func runBuiltinGitSync(ctx context.Context, r *BuiltinRunner, workspace string, with map[string]string) (string, error) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return "", errors.New("未找到 git，请先安装")
	}
	repo := strings.TrimSpace(with["repo"])
	dest, err := r.resolvePath(workspace, with["dest"])
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(with["branch"])
	if strings.HasPrefix(branch, "-") {
		return "", errors.New("invalid git branch")
	}
	depth := strings.TrimSpace(with["depth"])
	if depth != "" {
		if n, convErr := strconv.Atoi(depth); convErr != nil || n <= 0 {
			return "", fmt.Errorf("depth %q 无效：需为正整数", depth)
		}
	}
	git := func(args ...string) error {
		output, runErr := r.runCommand(ctx, gitPath, args)
		if runErr != nil {
			return fmt.Errorf("git %s 失败：%v：%s", args[0], runErr, truncateBuiltinOutput(string(output)))
		}
		return nil
	}
	if _, statErr := os.Stat(dest); os.IsNotExist(statErr) {
		args := []string{"clone"}
		if branch != "" {
			args = append(args, "--branch", branch)
		}
		if depth != "" {
			args = append(args, "--depth", depth)
		}
		if err := git(append(args, "--", repo, dest)...); err != nil {
			return "", err
		}
	} else {
		// 已存在目录必须是 git 仓库，随后快进/重置到指定分支的远端 HEAD。
		if err := git("-C", dest, "rev-parse", "--git-dir"); err != nil {
			return "", fmt.Errorf("dest %q 已存在但不是 git 仓库", with["dest"])
		}
		if branch == "" {
			if err := git("-C", dest, "pull", "--ff-only"); err != nil {
				return "", err
			}
		} else {
			if err := git("-C", dest, "fetch", "origin", branch); err != nil {
				return "", err
			}
			if err := git("-C", dest, "checkout", branch); err != nil {
				return "", err
			}
			if err := git("-C", dest, "reset", "--hard", "origin/"+branch); err != nil {
				return "", err
			}
		}
	}
	revision, err := r.runCommand(ctx, gitPath, []string{"-C", dest, "rev-parse", "--short", "HEAD"})
	if err != nil {
		return "", err
	}
	return "synced " + strings.TrimSpace(string(revision)), nil
}

func runBuiltinCopyFiles(ctx context.Context, r *BuiltinRunner, workspace string, with map[string]string) (string, error) {
	from, err := r.resolvePath(workspace, with["from"])
	if err != nil {
		return "", err
	}
	to, err := r.resolvePath(workspace, with["to"])
	if err != nil {
		return "", err
	}
	info, err := os.Stat(from)
	if err != nil {
		return "", fmt.Errorf("源 %q 不存在：%w", with["from"], err)
	}
	if destInfo, statErr := os.Stat(to); statErr == nil && os.SameFile(info, destInfo) {
		return "", errors.New("源和目标不能相同")
	}
	if info.IsDir() {
		if rel, e := filepath.Rel(from, to); e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", errors.New("目标不能位于源目录内")
		}
	}
	copied := 0
	if !info.IsDir() {
		if err := copyBuiltinFile(from, to, info.Mode()); err != nil {
			return "", err
		}
		return "copied 1 file", nil
	}
	err = filepath.Walk(from, func(path string, entry os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, relative)
		// Recheck each destination child so an existing symlink cannot redirect a copy.
		resolved := resolveExistingAncestor(target)
		if rel, e := filepath.Rel(to, resolved); e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return errors.New("复制目标越出目标目录")
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !entry.Mode().IsRegular() {
			return nil
		}
		if err := copyBuiltinFile(path, target, entry.Mode()); err != nil {
			return err
		}
		copied++
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("copied %d files", copied), nil
}

func copyBuiltinFile(from, to string, mode os.FileMode) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	target, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(target, source)
	if closeErr := target.Close(); copyErr == nil {
		copyErr = closeErr
	}
	return copyErr
}

func runBuiltinWriteFile(_ context.Context, r *BuiltinRunner, workspace string, with map[string]string) (string, error) {
	target, err := r.resolvePath(workspace, with["path"])
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	flags := os.O_CREATE | os.O_TRUNC | os.O_WRONLY
	if with["append"] == "true" {
		flags = os.O_CREATE | os.O_APPEND | os.O_WRONLY
	}
	file, err := os.OpenFile(target, flags, 0o644)
	if err != nil {
		return "", err
	}
	_, writeErr := io.WriteString(file, with["content"])
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return "", writeErr
	}
	return fmt.Sprintf("wrote %d bytes", len([]byte(with["content"]))), nil
}

func runBuiltinMakeDir(_ context.Context, r *BuiltinRunner, workspace string, with map[string]string) (string, error) {
	target, err := r.resolvePath(workspace, with["path"])
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", err
	}
	return "created", nil
}

func runBuiltinRemoveFiles(_ context.Context, r *BuiltinRunner, workspace string, with map[string]string) (string, error) {
	target, err := r.resolvePath(workspace, with["path"])
	if err != nil {
		return "", err
	}
	// 不允许删除工作区根目录本身，避免 "." 清空整个工作区。
	if root, rootErr := filepath.EvalSymlinks(workspace); rootErr == nil && target == root {
		return "", errors.New("不允许删除卡片工作区根目录")
	}
	if err := os.RemoveAll(target); err != nil {
		return "", err
	}
	return "removed", nil
}

func runBuiltinHTTPRequest(ctx context.Context, r *BuiltinRunner, _ string, with map[string]string) (string, error) {
	method := strings.ToUpper(strings.TrimSpace(with["method"]))
	if method == "" {
		method = http.MethodGet
	}
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
	default:
		return "", fmt.Errorf("method %q 无效：仅支持 GET/POST/PUT/DELETE/PATCH", method)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimSpace(with["url"]), strings.NewReader(with["body"]))
	if err != nil {
		return "", err
	}
	for key, value := range with {
		if name, ok := strings.CutPrefix(key, "header_"); ok && name != "" {
			request.Header.Set(name, value)
		}
	}
	// TLS verification is disabled only for this explicitly opted-in request.
	client := *r.httpClient
	if raw := with["insecure_skip_verify"]; raw != "" && raw != "false" && raw != "true" {
		return "", errors.New("insecure_skip_verify must be true or false")
	}
	if with["insecure_skip_verify"] == "true" {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		if existing, ok := client.Transport.(*http.Transport); ok {
			transport = existing.Clone()
		}
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // Explicit operator choice for this node.
		defer transport.CloseIdleConnections()
		client.Transport = transport
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	// 响应体不展示，仅排空以复用连接。
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return "", fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return fmt.Sprintf("HTTP %d", response.StatusCode), nil
}

func runBuiltinSleep(ctx context.Context, _ *BuiltinRunner, _ string, with map[string]string) (string, error) {
	seconds, err := strconv.Atoi(strings.TrimSpace(with["seconds"]))
	if err != nil || seconds < 0 || seconds > 300 {
		return "", fmt.Errorf("seconds %q 无效：需为 0-300 的数字", with["seconds"])
	}
	timer := time.NewTimer(time.Duration(seconds) * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
	}
	return fmt.Sprintf("slept %ds", seconds), nil
}

// truncateBuiltinOutput 把命令输出截到 200 字符，避免状态消息过长。
func truncateBuiltinOutput(output string) string {
	runes := []rune(strings.TrimSpace(output))
	if len(runes) > 200 {
		runes = runes[:200]
	}
	return string(runes)
}

// Git output is bounded before buffering, including failures from remote helpers.
type boundedBuiltinOutput struct{ text string }

func (b *boundedBuiltinOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 4096 - len(b.text)
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		b.text += string(p)
	}
	return n, nil
}

func defaultBuiltinCommand(ctx context.Context, path string, args []string) ([]byte, error) {
	command, err := processlaunch.Prepare(processlaunch.Spec{Context: ctx, Executable: path, Arguments: args, Environment: processlaunch.EnvironmentInherit})
	if err != nil {
		return nil, err
	}
	var output boundedBuiltinOutput
	command.Stdout, command.Stderr = &output, &output
	err = command.Run()
	return []byte(output.text), err
}
