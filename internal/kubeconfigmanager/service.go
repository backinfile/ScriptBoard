package kubeconfigmanager

import (
	"context"
	"errors"
	"os"
)

// Manager is the fixed kubeconfig-management boundary used by the Web control
// plane. Managed installs provide a privileged Broker implementation; portable
// installs use DirectManager in the current process.
type Manager interface {
	Exists(context.Context, string) (bool, error)
	Inspect(context.Context, string) (Snapshot, error)
	Download(context.Context, string) ([]byte, error)
	DownloadContext(context.Context, string, string) ([]byte, error)
	PreviewImport(context.Context, string, []byte) (ImportPreview, error)
	Import(context.Context, string, []byte, bool) (ImportPreview, error)
	UseContext(context.Context, string, string) error
	UpdateContext(context.Context, string, string, string, string, string) error
	RenameContext(context.Context, string, string, string) error
	DeleteContext(context.Context, string, string) error
}

type DirectManager struct{}

func (DirectManager) Exists(_ context.Context, path string) (bool, error) {
	path, err := cleanAbsolutePath(path)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil && info.Mode().IsRegular(), err
}

func (DirectManager) Inspect(_ context.Context, path string) (Snapshot, error) {
	return Inspect(path)
}

func (DirectManager) Download(_ context.Context, path string) ([]byte, error) {
	return Download(path)
}

func (DirectManager) DownloadContext(_ context.Context, path, name string) ([]byte, error) {
	return DownloadContext(path, name)
}

func (DirectManager) PreviewImport(_ context.Context, path string, raw []byte) (ImportPreview, error) {
	return PreviewImport(path, raw)
}

func (DirectManager) Import(_ context.Context, path string, raw []byte, useImportedCurrent bool) (ImportPreview, error) {
	return Import(path, raw, useImportedCurrent)
}

func (DirectManager) UseContext(_ context.Context, path, name string) error {
	return UseContext(path, name)
}

func (DirectManager) UpdateContext(_ context.Context, path, name, cluster, user, namespace string) error {
	return UpdateContext(path, name, cluster, user, namespace)
}

func (DirectManager) RenameContext(_ context.Context, path, oldName, newName string) error {
	return RenameContext(path, oldName, newName)
}

func (DirectManager) DeleteContext(_ context.Context, path, name string) error {
	return DeleteContext(path, name)
}

var _ Manager = DirectManager{}
