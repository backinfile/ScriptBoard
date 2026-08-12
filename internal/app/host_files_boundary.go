package app

import (
	"context"
	"os"
	"time"

	"scriptboard/internal/hostfiles"
)

type remoteHostFileInfo struct{ value remoteHostFileInfoValue }

type remoteHostFileInfoValue struct {
	name       string
	size       int64
	mode       os.FileMode
	modifiedAt time.Time
	directory  bool
}

func (info remoteHostFileInfo) Name() string       { return info.value.name }
func (info remoteHostFileInfo) Size() int64        { return info.value.size }
func (info remoteHostFileInfo) Mode() os.FileMode  { return info.value.mode }
func (info remoteHostFileInfo) ModTime() time.Time { return info.value.modifiedAt }
func (info remoteHostFileInfo) IsDir() bool        { return info.value.directory }
func (info remoteHostFileInfo) Sys() any           { return nil }

func (a *App) hostRoots(ctx context.Context) ([]hostfiles.Entry, error) {
	if a.hostFilesBackend != nil {
		return a.hostFilesBackend.Roots(ctx)
	}
	return a.files.Roots()
}

func (a *App) hostList(ctx context.Context, path string) ([]hostfiles.Entry, error) {
	if a.hostFilesBackend != nil {
		return a.hostFilesBackend.List(ctx, path)
	}
	return a.files.List(path)
}

func (a *App) hostInfo(ctx context.Context, path string) (os.FileInfo, bool, error) {
	if a.hostFilesBackend != nil {
		value, err := a.hostFilesBackend.Info(ctx, path)
		return remoteHostFileInfo{value: remoteHostFileInfoValue{name: value.Name, size: value.Size, mode: value.Mode, modifiedAt: value.ModifiedAt, directory: value.Directory}}, value.CanMutate, err
	}
	value, err := a.files.Info(path)
	return value, a.files.CanMutate(path), err
}

func (a *App) hostReadText(ctx context.Context, path string, maxBytes int64) (hostfiles.TextDocument, error) {
	if a.hostFilesBackend != nil {
		return a.hostFilesBackend.ReadText(ctx, path, maxBytes)
	}
	return a.files.ReadText(path, maxBytes)
}

func (a *App) hostCanonicalExisting(ctx context.Context, path string) (string, error) {
	if a.hostFilesBackend != nil {
		return a.hostFilesBackend.CanonicalExisting(ctx, path)
	}
	return a.files.CanonicalExisting(path)
}

func (a *App) hostCanonicalDirectory(ctx context.Context, path string) (string, error) {
	if a.hostFilesBackend != nil {
		return a.hostFilesBackend.CanonicalDirectory(ctx, path)
	}
	return a.files.CanonicalDirectory(path)
}

func (a *App) hostCanonicalDestination(ctx context.Context, path string) (string, error) {
	if a.hostFilesBackend != nil {
		return a.hostFilesBackend.CanonicalDestination(ctx, path)
	}
	return a.files.CanonicalDestination(path)
}

func (a *App) hostDestination(ctx context.Context, directory, name string) (string, error) {
	if a.hostFilesBackend != nil {
		return a.hostFilesBackend.Destination(ctx, directory, name)
	}
	return a.files.Destination(directory, name)
}

func (a *App) hostAvailableName(ctx context.Context, directory, name string) (string, error) {
	if a.hostFilesBackend != nil {
		return a.hostFilesBackend.AvailableName(ctx, directory, name)
	}
	return a.files.AvailableName(directory, name)
}

func (a *App) hostCreateDirectory(ctx context.Context, directory, name string) error {
	if a.hostFilesBackend != nil {
		return a.hostFilesBackend.CreateDirectory(ctx, directory, name)
	}
	return a.files.CreateDirectory(directory, name)
}

func (a *App) hostToggleOwnerExecute(ctx context.Context, path string) (bool, error) {
	if a.hostFilesBackend != nil {
		return a.hostFilesBackend.ToggleOwnerExecute(ctx, path)
	}
	return a.files.ToggleOwnerExecute(path)
}

func (a *App) hostMoveToTrash(ctx context.Context, path, storedName string) (hostfiles.Trashed, error) {
	if a.hostFilesBackend != nil {
		return a.hostFilesBackend.MoveToTrash(ctx, path, storedName)
	}
	return a.files.MoveToTrash(path, storedName)
}

func (a *App) hostRestoreFromTrash(ctx context.Context, storedPath, original string) error {
	if a.hostFilesBackend != nil {
		return a.hostFilesBackend.RestoreFromTrash(ctx, storedPath, original)
	}
	return a.files.RestoreFromTrash(storedPath, original)
}

func (a *App) hostRestoreFromTrashToAvailablePath(ctx context.Context, storedPath, original string) (string, error) {
	if a.hostFilesBackend != nil {
		return a.hostFilesBackend.RestoreFromTrashToAvailablePath(ctx, storedPath, original)
	}
	return a.files.RestoreFromTrashToAvailablePath(storedPath, original)
}

func (a *App) hostPurgeTrash(ctx context.Context, storedPath string) error {
	if a.hostFilesBackend != nil {
		return a.hostFilesBackend.PurgeTrash(ctx, storedPath)
	}
	return a.files.PurgeTrash(storedPath)
}

func (a *App) hostMove(ctx context.Context, source, destination string) error {
	if a.hostFilesBackend != nil {
		return a.hostFilesBackend.Move(ctx, source, destination)
	}
	return a.files.Move(source, destination)
}
