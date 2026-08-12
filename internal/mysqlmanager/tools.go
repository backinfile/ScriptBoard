package mysqlmanager

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
)

type ToolSettings struct {
	DumpExecutable, ClientExecutable string
}

type ToolStatus struct {
	DumpExecutable, ClientExecutable string
	DumpVersion, ClientVersion       string
	DumpOK, ClientOK                 bool
}

func (m *Manager) Tools() ToolSettings {
	return m.backend.Tools()
}

func (m *Manager) localTools() ToolSettings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return ToolSettings{DumpExecutable: m.dumpTool, ClientExecutable: m.clientTool}
}

func (m *Manager) SetTools(ctx context.Context, settings ToolSettings) error {
	return m.backend.SetTools(ctx, settings)
}

func (m *Manager) setLocalTools(ctx context.Context, settings ToolSettings) error {
	settings.DumpExecutable, settings.ClientExecutable = strings.TrimSpace(settings.DumpExecutable), strings.TrimSpace(settings.ClientExecutable)
	if !validToolPath(settings.DumpExecutable) || !validToolPath(settings.ClientExecutable) {
		return errors.New("MySQL tools must be command names from PATH or absolute executable paths")
	}
	transaction, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	for key, value := range map[string]string{"dump_executable": settings.DumpExecutable, "client_executable": settings.ClientExecutable} {
		if _, err := transaction.ExecContext(ctx, `INSERT INTO mysql_settings(key,value,updated_at) VALUES (?,?,?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, value, m.now().UTC().UnixNano()); err != nil {
			return err
		}
	}
	if err := transaction.Commit(); err != nil {
		return err
	}
	m.mu.Lock()
	m.dumpTool, m.clientTool = settings.DumpExecutable, settings.ClientExecutable
	m.dumpFlavor, m.flavorTool = "", ""
	m.mu.Unlock()
	return nil
}

func (m *Manager) dumpArguments(ctx context.Context, optionPath, database string) []string {
	settings := m.localTools()
	m.mu.Lock()
	flavor, flavorTool := m.dumpFlavor, m.flavorTool
	m.mu.Unlock()
	if flavor == "" || flavorTool != settings.DumpExecutable {
		output := &boundedBuffer{maximum: 8 << 10}
		if err := m.runner.Run(ctx, settings.DumpExecutable, []string{"--version"}, nil, output, io.Discard); err == nil && strings.Contains(strings.ToLower(output.String()), "mariadb") {
			flavor = "mariadb"
		} else {
			flavor = "mysql"
		}
		m.mu.Lock()
		m.dumpFlavor, m.flavorTool = flavor, settings.DumpExecutable
		m.mu.Unlock()
	}
	arguments := []string{"--defaults-extra-file=" + optionPath, "--single-transaction", "--quick", "--routines", "--events", "--triggers", "--no-tablespaces"}
	if flavor != "mariadb" {
		arguments = append(arguments, "--set-gtid-purged=OFF")
	}
	return append(arguments, "--hex-blob", "--default-character-set=utf8mb4", "--", database)
}

func (m *Manager) TestTools(ctx context.Context) ToolStatus {
	return m.backend.TestTools(ctx)
}

func (m *Manager) testLocalTools(ctx context.Context) ToolStatus {
	settings := m.localTools()
	result := ToolStatus{DumpExecutable: settings.DumpExecutable, ClientExecutable: settings.ClientExecutable}
	for executable, target := range map[string]*struct {
		version *string
		ok      *bool
	}{
		settings.DumpExecutable:   {&result.DumpVersion, &result.DumpOK},
		settings.ClientExecutable: {&result.ClientVersion, &result.ClientOK},
	} {
		output := &boundedBuffer{maximum: 8 << 10}
		err := m.runner.Run(ctx, executable, []string{"--version"}, nil, output, io.Discard)
		*target.version, *target.ok = strings.TrimSpace(output.String()), err == nil
	}
	return result
}

func validToolPath(value string) bool {
	if value == "" {
		return false
	}
	return filepath.IsAbs(value) || filepath.Base(value) == value
}
