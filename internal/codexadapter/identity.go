package codexadapter

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var errCodexIdentityPersistence = errors.New("persist Codex session identity")

func writeCodexIdentity(path, identity string) error {
	if err := validateCanonicalUUID("Codex session identity", identity); err != nil {
		return err
	}
	if path == "" || !filepath.IsAbs(path) {
		return fmt.Errorf("Codex session identity file must be absolute")
	}

	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create Codex session identity directory: %w", err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		return fmt.Errorf("secure Codex session identity directory: %w", err)
	}

	temporary, err := os.CreateTemp(parent, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create Codex session identity temp file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()

	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("secure Codex session identity temp file: %w", err)
	}
	if _, err := fmt.Fprintf(temporary, "%s\n", identity); err != nil {
		return fmt.Errorf("write Codex session identity temp file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync Codex session identity temp file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close Codex session identity temp file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace Codex session identity: %w", err)
	}
	if err := syncCodexIdentityDirectory(parent); err != nil {
		return fmt.Errorf("sync Codex session identity directory: %w", err)
	}
	return nil
}

func clearCodexIdentity(path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return fmt.Errorf("Codex session identity file must be absolute")
	}
	parent := filepath.Dir(path)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("remove Codex session identity: %w", err)
	}
	if err := syncCodexIdentityDirectory(parent); err != nil {
		return fmt.Errorf("sync Codex session identity directory: %w", err)
	}
	return nil
}

func syncCodexIdentityDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
