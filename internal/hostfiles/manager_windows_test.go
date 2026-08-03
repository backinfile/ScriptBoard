//go:build windows

package hostfiles_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"scriptboard/internal/hostfiles"
)

func TestManagerProtectsWindowsShortPathAliases(t *testing.T) {
	t.Parallel()

	longPath, shortPath := windowsAliasPair(t)
	manager, err := hostfiles.Open(hostfiles.Options{ProtectedPaths: []string{longPath}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CanonicalDirectory(shortPath); !errors.Is(err, hostfiles.ErrProtected) {
		t.Fatalf("short alias %q for protected path %q returned %v, want ErrProtected", shortPath, longPath, err)
	}
}

func TestWindowsShortPathAliasesContainNonexistentChildren(t *testing.T) {
	t.Parallel()

	longPath, shortPath := windowsAliasPair(t)
	child := filepath.Join(shortPath, "尚未创建.ps1")
	if !hostfiles.Contains(longPath, child) {
		t.Fatalf("long path %q does not contain nonexistent child through short alias %q", longPath, child)
	}
	destination := filepath.Join(longPath, "归档")
	rebased, err := hostfiles.Rebase(longPath, destination, child)
	if err != nil {
		t.Fatalf("rebase nonexistent short-alias child: %v", err)
	}
	if want := filepath.Join(destination, "尚未创建.ps1"); rebased != want {
		t.Fatalf("rebased path = %q, want %q", rebased, want)
	}
}

func windowsAliasPair(t *testing.T) (string, string) {
	t.Helper()

	candidates := []string{t.TempDir(), os.TempDir()}
	if programData := strings.TrimSpace(os.Getenv("ProgramData")); programData != "" {
		candidates = append(candidates, programData)
		if entries, err := os.ReadDir(programData); err == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					candidates = append(candidates, filepath.Join(programData, entry.Name()))
				}
			}
		}
	}
	for _, candidate := range candidates {
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		shortPath, err := windowsShortPath(absolute)
		if err == nil && !strings.EqualFold(filepath.Clean(shortPath), filepath.Clean(absolute)) {
			return absolute, shortPath
		}
	}
	t.Skip("this Windows volume exposes no accessible 8.3 path aliases")
	return "", ""
}

func windowsShortPath(path string) (string, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	buffer := make([]uint16, 32_768)
	length, err := windows.GetShortPathName(pointer, &buffer[0], uint32(len(buffer)))
	if err != nil {
		return "", err
	}
	return windows.UTF16ToString(buffer[:length]), nil
}
