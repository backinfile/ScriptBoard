package privilegebroker

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"scriptboard/internal/externaltrigger"
	"scriptboard/internal/hostfiles"
	"scriptboard/internal/logstream"
)

const (
	hostFilesPageSize  = 1000
	hostFilesHandleTTL = 2 * time.Minute
)

var ErrHostFilesUnavailable = errors.New("privileged Broker Host Files service is unavailable")

type hostFilesCanonicalKind string

const (
	hostFilesCanonicalExisting    hostFilesCanonicalKind = "existing"
	hostFilesCanonicalDirectory   hostFilesCanonicalKind = "directory"
	hostFilesCanonicalDestination hostFilesCanonicalKind = "destination"
	hostFilesCanonicalChild       hostFilesCanonicalKind = "child"
)

type HostFileInfo struct {
	Name       string      `json:"name"`
	Size       int64       `json:"size"`
	Mode       os.FileMode `json:"mode"`
	CreatedAt  time.Time   `json:"created_at"`
	ModifiedAt time.Time   `json:"modified_at"`
	Directory  bool        `json:"directory"`
	CanMutate  bool        `json:"can_mutate"`
}

type hostFilesWireRequest struct {
	Path              string                      `json:"path,omitempty"`
	Directory         string                      `json:"directory,omitempty"`
	Name              string                      `json:"name,omitempty"`
	Destination       string                      `json:"destination,omitempty"`
	StoredPath        string                      `json:"stored_path,omitempty"`
	StoredName        string                      `json:"stored_name,omitempty"`
	CanonicalKind     hostFilesCanonicalKind      `json:"canonical_kind,omitempty"`
	RestoreAvailable  bool                        `json:"restore_available,omitempty"`
	MaxBytes          int64                       `json:"max_bytes,omitempty"`
	Offset            int                         `json:"offset,omitempty"`
	Limit             int                         `json:"limit,omitempty"`
	ByteOffset        int64                       `json:"byte_offset,omitempty"`
	ByteLimit         int                         `json:"byte_limit,omitempty"`
	Handle            string                      `json:"handle,omitempty"`
	StagingPath       string                      `json:"staging_path,omitempty"`
	ExpectedDigest    string                      `json:"expected_digest,omitempty"`
	Replace           bool                        `json:"replace,omitempty"`
	DirectoryPrepare  bool                        `json:"directory_prepare,omitempty"`
	Record            string                      `json:"record,omitempty"`
	Rotate            bool                        `json:"rotate,omitempty"`
	MaxBackups        int                         `json:"max_backups,omitempty"`
	Cursor            string                      `json:"cursor,omitempty"`
	OperationID       string                      `json:"operation_id,omitempty"`
	ExternalToken     string                      `json:"external_token,omitempty"`
	ExternalEntryID   string                      `json:"external_entry_id,omitempty"`
	ExternalEntryName string                      `json:"external_entry_name,omitempty"`
	ExternalMessage   string                      `json:"external_message,omitempty"`
	ScheduleID        string                      `json:"schedule_id,omitempty"`
	Permissions       *hostfiles.PermissionChange `json:"permissions,omitempty"`
}

type hostFilesWireResponse struct {
	Entries        []hostfiles.Entry             `json:"entries,omitempty"`
	Info           *HostFileInfo                 `json:"info,omitempty"`
	Document       *hostfiles.TextDocument       `json:"document,omitempty"`
	CanonicalPath  string                        `json:"canonical_path,omitempty"`
	AvailableName  string                        `json:"available_name,omitempty"`
	Trashed        *hostfiles.Trashed            `json:"trashed,omitempty"`
	Batch          []hostfiles.UploadBatchResult `json:"batch,omitempty"`
	NextOffset     int                           `json:"next_offset,omitempty"`
	Content        []byte                        `json:"content,omitempty"`
	Handle         string                        `json:"handle,omitempty"`
	Prepared       *hostFilesPrepared            `json:"prepared,omitempty"`
	SameFilesystem *bool                         `json:"same_filesystem,omitempty"`
	Metadata       *logstream.Metadata           `json:"metadata,omitempty"`
	Page           *logstream.Page               `json:"page,omitempty"`
	Events         []logstream.Event             `json:"events,omitempty"`
	Operation      *hostfiles.FileOperation      `json:"operation,omitempty"`
	Permissions    *hostfiles.Permissions        `json:"permissions,omitempty"`
}

type hostFilesUploadBatchManifest struct {
	Files                []hostFilesUploadBatchFile `json:"files"`
	SynchronizeQuickRuns bool                       `json:"synchronize_quick_runs,omitempty"`
}

type hostFilesUploadBatchFile struct {
	Name        string `json:"name"`
	StagingPath string `json:"staging_path"`
	MaxBytes    int64  `json:"max_bytes"`
	StoredName  string `json:"stored_name"`
}

type hostFilesPrepared struct {
	Path      string `json:"path"`
	Directory string `json:"directory,omitempty"`
	Digest    string `json:"digest,omitempty"`
}

type brokerHostFilesService struct {
	files       *hostfiles.Manager
	stagingRoot string
	mutationMu  sync.Mutex
	mu          sync.Mutex
	reads       map[string]*hostFilesReadHandle
	logs        map[string]*hostFilesLogHandle
	moveEngine  *hostfiles.MoveEngine
	moveContext context.Context
	db          *sql.DB
	external    *externaltrigger.Manager
}

type hostFilesReadHandle struct {
	file      *os.File
	owner     string
	expiresAt time.Time
}

type hostFilesLogHandle struct {
	source    *hostfiles.LogSource
	owner     string
	expiresAt time.Time
}

func NewBrokerHostFilesService(files *hostfiles.Manager, stagingRoots ...string) (HostFilesService, error) {
	if files == nil {
		return nil, errors.New("Broker Host Files manager is required")
	}
	stagingRoot := ""
	if len(stagingRoots) != 0 {
		stagingRoot = filepath.Clean(strings.TrimSpace(stagingRoots[0]))
		if !filepath.IsAbs(stagingRoot) {
			return nil, errors.New("Broker Host Files staging root must be absolute")
		}
	}
	return newBrokerHostFilesService(files, stagingRoot, nil, context.Background(), nil), nil
}

func NewBrokerHostFilesServiceWithMoves(files *hostfiles.Manager, stagingRoot string, engine *hostfiles.MoveEngine, moveContext context.Context, db *sql.DB, external *externaltrigger.Manager) (HostFilesService, error) {
	if files == nil || engine == nil || moveContext == nil || db == nil || external == nil || !filepath.IsAbs(filepath.Clean(stagingRoot)) {
		return nil, errors.New("Broker Host Files move service is not fully configured")
	}
	service := newBrokerHostFilesService(files, filepath.Clean(stagingRoot), engine, moveContext, db)
	service.external = external
	return service, nil
}

func newBrokerHostFilesService(files *hostfiles.Manager, stagingRoot string, engine *hostfiles.MoveEngine, moveContext context.Context, db *sql.DB) *brokerHostFilesService {
	return &brokerHostFilesService{files: files, stagingRoot: stagingRoot, reads: make(map[string]*hostFilesReadHandle),
		logs: make(map[string]*hostFilesLogHandle), moveEngine: engine, moveContext: moveContext, db: db}
}

func (service *brokerHostFilesService) Roots(context.Context) ([]hostfiles.Entry, error) {
	return service.files.Roots()
}

func (service *brokerHostFilesService) List(_ context.Context, path string) ([]hostfiles.Entry, error) {
	return service.files.List(path)
}

func (service *brokerHostFilesService) Info(_ context.Context, path string) (HostFileInfo, error) {
	info, err := service.files.Info(path)
	if err != nil {
		return HostFileInfo{}, err
	}
	return HostFileInfo{Name: info.Name(), Size: info.Size(), Mode: info.Mode(), CreatedAt: hostfiles.CreatedAt(info), ModifiedAt: info.ModTime(), Directory: info.IsDir(), CanMutate: service.files.CanMutate(path)}, nil
}

func (service *brokerHostFilesService) ReadText(_ context.Context, path string, maxBytes int64) (hostfiles.TextDocument, error) {
	return service.files.ReadText(path, maxBytes)
}

func (service *brokerHostFilesService) Canonical(_ context.Context, kind hostFilesCanonicalKind, path, name string) (string, error) {
	switch kind {
	case hostFilesCanonicalExisting:
		return service.files.CanonicalExisting(path)
	case hostFilesCanonicalDirectory:
		return service.files.CanonicalDirectory(path)
	case hostFilesCanonicalDestination:
		return service.files.CanonicalDestination(path)
	case hostFilesCanonicalChild:
		return service.files.Destination(path, name)
	default:
		return "", errors.New("unknown Host Files canonicalization operation")
	}
}

func (service *brokerHostFilesService) AvailableName(_ context.Context, directory, name string) (string, error) {
	return service.files.AvailableName(directory, name)
}

func (service *brokerHostFilesService) CreateDirectory(_ context.Context, directory, name string) error {
	service.mutationMu.Lock()
	defer service.mutationMu.Unlock()
	return service.files.CreateDirectory(directory, name)
}

func (service *brokerHostFilesService) Permissions(_ context.Context, path string) (hostfiles.Permissions, error) {
	return service.files.Permissions(path)
}

func (service *brokerHostFilesService) SetPermissions(_ context.Context, path string, change hostfiles.PermissionChange) (hostfiles.Permissions, error) {
	service.mutationMu.Lock()
	defer service.mutationMu.Unlock()
	return service.files.SetPermissions(path, change)
}

func (service *brokerHostFilesService) MoveToTrash(_ context.Context, path, storedName string) (hostfiles.Trashed, error) {
	service.mutationMu.Lock()
	defer service.mutationMu.Unlock()
	return service.files.MoveToTrash(path, storedName)
}

func (service *brokerHostFilesService) RestoreFromTrash(_ context.Context, storedPath, original string, available bool) (string, error) {
	service.mutationMu.Lock()
	defer service.mutationMu.Unlock()
	if available {
		return service.files.RestoreFromTrashToAvailablePath(storedPath, original)
	}
	return original, service.files.RestoreFromTrash(storedPath, original)
}

func (service *brokerHostFilesService) PurgeTrash(_ context.Context, storedPath string) error {
	service.mutationMu.Lock()
	defer service.mutationMu.Unlock()
	return service.files.PurgeTrash(storedPath)
}

func (service *brokerHostFilesService) Move(_ context.Context, source, destination string) error {
	service.mutationMu.Lock()
	defer service.mutationMu.Unlock()
	return service.files.Move(source, destination)
}

func (service *brokerHostFilesService) pruneHandlesLocked(now time.Time) {
	for handle, value := range service.reads {
		if !now.Before(value.expiresAt) {
			_ = value.file.Close()
			delete(service.reads, handle)
		}
	}
	for handle, value := range service.logs {
		if !now.Before(value.expiresAt) {
			delete(service.logs, handle)
		}
	}
}

func newHostFilesHandle() (string, error) {
	var token [24]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(token[:]), nil
}

func (service *brokerHostFilesService) OpenRead(_ context.Context, owner, path string) (string, HostFileInfo, error) {
	file, info, err := service.files.OpenRegular(path)
	if err != nil {
		return "", HostFileInfo{}, err
	}
	handle, err := newHostFilesHandle()
	if err != nil {
		_ = file.Close()
		return "", HostFileInfo{}, err
	}
	service.mu.Lock()
	service.pruneHandlesLocked(time.Now())
	if len(service.reads) >= 1024 {
		service.mu.Unlock()
		_ = file.Close()
		return "", HostFileInfo{}, errors.New("too many open Host Files downloads")
	}
	service.reads[handle] = &hostFilesReadHandle{file: file, owner: owner, expiresAt: time.Now().Add(hostFilesHandleTTL)}
	service.mu.Unlock()
	return handle, HostFileInfo{Name: info.Name(), Size: info.Size(), Mode: info.Mode(), ModifiedAt: info.ModTime(), Directory: false}, nil
}

func (service *brokerHostFilesService) ReadChunk(_ context.Context, owner, handle string, offset int64, limit int) ([]byte, error) {
	service.mu.Lock()
	service.pruneHandlesLocked(time.Now())
	value := service.reads[handle]
	if value != nil && value.owner == owner {
		value.expiresAt = time.Now().Add(hostFilesHandleTTL)
	} else {
		value = nil
	}
	service.mu.Unlock()
	if value == nil {
		return nil, errors.New("Host Files download handle is unavailable")
	}
	buffer := make([]byte, limit)
	read, err := value.file.ReadAt(buffer, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buffer[:read], nil
}

func (service *brokerHostFilesService) CloseRead(_ context.Context, owner, handle string) error {
	service.mu.Lock()
	service.pruneHandlesLocked(time.Now())
	value := service.reads[handle]
	if value != nil && value.owner == owner {
		delete(service.reads, handle)
	} else {
		value = nil
	}
	service.mu.Unlock()
	if value == nil {
		return errors.New("Host Files download handle is unavailable")
	}
	return value.file.Close()
}

func (service *brokerHostFilesService) OpenLog(_ context.Context, owner, path string) (string, logstream.Metadata, error) {
	source, err := service.files.OpenLogSource(path)
	if err != nil {
		return "", logstream.Metadata{}, err
	}
	handle, err := newHostFilesHandle()
	if err != nil {
		return "", logstream.Metadata{}, err
	}
	service.mu.Lock()
	service.pruneHandlesLocked(time.Now())
	if len(service.logs) >= 1024 {
		service.mu.Unlock()
		return "", logstream.Metadata{}, errors.New("too many open Host Files log sources")
	}
	service.logs[handle] = &hostFilesLogHandle{source: source, owner: owner, expiresAt: time.Now().Add(hostFilesHandleTTL)}
	service.mu.Unlock()
	return handle, source.Metadata(), nil
}

func (service *brokerHostFilesService) logSource(owner, handle string) (*hostfiles.LogSource, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.pruneHandlesLocked(time.Now())
	value := service.logs[handle]
	if value == nil || value.owner != owner {
		return nil, errors.New("Host Files log handle is unavailable")
	}
	value.expiresAt = time.Now().Add(hostFilesHandleTTL)
	return value.source, nil
}

func (service *brokerHostFilesService) LogHistory(ctx context.Context, owner, handle, before string) (logstream.Page, error) {
	source, err := service.logSource(owner, handle)
	if err != nil {
		return logstream.Page{}, err
	}
	return source.History(ctx, before)
}

var errHostFilesFollowWindowComplete = errors.New("Host Files follow window complete")

func (service *brokerHostFilesService) LogFollow(ctx context.Context, owner, handle, after string) ([]logstream.Event, error) {
	source, err := service.logSource(owner, handle)
	if err != nil {
		return nil, err
	}
	pollContext, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	events := make([]logstream.Event, 0, 32)
	bytes := 0
	err = source.Follow(pollContext, after, func(event logstream.Event) error {
		encoded, _ := json.Marshal(event)
		if len(events) >= 256 || bytes+len(encoded) > 1<<20 {
			return errHostFilesFollowWindowComplete
		}
		events = append(events, event)
		bytes += len(encoded)
		return nil
	})
	if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, errHostFilesFollowWindowComplete) {
		return nil, err
	}
	return events, nil
}

func (service *brokerHostFilesService) CloseLog(_ context.Context, owner, handle string) error {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.pruneHandlesLocked(time.Now())
	value := service.logs[handle]
	if value == nil || value.owner != owner {
		return errors.New("Host Files log handle is unavailable")
	}
	delete(service.logs, handle)
	return nil
}

func (service *brokerHostFilesService) consumeStaging(path string) (*os.File, error) {
	if service.stagingRoot == "" || !filepath.IsAbs(path) || filepath.Dir(filepath.Clean(path)) != service.stagingRoot {
		return nil, errors.New("Host Files staging path is outside the exchange root")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("Host Files staging entry is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		_ = file.Close()
		return nil, errors.New("Host Files staging entry changed while opening")
	}
	return file, nil
}

func (service *brokerHostFilesService) Upload(_ context.Context, stagingPath, directory, name string, maxBytes int64, replace bool, storedName string) (*hostfiles.Trashed, error) {
	service.mutationMu.Lock()
	defer service.mutationMu.Unlock()
	file, err := service.consumeStaging(stagingPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close(); _ = os.Remove(stagingPath) }()
	return service.files.Upload(directory, name, file, maxBytes, replace, storedName)
}

func (service *brokerHostFilesService) UploadBatch(ctx context.Context, manifestPath, directory string, replace bool) ([]hostfiles.UploadBatchResult, error) {
	service.mutationMu.Lock()
	defer service.mutationMu.Unlock()
	manifestFile, err := service.consumeStaging(manifestPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = manifestFile.Close(); _ = os.Remove(manifestPath) }()
	var manifest hostFilesUploadBatchManifest
	decoder := json.NewDecoder(io.LimitReader(manifestFile, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil || len(manifest.Files) == 0 || len(manifest.Files) > 100 {
		return nil, errors.New("Host Files batch manifest is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("Host Files batch manifest has trailing content")
	}
	inputs := make([]hostfiles.UploadBatchInput, 0, len(manifest.Files))
	opened := make([]*os.File, 0, len(manifest.Files))
	defer func() {
		for _, file := range opened {
			_ = file.Close()
		}
		for _, entry := range manifest.Files {
			_ = os.Remove(entry.StagingPath)
		}
	}()
	for _, entry := range manifest.Files {
		if entry.MaxBytes <= 0 || entry.MaxBytes > 1<<30 {
			return nil, errors.New("Host Files batch file size limit is invalid")
		}
		file, openErr := service.consumeStaging(entry.StagingPath)
		if openErr != nil {
			return nil, openErr
		}
		opened = append(opened, file)
		inputs = append(inputs, hostfiles.UploadBatchInput{Name: entry.Name, Source: file, MaxBytes: entry.MaxBytes, StoredName: entry.StoredName})
	}
	results, err := service.files.UploadBatch(directory, inputs, replace)
	if err != nil {
		return results, err
	}
	if manifest.SynchronizeQuickRuns && service.db == nil {
		return nil, errors.Join(errors.New("Quick Run synchronization database is unavailable"), service.files.RollbackUploadBatch(results))
	}
	// Keep the broker-side file transaction recoverable until every referenced
	// Quick Run has the digest of the newly committed script.
	if manifest.SynchronizeQuickRuns {
		for index := range results {
			if results[index].Trashed == nil {
				continue
			}
			var references int
			if err := service.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM quick_runs WHERE script_path_key = ?", hostfiles.ComparisonKey(results[index].Path)).Scan(&references); err != nil {
				return nil, errors.Join(err, service.files.RollbackUploadBatch(results))
			}
			if references > 0 {
				prepared, prepareErr := service.files.PrepareScript(results[index].Path)
				if prepareErr != nil {
					return nil, errors.Join(prepareErr, service.files.RollbackUploadBatch(results))
				}
				results[index].ScriptSHA256 = prepared.Digest
			}
		}
	}
	if service.db == nil {
		return results, nil
	}
	transaction, err := service.db.BeginTx(ctx, nil)
	if err == nil {
		for _, result := range results {
			if result.Trashed == nil {
				continue
			}
			trashed := result.Trashed
			_, err = transaction.ExecContext(ctx, `INSERT INTO trash_entries
				(id, original_path, original_path_key, stored_path, stored_path_key, deleted_at, size, is_directory)
				VALUES (?, ?, ?, ?, ?, ?, ?, 0)`, trashed.StoredName, trashed.OriginalPath, hostfiles.ComparisonKey(trashed.OriginalPath),
				trashed.StoredPath, hostfiles.ComparisonKey(trashed.StoredPath), time.Now().UTC().Unix(), trashed.Size)
			if err != nil {
				break
			}
		}
		for index := range results {
			if err != nil || results[index].ScriptSHA256 == "" {
				continue
			}
			updated, updateErr := transaction.ExecContext(ctx, `UPDATE quick_runs
				SET script_sha256 = ?, revision = revision + 1, updated_at = ?
				WHERE script_path_key = ?`, results[index].ScriptSHA256, time.Now().UTC().Unix(), hostfiles.ComparisonKey(results[index].Path))
			if updateErr != nil {
				err = updateErr
				break
			}
			results[index].QuickRunsSynchronized, _ = updated.RowsAffected()
		}
	}
	if err == nil {
		err = transaction.Commit()
	} else if transaction != nil {
		_ = transaction.Rollback()
	}
	if err != nil {
		return nil, errors.Join(fmt.Errorf("record batch replacements: %w", err), service.files.RollbackUploadBatch(results))
	}
	return results, nil
}

func (service *brokerHostFilesService) SaveText(_ context.Context, stagingPath, path, expectedDigest, storedName string, maxBytes int64) (hostfiles.Trashed, error) {
	service.mutationMu.Lock()
	defer service.mutationMu.Unlock()
	file, err := service.consumeStaging(stagingPath)
	if err != nil {
		return hostfiles.Trashed{}, err
	}
	defer func() { _ = file.Close(); _ = os.Remove(stagingPath) }()
	body, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || int64(len(body)) > maxBytes {
		return hostfiles.Trashed{}, errors.New("Host Files staged text exceeds the size limit")
	}
	return service.files.SaveText(path, expectedDigest, string(body), storedName, maxBytes)
}

func (service *brokerHostFilesService) RollbackTextSave(_ context.Context, path, storedPath string) error {
	service.mutationMu.Lock()
	defer service.mutationMu.Unlock()
	return service.files.RollbackTextSave(path, storedPath)
}

func (service *brokerHostFilesService) RemoveRegular(_ context.Context, path string) error {
	service.mutationMu.Lock()
	defer service.mutationMu.Unlock()
	return service.files.RemoveRegular(path)
}

func (service *brokerHostFilesService) Prepare(_ context.Context, path string, directory bool) (hostFilesPrepared, error) {
	if directory {
		prepared, err := service.files.PrepareDirectory(path)
		return hostFilesPrepared{Path: prepared.Path}, err
	}
	prepared, err := service.files.PrepareScript(path)
	return hostFilesPrepared{Path: prepared.Path, Directory: prepared.Directory, Digest: prepared.Digest}, err
}

func (service *brokerHostFilesService) SameFilesystem(_ context.Context, source, destination string) (bool, error) {
	return service.files.SameFilesystem(source, destination)
}

func (service *brokerHostFilesService) AppendText(_ context.Context, path, record string) error {
	service.mutationMu.Lock()
	defer service.mutationMu.Unlock()
	return service.files.AppendText(path, record)
}

func (service *brokerHostFilesService) AppendRotatingText(_ context.Context, path, record string, maxBytes int64, maxBackups int) error {
	service.mutationMu.Lock()
	defer service.mutationMu.Unlock()
	return service.files.AppendRotatingText(path, record, maxBytes, maxBackups)
}

func (service *brokerHostFilesService) PrepareAppend(_ context.Context, path string) (string, error) {
	return service.files.PrepareAppendFile(path)
}

func (service *brokerHostFilesService) AppendExternalLog(ctx context.Context, token, entryID, entryName, message string) (Actor, string, error) {
	if service.external == nil {
		return Actor{}, "", errors.New("Broker External Interface service is unavailable")
	}
	key, entry, err := service.external.Resolve(ctx, token, entryName)
	if err != nil || entry.ID != entryID || entry.Type != externaltrigger.ActionLog {
		return Actor{}, "", errors.New("External Interface log capability is invalid")
	}
	var config externaltrigger.LogConfig
	if entry.DecodeConfig(&config) != nil || !utf8.ValidString(message) || len([]byte(message)) > config.MaxMessageBytes || strings.IndexFunc(message, unicode.IsControl) >= 0 {
		return Actor{}, "", errors.New("External Interface log request is invalid")
	}
	record := fmt.Sprintf("%s\t%s\n", time.Now().UTC().Format(time.RFC3339Nano), message)
	if config.Category != "" {
		record = fmt.Sprintf("%s\t[%s]\t%s\n", time.Now().UTC().Format(time.RFC3339Nano), config.Category, message)
	}
	var appendErr error
	if config.Rotate {
		appendErr = service.files.AppendRotatingText(config.File, record, config.MaxFileBytes, config.MaxBackups)
	} else {
		appendErr = service.files.AppendText(config.File, record)
	}
	if appendErr != nil {
		return Actor{}, "", appendErr
	}
	return Actor{UserID: "external:" + key.ID, Username: key.Label, Role: "external"}, entry.ID, nil
}

func (service *brokerHostFilesService) PrepareSchedule(ctx context.Context, id string) (hostFilesPrepared, error) {
	if service.db == nil {
		return hostFilesPrepared{}, errors.New("Broker schedule database is unavailable")
	}
	var path string
	if err := service.db.QueryRowContext(ctx, "SELECT script_path FROM schedules WHERE id = ? AND deleted = 0", id).Scan(&path); err != nil {
		return hostFilesPrepared{}, err
	}
	prepared, err := service.files.PrepareScript(path)
	return hostFilesPrepared{Path: prepared.Path, Directory: prepared.Directory, Digest: prepared.Digest}, err
}

func (service *brokerHostFilesService) StartCrossFilesystemMove(_ context.Context, id, source, destination, displacedStoredPath, displacedID string) (hostfiles.FileOperation, error) {
	if service.moveEngine == nil || service.db == nil {
		return hostfiles.FileOperation{}, errors.New("Broker Host Files move engine is unavailable")
	}
	started := make(chan hostfiles.FileOperation, 1)
	finished := make(chan struct {
		operation hostfiles.FileOperation
		err       error
	}, 1)
	go func() {
		operation, err := service.moveEngine.ExecuteWithStart(service.moveContext, id, source, destination, func(value hostfiles.FileOperation) { started <- value })
		if err != nil && displacedStoredPath != "" {
			if _, destinationErr := service.files.Info(destination); os.IsNotExist(destinationErr) {
				if restoreErr := service.files.RestoreFromTrash(displacedStoredPath, destination); restoreErr == nil {
					_, _ = service.db.ExecContext(context.WithoutCancel(service.moveContext), "DELETE FROM trash_entries WHERE id = ? AND stored_path_key = ?", displacedID, hostfiles.ComparisonKey(displacedStoredPath))
				}
			}
		}
		finished <- struct {
			operation hostfiles.FileOperation
			err       error
		}{operation: operation, err: err}
	}()
	select {
	case operation := <-started:
		return operation, nil
	case result := <-finished:
		return result.operation, result.err
	}
}

func (server *Server) hostFilesOperation(request wireRequest) wireResponse {
	if server.hostFiles == nil {
		return wireResponse{Status: statusError, ErrorCode: "host_files_unavailable", Message: "Host Files service is unavailable"}
	}
	actor, action, err := server.authorizeHostFilesOperation(request)
	if err != nil {
		server.logOperationFailure(request, "authorization", "host_files_forbidden", err)
		return wireResponse{Status: statusError, ErrorCode: "host_files_forbidden", Message: "Host Files operation is not authorized"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	payload := request.HostFiles
	result := &hostFilesWireResponse{}
	response := wireResponse{Status: statusOK, HostFiles: result}
	switch request.Operation {
	case operationHostFilesRoots:
		result.Entries, err = server.hostFiles.Roots(ctx)
	case operationHostFilesList:
		var entries []hostfiles.Entry
		entries, err = server.hostFiles.List(ctx, payload.Path)
		if err == nil {
			start := min(payload.Offset, len(entries))
			end := min(start+payload.Limit, len(entries))
			result.Entries = entries[start:end]
			if end < len(entries) {
				result.NextOffset = end
			}
		}
	case operationHostFilesInfo:
		var value HostFileInfo
		value, err = server.hostFiles.Info(ctx, payload.Path)
		result.Info = &value
	case operationHostFilesReadText:
		var value hostfiles.TextDocument
		value, err = server.hostFiles.ReadText(ctx, payload.Path, payload.MaxBytes)
		result.Document = &value
	case operationHostFilesCanonical:
		result.CanonicalPath, err = server.hostFiles.Canonical(ctx, payload.CanonicalKind, payload.Path, payload.Name)
	case operationHostFilesAvailable:
		result.AvailableName, err = server.hostFiles.AvailableName(ctx, payload.Directory, payload.Name)
	case operationHostFilesMkdir:
		err = server.hostFiles.CreateDirectory(ctx, payload.Directory, payload.Name)
	case operationHostFilesPermissions:
		var value hostfiles.Permissions
		value, err = server.hostFiles.Permissions(ctx, payload.Path)
		result.Permissions = &value
	case operationHostFilesSetPermissions:
		var value hostfiles.Permissions
		if payload.Permissions == nil {
			err = errors.New("permission change is required")
		} else {
			value, err = server.hostFiles.SetPermissions(ctx, payload.Path, *payload.Permissions)
			result.Permissions = &value
		}
	case operationHostFilesTrash:
		var value hostfiles.Trashed
		value, err = server.hostFiles.MoveToTrash(ctx, payload.Path, payload.StoredName)
		result.Trashed = &value
	case operationHostFilesRestore:
		result.CanonicalPath, err = server.hostFiles.RestoreFromTrash(ctx, payload.StoredPath, payload.Destination, payload.RestoreAvailable)
	case operationHostFilesPurge:
		err = server.hostFiles.PurgeTrash(ctx, payload.StoredPath)
	case operationHostFilesMove:
		err = server.hostFiles.Move(ctx, payload.Path, payload.Destination)
	case operationHostFilesOpenRead:
		var info HostFileInfo
		result.Handle, info, err = server.hostFiles.OpenRead(ctx, actor.UserID, payload.Path)
		result.Info = &info
	case operationHostFilesReadChunk:
		result.Content, err = server.hostFiles.ReadChunk(ctx, actor.UserID, payload.Handle, payload.ByteOffset, payload.ByteLimit)
	case operationHostFilesCloseRead:
		err = server.hostFiles.CloseRead(ctx, actor.UserID, payload.Handle)
	case operationHostFilesUpload:
		result.Trashed, err = server.hostFiles.Upload(ctx, payload.StagingPath, payload.Directory, payload.Name, payload.MaxBytes, payload.Replace, payload.StoredName)
	case operationHostFilesUploadBatch:
		result.Batch, err = server.hostFiles.UploadBatch(ctx, payload.StagingPath, payload.Directory, payload.Replace)
	case operationHostFilesSaveText:
		var value hostfiles.Trashed
		value, err = server.hostFiles.SaveText(ctx, payload.StagingPath, payload.Path, payload.ExpectedDigest, payload.StoredName, payload.MaxBytes)
		result.Trashed = &value
	case operationHostFilesRollback:
		err = server.hostFiles.RollbackTextSave(ctx, payload.Path, payload.StoredPath)
	case operationHostFilesRemove:
		err = server.hostFiles.RemoveRegular(ctx, payload.Path)
	case operationHostFilesPrepare:
		var value hostFilesPrepared
		value, err = server.hostFiles.Prepare(ctx, payload.Path, payload.DirectoryPrepare)
		result.Prepared = &value
	case operationHostFilesSameFS:
		var same bool
		same, err = server.hostFiles.SameFilesystem(ctx, payload.Path, payload.Destination)
		result.SameFilesystem = &same
	case operationHostFilesAppend:
		if payload.Rotate {
			err = server.hostFiles.AppendRotatingText(ctx, payload.Path, payload.Record, payload.MaxBytes, payload.MaxBackups)
		} else {
			err = server.hostFiles.AppendText(ctx, payload.Path, payload.Record)
		}
	case operationHostFilesLogOpen:
		var metadata logstream.Metadata
		result.Handle, metadata, err = server.hostFiles.OpenLog(ctx, actor.UserID, payload.Path)
		result.Metadata = &metadata
	case operationHostFilesLogHistory:
		var page logstream.Page
		page, err = server.hostFiles.LogHistory(ctx, actor.UserID, payload.Handle, payload.Cursor)
		result.Page = &page
	case operationHostFilesLogFollow:
		result.Events, err = server.hostFiles.LogFollow(ctx, actor.UserID, payload.Handle, payload.Cursor)
	case operationHostFilesLogClose:
		err = server.hostFiles.CloseLog(ctx, actor.UserID, payload.Handle)
	case operationHostFilesCrossMove:
		var operation hostfiles.FileOperation
		operation, err = server.hostFiles.StartCrossFilesystemMove(ctx, payload.OperationID, payload.Path, payload.Destination, payload.StoredPath, payload.StoredName)
		result.Operation = &operation
	case operationHostFilesPrepareAppend:
		result.CanonicalPath, err = server.hostFiles.PrepareAppend(ctx, payload.Path)
	}
	mutation := action != ActionHostFilesRead
	if mutation && server.auditor != nil {
		body, _ := json.Marshal(payload)
		resultText := "succeeded"
		if err != nil {
			resultText = "failed"
		}
		auditErr := server.auditor.Record(context.Background(), AuditRecord{OccurredAt: server.now().UTC(), RequestID: request.RequestID,
			Actor: actor, Action: action, Resource: hostFilesResource(payload), Revision: "host-files-v1", ParametersSHA256: parametersDigest(body), Result: resultText})
		if auditErr != nil {
			if err == nil {
				return server.operationFailureResponse(request, "audit", "audit_failed_after_execution", "Host Files operation completed but result audit failed", auditErr)
			}
			server.logOperationFailure(request, "audit", "audit_failed_after_execution", auditErr)
		}
	}
	if err != nil {
		if request.Operation == operationHostFilesInfo && os.IsNotExist(err) {
			return server.operationFailureResponse(request, "host_files", "host_files_not_found", "Host Files path does not exist", err)
		}
		return server.operationFailureResponse(request, "host_files", "host_files_failed", "Host Files operation failed", err)
	}
	return response
}

func (server *Server) authorizeHostFilesOperation(request wireRequest) (Actor, Action, error) {
	payload := request.HostFiles
	action, recent := hostFilesAction(request.Operation)
	body, _ := json.Marshal(payload)
	authorization := AuthorizationRequest{SessionToken: request.SessionToken, RequestID: request.RequestID, Action: action,
		Resource: hostFilesResource(payload), Revision: "host-files-v1", ParametersSHA256: parametersDigest(body)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mode := domainAuthorizationCurrentPrivileged
	if recent {
		mode = domainAuthorizationRecentPrivileged
	} else if action == ActionHostFilesRead {
		mode = domainAuthorizationCurrentActor
	}
	actor, err := server.authorizeActor(ctx, authorization, mode)
	if err != nil {
		return Actor{}, action, err
	}
	if action == ActionHostFilesRead {
		if actor.Role != "administrator" && actor.Role != "maintainer" && actor.Role != "operator" {
			return Actor{}, action, errors.New("role cannot read Host Files")
		}
	} else if actor.Role != "administrator" && actor.Role != "maintainer" {
		return Actor{}, action, errors.New("role cannot mutate Host Files")
	}
	return actor, action, nil
}

func (server *Server) externalHostFilesLogOperation(request wireRequest) wireResponse {
	if server.hostFiles == nil {
		return wireResponse{Status: statusError, ErrorCode: "host_files_unavailable", Message: "Host Files service is unavailable"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	actor, resource, err := server.hostFiles.AppendExternalLog(ctx, request.HostFiles.ExternalToken, request.HostFiles.ExternalEntryID, request.HostFiles.ExternalEntryName, request.HostFiles.ExternalMessage)
	if server.auditor != nil {
		result := "succeeded"
		if err != nil {
			result = "failed"
		}
		body, _ := json.Marshal(request.HostFiles)
		if auditErr := server.auditor.Record(context.Background(), AuditRecord{OccurredAt: server.now().UTC(), RequestID: request.RequestID,
			Actor: actor, Action: ActionHostFilesWrite, Resource: resource, Revision: "external-log-v1", ParametersSHA256: parametersDigest(body), Result: result}); auditErr != nil {
			if err == nil {
				return server.operationFailureResponse(request, "audit", "audit_failed_after_execution", "External log completed but result audit failed", auditErr)
			}
			server.logOperationFailure(request, "audit", "audit_failed_after_execution", auditErr)
		}
	}
	if err != nil {
		return server.operationFailureResponse(request, "host_files", "host_files_failed", "External Host Files log operation failed", err)
	}
	return wireResponse{Status: statusOK}
}

func (server *Server) hostFilesScheduleOperation(request wireRequest) wireResponse {
	if server.hostFiles == nil {
		return wireResponse{Status: statusError, ErrorCode: "host_files_unavailable", Message: "Host Files service is unavailable"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	prepared, err := server.hostFiles.PrepareSchedule(ctx, request.HostFiles.ScheduleID)
	if err != nil {
		return server.operationFailureResponse(request, "host_files", "host_files_failed", "Scheduled Host Files resource is unavailable", err)
	}
	return wireResponse{Status: statusOK, HostFiles: &hostFilesWireResponse{Prepared: &prepared}}
}

func hostFilesAction(operation string) (Action, bool) {
	switch operation {
	case operationHostFilesTrash, operationHostFilesRestore, operationHostFilesPurge:
		// The trash lifecycle follows normal WriteFiles authorization; path moves
		// while path moves below remain explicit recent-step-up operations.
		return ActionHostFilesDelete, false
	case operationHostFilesMove, operationHostFilesCrossMove:
		return ActionHostFilesMove, true
	case operationHostFilesSetPermissions:
		return ActionHostFilesWrite, true
	case operationHostFilesMkdir, operationHostFilesUpload, operationHostFilesUploadBatch, operationHostFilesSaveText, operationHostFilesRollback,
		operationHostFilesRemove, operationHostFilesAppend:
		return ActionHostFilesWrite, false
	default:
		return ActionHostFilesRead, false
	}
}

func hostFilesResource(payload *hostFilesWireRequest) string {
	for _, value := range []string{payload.Path, payload.StoredPath, payload.Directory, payload.Destination} {
		if value != "" {
			return value
		}
	}
	return "host-files-roots"
}

func validateHostFilesRequest(request wireRequest) error {
	if request.HostFiles == nil || request.Capability != "" || request.Action != "" || request.Resource != "" || request.Revision != "" ||
		request.ParametersSHA256 != "" || len(request.Parameters) != 0 || hasMFAFields(request) || hasPasskeyFields(request) || hasRemoteWebsiteFields(request) || request.MySQL != nil || request.Redis != nil || !validCredentialSessionToken(request.SessionToken) {
		return errors.New("Host Files request contains unrelated fields")
	}
	payload := request.HostFiles
	for _, value := range []string{payload.Path, payload.Directory, payload.Destination, payload.StoredPath} {
		if len(value) > 4096 || strings.ContainsAny(value, "\r\n\x00") {
			return errors.New("Host Files path is invalid")
		}
	}
	if len(payload.Handle) > 128 || len(payload.OperationID) > 255 || len(payload.StagingPath) > 4096 || len(payload.ExpectedDigest) > 128 || len(payload.Record) > 64<<10 || len(payload.Cursor) > 2048 || strings.ContainsAny(payload.Handle+payload.OperationID+payload.StagingPath+payload.ExpectedDigest+payload.Record+payload.Cursor, "\r\n\x00") {
		return errors.New("Host Files request value is invalid")
	}
	for _, value := range []string{payload.Name, payload.StoredName} {
		if len(value) > 255 || strings.ContainsAny(value, "\r\n\x00") {
			return errors.New("Host Files name is invalid")
		}
	}
	if hostFilesExpectedPayload(request.Operation, *payload) != *payload {
		return errors.New("Host Files request contains operation-forbidden fields")
	}
	switch request.Operation {
	case operationHostFilesRoots:
	case operationHostFilesList:
		if payload.Path != "" && !filepath.IsAbs(payload.Path) || payload.Offset < 0 || payload.Limit != hostFilesPageSize {
			return errors.New("Host Files list request is invalid")
		}
	case operationHostFilesInfo:
		if !isAbsoluteHostFilePath(payload.Path) {
			return errors.New("Host Files info request is invalid")
		}
	case operationHostFilesPermissions:
		if !isAbsoluteHostFilePath(payload.Path) || payload.Permissions != nil {
			return errors.New("Host Files permissions request is invalid")
		}
	case operationHostFilesSetPermissions:
		if !isAbsoluteHostFilePath(payload.Path) || payload.Permissions == nil || !validPermissionChange(*payload.Permissions) {
			return errors.New("Host Files permission change request is invalid")
		}
	case operationHostFilesOpenRead:
		if !isAbsoluteHostFilePath(payload.Path) {
			return errors.New("Host Files open-read request is invalid")
		}
	case operationHostFilesRemove:
		if !isAbsoluteHostFilePath(payload.Path) {
			return errors.New("Host Files remove request is invalid")
		}
	case operationHostFilesLogOpen:
		if !isAbsoluteHostFilePath(payload.Path) {
			return errors.New("Host Files log-open request is invalid")
		}
	case operationHostFilesPrepareAppend:
		if !isAbsoluteHostFilePath(payload.Path) {
			return errors.New("Host Files prepare-append request is invalid")
		}
	case operationHostFilesReadText:
		if !isAbsoluteHostFilePath(payload.Path) || payload.MaxBytes <= 0 || payload.MaxBytes > 1<<20 {
			return errors.New("Host Files text request is invalid")
		}
	case operationHostFilesCanonical:
		validKind := payload.CanonicalKind == hostFilesCanonicalExisting || payload.CanonicalKind == hostFilesCanonicalDirectory || payload.CanonicalKind == hostFilesCanonicalDestination || payload.CanonicalKind == hostFilesCanonicalChild
		if !validKind || !isAbsoluteHostFilePath(payload.Path) || (payload.CanonicalKind == hostFilesCanonicalChild) != (payload.Name != "") {
			return errors.New("Host Files canonical request is invalid")
		}
	case operationHostFilesAvailable, operationHostFilesMkdir:
		if !isAbsoluteHostFilePath(payload.Directory) || payload.Name == "" {
			return errors.New("Host Files directory request is invalid")
		}
	case operationHostFilesTrash:
		if !isAbsoluteHostFilePath(payload.Path) || payload.StoredName == "" {
			return errors.New("Host Files trash request is invalid")
		}
	case operationHostFilesRestore:
		if !isAbsoluteHostFilePath(payload.StoredPath) || !isAbsoluteHostFilePath(payload.Destination) {
			return errors.New("Host Files restore request is invalid")
		}
	case operationHostFilesPurge:
		if !isAbsoluteHostFilePath(payload.StoredPath) {
			return errors.New("Host Files purge request is invalid")
		}
	case operationHostFilesMove:
		if !isAbsoluteHostFilePath(payload.Path) || !isAbsoluteHostFilePath(payload.Destination) {
			return errors.New("Host Files move request is invalid")
		}
	case operationHostFilesSameFS:
		if !isAbsoluteHostFilePath(payload.Path) || !isAbsoluteHostFilePath(payload.Destination) {
			return errors.New("Host Files filesystem request is invalid")
		}
	case operationHostFilesReadChunk:
		if len(payload.Handle) != 32 || payload.ByteOffset < 0 || payload.ByteLimit <= 0 || payload.ByteLimit > 3<<20 {
			return errors.New("Host Files read-chunk request is invalid")
		}
	case operationHostFilesCloseRead:
		if len(payload.Handle) != 32 {
			return errors.New("Host Files close-read request is invalid")
		}
	case operationHostFilesUpload:
		if !isAbsoluteHostFilePath(payload.StagingPath) || !isAbsoluteHostFilePath(payload.Directory) || payload.Name == "" || payload.MaxBytes <= 0 || payload.MaxBytes > 1<<30 {
			return errors.New("Host Files upload request is invalid")
		}
	case operationHostFilesUploadBatch:
		if payload.StagingPath == "" || !filepath.IsAbs(payload.StagingPath) || payload.Directory == "" || !filepath.IsAbs(payload.Directory) || payload.Path != "" || payload.Name != "" || payload.Destination != "" || payload.StoredPath != "" || payload.StoredName != "" || payload.CanonicalKind != "" || payload.ExpectedDigest != "" || payload.Handle != "" || payload.Record != "" || payload.MaxBytes != 0 || payload.Offset != 0 || payload.Limit != 0 || payload.ByteOffset != 0 || payload.ByteLimit != 0 {
			return errors.New("Host Files batch upload request is invalid")
		}
	case operationHostFilesSaveText:
		if !isAbsoluteHostFilePath(payload.StagingPath) || !isAbsoluteHostFilePath(payload.Path) || len(payload.ExpectedDigest) != 64 || payload.StoredName == "" || payload.MaxBytes <= 0 || payload.MaxBytes > 1<<20 {
			return errors.New("Host Files save-text request is invalid")
		}
	case operationHostFilesRollback:
		if !isAbsoluteHostFilePath(payload.Path) || !isAbsoluteHostFilePath(payload.StoredPath) {
			return errors.New("Host Files rollback request is invalid")
		}
	case operationHostFilesPrepare:
		if !isAbsoluteHostFilePath(payload.Path) {
			return errors.New("Host Files prepare request is invalid")
		}
	case operationHostFilesAppend:
		if !isAbsoluteHostFilePath(payload.Path) || payload.Record == "" || (payload.Rotate && (payload.MaxBytes <= 0 || payload.MaxBackups <= 0)) || (!payload.Rotate && (payload.MaxBytes != 0 || payload.MaxBackups != 0)) {
			return errors.New("Host Files append request is invalid")
		}
	case operationHostFilesLogHistory, operationHostFilesLogFollow:
		if len(payload.Handle) != 32 {
			return errors.New("Host Files log-read request is invalid")
		}
	case operationHostFilesLogClose:
		if len(payload.Handle) != 32 {
			return errors.New("Host Files log-close request is invalid")
		}
	case operationHostFilesCrossMove:
		if payload.OperationID == "" || !isAbsoluteHostFilePath(payload.Path) || !isAbsoluteHostFilePath(payload.Destination) || (payload.StoredPath == "") != (payload.StoredName == "") || (payload.StoredPath != "" && !isAbsoluteHostFilePath(payload.StoredPath)) {
			return errors.New("Host Files cross-filesystem move request is invalid")
		}
	}
	return nil
}

func isAbsoluteHostFilePath(path string) bool {
	return path != "" && filepath.IsAbs(path)
}

func validPermissionChange(change hostfiles.PermissionChange) bool {
	if len(change.Owner) > 512 || len(change.Principal) > 512 || len(change.RuleAppliesTo) > 16 || strings.ContainsAny(change.Owner+change.Principal+change.RuleAppliesTo, "\r\n\x00") {
		return false
	}
	if change.Mode != nil && *change.Mode > 0o777 {
		return false
	}
	if change.Mode != nil && (change.Owner != "" || change.Principal != "" || change.InheritanceEnabled != nil) {
		return false
	}
	if (change.RemoveRule && change.Principal == "") || (change.AccessMask != nil && change.Principal == "") {
		return false
	}
	if change.AccessMask != nil && (*change.AccessMask == 0 || *change.AccessMask & ^hostfiles.WindowsAccessFull != 0) {
		return false
	}
	if (change.ReplaceChildOwners && change.Owner == "") || (change.ApplyRuleToChildren && change.Principal == "") {
		return false
	}
	if change.RuleAppliesTo != "" && (!change.ApplyRuleToChildren || change.RuleAppliesTo != "files" && change.RuleAppliesTo != "folders" && change.RuleAppliesTo != "children") {
		return false
	}
	return change.Mode != nil || change.Owner != "" || change.Principal != "" || change.InheritanceEnabled != nil
}

func validateExternalHostFilesLogRequest(request wireRequest) error {
	if request.HostFiles == nil || request.SessionToken != "" || request.Capability != "" || request.Action != "" || request.Resource != "" || request.Revision != "" || request.ParametersSHA256 != "" || len(request.Parameters) != 0 || hasMFAFields(request) || hasPasskeyFields(request) || hasRemoteWebsiteFields(request) || request.MySQL != nil || request.Redis != nil {
		return errors.New("External Host Files log request contains unrelated fields")
	}
	payload := request.HostFiles
	if len(payload.ExternalToken) < 16 || len(payload.ExternalToken) > 256 || len(payload.ExternalEntryID) == 0 || len(payload.ExternalEntryID) > 255 || len(payload.ExternalEntryName) == 0 || len(payload.ExternalEntryName) > 255 || len(payload.ExternalMessage) > 8<<10 || strings.ContainsAny(payload.ExternalToken+payload.ExternalEntryID+payload.ExternalEntryName, "\r\n\x00") || strings.ContainsRune(payload.ExternalMessage, 0) {
		return errors.New("External Host Files log request is invalid")
	}
	want := hostFilesWireRequest{ExternalToken: payload.ExternalToken, ExternalEntryID: payload.ExternalEntryID, ExternalEntryName: payload.ExternalEntryName, ExternalMessage: payload.ExternalMessage}
	if *payload != want {
		return errors.New("External Host Files log request contains operation-forbidden fields")
	}
	return nil
}

func validateHostFilesScheduleRequest(request wireRequest) error {
	if request.HostFiles == nil || request.SessionToken != "" || request.Capability != "" || request.Action != "" || request.Resource != "" || request.Revision != "" || request.ParametersSHA256 != "" || len(request.Parameters) != 0 || hasMFAFields(request) || hasPasskeyFields(request) || hasRemoteWebsiteFields(request) || request.MySQL != nil || request.Redis != nil {
		return errors.New("Scheduled Host Files request contains unrelated fields")
	}
	payload := request.HostFiles
	if len(payload.ScheduleID) == 0 || len(payload.ScheduleID) > 255 || strings.ContainsAny(payload.ScheduleID, "\r\n\x00") || *payload != (hostFilesWireRequest{ScheduleID: payload.ScheduleID}) {
		return errors.New("Scheduled Host Files request is invalid")
	}
	return nil
}

func hostFilesExpectedPayload(operation string, payload hostFilesWireRequest) hostFilesWireRequest {
	switch operation {
	case operationHostFilesRoots:
		return hostFilesWireRequest{}
	case operationHostFilesList:
		return hostFilesWireRequest{Path: payload.Path, Offset: payload.Offset, Limit: payload.Limit}
	case operationHostFilesInfo, operationHostFilesOpenRead, operationHostFilesRemove, operationHostFilesLogOpen, operationHostFilesPrepareAppend:
		return hostFilesWireRequest{Path: payload.Path}
	case operationHostFilesPermissions:
		return hostFilesWireRequest{Path: payload.Path}
	case operationHostFilesSetPermissions:
		return hostFilesWireRequest{Path: payload.Path, Permissions: payload.Permissions}
	case operationHostFilesReadText:
		return hostFilesWireRequest{Path: payload.Path, MaxBytes: payload.MaxBytes}
	case operationHostFilesCanonical:
		return hostFilesWireRequest{Path: payload.Path, Name: payload.Name, CanonicalKind: payload.CanonicalKind}
	case operationHostFilesAvailable, operationHostFilesMkdir:
		return hostFilesWireRequest{Directory: payload.Directory, Name: payload.Name}
	case operationHostFilesTrash:
		return hostFilesWireRequest{Path: payload.Path, StoredName: payload.StoredName}
	case operationHostFilesRestore:
		return hostFilesWireRequest{StoredPath: payload.StoredPath, Destination: payload.Destination, RestoreAvailable: payload.RestoreAvailable}
	case operationHostFilesPurge:
		return hostFilesWireRequest{StoredPath: payload.StoredPath}
	case operationHostFilesMove, operationHostFilesSameFS:
		return hostFilesWireRequest{Path: payload.Path, Destination: payload.Destination}
	case operationHostFilesReadChunk:
		return hostFilesWireRequest{Handle: payload.Handle, ByteOffset: payload.ByteOffset, ByteLimit: payload.ByteLimit}
	case operationHostFilesCloseRead:
		return hostFilesWireRequest{Handle: payload.Handle}
	case operationHostFilesUpload:
		return hostFilesWireRequest{StagingPath: payload.StagingPath, Directory: payload.Directory, Name: payload.Name, MaxBytes: payload.MaxBytes, Replace: payload.Replace, StoredName: payload.StoredName}
	case operationHostFilesUploadBatch:
		return hostFilesWireRequest{StagingPath: payload.StagingPath, Directory: payload.Directory, Replace: payload.Replace}
	case operationHostFilesSaveText:
		return hostFilesWireRequest{StagingPath: payload.StagingPath, Path: payload.Path, ExpectedDigest: payload.ExpectedDigest, StoredName: payload.StoredName, MaxBytes: payload.MaxBytes}
	case operationHostFilesRollback:
		return hostFilesWireRequest{Path: payload.Path, StoredPath: payload.StoredPath}
	case operationHostFilesPrepare:
		return hostFilesWireRequest{Path: payload.Path, DirectoryPrepare: payload.DirectoryPrepare}
	case operationHostFilesAppend:
		return hostFilesWireRequest{Path: payload.Path, Record: payload.Record, Rotate: payload.Rotate, MaxBytes: payload.MaxBytes, MaxBackups: payload.MaxBackups}
	case operationHostFilesLogHistory, operationHostFilesLogFollow:
		return hostFilesWireRequest{Handle: payload.Handle, Cursor: payload.Cursor}
	case operationHostFilesLogClose:
		return hostFilesWireRequest{Handle: payload.Handle}
	case operationHostFilesCrossMove:
		return hostFilesWireRequest{OperationID: payload.OperationID, Path: payload.Path, Destination: payload.Destination, StoredPath: payload.StoredPath, StoredName: payload.StoredName}
	default:
		return hostFilesWireRequest{}
	}
}

func isHostFilesOperation(operation string) bool {
	switch operation {
	case operationHostFilesRoots, operationHostFilesList, operationHostFilesInfo, operationHostFilesReadText,
		operationHostFilesCanonical, operationHostFilesAvailable, operationHostFilesMkdir, operationHostFilesPermissions, operationHostFilesSetPermissions,
		operationHostFilesTrash, operationHostFilesRestore, operationHostFilesPurge, operationHostFilesMove,
		operationHostFilesOpenRead, operationHostFilesReadChunk, operationHostFilesCloseRead, operationHostFilesUpload, operationHostFilesUploadBatch,
		operationHostFilesSaveText, operationHostFilesRollback, operationHostFilesRemove, operationHostFilesPrepare,
		operationHostFilesSameFS, operationHostFilesAppend, operationHostFilesLogOpen, operationHostFilesLogHistory,
		operationHostFilesLogFollow, operationHostFilesLogClose, operationHostFilesCrossMove, operationHostFilesPrepareAppend:
		return true
	default:
		return false
	}
}

type HostFilesBackend struct {
	client      *Client
	stagingRoot string
}

func NewHostFilesBackend(client *Client, stagingRoots ...string) *HostFilesBackend {
	root := ""
	if len(stagingRoots) != 0 {
		root = filepath.Clean(stagingRoots[0])
	}
	return &HostFilesBackend{client: client, stagingRoot: root}
}

func (backend *HostFilesBackend) call(ctx context.Context, operation string, payload hostFilesWireRequest) (hostFilesWireResponse, error) {
	if backend == nil || backend.client == nil {
		return hostFilesWireResponse{}, ErrHostFilesUnavailable
	}
	authorization, ok := AuthorizationFromContext(ctx)
	if !ok {
		return hostFilesWireResponse{}, errors.New("privileged Broker Host Files authorization is missing")
	}
	response, err := backend.client.call(ctx, wireRequest{Version: ProtocolVersion, Operation: operation, RequestID: authorization.RequestID, SessionToken: authorization.SessionToken, HostFiles: &payload})
	if err != nil {
		if response.ErrorCode == "host_files_not_found" {
			return hostFilesWireResponse{}, fmt.Errorf("%w: %v", &os.PathError{Op: "stat", Path: payload.Path, Err: os.ErrNotExist}, err)
		}
		return hostFilesWireResponse{}, fmt.Errorf("%w: %v", ErrHostFilesUnavailable, err)
	}
	if response.HostFiles == nil {
		return hostFilesWireResponse{}, errors.New("privileged Broker returned an invalid Host Files response")
	}
	return *response.HostFiles, nil
}

func (backend *HostFilesBackend) AppendExternalLog(ctx context.Context, requestID, token, entryID, entryName, message string) error {
	if backend == nil || backend.client == nil || !requestIDPattern.MatchString(requestID) {
		return ErrHostFilesUnavailable
	}
	_, err := backend.client.call(ctx, wireRequest{Version: ProtocolVersion, Operation: operationHostFilesExternalLog, RequestID: requestID,
		HostFiles: &hostFilesWireRequest{ExternalToken: token, ExternalEntryID: entryID, ExternalEntryName: entryName, ExternalMessage: message}})
	return err
}

func (backend *HostFilesBackend) PrepareSchedule(ctx context.Context, requestID, scheduleID string) (hostfiles.Script, error) {
	if backend == nil || backend.client == nil || !requestIDPattern.MatchString(requestID) {
		return hostfiles.Script{}, ErrHostFilesUnavailable
	}
	response, err := backend.client.call(ctx, wireRequest{Version: ProtocolVersion, Operation: operationHostFilesPrepareSchedule,
		RequestID: requestID, HostFiles: &hostFilesWireRequest{ScheduleID: scheduleID}})
	if response.HostFiles == nil || response.HostFiles.Prepared == nil {
		return hostfiles.Script{}, errors.Join(err, errors.New("privileged Broker returned no scheduled Host Files resource"))
	}
	prepared := response.HostFiles.Prepared
	return hostfiles.Script{Path: prepared.Path, Directory: prepared.Directory, Digest: prepared.Digest}, err
}

func (backend *HostFilesBackend) Roots(ctx context.Context) ([]hostfiles.Entry, error) {
	value, err := backend.call(ctx, operationHostFilesRoots, hostFilesWireRequest{})
	return value.Entries, err
}

func (backend *HostFilesBackend) List(ctx context.Context, path string) ([]hostfiles.Entry, error) {
	var entries []hostfiles.Entry
	for offset := 0; ; {
		value, err := backend.call(ctx, operationHostFilesList, hostFilesWireRequest{Path: path, Offset: offset, Limit: hostFilesPageSize})
		if err != nil {
			return nil, err
		}
		entries = append(entries, value.Entries...)
		if value.NextOffset == 0 {
			return entries, nil
		}
		if value.NextOffset <= offset {
			return nil, errors.New("privileged Broker returned an invalid Host Files page")
		}
		offset = value.NextOffset
	}
}

func (backend *HostFilesBackend) Info(ctx context.Context, path string) (HostFileInfo, error) {
	value, err := backend.call(ctx, operationHostFilesInfo, hostFilesWireRequest{Path: path})
	if errors.Is(err, os.ErrNotExist) {
		return HostFileInfo{}, err
	}
	if value.Info == nil {
		return HostFileInfo{}, errors.Join(err, errors.New("privileged Broker returned no Host Files metadata"))
	}
	return *value.Info, err
}

func (backend *HostFilesBackend) ReadText(ctx context.Context, path string, maxBytes int64) (hostfiles.TextDocument, error) {
	value, err := backend.call(ctx, operationHostFilesReadText, hostFilesWireRequest{Path: path, MaxBytes: maxBytes})
	if value.Document == nil {
		return hostfiles.TextDocument{}, errors.Join(err, errors.New("privileged Broker returned no Host Files text"))
	}
	return *value.Document, err
}

func (backend *HostFilesBackend) canonical(ctx context.Context, kind hostFilesCanonicalKind, path, name string) (string, error) {
	value, err := backend.call(ctx, operationHostFilesCanonical, hostFilesWireRequest{Path: path, Name: name, CanonicalKind: kind})
	return value.CanonicalPath, err
}

func (backend *HostFilesBackend) CanonicalExisting(ctx context.Context, path string) (string, error) {
	return backend.canonical(ctx, hostFilesCanonicalExisting, path, "")
}
func (backend *HostFilesBackend) CanonicalDirectory(ctx context.Context, path string) (string, error) {
	return backend.canonical(ctx, hostFilesCanonicalDirectory, path, "")
}
func (backend *HostFilesBackend) CanonicalDestination(ctx context.Context, path string) (string, error) {
	return backend.canonical(ctx, hostFilesCanonicalDestination, path, "")
}
func (backend *HostFilesBackend) Destination(ctx context.Context, directory, name string) (string, error) {
	return backend.canonical(ctx, hostFilesCanonicalChild, directory, name)
}

func (backend *HostFilesBackend) AvailableName(ctx context.Context, directory, name string) (string, error) {
	value, err := backend.call(ctx, operationHostFilesAvailable, hostFilesWireRequest{Directory: directory, Name: name})
	return value.AvailableName, err
}

func (backend *HostFilesBackend) CreateDirectory(ctx context.Context, directory, name string) error {
	_, err := backend.call(ctx, operationHostFilesMkdir, hostFilesWireRequest{Directory: directory, Name: name})
	return err
}

func (backend *HostFilesBackend) Permissions(ctx context.Context, path string) (hostfiles.Permissions, error) {
	value, err := backend.call(ctx, operationHostFilesPermissions, hostFilesWireRequest{Path: path})
	if value.Permissions == nil {
		return hostfiles.Permissions{}, errors.Join(err, errors.New("privileged Broker returned no Host Files permissions"))
	}
	return *value.Permissions, err
}

func (backend *HostFilesBackend) SetPermissions(ctx context.Context, path string, change hostfiles.PermissionChange) (hostfiles.Permissions, error) {
	value, err := backend.call(ctx, operationHostFilesSetPermissions, hostFilesWireRequest{Path: path, Permissions: &change})
	if value.Permissions == nil {
		return hostfiles.Permissions{}, errors.Join(err, errors.New("privileged Broker returned no updated Host Files permissions"))
	}
	return *value.Permissions, err
}

func (backend *HostFilesBackend) MoveToTrash(ctx context.Context, path, storedName string) (hostfiles.Trashed, error) {
	value, err := backend.call(ctx, operationHostFilesTrash, hostFilesWireRequest{Path: path, StoredName: storedName})
	if value.Trashed == nil {
		return hostfiles.Trashed{}, errors.Join(err, errors.New("privileged Broker returned no trash record"))
	}
	return *value.Trashed, err
}

func (backend *HostFilesBackend) RestoreFromTrash(ctx context.Context, storedPath, original string) error {
	_, err := backend.call(ctx, operationHostFilesRestore, hostFilesWireRequest{StoredPath: storedPath, Destination: original})
	return err
}

func (backend *HostFilesBackend) RestoreFromTrashToAvailablePath(ctx context.Context, storedPath, original string) (string, error) {
	value, err := backend.call(ctx, operationHostFilesRestore, hostFilesWireRequest{StoredPath: storedPath, Destination: original, RestoreAvailable: true})
	return value.CanonicalPath, err
}

func (backend *HostFilesBackend) PurgeTrash(ctx context.Context, storedPath string) error {
	_, err := backend.call(ctx, operationHostFilesPurge, hostFilesWireRequest{StoredPath: storedPath})
	return err
}

func (backend *HostFilesBackend) Move(ctx context.Context, source, destination string) error {
	_, err := backend.call(ctx, operationHostFilesMove, hostFilesWireRequest{Path: source, Destination: destination})
	return err
}

type RemoteHostFile struct {
	ctx     context.Context
	backend *HostFilesBackend
	handle  string
	offset  int64
	info    HostFileInfo
	closed  bool
}

func (backend *HostFilesBackend) OpenRegular(ctx context.Context, path string) (*RemoteHostFile, HostFileInfo, error) {
	value, err := backend.call(ctx, operationHostFilesOpenRead, hostFilesWireRequest{Path: path})
	if err != nil {
		return nil, HostFileInfo{}, err
	}
	if value.Info == nil || len(value.Handle) != 32 {
		return nil, HostFileInfo{}, errors.New("privileged Broker returned an invalid Host Files download")
	}
	return &RemoteHostFile{ctx: ctx, backend: backend, handle: value.Handle, info: *value.Info}, *value.Info, nil
}

func (file *RemoteHostFile) Read(destination []byte) (int, error) {
	if file.closed {
		return 0, os.ErrClosed
	}
	if file.offset >= file.info.Size {
		return 0, io.EOF
	}
	wanted := min(len(destination), 3<<20)
	value, err := file.backend.call(file.ctx, operationHostFilesReadChunk, hostFilesWireRequest{Handle: file.handle, ByteOffset: file.offset, ByteLimit: wanted})
	if err != nil {
		return 0, err
	}
	read := copy(destination, value.Content)
	file.offset += int64(read)
	if read == 0 || file.offset >= file.info.Size {
		return read, io.EOF
	}
	return read, nil
}

func (file *RemoteHostFile) Seek(offset int64, whence int) (int64, error) {
	var next int64
	switch whence {
	case io.SeekStart:
		next = offset
	case io.SeekCurrent:
		next = file.offset + offset
	case io.SeekEnd:
		next = file.info.Size + offset
	default:
		return 0, errors.New("invalid Host Files seek origin")
	}
	if next < 0 {
		return 0, errors.New("negative Host Files seek offset")
	}
	file.offset = next
	return next, nil
}

func (file *RemoteHostFile) Close() error {
	if file.closed {
		return nil
	}
	file.closed = true
	_, err := file.backend.call(file.ctx, operationHostFilesCloseRead, hostFilesWireRequest{Handle: file.handle})
	return err
}

func (backend *HostFilesBackend) stage(source io.Reader, maxBytes int64) (string, error) {
	if backend.stagingRoot == "" || !filepath.IsAbs(backend.stagingRoot) {
		return "", ErrHostFilesUnavailable
	}
	if err := os.MkdirAll(backend.stagingRoot, 0o750); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(backend.stagingRoot, "host-files-*")
	if err != nil {
		return "", err
	}
	path := file.Name()
	defer func() {
		if file != nil {
			_ = file.Close()
		}
	}()
	if err := file.Chmod(0o640); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	written, err := io.Copy(file, io.LimitReader(source, maxBytes+1))
	if err == nil && written > maxBytes {
		err = fmt.Errorf("Host Files staging input exceeds %d bytes", maxBytes)
	}
	if syncErr := file.Sync(); err == nil {
		err = syncErr
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	file = nil
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func (backend *HostFilesBackend) Upload(ctx context.Context, directory, name string, source io.Reader, maxBytes int64, replace bool, storedName string) (*hostfiles.Trashed, error) {
	staged, err := backend.stage(source, maxBytes)
	if err != nil {
		return nil, err
	}
	defer os.Remove(staged)
	value, err := backend.call(ctx, operationHostFilesUpload, hostFilesWireRequest{StagingPath: staged, Directory: directory, Name: name, MaxBytes: maxBytes, Replace: replace, StoredName: storedName})
	return value.Trashed, err
}

func (backend *HostFilesBackend) UploadBatch(ctx context.Context, directory string, inputs []hostfiles.UploadBatchInput, replace, synchronizeQuickRuns bool) ([]hostfiles.UploadBatchResult, error) {
	manifest := hostFilesUploadBatchManifest{Files: make([]hostFilesUploadBatchFile, 0, len(inputs)), SynchronizeQuickRuns: synchronizeQuickRuns}
	stagedPaths := make([]string, 0, len(inputs))
	defer func() {
		for _, path := range stagedPaths {
			_ = os.Remove(path)
		}
	}()
	for _, input := range inputs {
		staged, err := backend.stage(input.Source, input.MaxBytes)
		if err != nil {
			return nil, err
		}
		stagedPaths = append(stagedPaths, staged)
		manifest.Files = append(manifest.Files, hostFilesUploadBatchFile{Name: input.Name, StagingPath: staged, MaxBytes: input.MaxBytes, StoredName: input.StoredName})
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	manifestPath, err := backend.stage(bytes.NewReader(body), 1<<20)
	if err != nil {
		return nil, err
	}
	defer os.Remove(manifestPath)
	value, err := backend.call(ctx, operationHostFilesUploadBatch, hostFilesWireRequest{StagingPath: manifestPath, Directory: directory, Replace: replace})
	return value.Batch, err
}

func (backend *HostFilesBackend) SaveText(ctx context.Context, path, expectedDigest, content, storedName string, maxBytes int64) (hostfiles.Trashed, error) {
	staged, err := backend.stage(strings.NewReader(content), maxBytes)
	if err != nil {
		return hostfiles.Trashed{}, err
	}
	defer os.Remove(staged)
	value, err := backend.call(ctx, operationHostFilesSaveText, hostFilesWireRequest{StagingPath: staged, Path: path, ExpectedDigest: expectedDigest, StoredName: storedName, MaxBytes: maxBytes})
	if value.Trashed == nil {
		return hostfiles.Trashed{}, errors.Join(err, errors.New("privileged Broker returned no text-save trash record"))
	}
	return *value.Trashed, err
}

func (backend *HostFilesBackend) RollbackTextSave(ctx context.Context, path, storedPath string) error {
	_, err := backend.call(ctx, operationHostFilesRollback, hostFilesWireRequest{Path: path, StoredPath: storedPath})
	return err
}

func (backend *HostFilesBackend) RemoveRegular(ctx context.Context, path string) error {
	_, err := backend.call(ctx, operationHostFilesRemove, hostFilesWireRequest{Path: path})
	return err
}

func (backend *HostFilesBackend) PrepareScript(ctx context.Context, path string) (hostfiles.Script, error) {
	value, err := backend.call(ctx, operationHostFilesPrepare, hostFilesWireRequest{Path: path})
	if value.Prepared == nil {
		return hostfiles.Script{}, errors.Join(err, errors.New("privileged Broker returned no prepared script"))
	}
	return hostfiles.Script{Path: value.Prepared.Path, Directory: value.Prepared.Directory, Digest: value.Prepared.Digest}, err
}

func (backend *HostFilesBackend) PrepareDirectory(ctx context.Context, path string) (hostfiles.PreparedDirectory, error) {
	value, err := backend.call(ctx, operationHostFilesPrepare, hostFilesWireRequest{Path: path, DirectoryPrepare: true})
	if value.Prepared == nil {
		return hostfiles.PreparedDirectory{}, errors.Join(err, errors.New("privileged Broker returned no prepared directory"))
	}
	return hostfiles.PreparedDirectory{Path: value.Prepared.Path}, err
}

func (backend *HostFilesBackend) SameFilesystem(ctx context.Context, source, destination string) (bool, error) {
	value, err := backend.call(ctx, operationHostFilesSameFS, hostFilesWireRequest{Path: source, Destination: destination})
	if value.SameFilesystem == nil {
		return false, errors.Join(err, errors.New("privileged Broker returned no filesystem relationship"))
	}
	return *value.SameFilesystem, err
}

func (backend *HostFilesBackend) AppendText(ctx context.Context, path, record string) error {
	_, err := backend.call(ctx, operationHostFilesAppend, hostFilesWireRequest{Path: path, Record: record})
	return err
}

func (backend *HostFilesBackend) AppendRotatingText(ctx context.Context, path, record string, maxBytes int64, maxBackups int) error {
	_, err := backend.call(ctx, operationHostFilesAppend, hostFilesWireRequest{Path: path, Record: record, Rotate: true, MaxBytes: maxBytes, MaxBackups: maxBackups})
	return err
}

func (backend *HostFilesBackend) PrepareAppend(ctx context.Context, path string) (string, error) {
	value, err := backend.call(ctx, operationHostFilesPrepareAppend, hostFilesWireRequest{Path: path})
	return value.CanonicalPath, err
}

func (backend *HostFilesBackend) StartCrossFilesystemMove(ctx context.Context, id, source, destination, displacedStoredPath, displacedID string) (hostfiles.FileOperation, error) {
	value, err := backend.call(ctx, operationHostFilesCrossMove, hostFilesWireRequest{OperationID: id, Path: source,
		Destination: destination, StoredPath: displacedStoredPath, StoredName: displacedID})
	if value.Operation == nil {
		return hostfiles.FileOperation{}, errors.Join(err, errors.New("privileged Broker returned no Host Files operation"))
	}
	return *value.Operation, err
}

type RemoteHostFileLogSource struct {
	backend  *HostFilesBackend
	handle   string
	metadata logstream.Metadata
}

func (backend *HostFilesBackend) OpenLogSource(ctx context.Context, path string) (*RemoteHostFileLogSource, error) {
	value, err := backend.call(ctx, operationHostFilesLogOpen, hostFilesWireRequest{Path: path})
	if err != nil {
		return nil, err
	}
	if len(value.Handle) != 32 || value.Metadata == nil {
		return nil, errors.New("privileged Broker returned an invalid Host Files log source")
	}
	return &RemoteHostFileLogSource{backend: backend, handle: value.Handle, metadata: *value.Metadata}, nil
}

func (source *RemoteHostFileLogSource) Metadata() logstream.Metadata { return source.metadata }

func (source *RemoteHostFileLogSource) History(ctx context.Context, before string) (logstream.Page, error) {
	value, err := source.backend.call(ctx, operationHostFilesLogHistory, hostFilesWireRequest{Handle: source.handle, Cursor: before})
	closeContext := context.WithoutCancel(ctx)
	_, closeErr := source.backend.call(closeContext, operationHostFilesLogClose, hostFilesWireRequest{Handle: source.handle})
	if value.Page == nil {
		return logstream.Page{}, errors.Join(err, closeErr, errors.New("privileged Broker returned no Host Files log page"))
	}
	return *value.Page, errors.Join(err, closeErr)
}

func (source *RemoteHostFileLogSource) Follow(ctx context.Context, after string, emit func(logstream.Event) error) error {
	defer func() {
		_, _ = source.backend.call(context.WithoutCancel(ctx), operationHostFilesLogClose, hostFilesWireRequest{Handle: source.handle})
	}()
	lastState := ""
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		value, err := source.backend.call(ctx, operationHostFilesLogFollow, hostFilesWireRequest{Handle: source.handle, Cursor: after})
		if err != nil {
			return err
		}
		for _, event := range value.Events {
			if event.Entry != nil && event.Entry.Cursor != "" {
				after = event.Entry.Cursor
			}
			if event.Kind == logstream.EventState && event.State == lastState {
				continue
			}
			if event.Kind == logstream.EventState {
				lastState = event.State
			}
			if err := emit(event); err != nil {
				return err
			}
		}
	}
}

var _ logstream.Source = (*RemoteHostFileLogSource)(nil)

func (info HostFileInfo) String() string {
	return fmt.Sprintf("%s (%d bytes)", info.Name, info.Size)
}
