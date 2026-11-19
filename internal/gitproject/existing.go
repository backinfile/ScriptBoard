package gitproject

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func (repository repo) existingRemote(ctx context.Context, checkout *Checkout) (string, error) {
	metadata, err := checkout.files.CanonicalDirectory(repository.dir)
	if err != nil {
		return "", errors.New("local directory must contain a standard Git repository")
	}
	config, err := checkout.files.CanonicalExisting(filepath.Join(metadata, "config"))
	if err != nil {
		return "", errors.New("local Git configuration is unavailable")
	}
	output, err := repository.command(ctx, false, "config", "--file", config, "--null", "--no-includes", "--list")
	if err != nil {
		return "", err
	}
	var origin string
	for _, entry := range strings.Split(output, "\x00") {
		if entry == "" {
			continue
		}
		key, value, _ := strings.Cut(entry, "\n")
		if !safeExistingConfig(key, value) {
			return "", errors.New("local repository uses Git configuration that cannot be run by ScriptBoard: " + key)
		}
		if key == "remote.origin.url" && origin == "" {
			origin = value
		}
	}
	for _, marker := range []string{"commondir", "gitdir", "config.worktree", "objects/info/alternates",
		"objects/info/http-alternates", "worktrees"} {
		if _, err := os.Lstat(filepath.Join(metadata, filepath.FromSlash(marker))); !errors.Is(err, os.ErrNotExist) {
			return "", errors.New("shared Git metadata is not supported")
		}
	}
	remote := repository.project.Remote
	if remote == "" {
		remote = origin
	}
	if err := ValidateRemote(remote, repository.project.SkipTLS); err != nil {
		return "", errors.New("configure a supported origin URL for this Git repository")
	}
	// Let Git validate ownership and repository discovery for an in-place
	// checkout. In particular, do not add safe.directory exceptions here.
	top, err := repository.command(ctx, true, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", errors.New("Git cannot open this checkout with the service identity")
	}
	canonical, err := checkout.files.CanonicalDirectory(strings.TrimSpace(top))
	if err != nil || !sameCheckoutLocation(canonical, repository.path) {
		return "", errors.New("registered directory is not the Git working tree root")
	}
	return remote, nil
}

func safeExistingConfig(key, value string) bool {
	switch key {
	case "core.repositoryformatversion":
		return value == "0" || value == "1"
	case "extensions.objectformat":
		return value == "sha1" || value == "sha256"
	case "core.bare":
		return value == "false" || value == "no" || value == "0"
	case "core.filemode", "core.logallrefupdates", "core.symlinks", "core.ignorecase", "core.autocrlf",
		"core.eol", "core.safecrlf", "core.precomposeunicode", "core.longpaths", "core.quotePath", "core.quotepath",
		"core.hookspath", "core.fsmonitor", "user.name", "user.email", "user.signingkey":
		return true
	}
	section, name, _ := strings.Cut(key, ".")
	suffix := name[strings.LastIndex(name, ".")+1:]
	switch section {
	case "branch":
		return suffix == "remote" || suffix == "merge" || suffix == "rebase" || suffix == "description"
	case "remote":
		return suffix == "url" || suffix == "fetch" || suffix == "pushurl" || suffix == "push"
	default:
		return false
	}
}
