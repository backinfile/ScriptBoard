// Package gitproject manages the working copies registered in ScriptBoard.
package gitproject

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"scriptboard/internal/hostfiles"
	"scriptboard/internal/privatepath"
	"scriptboard/internal/processlaunch"
)

type Action string

const (
	Inspect  Action = "inspect"
	Overview Action = "overview"
	Sync     Action = "sync"
	Switch   Action = "switch"
	Discard  Action = "discard"
	Force    Action = "force"

	UnclonedRevision = "uncloned"
)

type Project struct {
	ID, Name, Remote, Directory, PathKey, Branch string
	SkipTLS, Existing                            bool
}

type Branch struct {
	Name   string
	Local  bool
	Remote bool
}

type Change struct {
	Path string
	Code string
}

type Status struct {
	Cloned                           bool
	Branch, Commit, Upstream, Remote string
	Ahead, Behind, Tracked           int
	Untracked                        int
	Changes                          []Change
	Branches                         []Branch
	Revision                         string
}

type Request struct {
	ProjectID string `json:"project_id"`
	QuickID   string `json:"quick_id,omitempty"`
	Action    Action `json:"action"`
	Branch    string `json:"branch,omitempty"`
	Revision  string `json:"revision,omitempty"`
}

type Result struct {
	Before Status
	After  Status
}

const (
	maxGitOutput = 4 << 20
	changeLimit  = 60
)

var (
	idPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)
	sshUser    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
	scpRemote  = regexp.MustCompile(`^(?:[A-Za-z0-9_][A-Za-z0-9_.-]*@)?[A-Za-z0-9][A-Za-z0-9.-]*:[^[:space:]]+$`)
	ErrChanged = errors.New("Git worktree changed after confirmation")
	ErrNoGit   = errors.New("Git is unavailable on this host")
)

func ValidID(value string) bool { return idPattern.MatchString(value) }

func ValidBranch(value string) bool {
	return value != "" && len(value) <= 255 && !strings.HasPrefix(value, "-") && !strings.HasPrefix(value, "/") &&
		!strings.HasSuffix(value, ".") && !strings.Contains(value, "..") && !strings.Contains(value, "@{") &&
		value != "@" && value != "HEAD" && strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune(`~^:?*[\`, r)
	}) < 0 && validRefComponents(value)
}

func validRefComponents(value string) bool {
	for _, part := range strings.Split(value, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

func ValidateRemote(remote string, skipTLS bool) error {
	if len(remote) > 2048 || strings.IndexFunc(remote, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 {
		return errors.New("invalid Git remote URL")
	}
	if !strings.Contains(remote, "://") && scpRemote.MatchString(remote) && !filepath.IsAbs(remote) {
		if skipTLS {
			return errors.New("skipping TLS verification is only available for HTTPS")
		}
		return nil
	}
	parsed, err := url.Parse(remote)
	if err != nil || parsed.Hostname() == "" || strings.HasPrefix(parsed.Hostname(), "-") || parsed.Path == "" ||
		parsed.Path == "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("invalid Git remote URL")
	}
	switch parsed.Scheme {
	case "http", "https", "git":
		if parsed.User != nil {
			return errors.New("Git remote URLs cannot contain credentials")
		}
	case "ssh":
		if parsed.User != nil {
			_, password := parsed.User.Password()
			if password || !sshUser.MatchString(parsed.User.Username()) {
				return errors.New("invalid SSH user in Git remote URL")
			}
		}
	default:
		return errors.New("use an HTTPS, HTTP, SSH or Git remote URL")
	}
	if skipTLS && parsed.Scheme != "https" {
		return errors.New("skipping TLS verification is only available for HTTPS")
	}
	return nil
}

func ValidateRequest(request Request) bool {
	if !ValidID(request.ProjectID) {
		return false
	}
	switch request.Action {
	case Inspect, Overview, Sync:
		return request.Branch == "" && request.Revision == "" && (request.QuickID == "" || request.Action == Sync && ValidID(request.QuickID))
	case Switch:
		return ValidBranch(request.Branch) && request.QuickID == "" && request.Revision == ""
	case Discard, Force:
		_, err := hex.DecodeString(request.Revision)
		validRevision := len(request.Revision) == sha256.Size*2 && err == nil || request.Action == Force && request.Revision == UnclonedRevision
		return validRevision && request.QuickID == "" && request.Branch == ""
	default:
		return false
	}
}

type Checkout struct {
	files  *hostfiles.Manager
	root   string
	leases atomic.Uint64
}

func NewCheckout(files *hostfiles.Manager, stateRoot string) *Checkout {
	return &Checkout{files: files, root: filepath.Join(stateRoot, "git-projects")}
}

// Apply uses a project-specific lease shared with host file operations.
func (checkout *Checkout) Apply(ctx context.Context, project Project, request Request) (Result, error) {
	if project.ID != request.ProjectID || !ValidateRequest(request) {
		return Result{}, errors.New("invalid Git project operation")
	}
	if !project.Existing {
		if err := ValidateRemote(project.Remote, project.SkipTLS); err != nil {
			return Result{}, err
		}
	}
	if project.Branch != "" && !ValidBranch(project.Branch) {
		return Result{}, errors.New("invalid Git branch")
	}
	lease := "git:" + project.ID
	// Identical run leases are intentionally shareable. Git requests each
	// need their own lease ID, including simultaneous requests for one project.
	lease += ":" + strconv.FormatUint(checkout.leases.Add(1), 36)
	if err := checkout.files.AcquireLease(lease, project.Directory); err != nil {
		return Result{}, err
	}
	defer checkout.files.ReleaseLease(lease)

	target, err := checkout.files.CanonicalDestination(project.Directory)
	if err != nil {
		return Result{}, err
	}
	if hostfiles.ComparisonKey(target) != project.PathKey {
		return Result{}, errors.New("Git destination no longer matches the registered directory")
	}
	repository := repo{path: target, dir: filepath.Join(checkout.root, project.ID), project: project, cwd: filepath.Dir(checkout.root)}
	if project.Existing {
		repository.dir = filepath.Join(target, ".git")
		remote, err := repository.existingRemote(ctx, checkout)
		if err != nil {
			return Result{}, err
		}
		repository.project.Remote = remote
	}
	_, err = os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		if request.Action == Inspect || request.Action == Overview {
			return Result{After: Status{Branch: project.Branch}}, nil
		}
		if request.Action != Sync && !(request.Action == Force && request.Revision == UnclonedRevision) {
			return Result{}, errors.New("clone this Git project first")
		}
		if err := checkout.clone(ctx, repository); err != nil {
			return Result{}, err
		}
		after, err := repository.status(ctx, false)
		return Result{After: after}, err
	} else if err != nil {
		return Result{}, err
	}
	if request.Revision == UnclonedRevision {
		return Result{}, ErrChanged
	}
	if !project.Existing {
		if err := checkout.validateCheckout(repository); err != nil {
			return Result{}, err
		}
	}
	before, err := repository.status(ctx, request.Action == Inspect || request.Action == Force || request.Action == Discard)
	if err != nil {
		return Result{}, err
	}
	if request.Action == Inspect || request.Action == Overview {
		return Result{After: before}, nil
	}
	if request.Revision != "" && request.Revision != before.Revision {
		return Result{Before: before, After: before}, ErrChanged
	}
	if request.Action == Sync || request.Action == Switch || request.Action == Force {
		if err := repository.change(ctx, request, before); err != nil {
			return Result{Before: before, After: before}, err
		}
	}
	if request.Action == Discard {
		if err := repository.restore(ctx, before.Commit); err != nil {
			return Result{Before: before, After: before}, err
		}
	}
	after, err := repository.status(ctx, false)
	return Result{Before: before, After: after}, err
}

func (checkout *Checkout) validateCheckout(repository repo) error {
	if _, err := checkout.files.CanonicalDirectory(repository.path); err != nil {
		return err
	}
	markerPath, err := checkout.files.CanonicalExisting(filepath.Join(repository.path, ".git"))
	if err != nil {
		return errors.New("destination is not a ScriptBoard Git checkout")
	}
	file, err := os.Open(markerPath)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return errors.New("invalid Git checkout marker")
	}
	marker, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return err
	}
	target, ok := strings.CutPrefix(strings.TrimSpace(string(marker)), "gitdir: ")
	if !ok || !filepath.IsAbs(target) || hostfiles.ComparisonKey(target) != hostfiles.ComparisonKey(repository.dir) {
		return errors.New("Git checkout metadata is outside its managed location")
	}
	return nil
}

func (checkout *Checkout) clone(ctx context.Context, repository repo) error {
	if err := os.MkdirAll(checkout.root, 0700); err != nil {
		return err
	}
	if err := privatepath.ProtectDirectory(checkout.root); err != nil {
		return err
	}
	// Reserve a private sibling of the destination so checkout can be
	// published without replacing an entry or crossing a filesystem.
	temp, err := os.MkdirTemp(filepath.Dir(repository.path), ".scriptboard-git-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	if _, err := os.Lstat(repository.dir); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return err
		}
		return errors.New("Git project metadata already exists")
	}
	completed := false
	defer func() {
		if !completed {
			os.RemoveAll(repository.dir)
		}
	}()
	args := []string{"clone", "--origin", "origin", "--no-tags", "--no-recurse-submodules", "--template=", "--separate-git-dir", repository.dir}
	if repository.project.Branch != "" {
		args = append(args, "--branch", repository.project.Branch)
	}
	if _, err := repository.command(ctx, false, append(args, "--", repository.project.Remote, temp)...); err != nil {
		return err
	}
	stagedRepo := repository
	stagedRepo.path = temp
	if _, err := stagedRepo.commit(ctx, "HEAD"); err != nil {
		return errors.New("remote repository has no commit to check out")
	}
	if _, err := checkout.files.CanonicalDestination(repository.path); err != nil {
		return err
	}
	if err := publishDirectory(temp, repository.path); err != nil {
		return fmt.Errorf("publish Git checkout: %w", err)
	}
	completed = true
	return nil
}

type repo struct {
	path, dir, cwd string
	project        Project
}

func gitEnvironment() []string {
	allowed := []string{"PATH", "HOME", "USERPROFILE", "SystemRoot", "WINDIR", "TEMP", "TMP", "TMPDIR", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA", "SSH_AUTH_SOCK", "GIT_SSL_CAINFO"}
	env := make([]string, 0, len(allowed)+8)
	for _, key := range allowed {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0", "GIT_SSH_COMMAND=ssh -oBatchMode=yes -oStrictHostKeyChecking=yes", "LC_ALL=C")
}

type cappedBuffer struct {
	bytes.Buffer
}

func (buffer *cappedBuffer) Write(data []byte) (int, error) {
	if len(data) > maxGitOutput-buffer.Len() {
		return 0, errors.New("Git output exceeds the supported size")
	}
	return buffer.Buffer.Write(data)
}

func (repository repo) command(ctx context.Context, worktree bool, args ...string) (string, error) {
	var output cappedBuffer
	if err := repository.run(ctx, worktree, &output, args...); err != nil {
		return "", err
	}
	return output.String(), nil
}

func (repository repo) run(ctx context.Context, worktree bool, output io.Writer, args ...string) error {
	git, err := exec.LookPath("git")
	if err != nil {
		return ErrNoGit
	}
	options := []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false", "-c", "protocol.allow=never",
		"-c", "protocol.http.allow=always", "-c", "protocol.https.allow=always", "-c", "protocol.ssh.allow=always", "-c", "protocol.git.allow=always",
		"-c", "credential.helper=", "-c", "fetch.recurseSubmodules=false", "-c", "submodule.recurse=false",
		"-c", "gc.auto=0", "-c", "maintenance.auto=false",
		"-c", "http.sslVerify=" + strconv.FormatBool(!repository.project.SkipTLS)}
	if worktree {
		if repository.project.Existing {
			options = append(options, "-C", repository.path)
		} else {
			options = append(options, "--git-dir="+repository.dir, "--work-tree="+repository.path)
		}
	}
	command, err := processlaunch.Prepare(processlaunch.Spec{
		Context: ctx, Executable: git, Arguments: append(options, args...), Environment: processlaunch.EnvironmentExact,
		Env: gitEnvironment(), Directory: repository.cwd,
	})
	if err != nil {
		return err
	}
	command.Stdout = output
	command.WaitDelay = 2 * time.Second
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("Git %s failed: %w", args[0], err)
	}
	return nil
}
