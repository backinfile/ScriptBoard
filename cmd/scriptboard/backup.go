package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"scriptboard/internal/auditlog"
	"scriptboard/internal/buildinfo"
	"scriptboard/internal/config"
	"scriptboard/internal/statebackup"
)

const maximumBackupPassphraseBytes = 4096

func runBackup(action string, arguments []string) error {
	jsonOutput, arguments := takeBooleanArgument(arguments, "--json")
	archivePath, arguments := takeStringArgument(arguments, "--archive")
	outputPath, arguments := takeStringArgument(arguments, "--output")
	passphrasePath, arguments := takeStringArgument(arguments, "--passphrase-file")
	confirmation, arguments := takeStringArgument(arguments, "--confirm-backup-id")
	passphrase, err := readBackupPassphrase(passphrasePath)
	if err != nil {
		return err
	}
	defer clearBytes(passphrase)
	ctx := context.Background()
	switch action {
	case "create":
		if outputPath == "" || !filepath.IsAbs(outputPath) || archivePath != "" || confirmation != "" {
			return errors.New("backup create 需要绝对路径 --output PATH 和 --passphrase-file PATH")
		}
		loaded, err := config.Load(arguments, os.Getenv)
		if err != nil {
			return err
		}
		database, err := openEmergencyDatabase(loaded.StateRoot)
		if err != nil {
			return err
		}
		defer database.Close()
		audit := auditlog.New(database)
		checkpoint, err := openSignedAuditCheckpoint(loaded.StateRoot, false)
		if err != nil {
			return err
		}
		if err := checkpoint.VerifyOrBootstrap(ctx, audit, time.Now().UTC()); err != nil {
			return fmt.Errorf("创建备份前验证签名审计 checkpoint: %w", err)
		}
		checkpointDocument, err := checkpoint.TrustedDocument()
		if err != nil {
			return err
		}
		manager, err := statebackup.New(statebackup.Options{StateRoot: loaded.StateRoot, Database: database})
		if err != nil {
			return err
		}
		artifact, err := manager.Create(ctx, statebackup.CreateRequest{Destination: outputPath, Passphrase: passphrase, AuditCheckpoint: checkpointDocument})
		if err != nil {
			return fmt.Errorf("创建私有状态备份: %w", err)
		}
		if err := emergencyMutation(ctx, database, loaded.StateRoot, func(context.Context, *sql.Tx) (auditlog.Event, error) {
			return localEmergencyEvent("state_backup.create", artifact.Manifest.ID), nil
		}); err != nil {
			_ = os.Remove(artifact.Path)
			return fmt.Errorf("备份已撤销，因为无法写入签名审计链: %w", err)
		}
		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(artifact)
		}
		fmt.Fprintf(os.Stdout, "私有状态备份已创建：%s\nBackup ID：%s\nSchema：%d\n", artifact.Path, artifact.Manifest.ID, artifact.Manifest.SchemaVersion)
		return nil
	case "inspect":
		if archivePath == "" || !filepath.IsAbs(archivePath) || outputPath != "" || confirmation != "" || len(arguments) != 0 {
			return errors.New("backup inspect 需要绝对路径 --archive PATH 和 --passphrase-file PATH")
		}
		manifest, err := statebackup.Inspect(ctx, archivePath, passphrase)
		if err != nil {
			return fmt.Errorf("验证私有状态备份: %w", err)
		}
		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(manifest)
		}
		fmt.Fprintf(os.Stdout, "私有状态备份有效：%s\nBackup ID：%s\nSchema：%d\n文件：%d\n", archivePath, manifest.ID, manifest.SchemaVersion, len(manifest.Files))
		return nil
	case "restore":
		if archivePath == "" || !filepath.IsAbs(archivePath) || outputPath != "" || confirmation == "" {
			return errors.New("backup restore 需要 --archive PATH、--passphrase-file PATH 与 --confirm-backup-id ID")
		}
		loaded, err := config.Load(arguments, os.Getenv)
		if err != nil {
			return err
		}
		currentDatabase, err := openEmergencyDatabase(loaded.StateRoot)
		if err != nil {
			return err
		}
		checkpoint, err := openSignedAuditCheckpoint(loaded.StateRoot, false)
		if err != nil {
			_ = currentDatabase.Close()
			return err
		}
		if err := checkpoint.VerifyOrBootstrap(ctx, auditlog.New(currentDatabase), time.Now().UTC()); err != nil {
			_ = currentDatabase.Close()
			return fmt.Errorf("恢复前验证当前签名审计 checkpoint: %w", err)
		}
		previousCheckpoint, err := checkpoint.TrustedDocument()
		if err != nil {
			_ = currentDatabase.Close()
			return err
		}
		if err := currentDatabase.Close(); err != nil {
			return fmt.Errorf("恢复前关闭当前数据库: %w", err)
		}
		result, err := statebackup.Restore(ctx, statebackup.RestoreRequest{
			StateRoot: loaded.StateRoot, ArchivePath: archivePath, Passphrase: passphrase, ConfirmBackupID: confirmation,
			MinimumSchemaVersion: 20, MaximumSchemaVersion: buildinfo.DatabaseSchemaVersion,
			ValidateStaged: func(ctx context.Context, databasePath string, manifest statebackup.Manifest) error {
				database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(databasePath)+"?mode=ro")
				if err != nil {
					return err
				}
				defer database.Close()
				_, err = checkpoint.VerifyDetached(ctx, auditlog.New(database), manifest.AuditCheckpoint)
				return err
			},
			Finalize: func(ctx context.Context, restored statebackup.RestoreResult) error {
				if err := preserveRestoreCheckpoint(restored.PreservedStatePath, previousCheckpoint); err != nil {
					return err
				}
				database, err := openEmergencyDatabase(loaded.StateRoot)
				if err != nil {
					return err
				}
				defer database.Close()
				return checkpoint.ReanchorRestoredState(ctx, auditlog.New(database), previousCheckpoint, restored.Manifest.AuditCheckpoint, localEmergencyEvent("state_backup.restore", restored.Manifest.ID), time.Now().UTC())
			},
		})
		if err != nil {
			return fmt.Errorf("恢复私有状态备份: %w", err)
		}
		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(result)
		}
		fmt.Fprintf(os.Stdout, "私有状态已从 Backup ID %s 恢复；恢复前状态保留在 %s。服务保持停止。\n", result.Manifest.ID, result.PreservedStatePath)
		return nil
	default:
		return fmt.Errorf("未知备份命令 %q；可用命令：backup create|inspect|restore", action)
	}
}

func preserveRestoreCheckpoint(directory string, body []byte) error {
	path := filepath.Join(directory, "external-audit-checkpoint.before-restore.json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("保留恢复前审计 checkpoint: %w", err)
	}
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func readBackupPassphrase(rawPath string) ([]byte, error) {
	if rawPath == "" || !filepath.IsAbs(rawPath) {
		return nil, errors.New("备份命令需要绝对路径 --passphrase-file PATH；口令不能通过参数或环境传入")
	}
	info, err := os.Lstat(rawPath)
	if err != nil {
		return nil, fmt.Errorf("检查备份口令文件: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maximumBackupPassphraseBytes {
		return nil, errors.New("备份口令文件必须是不超过 4096 字节的普通文件且不能是链接")
	}
	file, err := os.Open(rawPath)
	if err != nil {
		return nil, fmt.Errorf("打开备份口令文件: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) {
		return nil, errors.New("备份口令文件在检查后发生变化")
	}
	value, err := io.ReadAll(io.LimitReader(file, maximumBackupPassphraseBytes+1))
	if err != nil || len(value) > maximumBackupPassphraseBytes {
		return nil, errors.New("读取备份口令文件失败或内容过大")
	}
	value = bytes.TrimSuffix(value, []byte("\r\n"))
	value = bytes.TrimSuffix(value, []byte("\n"))
	if len(value) < 16 {
		clearBytes(value)
		return nil, errors.New("备份口令至少需要 16 字节")
	}
	return value, nil
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
