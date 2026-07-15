// Package opencodeadapter supervises OpenCode's plugin-free semantic attention
// runtime for one Wisp Deck generation.
package opencodeadapter

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var generationName = regexp.MustCompile(`^generation\.[A-Za-z0-9]+$`)

const silentTUIConfig = `{
  "attention": {
    "enabled": false,
    "notifications": false,
    "sound": false
  }
}
`

// WriteSilentTUIConfig atomically publishes the private TUI configuration in
// an already-created attention generation. It never creates the generation.
func WriteSilentTUIConfig(generationDir string) (string, error) {
	if generationDir == "" || !filepath.IsAbs(generationDir) {
		return "", errors.New("OpenCode generation directory must be absolute")
	}
	generationDir = filepath.Clean(generationDir)
	if !generationName.MatchString(filepath.Base(generationDir)) {
		return "", fmt.Errorf("invalid OpenCode generation directory %q", generationDir)
	}
	info, err := os.Lstat(generationDir)
	if err != nil {
		return "", fmt.Errorf("stat OpenCode generation directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("OpenCode generation path is not a directory")
	}

	target := filepath.Join(generationDir, "opencode-tui.json")
	temporary, err := os.CreateTemp(generationDir, ".opencode-tui.*")
	if err != nil {
		return "", fmt.Errorf("create OpenCode TUI config: %w", err)
	}
	temporaryPath := temporary.Name()
	keep := false
	defer func() {
		_ = temporary.Close()
		if !keep {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return "", fmt.Errorf("protect OpenCode TUI config: %w", err)
	}
	if _, err := temporary.WriteString(silentTUIConfig); err != nil {
		return "", fmt.Errorf("write OpenCode TUI config: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return "", fmt.Errorf("sync OpenCode TUI config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("close OpenCode TUI config: %w", err)
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return "", fmt.Errorf("publish OpenCode TUI config: %w", err)
	}
	keep = true
	info, err = os.Lstat(target)
	if err != nil {
		return "", fmt.Errorf("verify OpenCode TUI config: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 {
		return "", errors.New("published OpenCode TUI config is not a private regular file")
	}
	return target, nil
}
