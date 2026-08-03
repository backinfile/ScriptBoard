package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"time"

	"scriptboard/internal/buildinfo"
	"scriptboard/internal/config"
	"scriptboard/internal/installation"
	"scriptboard/internal/localtls"
	"scriptboard/internal/platformservice"
)

type Result struct {
	Schema          int    `json:"schema"`
	OperationID     string `json:"operation_id"`
	Outcome         Phase  `json:"outcome"`
	PreviousVersion string `json:"previous_version"`
	TargetVersion   string `json:"target_version"`
	CompletedAt     string `json:"completed_at"`
	Error           string `json:"error,omitempty"`
}

func ApplyOperation(ctx context.Context, stateRoot, operationID string) error {
	lock, err := acquireOperationLock(stateRoot)
	if err != nil {
		return err
	}
	defer lock.Close()
	operation, err := LoadOperation(stateRoot, operationID)
	if err != nil {
		return err
	}
	if operation.Phase != PhaseHandoff && operation.Phase != PhaseWaitingForOldExit {
		return fmt.Errorf("operation is not ready for helper handoff: %s", operation.Phase)
	}
	if err := verifyPreparedOperation(operation); err != nil {
		return failSafe(operation, fmt.Errorf("revalidate prepared update: %w", err))
	}
	operation.Phase = PhaseWaitingForOldExit
	if err := SaveOperation(operation); err != nil {
		return err
	}
	if err := waitForProcessExit(ctx, operation.OldPID, 60*time.Second); err != nil {
		return failSafe(operation, err)
	}
	if err := waitForServiceStopped(ctx, 15*time.Second); err != nil {
		return recoverBeforeSwitch(ctx, &operation, err)
	}
	operation.Phase = PhaseSnapshotting
	if err := SaveOperation(operation); err != nil {
		return err
	}
	if err := copyFileSync(operation.DatabasePath, operation.SnapshotPath, 0o600); err != nil {
		return recoverBeforeSwitch(ctx, &operation, fmt.Errorf("snapshot database: %w", err))
	}
	if err := switchAndValidate(ctx, &operation); err != nil {
		return rollbackOperation(ctx, &operation, err)
	}
	operation.Phase = PhaseCommitted
	operation.Error = ""
	if err := SaveOperation(operation); err != nil {
		return err
	}
	if err := writeResult(operation, ""); err != nil {
		return err
	}
	_ = os.Remove(operation.SnapshotPath)
	return nil
}

func RecoverOperation(ctx context.Context, stateRoot, operationID string) error {
	lock, err := acquireOperationLock(stateRoot)
	if err != nil {
		return err
	}
	defer lock.Close()
	operation, err := LoadOperation(stateRoot, operationID)
	if err != nil {
		return err
	}
	switch operation.Phase {
	case PhaseHandoff, PhaseWaitingForOldExit, PhaseSnapshotting, PhaseFailedSafe:
		return recoverBeforeSwitch(ctx, &operation, errors.New("administrator recovered an update before version switching"))
	case PhaseNeedsRecovery, PhaseRollingBack, PhaseValidating, PhaseStartingTarget, PhaseSwitching:
	default:
		return fmt.Errorf("operation phase %s does not require recovery", operation.Phase)
	}
	err = rollbackOperation(ctx, &operation, errors.New("administrator requested recovery"))
	if err != nil {
		if recovered, loadErr := LoadOperation(stateRoot, operationID); loadErr == nil && recovered.Phase == PhaseRolledBack {
			return nil
		}
	}
	return err
}

func recoverBeforeSwitch(ctx context.Context, operation *Operation, cause error) error {
	metadata, err := loadOperationInstallation(*operation)
	if err != nil {
		return markRecoveryFailure(operation, cause, err)
	}
	if metadata.Current != operation.PreviousVersion {
		return markRecoveryFailure(operation, cause, errors.New("managed installation has already switched versions"))
	}
	previousInfo, err := installation.ReadVersionInfo(metadata, operation.PreviousVersion)
	if err != nil || previousInfo.Commit != operation.PreviousCommit {
		if err == nil {
			err = errors.New("previous Installed Release commit does not match the update operation")
		}
		return markRecoveryFailure(operation, cause, err)
	}
	if err := installation.ValidateVersion(metadata, operation.PreviousVersion, previousInfo); err != nil {
		return markRecoveryFailure(operation, cause, err)
	}
	if err := platformservice.Stop(); err != nil {
		return markRecoveryFailure(operation, cause, fmt.Errorf("stop service before recovery restart: %w", err))
	}
	_ = os.Remove(operation.SnapshotPath)
	operation.Phase = PhaseFailedSafe
	operation.Error = cause.Error()
	if err := SaveOperation(*operation); err != nil {
		return err
	}
	if err := platformservice.SwitchExecutable(installation.ServiceExecutable(metadata), metadata.ConfigPath); err != nil {
		return markRecoveryFailure(operation, cause, err)
	}
	RemoveRuntimeMarker(operation.StateRoot)
	startedAfter := time.Now().UTC()
	err = platformservice.Start()
	if err == nil {
		err = validateService(ctx, *operation, operation.PreviousVersion, operation.PreviousCommit, "", startedAfter, 60*time.Second)
	}
	if err != nil {
		operation.Error = cause.Error() + "; restarting the previous release failed: " + err.Error()
		_ = SaveOperation(*operation)
		_ = writeResult(*operation, operation.Error)
		return errors.New(operation.Error)
	}
	if err := writeResult(*operation, cause.Error()); err != nil {
		return err
	}
	return nil
}

func markRecoveryFailure(operation *Operation, cause, recoveryErr error) error {
	operation.Phase = PhaseNeedsRecovery
	operation.Error = cause.Error() + "; recovery failed: " + recoveryErr.Error()
	_ = SaveOperation(*operation)
	_ = writeResult(*operation, operation.Error)
	return errors.New(operation.Error)
}

func switchAndValidate(ctx context.Context, operation *Operation) error {
	metadata, err := loadOperationInstallation(*operation)
	if err != nil {
		return err
	}
	if err := installation.ValidateVersion(metadata, operation.TargetVersion, targetBuild(*operation)); err != nil {
		return fmt.Errorf("validate target Installed Release: %w", err)
	}
	operation.Phase = PhaseSwitching
	if err := SaveOperation(*operation); err != nil {
		return err
	}
	metadata, err = installation.SetCurrent(metadata, operation.TargetVersion)
	if err != nil {
		return err
	}
	if err := platformservice.SwitchExecutable(installation.ServiceExecutable(metadata), metadata.ConfigPath); err != nil {
		return err
	}
	operation.Phase = PhaseStartingTarget
	if err := SaveOperation(*operation); err != nil {
		return err
	}
	RemoveRuntimeMarker(operation.StateRoot)
	operation.Phase = PhaseValidating
	if err := SaveOperation(*operation); err != nil {
		return err
	}
	startedAfter := time.Now().UTC()
	if err := platformservice.Start(); err != nil {
		return fmt.Errorf("start target service: %w", err)
	}
	return validateService(ctx, *operation, operation.TargetVersion, operation.TargetCommit, operation.ID, startedAfter, 90*time.Second)
}

func rollbackOperation(ctx context.Context, operation *Operation, cause error) error {
	operation.Phase = PhaseRollingBack
	operation.Error = cause.Error()
	if err := SaveOperation(*operation); err != nil {
		return err
	}
	_ = platformservice.Stop()
	metadata, err := loadOperationInstallation(*operation)
	if err == nil {
		var previousInfo buildinfo.Info
		previousInfo, err = installation.ReadVersionInfo(metadata, operation.PreviousVersion)
		if err == nil && previousInfo.Commit != operation.PreviousCommit {
			err = errors.New("previous Installed Release commit does not match the update operation")
		}
		if err == nil {
			err = installation.ValidateVersion(metadata, operation.PreviousVersion, previousInfo)
		}
	}
	if err == nil {
		metadata, err = installation.SetCurrent(metadata, operation.PreviousVersion)
	}
	if err == nil {
		err = platformservice.SwitchExecutable(installation.ServiceExecutable(metadata), metadata.ConfigPath)
	}
	if err == nil {
		err = restoreDatabase(operation.SnapshotPath, operation.DatabasePath)
	}
	if err == nil {
		RemoveRuntimeMarker(operation.StateRoot)
		startedAfter := time.Now().UTC()
		err = platformservice.Start()
		if err == nil {
			err = validateService(ctx, *operation, operation.PreviousVersion, operation.PreviousCommit, operation.ID, startedAfter, 60*time.Second)
		}
	}
	if err != nil {
		operation.Phase = PhaseNeedsRecovery
		operation.Error = cause.Error() + "; rollback failed: " + err.Error()
		_ = SaveOperation(*operation)
		_ = writeResult(*operation, operation.Error)
		return errors.New(operation.Error)
	}
	operation.Phase = PhaseRolledBack
	operation.Error = cause.Error()
	if err := SaveOperation(*operation); err != nil {
		return err
	}
	if err := writeResult(*operation, cause.Error()); err != nil {
		return err
	}
	return fmt.Errorf("target release failed validation and was rolled back: %w", cause)
}

func failSafe(operation Operation, cause error) error {
	operation.Phase = PhaseFailedSafe
	operation.Error = cause.Error()
	_ = SaveOperation(operation)
	_ = writeResult(operation, cause.Error())
	return cause
}

func loadOperationInstallation(operation Operation) (installation.Metadata, error) {
	metadata, err := installation.Load(operation.StateRoot)
	if err != nil {
		return installation.Metadata{}, err
	}
	if metadata.InstallRoot != operation.InstallRoot || metadata.StateRoot != operation.StateRoot || metadata.ConfigPath != operation.ConfigPath {
		return installation.Metadata{}, errors.New("operation no longer matches managed installation")
	}
	return metadata, nil
}

func targetBuild(operation Operation) buildinfo.Info {
	return buildinfo.Info{
		Version: operation.Manifest.Version, Tag: operation.Manifest.Tag, Commit: operation.Manifest.Commit,
		BuiltAt: operation.Manifest.PublishedAt, ReleaseBuild: true,
		DatabaseSchemaVersion:  operation.Manifest.DatabaseSchema,
		UpdaterProtocolVersion: operation.Manifest.UpdaterProtocol, Repository: operation.Manifest.Repository,
	}
}

func verifyPreparedOperation(operation Operation) error {
	root, err := OperationDirectory(operation.StateRoot, operation.ID)
	if err != nil {
		return err
	}
	manifestRaw, err := readLimitedFile(filepath.Join(root, ManifestFilename), MaxManifestBytes)
	if err != nil {
		return err
	}
	signatureRaw, err := readLimitedFile(filepath.Join(root, SignatureFilename), MaxSignatureBytes)
	if err != nil {
		return err
	}
	manifest, err := VerifyTrustedManifest(manifestRaw, signatureRaw)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(manifest, operation.Manifest) {
		return errors.New("signed manifest does not match the persisted update operation")
	}
	asset, ok := manifest.AssetFor(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return errors.New("signed manifest does not contain this updater platform")
	}
	archive, err := os.Open(operation.ArchivePath)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(hash, io.LimitReader(archive, asset.Size+1))
	closeErr := archive.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written != asset.Size || hex.EncodeToString(hash.Sum(nil)) != asset.SHA256 {
		return errors.New("prepared archive no longer matches the signed manifest")
	}
	metadata, err := loadOperationInstallation(operation)
	if err != nil {
		return err
	}
	return installation.ValidateVersion(metadata, operation.TargetVersion, targetBuild(operation))
}

func readLimitedFile(path string, maximum int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maximum {
		return nil, errors.New("update metadata file exceeds its size limit")
	}
	return raw, nil
}

func validateService(ctx context.Context, operation Operation, version, commit, validationOperationID string, startedAfter time.Time, timeout time.Duration) error {
	loaded, err := config.Load([]string{"--config", operation.ConfigPath}, os.Getenv)
	if err != nil {
		return err
	}
	serviceURL, client, err := localServiceClient(loaded)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	var healthySince time.Time
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		running, _ := platformservice.IsRunning()
		marker, markerErr := LoadRuntimeMarker(operation.StateRoot)
		markerStarted, _ := time.Parse(time.RFC3339Nano, marker.StartedAt)
		markerOK := markerErr == nil && marker.Build.Version == version && marker.Build.Commit == commit &&
			marker.ValidationOperationID == validationOperationID && markerStarted.After(startedAfter.Add(-time.Second))
		httpOK := false
		if running && markerOK {
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, serviceURL+"/login", nil)
			response, requestErr := client.Do(request)
			if requestErr == nil {
				httpOK = response.StatusCode < http.StatusInternalServerError
				_ = response.Body.Close()
			}
		}
		if running && markerOK && httpOK {
			if healthySince.IsZero() {
				healthySince = time.Now()
			}
			if time.Since(healthySince) >= 15*time.Second {
				return nil
			}
		} else {
			healthySince = time.Time{}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return errors.New("service did not remain healthy for 15 seconds before the validation timeout")
}

func localServiceClient(loaded config.Config) (string, *http.Client, error) {
	host, port, err := net.SplitHostPort(loaded.Listen)
	if err != nil {
		return "", nil, err
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	scheme := "http"
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if loaded.TLSCert != "" {
		scheme = "https"
		tlsConfig, err := localtls.PinnedConfig(loaded.TLSCert, host)
		if err != nil {
			return "", nil, err
		}
		transport.TLSClientConfig = tlsConfig
	}
	return fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(host, port)), &http.Client{Timeout: 2 * time.Second, Transport: transport}, nil
}

func waitForProcessExit(ctx context.Context, pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return errors.New("old ScriptBoard process did not exit before the handoff timeout")
}

func waitForServiceStopped(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		running, err := platformservice.IsRunning()
		if err == nil && !running {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return errors.New("ScriptBoard service manager did not reach the stopped state")
}

func restoreDatabase(snapshot, database string) error {
	temporary := database + ".update-restore"
	_ = os.Remove(temporary)
	if err := copyFileSync(snapshot, temporary, 0o600); err != nil {
		return err
	}
	_ = os.Remove(database + "-wal")
	_ = os.Remove(database + "-shm")
	if err := os.Remove(database); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(temporary, database)
}

func copyFileSync(source, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err = io.Copy(output, input); err == nil {
		err = output.Sync()
	}
	if closeErr := output.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(destination)
	}
	return err
}

func writeResult(operation Operation, resultError string) error {
	root, err := OperationDirectory(operation.StateRoot, operation.ID)
	if err != nil {
		return err
	}
	result := Result{
		Schema: OperationSchema, OperationID: operation.ID, Outcome: operation.Phase,
		PreviousVersion: operation.PreviousVersion, TargetVersion: operation.TargetVersion,
		CompletedAt: time.Now().UTC().Format(time.RFC3339Nano), Error: resultError,
	}
	return writeAtomicJSON(filepath.Join(root, "result.json"), result, 0o600)
}
