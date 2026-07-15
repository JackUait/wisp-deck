package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
	"github.com/spf13/cobra"

	"github.com/jackuait/wisp-deck/internal/screenshotfilter"
	"github.com/jackuait/wisp-deck/internal/terminalcontrol"
)

// screenshot-filter runs a child command in a PTY and transparently proxies the
// terminal to it, except it rewrites a dropped screencaptureui temp-screenshot
// path (delivered as a bracketed paste) to a stable copy before the child reads
// it. This makes the literal drag-and-drop of a screenshot into the AI pane work
// even though macOS deletes the original temp file moments after the drop.
var screenshotFilterCmd = &cobra.Command{
	Use:                "screenshot-filter -- command [args...]",
	Short:              "Run a command in a PTY, rewriting dropped screenshot temp paths to stable copies",
	DisableFlagParsing: true,
	SilenceUsage:       true,
	SilenceErrors:      true,
	RunE:               runScreenshotFilter,
}

func init() { rootCmd.AddCommand(screenshotFilterCmd) }

func runScreenshotFilter(_ *cobra.Command, args []string) error {
	for len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		return errors.New("usage: wisp-deck-tui screenshot-filter -- <command> [args...]")
	}

	// Non-interactive (no tty on stdin): run the child transparently — the filter
	// input rewrite only matters for a live terminal drop, but the output policy
	// still applies because stdout may be an outer agent terminal.
	if !term.IsTerminal(os.Stdin.Fd()) {
		c := exec.Command(args[0], args[1:]...)
		outputReader, outputWriter, err := os.Pipe()
		if err != nil {
			return err
		}
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, outputWriter, outputWriter
		if err := c.Start(); err != nil {
			_ = outputReader.Close()
			_ = outputWriter.Close()
			return err
		}
		_ = outputWriter.Close()
		outputErr := pumpTerminalOutput(outputReader, os.Stdout)
		_ = outputReader.Close()
		if err := c.Wait(); err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				os.Exit(ee.ExitCode())
			}
			return err
		}
		return outputErr
	}

	c := exec.Command(args[0], args[1:]...)
	ptmx, err := pty.Start(c)
	if err != nil {
		return err
	}
	defer func() { _ = ptmx.Close() }()

	// Keep the child PTY sized to our terminal.
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	go func() {
		for range ch {
			_ = pty.InheritSize(os.Stdin, ptmx)
		}
	}()
	ch <- syscall.SIGWINCH
	defer signal.Stop(ch)

	// Raw mode so every byte (control sequences, raw keys) passes through to the
	// child unchanged; the child sets its own modes on the inner PTY.
	oldState, rawErr := term.MakeRaw(os.Stdin.Fd())
	restore := func() {
		if rawErr == nil {
			_ = term.Restore(os.Stdin.Fd(), oldState)
		}
	}

	// stdin -> filter -> child
	go func() {
		filt := screenshotfilter.New()
		b := make([]byte, 32*1024)
		for {
			n, rerr := os.Stdin.Read(b)
			if n > 0 {
				if out := filt.Process(b[:n]); len(out) > 0 {
					_, _ = ptmx.Write(out)
				}
			}
			if rerr != nil {
				return
			}
		}
	}()

	// child -> notification filter -> stdout (returns when the child exits and
	// the PTY closes)
	outputErr := pumpTerminalOutput(ptmx, os.Stdout)
	werr := c.Wait()
	restore()
	var ee *exec.ExitError
	if errors.As(werr, &ee) {
		os.Exit(ee.ExitCode())
	}
	if werr == nil && outputErr != nil {
		return outputErr
	}
	return werr
}

func pumpTerminalOutput(input io.Reader, output io.Writer) error {
	var filter terminalcontrol.Filter
	buffer := make([]byte, 32*1024)
	for {
		n, readErr := input.Read(buffer)
		if n > 0 {
			filtered, _ := filter.Feed(buffer[:n])
			if err := writeTerminalOutput(output, filtered); err != nil {
				return err
			}
		}
		if readErr != nil {
			if err := writeTerminalOutput(output, filter.Flush()); err != nil {
				return err
			}
			if errors.Is(readErr, io.EOF) || errors.Is(readErr, syscall.EIO) {
				return nil
			}
			return readErr
		}
	}
}

func writeTerminalOutput(output io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := output.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(data) {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
