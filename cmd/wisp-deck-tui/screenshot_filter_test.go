package main

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/creack/pty"
)

type byteReader struct {
	data []byte
}

func (r *byteReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	p[0] = r.data[0]
	r.data = r.data[1:]
	return 1, nil
}

func TestPumpTerminalOutputFiltersNotificationsAcrossOneByteReads(t *testing.T) {
	input := []byte(strings.Join([]string{
		"before",
		"\x07",
		"\x1b]9;plain\x07",
		"\x1bPtmux;\x1b\x1b]9;wrapped\x07\x1b\\",
		"\x1b]0;title\x07",
		"after",
	}, ""))
	var output bytes.Buffer
	if err := pumpTerminalOutput(&byteReader{data: input}, &output); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "before\x1b]0;title\x07after"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

type terminalReadError struct {
	sent bool
}

func (r *terminalReadError) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		copy(p, "safe\x07")
		return len("safe\x07"), nil
	}
	return 0, errors.New("read failed")
}

func TestPumpTerminalOutputFlushesBeforeReturningReadError(t *testing.T) {
	var output bytes.Buffer
	err := pumpTerminalOutput(&terminalReadError{}, &output)
	if err == nil || err.Error() != "read failed" {
		t.Fatalf("error = %v, want read failed", err)
	}
	if got := output.String(); got != "safe" {
		t.Fatalf("output = %q, want safe", got)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestPumpTerminalOutputReturnsWriteError(t *testing.T) {
	err := pumpTerminalOutput(bytes.NewBufferString("ordinary"), failingWriter{})
	if err == nil || err.Error() != "write failed" {
		t.Fatalf("error = %v, want write failed", err)
	}
}

func TestPumpTerminalOutputFiltersRealPTY(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", `printf 'before\007\033]9;plain\007\033Ptmux;\033\033]9;wrapped\007\033\\after'`)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	pumpErr := pumpTerminalOutput(ptmx, &output)
	_ = ptmx.Close()
	waitErr := cmd.Wait()
	if pumpErr != nil {
		t.Fatalf("pump PTY: %v", pumpErr)
	}
	if waitErr != nil {
		t.Fatalf("wait PTY child: %v", waitErr)
	}
	if got := output.String(); got != "beforeafter" {
		t.Fatalf("PTY output = %q, want beforeafter", got)
	}
}
