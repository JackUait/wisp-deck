//go:build !darwin

package util

// ControllingTerminalPath falls back to /dev/tty off macOS, where kqueue's
// refusal of it does not apply.
func ControllingTerminalPath() (string, error) { return "/dev/tty", nil }
