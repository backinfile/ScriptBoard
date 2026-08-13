package uploadinbox

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"scriptboard/internal/hostfiles"
)

const (
	payloadName           = "payload"
	metadataName          = "metadata"
	publicationIntentName = "publication-intent"
)

type Store struct{ root string }

type Claim struct {
	store     *Store
	id        string
	directory string
	finished  bool
	sealed    bool
}

type Input struct {
	EntryID, OriginalName, TargetDirectory, ConflictPolicy string
}

type Pending struct {
	ID, EntryID, OriginalName, TargetDirectory, ConflictPolicy string
	SHA256                                                     string
	Size                                                       int64
	CreatedAt                                                  time.Time
}

func New(root string) (*Store, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create upload inbox: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, fmt.Errorf("secure upload inbox: %w", err)
	}
	store := &Store{root: root}
	if err := store.recoverClaims(); err != nil {
		return nil, err
	}
	return store, nil
}

func (store *Store) Receive(input Input, source io.Reader, maximum int64) (Pending, error) {
	if err := validateInput(input); err != nil {
		return Pending{}, err
	}
	if maximum <= 0 {
		return Pending{}, errors.New("upload maximum must be positive")
	}
	id, err := randomID()
	if err != nil {
		return Pending{}, err
	}
	temporary, err := os.MkdirTemp(store.root, ".incoming-")
	if err != nil {
		return Pending{}, fmt.Errorf("create inbox transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(temporary)
		}
	}()
	payload, err := os.OpenFile(filepath.Join(temporary, payloadName), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Pending{}, err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(payload, hash), io.LimitReader(source, maximum+1))
	closeErr := payload.Close()
	if copyErr != nil || closeErr != nil {
		return Pending{}, errors.Join(copyErr, closeErr)
	}
	if written > maximum {
		return Pending{}, errors.New("upload exceeds configured maximum")
	}
	pending := Pending{
		ID: id, EntryID: input.EntryID, OriginalName: input.OriginalName,
		TargetDirectory: input.TargetDirectory, ConflictPolicy: input.ConflictPolicy,
		SHA256: hex.EncodeToString(hash.Sum(nil)), Size: written, CreatedAt: time.Now().UTC(),
	}
	metadata, err := json.Marshal(pending)
	if err != nil {
		return Pending{}, err
	}
	if err := os.WriteFile(filepath.Join(temporary, metadataName), metadata, 0o600); err != nil {
		return Pending{}, err
	}
	if err := os.Rename(temporary, filepath.Join(store.root, id)); err != nil {
		return Pending{}, fmt.Errorf("commit inbox upload: %w", err)
	}
	committed = true
	return pending, nil
}

func (store *Store) Open(id string) (Pending, *os.File, error) {
	if !validID(id) {
		return Pending{}, nil, errors.New("invalid inbox identifier")
	}
	directory := filepath.Join(store.root, id)
	metadata, err := os.ReadFile(filepath.Join(directory, metadataName))
	if err != nil {
		return Pending{}, nil, err
	}
	var pending Pending
	if err := json.Unmarshal(metadata, &pending); err != nil || pending.ID != id {
		return Pending{}, nil, errors.New("invalid inbox metadata")
	}
	if err := validateInput(Input{EntryID: pending.EntryID, OriginalName: pending.OriginalName, TargetDirectory: pending.TargetDirectory, ConflictPolicy: pending.ConflictPolicy}); err != nil {
		return Pending{}, nil, err
	}
	payload, err := os.Open(filepath.Join(directory, payloadName))
	if err != nil {
		return Pending{}, nil, err
	}
	return pending, payload, nil
}

func (store *Store) Claim(id string) (Pending, *os.File, *Claim, error) {
	if !validID(id) {
		return Pending{}, nil, nil, errors.New("invalid inbox identifier")
	}
	directory := filepath.Join(store.root, id)
	claimed := filepath.Join(store.root, ".publishing-"+id)
	if err := os.Rename(directory, claimed); err != nil {
		return Pending{}, nil, nil, err
	}
	claim := &Claim{store: store, id: id, directory: claimed}
	pending, payload, err := readPending(claimed, id)
	if err != nil {
		_ = claim.Release()
		return Pending{}, nil, nil, err
	}
	return pending, payload, claim, nil
}

func (claim *Claim) Release() error {
	if claim == nil || claim.finished {
		return nil
	}
	if claim.sealed {
		return errors.New("publication has started; claim cannot be released")
	}
	err := os.Rename(claim.directory, filepath.Join(claim.store.root, claim.id))
	if err == nil {
		claim.finished = true
	}
	return err
}

// BeginPublication persists the point after which recovery must not make the
// upload available for a second publication. Call CancelPublication when the
// atomic target publication fails before committing a target file.
func (claim *Claim) BeginPublication() error {
	if claim == nil || claim.finished || claim.sealed {
		return errors.New("claim is not available for publication")
	}
	marker := filepath.Join(claim.directory, publicationIntentName)
	file, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("persist publication intent: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(marker)
		return fmt.Errorf("persist publication intent: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(marker)
		return fmt.Errorf("persist publication intent: %w", err)
	}
	claim.sealed = true
	return nil
}

func (claim *Claim) CancelPublication() error {
	if claim == nil || claim.finished || !claim.sealed {
		return errors.New("publication has not started")
	}
	if err := os.Remove(filepath.Join(claim.directory, publicationIntentName)); err != nil {
		return fmt.Errorf("cancel publication intent: %w", err)
	}
	claim.sealed = false
	return claim.Release()
}

func (claim *Claim) Complete() error {
	if claim == nil || claim.finished {
		return nil
	}
	if claim.sealed {
		// Once target publication has succeeded, never release this claim even
		// when best-effort cleanup fails. Recovery recognizes the marker.
		claim.finished = true
		return os.RemoveAll(claim.directory)
	}
	err := os.RemoveAll(claim.directory)
	if err == nil {
		claim.finished = true
	}
	return err
}

func (store *Store) List() ([]Pending, error) {
	entries, err := os.ReadDir(store.root)
	if err != nil {
		return nil, err
	}
	result := make([]Pending, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !validID(entry.Name()) {
			continue
		}
		metadata, err := os.ReadFile(filepath.Join(store.root, entry.Name(), metadataName))
		if err != nil {
			return nil, err
		}
		var pending Pending
		if err := json.Unmarshal(metadata, &pending); err != nil || pending.ID != entry.Name() {
			return nil, errors.New("invalid inbox metadata")
		}
		result = append(result, pending)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].CreatedAt.After(result[right].CreatedAt) })
	return result, nil
}

func (store *Store) Remove(id string) error {
	if !validID(id) {
		return errors.New("invalid inbox identifier")
	}
	return os.RemoveAll(filepath.Join(store.root, id))
}

func (store *Store) recoverClaims() error {
	entries, err := os.ReadDir(store.root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), ".publishing-") {
			continue
		}
		id := strings.TrimPrefix(entry.Name(), ".publishing-")
		if !validID(id) {
			continue
		}
		directory := filepath.Join(store.root, entry.Name())
		if _, err := os.Stat(filepath.Join(directory, publicationIntentName)); err == nil {
			// The process may have crashed immediately before or after the atomic
			// target publish. Removing the inbox copy is fail-closed: it prevents
			// an operator from unknowingly publishing the same payload twice.
			if err := os.RemoveAll(directory); err != nil {
				return fmt.Errorf("remove sealed inbox upload: %w", err)
			}
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect claimed inbox upload: %w", err)
		}
		if err := os.Rename(directory, filepath.Join(store.root, id)); err != nil {
			return fmt.Errorf("recover claimed inbox upload: %w", err)
		}
	}
	return nil
}

func readPending(directory, id string) (Pending, *os.File, error) {
	metadata, err := os.ReadFile(filepath.Join(directory, metadataName))
	if err != nil {
		return Pending{}, nil, err
	}
	var pending Pending
	if err := json.Unmarshal(metadata, &pending); err != nil || pending.ID != id {
		return Pending{}, nil, errors.New("invalid inbox metadata")
	}
	payload, err := os.Open(filepath.Join(directory, payloadName))
	if err != nil {
		return Pending{}, nil, err
	}
	return pending, payload, nil
}

func validateInput(input Input) error {
	if input.EntryID == "" || input.OriginalName == "" || input.TargetDirectory == "" ||
		filepath.Base(input.OriginalName) != input.OriginalName || strings.ContainsAny(input.OriginalName, `/\\`) ||
		(input.ConflictPolicy != "reject" && input.ConflictPolicy != "rename") {
		return errors.New("invalid inbox metadata")
	}
	if err := hostfiles.ValidateName(input.OriginalName); err != nil {
		return errors.New("invalid inbox filename")
	}
	for _, value := range []string{input.EntryID, input.OriginalName, input.TargetDirectory} {
		if strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return errors.New("inbox metadata contains control characters")
		}
	}
	return nil
}

func randomID() (string, error) {
	value := make([]byte, 18)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func validID(value string) bool {
	if len(value) != 24 {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil
}
