package gitproject

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const remotePrefix = "refs/remotes/origin/"

func (repository repo) status(ctx context.Context, detailed bool) (Status, error) {
	commit, err := repository.commit(ctx, "HEAD")
	if err != nil {
		return Status{}, err
	}
	branch, _ := repository.command(ctx, true, "symbolic-ref", "--quiet", "--short", "HEAD")
	state := Status{Cloned: true, Commit: commit, Branch: strings.TrimSpace(branch), Remote: repository.project.Remote}
	lines, err := repository.command(ctx, true, "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err != nil {
		return Status{}, err
	}
	for index, entries := 0, strings.Split(lines, "\x00"); index < len(entries); index++ {
		entry := entries[index]
		if len(entry) < 4 || entry[2] != ' ' {
			continue
		}
		code := entry[:2]
		if code == "??" {
			state.Untracked++
		} else {
			state.Tracked++
		}
		if detailed && len(state.Changes) < changeLimit {
			state.Changes = append(state.Changes, Change{Code: code, Path: entry[3:]})
		}
		if strings.ContainsAny(code, "RC") {
			index++
		}
	}
	upstream, err := repository.upstream(ctx)
	if err == nil {
		state.Upstream = strings.TrimPrefix(upstream, "refs/remotes/")
		state.Ahead, state.Behind, err = repository.distance(ctx, upstream)
		if err != nil {
			return Status{}, err
		}
	}
	if !detailed {
		return state, nil
	}
	state.Revision, err = repository.revision(ctx, state.Branch, commit, lines)
	if err != nil {
		return Status{}, err
	}
	state.Branches, err = repository.branches(ctx)
	return state, err
}

func (repository repo) revision(ctx context.Context, branch, commit, lines string) (string, error) {
	digest := sha256.New()
	fmt.Fprintf(digest, "%s\x00%s\x00%s", branch, commit, lines)
	// Porcelain identifies paths, but the same paths can change between
	// confirmation and execution without changing the porcelain listing.
	for _, flags := range [][]string{nil, {"--cached"}} {
		_, _ = digest.Write([]byte{0})
		args := []string{"diff", "--no-ext-diff", "--no-textconv", "--no-color", "--binary"}
		if err := repository.run(ctx, true, digest, append(args, flags...)...); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func (repository repo) branches(ctx context.Context) ([]Branch, error) {
	output, err := repository.command(ctx, true, "for-each-ref", "--format=%(refname)", "refs/heads/", remotePrefix)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]Branch)
	for _, reference := range strings.Fields(output) {
		name, local := strings.CutPrefix(reference, "refs/heads/")
		if !local {
			name = strings.TrimPrefix(reference, remotePrefix)
		}
		if !ValidBranch(name) {
			continue
		}
		branch := byName[name]
		branch.Name, branch.Local, branch.Remote = name, branch.Local || local, branch.Remote || !local
		byName[name] = branch
	}
	branches := make([]Branch, 0, len(byName))
	for _, branch := range byName {
		branches = append(branches, branch)
	}
	sort.Slice(branches, func(i, j int) bool { return branches[i].Name < branches[j].Name })
	return branches, nil
}

func (repository repo) commit(ctx context.Context, ref string) (string, error) {
	output, err := repository.command(ctx, true, "rev-parse", "--verify", ref+"^{commit}")
	return strings.TrimSpace(output), err
}

func (repository repo) upstream(ctx context.Context) (string, error) {
	output, err := repository.command(ctx, true, "rev-parse", "--symbolic-full-name", "@{upstream}")
	output = strings.TrimSpace(output)
	if err != nil || !strings.HasPrefix(output, remotePrefix) || !ValidBranch(strings.TrimPrefix(output, remotePrefix)) {
		return "", errors.New("current branch has no upstream on origin")
	}
	return output, nil
}

func (repository repo) distance(ctx context.Context, ref string) (ahead, behind int, err error) {
	output, err := repository.command(ctx, true, "rev-list", "--left-right", "--count", "HEAD..."+ref, "--")
	if err == nil {
		_, err = fmt.Sscan(output, &ahead, &behind)
	}
	return ahead, behind, err
}

func (repository repo) ready() error {
	for _, marker := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "sequencer", "BISECT_LOG"} {
		if _, err := os.Lstat(filepath.Join(repository.dir, marker)); !errors.Is(err, os.ErrNotExist) {
			return errors.New("another Git operation is in progress")
		}
	}
	return nil
}

func (repository repo) change(ctx context.Context, request Request, before Status) error {
	if err := repository.ready(); err != nil {
		return err
	}
	if request.Action == Switch {
		return repository.switchBranch(ctx, request.Branch)
	}
	if before.Branch == "" {
		return errors.New("switch to a local branch before updating")
	}
	if request.Action == Sync && before.Tracked != 0 {
		return errors.New("working tree has uncommitted changes")
	}
	if _, err := repository.command(ctx, true, "fetch", "--prune", "--no-tags", "--no-recurse-submodules", "--", repository.project.Remote, "+refs/heads/*:refs/remotes/origin/*"); err != nil {
		return err
	}
	upstream, err := repository.upstream(ctx)
	if err != nil {
		return err
	}
	target, err := repository.commit(ctx, upstream)
	if err != nil {
		return err
	}
	ahead, behind, err := repository.distance(ctx, target)
	if err != nil {
		return err
	}
	if request.Action == Force {
		observed, err := repository.status(ctx, true)
		if err != nil {
			return err
		}
		if observed.Revision != before.Revision || ahead > before.Ahead {
			return ErrChanged
		}
		return repository.restore(ctx, target)
	}
	if ahead > 0 && behind > 0 {
		return errors.New("local and upstream branches have diverged")
	}
	if behind == 0 {
		return nil
	}
	_, err = repository.command(ctx, true, "merge", "--ff-only", "--no-autostash", "--no-overwrite-ignore", target, "--")
	return err
}

func (repository repo) switchBranch(ctx context.Context, branch string) error {
	local, localErr := repository.commit(ctx, "refs/heads/"+branch)
	target := local
	if localErr != nil {
		remote, err := repository.commit(ctx, remotePrefix+branch)
		if err != nil {
			return errors.New("target Git branch does not exist")
		}
		target = remote
	}
	if err := repository.checkUntracked(ctx, target); err != nil {
		return err
	}
	if localErr != nil {
		_, err := repository.command(ctx, true, "checkout", "--no-overwrite-ignore", "--no-recurse-submodules", "-b", branch, "--track", remotePrefix+branch, "--")
		return err
	}
	_, err := repository.command(ctx, true, "checkout", "--no-overwrite-ignore", "--no-recurse-submodules", branch, "--")
	return err
}

func (repository repo) restore(ctx context.Context, target string) error {
	if err := repository.ready(); err != nil {
		return err
	}
	if err := repository.checkUntracked(ctx, target); err != nil {
		return err
	}
	_, err := repository.command(ctx, true, "reset", "--hard", "--no-recurse-submodules", target, "--")
	return err
}

func (repository repo) checkUntracked(ctx context.Context, target string) error {
	// Omit --exclude-standard so ignored paths are covered as well.
	untracked, err := repository.command(ctx, true, "ls-files", "--others", "-z")
	if err != nil || untracked == "" {
		return err
	}
	incoming, err := repository.command(ctx, true, "ls-tree", "-r", "-z", "--name-only", target, "--")
	if err != nil {
		return err
	}
	if name := conflictingPath(untracked, incoming); name != "" {
		return fmt.Errorf("incoming files would overwrite untracked path %q", name)
	}
	return nil
}

func conflictingPath(untracked, incoming string) string {
	files, directories := make(map[string]bool), make(map[string]bool)
	key := func(value string) string {
		value = strings.TrimSuffix(value, "/")
		if runtime.GOOS == "windows" {
			return strings.ToLower(value)
		}
		return value
	}
	for _, name := range strings.Split(incoming, "\x00") {
		if name == "" {
			continue
		}
		name = key(name)
		files[name] = true
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			directories[parent] = true
		}
	}
	for _, name := range strings.Split(untracked, "\x00") {
		normalized := key(name)
		if files[normalized] || directories[normalized] {
			return name
		}
		for parent := path.Dir(normalized); parent != "."; parent = path.Dir(parent) {
			if files[parent] {
				return name
			}
		}
	}
	return ""
}
