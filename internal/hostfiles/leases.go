package hostfiles

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

var ErrPathBusy = errors.New("host path is leased by another operation")

func (m *Manager) AcquireLease(id string, paths ...string) error {
	id = strings.TrimSpace(id)
	if id == "" || len(paths) == 0 {
		return fmt.Errorf("lease ID and paths are required")
	}
	canonical := make([]string, 0, len(paths))
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("leased host path must be absolute")
		}
		canonical = append(canonical, filepath.Clean(path))
	}
	m.leaseMu.Lock()
	defer m.leaseMu.Unlock()
	if existing, exists := m.leases[id]; exists {
		if len(existing) != len(canonical) {
			return fmt.Errorf("lease %q already exists with different paths", id)
		}
		for index := range existing {
			if ComparisonKey(existing[index]) != ComparisonKey(canonical[index]) {
				return fmt.Errorf("lease %q already exists with different paths", id)
			}
		}
		return nil
	}
	for owner, heldPaths := range m.leases {
		for _, held := range heldPaths {
			for _, candidate := range canonical {
				if pathContains(held, candidate) || pathContains(candidate, held) {
					return fmt.Errorf("%w: %s conflicts with %s (%s)", ErrPathBusy, candidate, held, owner)
				}
			}
		}
	}
	m.leases[id] = canonical
	return nil
}

func (m *Manager) ReleaseLease(id string) {
	m.leaseMu.Lock()
	delete(m.leases, id)
	m.leaseMu.Unlock()
}

func (m *Manager) LeaseConflicts(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	candidate := filepath.Clean(path)
	m.leaseMu.Lock()
	defer m.leaseMu.Unlock()
	for _, paths := range m.leases {
		for _, held := range paths {
			if pathContains(held, candidate) || pathContains(candidate, held) {
				return true
			}
		}
	}
	return false
}
