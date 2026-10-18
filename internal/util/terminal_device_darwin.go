//go:build darwin

package util

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// ControllingTerminalPath is the real device (/dev/ttysNNN) behind this
// process's controlling terminal. A Bun child such as claude dies with
// "EINVAL: kqueue" when handed /dev/tty itself.
func ControllingTerminalPath() (string, error) {
	proc, err := unix.SysctlKinfoProc("kern.proc.pid", os.Getpid())
	if err != nil {
		return "", err
	}
	if proc.Eproc.Tdev == -1 {
		return "", errors.New("no controlling terminal")
	}
	entries, err := os.ReadDir("/dev")
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return deviceWithRdev("/dev", names, uint64(uint32(proc.Eproc.Tdev)), func(name string) (uint64, error) {
		var st unix.Stat_t
		if err := unix.Stat(filepath.Join("/dev", name), &st); err != nil {
			return 0, err
		}
		if st.Mode&unix.S_IFMT != unix.S_IFCHR {
			return 0, errors.New("not a character device")
		}
		return uint64(uint32(st.Rdev)), nil
	})
}
