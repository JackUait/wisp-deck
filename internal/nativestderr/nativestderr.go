//go:build unix

// Package nativestderr keeps macOS system libraries from writing onto the
// terminal a wisp-deck helper shares with an agent.
package nativestderr

import (
	"log"
	"os"
	"runtime/debug"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

// Shield moves Go's stderr to a private copy of fd 2, then points fd 2 itself
// at /dev/null. It does nothing unless fd 2 is a terminal.
//
// Apple's C libraries write straight to fd 2. Under system memory pressure
// libmalloc prints "MallocStackLogging: can't turn off malloc stack logging"
// in every process that uses libdispatch, and the launch chain's helpers
// share the agent pane's tty, so that line lands on top of Claude's screen.
// No malloc env var turns it off or redirects it.
//
// Call it first in main: anything that captured os.Stderr before it runs
// keeps writing to /dev/null. The log package is re-pointed here for that
// reason. A child started with Stderr = os.Stderr gets the terminal; a
// child that inherits fd 2 some other way gets /dev/null.
func Shield() {
	if !term.IsTerminal(2) {
		return
	}
	private, err := unix.FcntlInt(2, unix.F_DUPFD_CLOEXEC, 3)
	if err != nil {
		return
	}
	devnull, err := unix.Open(os.DevNull, unix.O_WRONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		_ = unix.Close(private)
		return
	}
	defer func() { _ = unix.Close(devnull) }()
	if err := unix.Dup2(devnull, 2); err != nil {
		_ = unix.Close(private)
		return
	}
	os.Stderr = os.NewFile(uintptr(private), "/dev/stderr")
	log.SetOutput(os.Stderr)
	// The runtime writes a fatal panic to fd 2; this mirrors it to the terminal.
	_ = debug.SetCrashOutput(os.Stderr, debug.CrashOptions{})
}
