package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"scriptboard/internal/auditlog"
	"scriptboard/internal/config"
)

const emergencySource = "local-emergency-cli"

func runEmergency(action string, arguments []string) error {
	switch action {
	case "pause-external":
		confirmation, remaining := takeStringArgument(arguments, "--confirm")
		if confirmation != "PAUSE-EXTERNAL" {
			return errors.New("emergency pause-external 需要 --confirm PAUSE-EXTERNAL")
		}
		loaded, err := config.Load(remaining, os.Getenv)
		if err != nil {
			return err
		}
		database, err := openEmergencyDatabase(loaded.StateRoot)
		if err != nil {
			return err
		}
		defer database.Close()
		if err := emergencyMutation(context.Background(), database, func(ctx context.Context, tx *sql.Tx) (auditlog.Event, error) {
			result, err := tx.ExecContext(ctx, "UPDATE external_trigger_control SET enabled = 0, updated_at = ? WHERE id = 1", time.Now().UTC().Unix())
			if err != nil {
				return auditlog.Event{}, err
			}
			changed, err := result.RowsAffected()
			if err != nil {
				return auditlog.Event{}, err
			}
			if changed != 1 {
				return auditlog.Event{}, sql.ErrNoRows
			}
			return localEmergencyEvent("emergency.external.pause", "external-interfaces"), nil
		}); err != nil {
			return fmt.Errorf("暂停外部接口: %w", err)
		}
		fmt.Fprintln(os.Stdout, "所有外部接口已暂停；该操作已写入审计链。")
		return nil
	case "revoke-key":
		keyID, remaining := takeStringArgument(arguments, "--key-id")
		confirmation, remaining := takeStringArgument(remaining, "--confirm-key-id")
		if !validEmergencyIdentifier(keyID) || confirmation != keyID {
			return errors.New("emergency revoke-key 需要有效且匹配的 --key-id ID 与 --confirm-key-id ID")
		}
		loaded, err := config.Load(remaining, os.Getenv)
		if err != nil {
			return err
		}
		database, err := openEmergencyDatabase(loaded.StateRoot)
		if err != nil {
			return err
		}
		defer database.Close()
		if err := emergencyMutation(context.Background(), database, func(ctx context.Context, tx *sql.Tx) (auditlog.Event, error) {
			result, err := tx.ExecContext(ctx, "UPDATE external_trigger_keys SET enabled = 0, updated_at = ? WHERE id = ?", time.Now().UTC().Unix(), keyID)
			if err != nil {
				return auditlog.Event{}, err
			}
			changed, err := result.RowsAffected()
			if err != nil {
				return auditlog.Event{}, err
			}
			if changed != 1 {
				return auditlog.Event{}, sql.ErrNoRows
			}
			return localEmergencyEvent("emergency.external-key.revoke", keyID), nil
		}); err != nil {
			return fmt.Errorf("吊销外部 Key %q: %w", keyID, err)
		}
		fmt.Fprintf(os.Stdout, "外部 Key %s 已吊销并保留取证元数据；该操作已写入审计链。\n", keyID)
		return nil
	case "export-evidence":
		output, remaining := takeStringArgument(arguments, "--output")
		if output == "" || !filepath.IsAbs(output) {
			return errors.New("emergency export-evidence 需要绝对路径 --output PATH")
		}
		loaded, err := config.Load(remaining, os.Getenv)
		if err != nil {
			return err
		}
		database, err := openEmergencyDatabaseReadOnly(loaded.StateRoot)
		if err != nil {
			return err
		}
		defer database.Close()
		return exportEmergencyEvidence(context.Background(), database, output)
	default:
		return fmt.Errorf("未知应急命令 %q；可用命令：emergency pause-external|revoke-key|export-evidence", action)
	}
}

func openEmergencyDatabase(stateRoot string) (*sql.DB, error) {
	databasePath := filepath.ToSlash(filepath.Join(stateRoot, "app.db"))
	database, err := sql.Open("sqlite", "file:"+databasePath+"?mode=rw&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if err := database.Ping(); err != nil {
		database.Close()
		return nil, fmt.Errorf("打开 ScriptBoard 状态数据库: %w", err)
	}
	return database, nil
}

func openEmergencyDatabaseReadOnly(stateRoot string) (*sql.DB, error) {
	databasePath := filepath.ToSlash(filepath.Join(stateRoot, "app.db"))
	database, err := sql.Open("sqlite", "file:"+databasePath+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if err := database.Ping(); err != nil {
		database.Close()
		return nil, fmt.Errorf("只读打开 ScriptBoard 状态数据库: %w", err)
	}
	return database, nil
}

func emergencyMutation(ctx context.Context, database *sql.DB, mutate func(context.Context, *sql.Tx) (auditlog.Event, error)) error {
	store := auditlog.New(database)
	transaction, err := store.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	event, err := mutate(ctx, transaction.SQL())
	if err != nil {
		return err
	}
	if _, err := transaction.Append(ctx, event); err != nil {
		return err
	}
	return transaction.Commit()
}

func localEmergencyEvent(action, target string) auditlog.Event {
	return auditlog.Event{
		OccurredAt:              strconv.FormatInt(time.Now().UTC().Unix(), 10),
		Action:                  action,
		Target:                  target,
		Result:                  "succeeded",
		SourceAddress:           emergencySource,
		ActorRole:               "local-administrator",
		AuthenticationAssurance: "local-os-access",
	}
}

func exportEmergencyEvidence(ctx context.Context, database *sql.DB, output string) (resultErr error) {
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		return fmt.Errorf("创建取证导出目录: %w", err)
	}
	destination, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("创建取证导出（不会覆盖已有文件）: %w", err)
	}
	committed := false
	defer func() {
		if closeErr := destination.Close(); resultErr == nil && closeErr != nil {
			resultErr = closeErr
		}
		if !committed {
			_ = os.Remove(output)
		}
	}()
	verification, err := auditlog.New(database).Export(ctx, destination, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("审计链验证或取证导出失败: %w", err)
	}
	if err := destination.Sync(); err != nil {
		return fmt.Errorf("同步取证导出: %w", err)
	}
	committed = true
	fmt.Fprintf(os.Stdout, "取证证据已导出到 %s：%d 条事件，链尾 %s\n", output, verification.Count, verification.LastHash)
	return nil
}

func validEmergencyIdentifier(value string) bool {
	if value == "" || len(value) > 128 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	return strings.IndexFunc(value, unicode.IsControl) < 0
}
