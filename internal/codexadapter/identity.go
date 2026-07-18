package codexadapter

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var errCodexIdentityPersistence = errors.New("persist Codex session identity")
var errCodexIdentityObserver = errors.New("observer lost before Codex session identity was persisted")

// ValidateIdentityFilePath limits the adapter's mutable identity path to one
// direct .codex child of a real session-identities directory.
func ValidateIdentityFilePath(path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return errors.New("Codex session identity file must be absolute")
	}
	clean := filepath.Clean(path)
	if clean != path {
		return errors.New("Codex session identity file path must be clean")
	}
	name := filepath.Base(clean)
	if name == ".codex" || !strings.HasSuffix(name, ".codex") {
		return errors.New("Codex session identity file must have a non-empty .codex basename")
	}
	parent := filepath.Dir(clean)
	if filepath.Base(parent) != "session-identities" {
		return errors.New("Codex session identity file must be inside session-identities")
	}
	info, err := os.Lstat(parent)
	switch {
	case err == nil && info.Mode()&os.ModeSymlink != 0:
		return errors.New("Codex session identity directory must not be a symlink")
	case err == nil && !info.IsDir():
		return errors.New("Codex session identity parent is not a directory")
	case err != nil && !os.IsNotExist(err):
		return fmt.Errorf("inspect Codex session identity directory: %w", err)
	}
	return nil
}

func writeCodexIdentity(path, identity string) error {
	if err := validateCanonicalUUID("Codex session identity", identity); err != nil {
		return err
	}
	if err := ValidateIdentityFilePath(path); err != nil {
		return err
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
	if err := ValidateIdentityFilePath(path); err != nil {
		return err
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
