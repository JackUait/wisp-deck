//go:build !darwin

package main

import "fmt"

func lookupHostProcess(pid int) (hostProcessInfo, error) {
	return hostProcessInfo{}, fmt.Errorf(
		"host process ancestry is unsupported for PID %d",
		pid,
	)
}
