package ai

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ErrSecretNotFound = errors.New("AI secret not found")

type SecretVault struct {
	root string
}

func OpenSecretVault(stateRoot string) (*SecretVault, error) {
	root := filepath.Join(stateRoot, "secrets", "ai")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create AI secret directory: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, fmt.Errorf("restrict AI secret directory: %w", err)
	}
	return &SecretVault{root: root}, nil
}

func (v *SecretVault) Write(_ context.Context, value string) (string, error) {
	id, err := randomAIID()
	if err != nil {
		return "", err
	}
	path := filepath.Join(v.root, id)
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		return "", fmt.Errorf("write AI secret: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("restrict AI secret: %w", err)
	}
	return id, nil
}

func (v *SecretVault) Read(reference string) (string, error) {
	if !validSecretReference(reference) {
		return "", ErrSecretNotFound
	}
	content, err := os.ReadFile(filepath.Join(v.root, reference))
	if os.IsNotExist(err) {
		return "", ErrSecretNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read AI secret: %w", err)
	}
	return string(content), nil
}

func (v *SecretVault) Delete(reference string) error {
	if !validSecretReference(reference) {
		return ErrSecretNotFound
	}
	err := os.Remove(filepath.Join(v.root, reference))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func validSecretReference(value string) bool {
	return value != "" && value == filepath.Base(value) && !strings.ContainsAny(value, `/\`)
}
