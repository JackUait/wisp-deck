package util

import (
	"fmt"
	"path/filepath"
	"strings"
)

// deviceWithRdev is the tty* entry in dir whose device number is rdev.
func deviceWithRdev(dir string, names []string, rdev uint64, rdevOf func(name string) (uint64, error)) (string, error) {
	for _, name := range names {
		if !strings.HasPrefix(name, "tty") {
			continue
		}
		if got, err := rdevOf(name); err == nil && got == rdev {
			return filepath.Join(dir, name), nil
		}
	}
	return "", fmt.Errorf("no terminal device %d in %s", rdev, dir)
}
