package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"

	"scriptboard/internal/kubeconfigmanager"
)

type brokerKubeconfigManager struct {
	db          *sql.DB
	stateRoot   string
	direct      kubeconfigmanager.DirectManager
	webDefaults []string
}

func newBrokerKubeconfigManager(db *sql.DB, stateRoot, allowedIdentity string) *brokerKubeconfigManager {
	manager := &brokerKubeconfigManager{db: db, stateRoot: filepath.Clean(stateRoot)}
	if account, err := user.Lookup(strings.TrimSpace(allowedIdentity)); err == nil && strings.TrimSpace(account.HomeDir) != "" {
		manager.webDefaults = append(manager.webDefaults, filepath.Join(account.HomeDir, ".kube", "config"))
	}
	return manager
}

func sameBrokerKubeconfigPath(left, right string) bool {
	left, right = filepath.Clean(strings.TrimSpace(left)), filepath.Clean(strings.TrimSpace(right))
	if left == "." || right == "." {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func (manager *brokerKubeconfigManager) allowed(ctx context.Context, path string) error {
	path = filepath.Clean(strings.TrimSpace(path))
	if !filepath.IsAbs(path) {
		return errors.New("kubeconfig path must be absolute")
	}
	paths, err := kubeconfigmanager.SuggestedPaths()
	if err != nil {
		return err
	}
	paths = append(paths, manager.webDefaults...)
	for _, candidate := range paths {
		if sameBrokerKubeconfigPath(candidate, path) {
			return nil
		}
	}
	rows, err := manager.db.QueryContext(ctx, `SELECT kubeconfig_path FROM kubernetes_connection`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var candidate string
		if err := rows.Scan(&candidate); err != nil {
			return err
		}
		if sameBrokerKubeconfigPath(candidate, path) {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return errors.New("kubeconfig path is not registered for local management")
}

func (manager *brokerKubeconfigManager) Exportable(ctx context.Context, path string) (bool, error) {
	if err := manager.allowed(ctx, path); err != nil {
		return false, err
	}
	relative, err := filepath.Rel(manager.stateRoot, filepath.Clean(path))
	if err != nil {
		return false, err
	}
	return relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)), nil
}

func (manager *brokerKubeconfigManager) Inspect(ctx context.Context, path string) (kubeconfigmanager.Snapshot, error) {
	if err := manager.allowed(ctx, path); err != nil {
		return kubeconfigmanager.Snapshot{}, err
	}
	return manager.direct.Inspect(ctx, path)
}

func (manager *brokerKubeconfigManager) Exists(ctx context.Context, path string) (bool, error) {
	if err := manager.allowed(ctx, path); err != nil {
		return false, err
	}
	return manager.direct.Exists(ctx, path)
}

func (manager *brokerKubeconfigManager) Download(ctx context.Context, path string) ([]byte, error) {
	exportable, err := manager.Exportable(ctx, path)
	if err != nil || !exportable {
		return nil, errors.New("privileged kubeconfig export is not allowed")
	}
	return manager.direct.Download(ctx, path)
}

func (manager *brokerKubeconfigManager) DownloadContext(ctx context.Context, path, name string) ([]byte, error) {
	exportable, err := manager.Exportable(ctx, path)
	if err != nil || !exportable {
		return nil, errors.New("privileged kubeconfig export is not allowed")
	}
	return manager.direct.DownloadContext(ctx, path, name)
}

func (manager *brokerKubeconfigManager) PreviewImport(ctx context.Context, path string, raw []byte) (kubeconfigmanager.ImportPreview, error) {
	if err := manager.allowed(ctx, path); err != nil {
		return kubeconfigmanager.ImportPreview{}, err
	}
	return manager.direct.PreviewImport(ctx, path, raw)
}

func (manager *brokerKubeconfigManager) Import(ctx context.Context, path string, raw []byte, useImportedCurrent bool) (kubeconfigmanager.ImportPreview, error) {
	if err := manager.allowed(ctx, path); err != nil {
		return kubeconfigmanager.ImportPreview{}, err
	}
	return manager.direct.Import(ctx, path, raw, useImportedCurrent)
}

func (manager *brokerKubeconfigManager) UseContext(ctx context.Context, path, name string) error {
	if err := manager.allowed(ctx, path); err != nil {
		return err
	}
	return manager.direct.UseContext(ctx, path, name)
}

func (manager *brokerKubeconfigManager) UpdateContext(ctx context.Context, path, name, cluster, userName, namespace string) error {
	if err := manager.allowed(ctx, path); err != nil {
		return err
	}
	return manager.direct.UpdateContext(ctx, path, name, cluster, userName, namespace)
}

func (manager *brokerKubeconfigManager) RenameContext(ctx context.Context, path, oldName, newName string) error {
	if err := manager.allowed(ctx, path); err != nil {
		return err
	}
	return manager.direct.RenameContext(ctx, path, oldName, newName)
}

func (manager *brokerKubeconfigManager) DeleteContext(ctx context.Context, path, name string) error {
	if err := manager.allowed(ctx, path); err != nil {
		return err
	}
	return manager.direct.DeleteContext(ctx, path, name)
}

var _ kubeconfigmanager.Manager = (*brokerKubeconfigManager)(nil)
