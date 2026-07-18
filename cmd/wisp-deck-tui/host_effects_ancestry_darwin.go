//go:build darwin

package main

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func lookupHostProcess(pid int) (hostProcessInfo, error) {
	if pid < 1 {
		return hostProcessInfo{}, fmt.Errorf("invalid process ID %d", pid)
	}
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return hostProcessInfo{}, fmt.Errorf("lookup process %d: %w", pid, err)
	}
	if process == nil || int(process.Proc.P_pid) != pid {
		return hostProcessInfo{}, fmt.Errorf("process %d identity mismatch", pid)
	}

	parentPID := int(process.Eproc.Ppid)
	if pid == 1 {
		if parentPID != 0 {
			return hostProcessInfo{}, fmt.Errorf(
				"PID 1 has unexpected parent %d",
				parentPID,
			)
		}
		return hostProcessInfo{ParentPID: 0}, nil
	}

	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return hostProcessInfo{}, fmt.Errorf("read process %d arguments: %w", pid, err)
	}
	executable, arguments, environment, err := parseKernProcArgs2(raw)
	if err != nil {
		return hostProcessInfo{}, fmt.Errorf("parse process %d arguments: %w", pid, err)
	}
	return hostProcessInfo{
		ParentPID:   parentPID,
		Executable:  executable,
		Arguments:   arguments,
		Environment: environment,
	}, nil
}
