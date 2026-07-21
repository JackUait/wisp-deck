package bash_test

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// TestLedgerHoverRouting_realTmux proves the pane-boundary behavior against
// tmux itself. Mouse motion over the ledger must reach the ledger, motion over
// a neighbouring pane must both clear the ledger and reach that pane, and
// ordinary/custom-bound keys must retain their normal root-table behavior.
func TestLedgerHoverRouting_realTmux(t *testing.T) {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not available")
	}

	dir := t.TempDir()
	sock := fmt.Sprintf("wisp-hover-%d", os.Getpid())
	session := "hover"
	leftCapture := filepath.Join(dir, "left.input")
	rightCapture := filepath.Join(dir, "right.input")
	captureScript := filepath.Join(dir, "capture-pane")
	shim := filepath.Join(dir, "tmux-shim")

	if err := os.WriteFile(captureScript, []byte(`#!/bin/sh
stty raw -echo
printf '\033[?1003h\033[?1006h'
exec cat >> "$1"
`), 0o755); err != nil {
		t.Fatalf("write pane capture helper: %v", err)
	}
	if err := os.WriteFile(shim, []byte(fmt.Sprintf("#!/bin/sh\nexec %s -L %s \"$@\"\n",
		strconv.Quote(tmux), strconv.Quote(sock))), 0o755); err != nil {
		t.Fatalf("write tmux shim: %v", err)
	}

	runTmux := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(tmux, append([]string{"-L", sock}, args...)...)
		cmd.Env = tmuxTestEnv()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}

	_ = exec.Command(tmux, "-L", sock, "kill-server").Run()
	t.Cleanup(func() { _ = exec.Command(tmux, "-L", sock, "kill-server").Run() })

	leftCommand := fmt.Sprintf("%s %s", strconv.Quote(captureScript), strconv.Quote(leftCapture))
	rightCommand := fmt.Sprintf("%s %s", strconv.Quote(captureScript), strconv.Quote(rightCapture))
	runTmux("-f", "/dev/null", "new-session", "-d", "-s", session, "-x", "100", "-y", "20", leftCommand)
	leftPane := runTmux("display-message", "-p", "-t", session+":0.0", "#{pane_id}")
	rightPane := runTmux("split-window", "-h", "-p", "50", "-t", leftPane, "-P", "-F", "#{pane_id}", rightCommand)
	runTmux("set-option", "-t", session, "status", "off")
	// A user root-table binding must survive the session-specific table clone.
	runTmux("bind-key", "-T", "root", "x", "send-keys", "-t", leftPane, "X")
	rootMouseBefore := runTmux("list-keys", "-T", "root", "MouseDown1Pane")
	socketPath := runTmux("display-message", "-p", "-t", session, "#{socket_path}")
	checksum := exec.Command("cksum")
	checksum.Stdin = strings.NewReader(socketPath)
	checksumOut, err := checksum.Output()
	if err != nil {
		t.Fatalf("checksum tmux socket path: %v", err)
	}
	checksumFields := strings.Fields(string(checksumOut))
	if len(checksumFields) == 0 {
		t.Fatalf("empty tmux socket checksum for %q", socketPath)
	}
	staleLock := filepath.Join(os.TempDir(), "wisp-ledger-hover-root-"+checksumFields[0]+".lock")
	if err := os.WriteFile(staleLock, nil, 0o600); err != nil {
		t.Fatalf("create leftover hover lock file: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(staleLock) })

	module := filepath.Join(projectRoot(t), "lib", "ledger-hover.sh")
	install := exec.Command("bash", "-c", `source "$1" && ledger_hover_install "$2" "$3" "$4"`,
		"ledger-hover-test", module, shim, session, leftPane)
	install.Env = tmuxTestEnv()
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatalf("install ledger hover routing: %v\n%s", err, out)
	}
	if _, err := os.Stat(staleLock); err != nil {
		t.Fatalf("ledger hover transaction removed its persistent lock file %q: %v", staleLock, err)
	}
	customTable := runTmux("show-options", "-qv", "-t", session, "@wisp_ledger_hover_table")
	if customTable == "" || runTmux("show-options", "-Aqv", "-t", session, "key-table") != customTable {
		t.Fatalf("ledger session did not activate its private key table %q", customTable)
	}
	if got := runTmux("show-options", "-Aqv", "-t", session, "mouse"); got != "on" {
		t.Fatalf("ledger session did not enable tmux mouse routing: got %q", got)
	}
	controlSession := `control"quoted`
	runTmux("new-session", "-d", "-s", controlSession, "sleep", "120")
	if got := runTmux("show-options", "-Aqv", "-t", controlSession, "key-table"); got != "root" {
		t.Fatalf("ledger routing leaked into another tmux session: control table is %q", got)
	}
	if got := runTmux("show-options", "-Aqv", "-t", controlSession, "mouse"); got != "off" {
		t.Fatalf("ledger mouse routing leaked into another tmux session: control mouse is %q", got)
	}
	rootWrappedOnce := runTmux("list-keys", "-T", "root", "MouseDown1Pane")
	controlPane := runTmux("display-message", "-p", "-t", controlSession+":0.0", "#{pane_id}")
	installControl := exec.Command("bash", "-c", `source "$1" && ledger_hover_install "$2" "$3" "$4"`,
		"ledger-hover-test", module, shim, controlSession, controlPane)
	installControl.Env = tmuxTestEnv()
	if out, err := installControl.CombinedOutput(); err != nil {
		t.Fatalf("install second ledger hover routing with quoted session name: %v\n%s", err, out)
	}
	if got := runTmux("list-keys", "-T", "root", "MouseDown1Pane"); got != rootWrappedOnce {
		t.Fatalf("second ledger session grew or changed the generic root wrapper:\nwant %s\n got %s",
			rootWrappedOnce, got)
	}
	uninstallControl := exec.Command("bash", "-c", `source "$1" && ledger_hover_uninstall "$2" "$3"`,
		"ledger-hover-test", module, shim, controlSession)
	uninstallControl.Env = tmuxTestEnv()
	if out, err := uninstallControl.CombinedOutput(); err != nil {
		t.Fatalf("uninstall second ledger hover routing with quoted session name: %v\n%s", err, out)
	}
	if got := runTmux("list-keys", "-T", "root", "MouseDown1Pane"); got != rootWrappedOnce {
		t.Fatalf("removing one of two ledger sessions changed the generic root wrapper:\nwant %s\n got %s",
			rootWrappedOnce, got)
	}
	if got := runTmux("show-options", "-Aqv", "-t", controlSession, "key-table"); got != "root" {
		t.Fatalf("second-session uninstall did not restore key table: got %q", got)
	}
	if got := runTmux("show-options", "-Aqv", "-t", controlSession, "mouse"); got != "off" {
		t.Fatalf("second-session uninstall did not restore mouse: got %q", got)
	}

	attach := exec.Command(tmux, "-L", sock, "attach-session", "-t", session)
	attach.Env = tmuxTestEnv()
	ptmx, err := pty.StartWithSize(attach, &pty.Winsize{Rows: 20, Cols: 100})
	if err != nil {
		t.Fatalf("attach tmux client: %v", err)
	}
	t.Cleanup(func() {
		_ = ptmx.Close()
		if attach.Process != nil {
			_ = attach.Process.Kill()
		}
	})
	go func() { _, _ = io.Copy(io.Discard, ptmx) }()

	waitForFile := func(path string, predicate func([]byte) bool, description string) []byte {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			data, _ := os.ReadFile(path)
			if predicate(data) {
				return data
			}
			time.Sleep(20 * time.Millisecond)
		}
		data, _ := os.ReadFile(path)
		t.Fatalf("timed out waiting for %s; captured %q", description, data)
		return nil
	}
	waitForFile(leftCapture, func(_ []byte) bool { _, err := os.Stat(leftCapture); return err == nil }, "left pane startup")
	waitForFile(rightCapture, func(_ []byte) bool { _, err := os.Stat(rightCapture); return err == nil }, "right pane startup")
	hookCapture := filepath.Join(dir, "after-set-option")
	runTmux("set-hook", "-t", session, "after-set-option",
		fmt.Sprintf(`run-shell "printf x >> %s"`, strconv.Quote(hookCapture)))

	paneGeometry := func(pane string) (left, top, width int) {
		t.Helper()
		fields := strings.Fields(runTmux("display-message", "-p", "-t", pane,
			"#{pane_left} #{pane_top} #{pane_width}"))
		if len(fields) != 3 {
			t.Fatalf("unexpected geometry for %s: %q", pane, fields)
		}
		values := []*int{&left, &top, &width}
		for i := range fields {
			value, err := strconv.Atoi(fields[i])
			if err != nil {
				t.Fatalf("parse geometry %q: %v", fields[i], err)
			}
			*values[i] = value
		}
		return
	}
	leftX, leftY, leftWidth := paneGeometry(leftPane)
	rightX, rightY, _ := paneGeometry(rightPane)
	leftMotion := []byte(fmt.Sprintf("\x1b[<35;%d;%dM", leftX+5, leftY+4))
	rightMotion := []byte(fmt.Sprintf("\x1b[<35;%d;%dM", rightX+5, rightY+4))
	syntheticLeave := []byte("\x1b[<35;9999;9999M")

	if _, err := ptmx.Write(leftMotion); err != nil {
		t.Fatalf("write left mouse motion: %v", err)
	}
	leftInput := waitForFile(leftCapture, func(data []byte) bool {
		return bytes.Contains(data, []byte("\x1b[<35;"))
	}, "mouse motion forwarded to ledger")
	if bytes.Contains(leftInput, syntheticLeave) {
		t.Fatalf("in-ledger mouse motion incorrectly emitted a leave event: %q", leftInput)
	}
	hookAfterEnter := waitForFile(hookCapture, func(data []byte) bool { return len(data) > 0 },
		"inside transition option hook")
	leftBeforeRepeat := len(leftInput)
	leftMotionAgain := []byte(fmt.Sprintf("\x1b[<35;%d;%dM", leftX+6, leftY+4))
	if _, err := ptmx.Write(leftMotionAgain); err != nil {
		t.Fatalf("write repeated in-ledger mouse motion: %v", err)
	}
	leftInput = waitForFile(leftCapture, func(data []byte) bool { return len(data) > leftBeforeRepeat },
		"repeated mouse motion forwarded within ledger")
	time.Sleep(150 * time.Millisecond)
	hookAfterRepeat, _ := os.ReadFile(hookCapture)
	if len(hookAfterRepeat) != len(hookAfterEnter) {
		t.Fatalf("same-pane motion repeatedly changed tmux options and fired hooks: before %q, after %q",
			hookAfterEnter, hookAfterRepeat)
	}
	stationarySize := len(leftInput)
	time.Sleep(450 * time.Millisecond)
	stationaryInput, _ := os.ReadFile(leftCapture)
	if len(stationaryInput) != stationarySize {
		t.Fatalf("stationary hover changed after an idle delay: before %q, after %q", leftInput, stationaryInput)
	}

	if _, err := ptmx.Write(rightMotion); err != nil {
		t.Fatalf("write neighbouring-pane mouse motion: %v", err)
	}
	waitForFile(leftCapture, func(data []byte) bool { return bytes.Contains(data, syntheticLeave) },
		"synthetic ledger leave when pointer enters neighbouring pane")
	waitForFile(rightCapture, func(data []byte) bool { return bytes.Contains(data, []byte("\x1b[<35;")) },
		"original mouse motion forwarded to neighbouring pane")
	leftAfterLeave, _ := os.ReadFile(leftCapture)
	rightBeforeRepeat, _ := os.ReadFile(rightCapture)
	rightMotionAgain := []byte(fmt.Sprintf("\x1b[<35;%d;%dM", rightX+6, rightY+4))
	if _, err := ptmx.Write(rightMotionAgain); err != nil {
		t.Fatalf("write repeated neighbouring-pane mouse motion: %v", err)
	}
	waitForFile(rightCapture, func(data []byte) bool { return len(data) > len(rightBeforeRepeat) },
		"repeated mouse motion forwarded within neighbouring pane")
	leftAfterRepeat, _ := os.ReadFile(leftCapture)
	if !bytes.Equal(leftAfterLeave, leftAfterRepeat) {
		t.Fatalf("ledger received repeated leave events while pointer stayed in its neighbour: before %q, after %q",
			leftAfterLeave, leftAfterRepeat)
	}

	// Pane position is not pane identity: users can swap/rearrange panes through
	// tmux's own menu. Leave delivery must continue targeting the stored ledger
	// pane rather than whichever application happens to become top-left.
	runTmux("swap-pane", "-s", leftPane, "-t", rightPane)
	swappedLedgerX, swappedLedgerY, _ := paneGeometry(leftPane)
	swappedNeighbourX, swappedNeighbourY, _ := paneGeometry(rightPane)
	swappedLedgerMotion := []byte(fmt.Sprintf("\x1b[<35;%d;%dM", swappedLedgerX+5, swappedLedgerY+4))
	leftBeforeSwapMotion, _ := os.ReadFile(leftCapture)
	if _, err := ptmx.Write(swappedLedgerMotion); err != nil {
		t.Fatalf("write ledger motion after pane swap: %v", err)
	}
	swappedLedgerInput := waitForFile(leftCapture, func(data []byte) bool {
		return len(data) > len(leftBeforeSwapMotion)
	}, "mouse motion forwarded to swapped ledger pane")
	if !bytes.Contains(swappedLedgerInput[len(leftBeforeSwapMotion):], []byte("\x1b[<35;")) {
		t.Fatalf("swapped ledger did not receive pane-local mouse motion: %q",
			swappedLedgerInput[len(leftBeforeSwapMotion):])
	}
	leftBeforeSwappedLeave, _ := os.ReadFile(leftCapture)
	rightBeforeSwappedMotion, _ := os.ReadFile(rightCapture)
	swappedNeighbourMotion := []byte(fmt.Sprintf("\x1b[<35;%d;%dM", swappedNeighbourX+5, swappedNeighbourY+4))
	if _, err := ptmx.Write(swappedNeighbourMotion); err != nil {
		t.Fatalf("write neighbouring motion after pane swap: %v", err)
	}
	swappedLeaveInput := waitForFile(leftCapture, func(data []byte) bool {
		return len(data) > len(leftBeforeSwappedLeave)
	}, "synthetic leave delivered to swapped ledger pane")
	if !bytes.Contains(swappedLeaveInput[len(leftBeforeSwappedLeave):], syntheticLeave) {
		t.Fatalf("pane swap redirected the synthetic leave away from the ledger: %q",
			swappedLeaveInput[len(leftBeforeSwappedLeave):])
	}
	swappedNeighbourInput := waitForFile(rightCapture, func(data []byte) bool {
		return len(data) > len(rightBeforeSwappedMotion)
	}, "motion forwarded to swapped neighbouring pane")
	if bytes.Contains(swappedNeighbourInput[len(rightBeforeSwappedMotion):], syntheticLeave) {
		t.Fatalf("pane swap leaked the synthetic leave into the neighbouring application: %q",
			swappedNeighbourInput[len(rightBeforeSwappedMotion):])
	}
	runTmux("swap-pane", "-s", leftPane, "-t", rightPane)

	// Specifically bound mouse keys bypass Any. A ledger click must still mark
	// the pointer as inside so a direct next motion in the neighbour clears it.
	leftBeforeClick, _ := os.ReadFile(leftCapture)
	leftClick := []byte(fmt.Sprintf("\x1b[<0;%d;%dM", leftX+6, leftY+4))
	if _, err := ptmx.Write(leftClick); err != nil {
		t.Fatalf("write ledger click: %v", err)
	}
	waitForFile(leftCapture, func(data []byte) bool { return len(data) > len(leftBeforeClick) }, "ledger click")
	if inside := runTmux("show-options", "-qv", "-t", session, "@wisp_ledger_hover_inside"); inside != "1" {
		t.Fatalf("specifically bound ledger click did not mark pointer inside: got %q", inside)
	}
	leftBeforeDirectLeave, _ := os.ReadFile(leftCapture)
	if _, err := ptmx.Write(rightMotion); err != nil {
		t.Fatalf("write direct neighbour motion after ledger click: %v", err)
	}
	directLeaveInput := waitForFile(leftCapture, func(data []byte) bool { return len(data) > len(leftBeforeDirectLeave) },
		"leave after ledger click and direct neighbouring motion")
	if !bytes.Contains(directLeaveInput[len(leftBeforeDirectLeave):], syntheticLeave) {
		t.Fatalf("ledger click did not establish pane state for direct leave: %q",
			directLeaveInput[len(leftBeforeDirectLeave):])
	}

	// Enabling mouse on the outer Wisp session must retain tmux's normal click
	// forwarding. The nested spare tmux depends on this same application-mouse
	// path to receive its tab-bar clicks.
	if _, err := ptmx.Write(leftMotion); err != nil {
		t.Fatalf("write ledger motion before neighbouring click: %v", err)
	}
	waitForFile(leftCapture, func(data []byte) bool { return bytes.HasSuffix(data, leftMotion) },
		"ledger motion before neighbouring click")
	time.Sleep(350 * time.Millisecond) // keep tmux from classifying this as a second-click key
	runTmux("select-pane", "-t", leftPane)
	leftBeforeRightClick, _ := os.ReadFile(leftCapture)
	rightBeforeClick, _ := os.ReadFile(rightCapture)
	rightClick := []byte(fmt.Sprintf("\x1b[<0;%d;%dM", rightX+6, rightY+4))
	if _, err := ptmx.Write(rightClick); err != nil {
		t.Fatalf("write neighbouring-pane click: %v", err)
	}
	rightClickInput := waitForFile(rightCapture, func(data []byte) bool { return len(data) > len(rightBeforeClick) },
		"mouse click forwarded to neighbouring application pane")
	if !bytes.Contains(rightClickInput[len(rightBeforeClick):], []byte("\x1b[<0;")) {
		t.Fatalf("neighbouring application did not receive its SGR click: %q", rightClickInput[len(rightBeforeClick):])
	}
	leftFromRightClick, _ := os.ReadFile(leftCapture)
	if !bytes.Contains(leftFromRightClick[len(leftBeforeRightClick):], syntheticLeave) {
		t.Fatalf("specifically bound neighbouring click did not clear ledger hover: %q",
			leftFromRightClick[len(leftBeforeRightClick):])
	}
	if active := runTmux("display-message", "-p", "-t", session, "#{pane_id}"); active != rightPane {
		t.Fatalf("outer tmux click did not select neighbouring pane: got %s, want %s", active, rightPane)
	}

	// The catch-all also has to forward ordinary keys, while exact user root
	// bindings copied into the custom table must continue to win over it.
	runTmux("select-pane", "-t", leftPane)
	if _, err := ptmx.Write([]byte("z")); err != nil {
		t.Fatalf("write ordinary key: %v", err)
	}
	waitForFile(leftCapture, func(data []byte) bool { return bytes.Contains(data, []byte("z")) },
		"ordinary key through Any fallback")
	if _, err := ptmx.Write([]byte("x")); err != nil {
		t.Fatalf("write custom-bound key: %v", err)
	}
	waitForFile(leftCapture, func(data []byte) bool { return bytes.Contains(data, []byte("X")) },
		"copied root-table binding")

	// User/plugin changes made while Wisp is running are newer than Wisp's root
	// snapshot. Cleanup must preserve them instead of restoring stale startup
	// configuration over the top.
	runTmux("bind-key", "-T", "root", "MouseDown1Pane", "display-message", "runtime-change")
	runtimeRootMouse := runTmux("list-keys", "-T", "root", "MouseDown1Pane")

	uninstall := exec.Command("bash", "-c", `source "$1" && ledger_hover_uninstall "$2" "$3"`,
		"ledger-hover-test", module, shim, session)
	uninstall.Env = tmuxTestEnv()
	if out, err := uninstall.CombinedOutput(); err != nil {
		t.Fatalf("uninstall ledger hover routing: %v\n%s", err, out)
	}
	if got := runTmux("show-options", "-Aqv", "-t", session, "key-table"); got != "root" {
		t.Fatalf("uninstall did not restore the original key table: got %q", got)
	}
	if got := runTmux("show-options", "-Aqv", "-t", session, "mouse"); got != "off" {
		t.Fatalf("uninstall did not restore the original mouse option: got %q", got)
	}
	listRemovedTable := exec.Command(tmux, "-L", sock, "list-keys", "-T", customTable)
	listRemovedTable.Env = tmuxTestEnv()
	if err := listRemovedTable.Run(); err == nil {
		t.Fatalf("uninstall left private key table %q behind", customTable)
	}
	if got := runTmux("list-keys", "-T", "root", "MouseDown1Pane"); got != runtimeRootMouse {
		t.Fatalf("uninstall overwrote a runtime root mouse binding change:\nwant %s\n got %s", runtimeRootMouse, got)
	}

	// A pre-existing Any fallback is user configuration, not an unbound key.
	// Wisp may specialize mouse motion but must delegate ordinary keys to it.
	runTmux("bind-key", "-T", "root", "Any", "send-keys", "-t", leftPane, "A")
	install = exec.Command("bash", "-c", `source "$1" && ledger_hover_install "$2" "$3" "$4"`,
		"ledger-hover-test", module, shim, session, leftPane)
	install.Env = tmuxTestEnv()
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatalf("reinstall ledger hover routing with user Any binding: %v\n%s", err, out)
	}
	beforeUserAny, _ := os.ReadFile(leftCapture)
	if _, err := ptmx.Write([]byte("v")); err != nil {
		t.Fatalf("write key handled by user Any binding: %v", err)
	}
	userAnyInput := waitForFile(leftCapture, func(data []byte) bool { return len(data) > len(beforeUserAny) },
		"pre-existing user Any binding")
	userAnyDelta := userAnyInput[len(beforeUserAny):]
	if !bytes.Contains(userAnyDelta, []byte("A")) || bytes.Contains(userAnyDelta, []byte("v")) {
		t.Fatalf("custom table replaced rather than delegated to user Any binding: %q", userAnyDelta)
	}

	// If the pane exits before wrapper cleanup, the session options disappear.
	// The deterministic private-table name must still let cleanup remove it from
	// a tmux server kept alive by another session.
	customTable = runTmux("show-options", "-qv", "-t", session, "@wisp_ledger_hover_table")
	runTmux("kill-session", "-t", session)
	uninstall = exec.Command("bash", "-c", `source "$1" && ledger_hover_uninstall "$2" "$3"`,
		"ledger-hover-test", module, shim, session)
	uninstall.Env = tmuxTestEnv()
	if out, err := uninstall.CombinedOutput(); err != nil {
		t.Fatalf("uninstall ledger hover routing after session exit: %v\n%s", err, out)
	}
	listRemovedTable = exec.Command(tmux, "-L", sock, "list-keys", "-T", customTable)
	listRemovedTable.Env = tmuxTestEnv()
	if err := listRemovedTable.Run(); err == nil {
		t.Fatalf("post-session cleanup left private key table %q behind", customTable)
	}
	if got := runTmux("list-keys", "-T", "root", "MouseDown1Pane"); got != runtimeRootMouse {
		t.Fatalf("post-session cleanup overwrote the runtime root mouse binding:\nwant %s\n got %s", runtimeRootMouse, got)
	}
	if runtimeRootMouse == rootMouseBefore {
		t.Fatal("runtime root mouse test did not actually change the binding")
	}

	if leftWidth <= 0 {
		t.Fatalf("invalid ledger pane width %d", leftWidth)
	}
}

// The hover install must name the LEDGER pane explicitly — the pane id
// captured when new-session created it (%0 from the recording mock's -P
// reply) — and must be issued before the blocking attach so the routing
// exists while the session runs. Positional targeting ("whatever pane is
// active when the install runs") is not acceptable: the AI pane already
// exists by then and the focus watcher may have moved the active pane.
func TestLedgerHoverRouting_wrapperInstallsBeforeNeighbouringPane(t *testing.T) {
	chain := recordWrapperNewSession(t)
	install := strings.Index(chain, "ledger_hover_install")
	attach := strings.Index(chain, "attach-session")
	if install < 0 {
		t.Fatalf("wrapper does not install exact ledger hover routing; launch record:\n%s", chain)
	}
	if !strings.Contains(chain[install:install+300], "%0") {
		t.Fatalf("ledger hover setup does not pass the captured ledger pane id; launch record:\n%s", chain)
	}
	if attach < 0 || install > attach {
		t.Fatalf("ledger hover routing must be installed before the blocking attach; launch record:\n%s", chain)
	}
}

func TestLedgerHoverRouting_cleanupUninstallsBeforeSessionKill(t *testing.T) {
	path := filepath.Join(projectRoot(t), "lib", "tmux-session.sh")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cleanupStart := bytes.Index(data, []byte("cleanup_tmux_session()"))
	if cleanupStart < 0 {
		t.Fatal("cleanup_tmux_session not found")
	}
	cleanup := data[cleanupStart:]
	uninstall := bytes.Index(cleanup, []byte("ledger_hover_uninstall"))
	firstPaneTermination := bytes.Index(cleanup, []byte("list-panes"))
	killSession := bytes.Index(cleanup, []byte("kill-session"))
	if uninstall < 0 || firstPaneTermination < 0 || killSession < 0 || uninstall > firstPaneTermination {
		t.Fatalf("cleanup must uninstall the private ledger key table before terminating any pane: uninstall=%d panes=%d kill=%d",
			uninstall, firstPaneTermination, killSession)
	}
}

func tmuxTestEnv() []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "TMUX=") || strings.HasPrefix(entry, "TMUX_PANE=") || strings.HasPrefix(entry, "TERM=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "TERM=xterm-256color")
}
