package externalapproval

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"scriptboard/internal/privatepath"
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

type Store struct{ root string }

type Staged struct {
	SHA256 string
	Size   int64
}

type Claim struct {
	path     string
	finished bool
}

func New(root string) (*Store, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("create External Interface approval staging: %w", err)
	}
	if err := privatepath.ProtectDirectory(absolute); err != nil {
		return nil, fmt.Errorf("protect External Interface approval staging: %w", err)
	}
	store := &Store{root: absolute}
	entries, err := os.ReadDir(absolute)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() && (len(entry.Name()) > len(".processing-") && entry.Name()[:len(".processing-")] == ".processing-" || len(entry.Name()) > len(".incoming-") && entry.Name()[:len(".incoming-")] == ".incoming-") {
			// A processing payload may already have produced a host-side effect; discard it on recovery to prevent duplicate execution.
			if err := os.RemoveAll(filepath.Join(absolute, entry.Name())); err != nil {
				return nil, err
			}
		}
	}
	return store, nil
}

// Retain removes payloads without a matching pending database row. This closes
// the crash window between the durable approval claim and the payload claim.
func (store *Store) Retain(pending map[string]bool) error {
	entries, err := os.ReadDir(store.root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !identifierPattern.MatchString(entry.Name()) || pending[entry.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(store.root, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) Stage(id string, source io.Reader, maximum int64) (Staged, error) {
	if !identifierPattern.MatchString(id) || maximum <= 0 {
		return Staged{}, errors.New("invalid approval upload")
	}
	temporary, err := os.MkdirTemp(store.root, ".incoming-")
	if err != nil {
		return Staged{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(temporary)
		}
	}()
	payload, err := os.OpenFile(filepath.Join(temporary, "payload"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Staged{}, err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(payload, hash), io.LimitReader(source, maximum+1))
	closeErr := payload.Close()
	if copyErr != nil || closeErr != nil {
		return Staged{}, errors.Join(copyErr, closeErr)
	}
	if written > maximum {
		return Staged{}, errors.New("approval upload exceeds configured maximum")
	}
	if err := os.Rename(temporary, filepath.Join(store.root, id)); err != nil {
		return Staged{}, err
	}
	committed = true
	return Staged{SHA256: hex.EncodeToString(hash.Sum(nil)), Size: written}, nil
}

func (store *Store) Claim(id string) (*os.File, *Claim, error) {
	if !identifierPattern.MatchString(id) {
		return nil, nil, errors.New("invalid approval identifier")
	}
	claimed := filepath.Join(store.root, ".processing-"+id)
	if err := os.Rename(filepath.Join(store.root, id), claimed); err != nil {
		return nil, nil, err
	}
	payload, err := os.Open(filepath.Join(claimed, "payload"))
	if err != nil {
		_ = os.RemoveAll(claimed)
		return nil, nil, err
	}
	return payload, &Claim{path: claimed}, nil
}

// Preview reads a bounded prefix without moving the payload into processing.
// Approval review must never claim or mutate the staged upload.
func (store *Store) Preview(id string, maximum int64) ([]byte, bool, error) {
	if !identifierPattern.MatchString(id) || maximum <= 0 {
		return nil, false, errors.New("invalid approval preview")
	}
	payload, err := os.Open(filepath.Join(store.root, id, "payload"))
	if err != nil {
		return nil, false, err
	}
	defer payload.Close()
	content, err := io.ReadAll(io.LimitReader(payload, maximum+1))
	if err != nil {
		return nil, false, err
	}
	truncated := int64(len(content)) > maximum
	if truncated {
		content = content[:maximum]
	}
	return content, truncated, nil
}

func (store *Store) Remove(id string) error {
	if !identifierPattern.MatchString(id) {
		return errors.New("invalid approval identifier")
	}
	return os.RemoveAll(filepath.Join(store.root, id))
}

func (claim *Claim) Complete() error {
	if claim == nil || claim.finished {
		return nil
	}
	claim.finished = true
	return os.RemoveAll(claim.path)
}
