package bash_test

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const attentionWrapperTmuxMock = `#!/bin/bash
if [ "$1" = "new-session" ]; then
  tmp="${GT_CAPTURE}.tmp.$$"
  : > "$tmp"
  state=""
  descriptor=""
  for arg in "$@"; do
    printf '%s\n' "$arg" >> "$tmp"
    case "$arg" in
      WISP_DECK_ATTENTION_FILE=*) state=${arg#*=} ;;
      WISP_DECK_ATTENTION_DESCRIPTOR=*) descriptor=${arg#*=} ;;
    esac
  done
  mv "$tmp" "$GT_CAPTURE"
  [ -n "$GT_STATE_COPY" ] && cp "$state" "$GT_STATE_COPY"
  [ -n "$GT_DESCRIPTOR_COPY" ] && cp "$descriptor" "$GT_DESCRIPTOR_COPY"
  [ -n "$GT_READY" ] && printf 'ready\n' > "$GT_READY"
  if [ -n "$GT_RELEASE" ]; then
    while [ ! -f "$GT_RELEASE" ]; do sleep 0.02; done
  fi
  exit 0
fi
exit 0
`

type attentionWrapperFixture struct {
	home       string
	project    string
	runtimeDir string
	configDir  string
}

type attentionLaunchCapture struct {
	args       []string
	root       string
	descriptor string
	generation string
	state      string
	stateBytes string
	descBytes  string
}

func newAttentionWrapperFixture(t *testing.T, tool string) attentionWrapperFixture {
	t.Helper()
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	configDir := filepath.Join(home, ".config", "wisp-deck")
	project := filepath.Join(home, "same-project")
	runtimeDir := filepath.Join(home, "runtime")
	for _, dir := range []string{binDir, configDir, project, runtimeDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	writeTempFile(t, configDir, "ai-tool", tool+"\n")

	mocks := map[string]string{
		"tmux":          attentionWrapperTmuxMock,
		"claude":        "#!/bin/bash\nexit 0\n",
		"codex":         "#!/bin/bash\nexit 0\n",
		"opencode":      "#!/bin/bash\nexit 0\n",
		"wisp-deck-tui": "#!/bin/bash\nexit 0\n",
		"sysctl": `#!/bin/bash
case "$*" in
  *kern.bootsessionuuid*) echo attention-test-boot ;;
  *kern.boottime*) echo '{ sec = 12345, usec = 1 }' ;;
esac
`,
	}
	for name, body := range mocks {
		writeExecutable(t, filepath.Join(binDir, name), body)
	}

	return attentionWrapperFixture{
		home:       home,
		project:    project,
		runtimeDir: runtimeDir,
		configDir:  configDir,
	}
}

func (f attentionWrapperFixture) env(t *testing.T, capture, stateCopy, descriptorCopy string, extra ...string) []string {
	t.Helper()
	base := []string{
		"HOME=" + f.home,
		"XDG_CONFIG_HOME=" + filepath.Join(f.home, ".config"),
		"TMPDIR=" + f.runtimeDir,
		"GT_CAPTURE=" + capture,
		"GT_STATE_COPY=" + stateCopy,
		"GT_DESCRIPTOR_COPY=" + descriptorCopy,
		"WISP_DECK_WATCH_INTERVAL=0.05",
	}
	return buildEnv(t, nil, append(base, extra...)...)
}

func readAttentionLaunchCapture(t *testing.T, capturePath, statePath, descriptorPath string) attentionLaunchCapture {
	t.Helper()
	data, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read tmux launch capture: %v", err)
	}
	args := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	fields := make(map[string]string)
	for i, arg := range args {
		if i == 0 || args[i-1] != "-e" {
			continue
		}
		name, value, ok := strings.Cut(arg, "=")
		if ok && strings.HasPrefix(name, "WISP_DECK_ATTENTION_") {
			fields[name] = value
		}
	}
	stateBytes, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read captured initial state: %v", err)
	}
	descBytes, err := os.ReadFile(descriptorPath)
	if err != nil {
		t.Fatalf("read captured descriptor: %v", err)
	}
	return attentionLaunchCapture{
		args:       args,
		root:       fields["WISP_DECK_ATTENTION_ROOT"],
		descriptor: fields["WISP_DECK_ATTENTION_DESCRIPTOR"],
		generation: fields["WISP_DECK_ATTENTION_GENERATION"],
		state:      fields["WISP_DECK_ATTENTION_FILE"],
		stateBytes: string(stateBytes),
		descBytes:  string(descBytes),
	}
}

func assertFreshAttentionLaunch(t *testing.T, got attentionLaunchCapture, tool, runtimeDir string) {
	t.Helper()
	if !strings.HasPrefix(got.root, runtimeDir+string(os.PathSeparator)+"wisp-deck-attention.") {
		t.Fatalf("attention root %q was not freshly allocated below %q", got.root, runtimeDir)
	}
	if got.descriptor != filepath.Join(got.root, "descriptor") {
		t.Fatalf("descriptor = %q, want root descriptor below %q", got.descriptor, got.root)
	}
	if !strings.HasPrefix(got.generation, "generation.") {
		t.Fatalf("generation = %q, want fresh generation.*", got.generation)
	}
	if got.state != filepath.Join(got.root, got.generation, "state") {
		t.Fatalf("state = %q, want state below fresh generation", got.state)
	}
	if want := fmt.Sprintf("1\t%s\t0\tunknown\t-\n", got.generation); got.stateBytes != want {
		t.Fatalf("initial state = %q, want %q", got.stateBytes, want)
	}
	if want := fmt.Sprintf("1\t%s\t%s\t%s\n", got.generation, tool, got.state); got.descBytes != want {
		t.Fatalf("descriptor record = %q, want %q", got.descBytes, want)
	}
}

func runAttentionWrapper(t *testing.T, args []string, env []string) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{filepath.Join(projectRoot(t), "wrapper.sh")}, args...)...)
	cmd.Env = env
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		t.Fatalf("wrapper launch: %v", err)
	}
}

func TestAttentionIntegrationInitialWrapperLaunchPublishesFreshRuntime(t *testing.T) {
	tests := []struct {
		tool          string
		commandNeedle string
	}{
		{tool: "claude", commandNeedle: "wisp-deck-tui claude-attention"},
		{tool: "codex", commandNeedle: "wisp-deck-tui codex-adapter"},
		{tool: "opencode", commandNeedle: "wisp-deck-tui opencode-adapter"},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			t.Parallel()
			fixture := newAttentionWrapperFixture(t, tt.tool)
			capture := filepath.Join(fixture.home, "tmux.args")
			stateCopy := filepath.Join(fixture.home, "state.copy")
			descriptorCopy := filepath.Join(fixture.home, "descriptor.copy")

			runAttentionWrapper(t, []string{fixture.project},
				fixture.env(t, capture, stateCopy, descriptorCopy))
			got := readAttentionLaunchCapture(t, capture, stateCopy, descriptorCopy)
			assertFreshAttentionLaunch(t, got, tt.tool, fixture.runtimeDir)

			launchArgs := strings.Join(got.args, "\n")
			for _, field := range []string{got.root, got.descriptor, got.generation, got.state} {
				if field == "" || !strings.Contains(launchArgs, field) {
					t.Errorf("tmux launch environment does not contain attention field %q", field)
				}
			}
			launchCommand := ""
			for _, arg := range got.args {
				if strings.Contains(arg, tt.commandNeedle) {
					launchCommand = arg
					break
				}
			}
			if launchCommand == "" {
				t.Fatalf("%s launch command missing %q:\n%s", tt.tool, tt.commandNeedle, launchArgs)
			}
			for _, adapterField := range []string{got.generation, got.state} {
				if !strings.Contains(launchCommand, adapterField) {
					t.Errorf("%s adapter command missing %q", tt.tool, adapterField)
				}
			}
		})
	}
}

func TestAttentionIntegrationRestoredWrapperRejectsPoisonedRuntime(t *testing.T) {
	t.Parallel()
	fixture := newAttentionWrapperFixture(t, "claude")
	writeTempFile(t, fixture.configDir, "restore-queue",
		"attention-test-boot|"+fixture.project+"|claude|||\n")
	writeTempFile(t, fixture.configDir, "last-restore-boot", "attention-test-boot\n")
	seedChainTicket(t, fixture.configDir)
	capture := filepath.Join(fixture.home, "tmux.args")
	stateCopy := filepath.Join(fixture.home, "state.copy")
	descriptorCopy := filepath.Join(fixture.home, "descriptor.copy")
	poison := []string{
		"WISP_DECK_ATTENTION_ROOT=/poison/root",
		"WISP_DECK_ATTENTION_DESCRIPTOR=/poison/descriptor",
		"WISP_DECK_ATTENTION_GENERATION=generation.poison",
		"WISP_DECK_ATTENTION_FILE=/poison/state",
	}

	runAttentionWrapper(t, nil,
		fixture.env(t, capture, stateCopy, descriptorCopy, poison...))
	got := readAttentionLaunchCapture(t, capture, stateCopy, descriptorCopy)
	assertFreshAttentionLaunch(t, got, "claude", fixture.runtimeDir)
	joined := strings.Join(got.args, "\n")
	for _, value := range []string{"/poison/root", "/poison/descriptor", "generation.poison", "/poison/state"} {
		if strings.Contains(joined, value) {
			t.Errorf("restored wrapper imported poisoned attention value %q", value)
		}
	}
	if _, err := os.Stat(filepath.Join(fixture.configDir, "restore-queue")); !os.IsNotExist(err) {
		t.Fatalf("restore queue was not consumed: %v", err)
	}
}

type runningAttentionHarness struct {
	cmd      *exec.Cmd
	done     chan error
	finished bool
}

const attentionLifecycleHarness = `
source "$1/lib/process.sh" || exit 10
source "$1/lib/tui.sh" || exit 11
source "$1/lib/tmux-session.sh" || exit 12
source "$1/lib/screenshot.sh" || exit 13
source "$1/lib/tab-title-watcher.sh" || exit 14
source "$1/lib/session-restore.sh" || exit 15
source "$1/lib/attention.sh" || exit 16
attention_session_create "$2" >/dev/null || exit 17
root=$WISP_DECK_ATTENTION_ROOT
attention_begin_generation "$root" codex >/dev/null || exit 18
start_tab_title_watcher same-project same-project full "$3" "$WISP_DECK_ATTENTION_DESCRIPTOR" "$2"
title_pid=$_TAB_TITLE_WATCHER_PID
gt_focus_ai_pane_when_ready "$3" same-project >/dev/null 2>/dev/null &
focus_pid=$!
run_snapshot_heartbeat "$1" "$3" "$7" 0.05 >/dev/null 2>/dev/null &
heartbeat_pid=$!
{
  printf 'root=%s\n' "$root"
  printf 'descriptor=%s\n' "$WISP_DECK_ATTENTION_DESCRIPTOR"
  printf 'generation=%s\n' "$WISP_DECK_ATTENTION_GENERATION"
  printf 'state=%s\n' "$WISP_DECK_ATTENTION_FILE"
  printf 'title_pid=%s\n' "$title_pid"
  printf 'focus_pid=%s\n' "$focus_pid"
  printf 'heartbeat_pid=%s\n' "$heartbeat_pid"
} > "$4.tmp"
mv "$4.tmp" "$4"
while [ ! -f "$5" ]; do sleep 0.02; done
stop_tab_title_watcher
kill_tree "$heartbeat_pid" TERM 2>/dev/null || true
cleanup_tmux_session same-project "$focus_pid" "$3"
attention_cleanup "$root" || exit 19
printf 'cleaned\n' > "$6"
`

func startAttentionLifecycleHarness(t *testing.T, runtimeDir, tmuxPath, ready, release, cleaned, snapshot string) *runningAttentionHarness {
	t.Helper()
	cmd := exec.Command("bash", "-c", attentionLifecycleHarness, "attention-lifecycle",
		projectRoot(t), runtimeDir, tmuxPath, ready, release, cleaned, snapshot)
	cmd.Env = buildEnv(t, nil, "WISP_DECK_WATCH_INTERVAL=0.05")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start attention lifecycle harness: %v", err)
	}
	running := &runningAttentionHarness{cmd: cmd, done: make(chan error, 1)}
	go func() { running.done <- cmd.Wait() }()
	t.Cleanup(func() {
		// Always kill the harness process group: if the wrapper shell already
		// exited but a helper survived, finished is true while that helper still
		// needs teardown.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if !running.finished {
			select {
			case <-running.done:
			case <-time.After(2 * time.Second):
			}
			running.finished = true
		}
	})
	return running
}

func waitForAttentionFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func waitAttentionHarness(t *testing.T, running *runningAttentionHarness, timeout time.Duration) {
	t.Helper()
	select {
	case err := <-running.done:
		running.finished = true
		if err != nil {
			t.Fatalf("attention lifecycle harness exited with error: %v", err)
		}
	case <-time.After(timeout):
		t.Fatalf("attention lifecycle harness %d did not exit", running.cmd.Process.Pid)
	}
}

func assertAttentionProcessesStop(t *testing.T, pids []int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		allStopped := true
		for _, pid := range pids {
			if isProcessRunning(pid) {
				allStopped = false
				break
			}
		}
		if allStopped {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, pid := range pids {
		if isProcessRunning(pid) {
			t.Errorf("wrapper helper process %d survived cleanup", pid)
		}
	}
}

func assertAttentionProcessesRunning(t *testing.T, pids []int) {
	t.Helper()
	for _, pid := range pids {
		if !isProcessRunning(pid) {
			t.Fatalf("lifecycle helper process %d exited before cleanup", pid)
		}
	}
}

func readAttentionLifecycle(t *testing.T, ready string) (attentionLaunchCapture, []int) {
	t.Helper()
	data, err := os.ReadFile(ready)
	if err != nil {
		t.Fatalf("read attention lifecycle: %v", err)
	}
	fields := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			fields[key] = value
		}
	}
	pids := make([]int, 0, 3)
	for _, key := range []string{"title_pid", "focus_pid", "heartbeat_pid"} {
		pid, err := strconv.Atoi(fields[key])
		if err != nil {
			t.Fatalf("%s = %q, want process id", key, fields[key])
		}
		pids = append(pids, pid)
	}
	return attentionLaunchCapture{
		root:       fields["root"],
		descriptor: fields["descriptor"],
		generation: fields["generation"],
		state:      fields["state"],
	}, pids
}

func TestAttentionIntegrationSameProjectRuntimeSessionsOwnDisjointLifecycles(t *testing.T) {
	dir := t.TempDir()
	runtimeDir := filepath.Join(dir, "runtime")
	if err := os.Mkdir(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binDir := mockCommand(t, dir, "tmux", `exit 0`)
	tmuxPath := filepath.Join(binDir, "tmux")
	type harnessFiles struct {
		ready, release, cleaned, snapshot string
	}
	files := func(name string) harnessFiles {
		return harnessFiles{
			ready:    filepath.Join(dir, name+".ready"),
			release:  filepath.Join(dir, name+".release"),
			cleaned:  filepath.Join(dir, name+".cleaned"),
			snapshot: filepath.Join(dir, name+".snapshot"),
		}
	}
	oneFiles, twoFiles := files("one"), files("two")
	one := startAttentionLifecycleHarness(t, runtimeDir, tmuxPath, oneFiles.ready,
		oneFiles.release, oneFiles.cleaned, oneFiles.snapshot)
	two := startAttentionLifecycleHarness(t, runtimeDir, tmuxPath, twoFiles.ready,
		twoFiles.release, twoFiles.cleaned, twoFiles.snapshot)
	waitForAttentionFile(t, oneFiles.ready, 3*time.Second)
	waitForAttentionFile(t, twoFiles.ready, 3*time.Second)
	oneCapture, oneHelpers := readAttentionLifecycle(t, oneFiles.ready)
	twoCapture, twoHelpers := readAttentionLifecycle(t, twoFiles.ready)
	assertAttentionProcessesRunning(t, oneHelpers)
	assertAttentionProcessesRunning(t, twoHelpers)

	if oneCapture.root == twoCapture.root {
		t.Fatalf("same-project runtime sessions share attention root %q", oneCapture.root)
	}
	for _, capture := range []attentionLaunchCapture{oneCapture, twoCapture} {
		if capture.descriptor != filepath.Join(capture.root, "descriptor") {
			t.Fatalf("descriptor %q does not belong to root %q", capture.descriptor, capture.root)
		}
		if capture.state != filepath.Join(capture.root, capture.generation, "state") {
			t.Fatalf("state %q does not belong to generation %q", capture.state, capture.generation)
		}
	}

	writeTempFile(t, dir, filepath.Base(oneFiles.release), "release\n")
	waitAttentionHarness(t, one, 3*time.Second)
	waitForAttentionFile(t, oneFiles.cleaned, time.Second)
	assertAttentionProcessesStop(t, oneHelpers, 2*time.Second)
	if _, err := os.Stat(oneCapture.root); !os.IsNotExist(err) {
		t.Fatalf("first runtime root survived its cleanup: %v", err)
	}
	for _, livePath := range []string{twoCapture.root, twoCapture.descriptor, twoCapture.state} {
		if _, err := os.Stat(livePath); err != nil {
			t.Fatalf("first lifecycle cleanup affected second runtime %s: %v", livePath, err)
		}
	}
	if !isProcessRunning(two.cmd.Process.Pid) {
		t.Fatal("first lifecycle cleanup stopped the second runtime session")
	}
	for _, pid := range twoHelpers {
		if !isProcessRunning(pid) {
			t.Errorf("first lifecycle cleanup stopped second session helper %d", pid)
		}
	}

	writeTempFile(t, dir, filepath.Base(twoFiles.release), "release\n")
	waitAttentionHarness(t, two, 3*time.Second)
	waitForAttentionFile(t, twoFiles.cleaned, time.Second)
	assertAttentionProcessesStop(t, twoHelpers, 2*time.Second)
	if _, err := os.Stat(twoCapture.root); !os.IsNotExist(err) {
		t.Fatalf("second runtime root survived its cleanup: %v", err)
	}
}

func TestAttentionIntegrationWatcherFollowsToolRotationsWithoutRestart(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "watch.log")
	writeTempFile(t, dir, "settings", "tab_title=full\n")
	binDir := mockCommand(t, dir, "tmux", `
case "$1" in
  list-panes) printf '%%42\t1\n' ;;
esac
`)
	tmuxPath := filepath.Join(binDir, "tmux")
	root := projectRoot(t)
	script := fmt.Sprintf(`
source %q || exit 10
source %q || exit 11
source %q || exit 12
source %q || exit 13
WATCH_LOG=%q
apply_tab_title() { printf 'title:%%s:%%s:%%s\n' "$4" "$1" "$2" >> "$WATCH_LOG"; }
play_notification_sound() { printf 'sound:%%s\n' "$1" >> "$WATCH_LOG"; }
keep_awake_tick() { printf 'awake:%%s:%%s\n' "$EXPECTED_TOOL" "$4" >> "$WATCH_LOG"; }
apply_session_theme() { printf 'theme:%%s:%%s\n' "$EXPECTED_TOOL" "$3" >> "$WATCH_LOG"; }
publish_attention() {
  printf '1\t%%s\t1\tattention\t%%s\n' \
    "$WISP_DECK_ATTENTION_GENERATION" "$1" > "$WISP_DECK_ATTENTION_FILE.tmp"
  mv "$WISP_DECK_ATTENTION_FILE.tmp" "$WISP_DECK_ATTENTION_FILE"
}
attention_session_create %q >/dev/null || exit 14
attention_root=$WISP_DECK_ATTENTION_ROOT
attention_watcher_reset
for spec in claude:question codex:permission opencode:error; do
  current_tool=${spec%%%%:*}
  EXPECTED_TOOL=$current_tool
  reason=${spec#*:}
  attention_begin_generation "$attention_root" "$current_tool" >/dev/null || exit 15
  attention_watcher_tick same-project same-project full %q \
    "$WISP_DECK_ATTENTION_DESCRIPTOR" %q
  publish_attention "$reason"
  attention_watcher_tick same-project same-project full %q \
    "$WISP_DECK_ATTENTION_DESCRIPTOR" %q
done
attention_cleanup "$attention_root" || exit 16
`, filepath.Join(root, "lib", "attention.sh"), filepath.Join(root, "lib", "tui.sh"),
		filepath.Join(root, "lib", "theme.sh"), filepath.Join(root, "lib", "tab-title-watcher.sh"),
		logPath, dir, tmuxPath, dir, tmuxPath, dir)

	out, code := runBashSnippet(t, script, buildEnv(t, []string{binDir}))
	if code != 0 {
		t.Fatalf("dynamic watcher chain failed with %d: %s", code, out)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"theme:claude:209",
		"awake:claude:active",
		"title:claude:active:full",
		"awake:claude:waiting",
		"title:claude:waiting:full",
		"sound:claude",
		"theme:codex:36",
		"awake:codex:active",
		"title:codex:active:full",
		"awake:codex:waiting",
		"title:codex:waiting:full",
		"sound:codex",
		"theme:opencode:141",
		"awake:opencode:active",
		"title:opencode:active:full",
		"awake:opencode:waiting",
		"title:opencode:waiting:full",
		"sound:opencode",
	}, "\n") + "\n"
	if got := string(data); got != want {
		t.Fatalf("one watcher did not follow the live tool chain:\n got:\n%s\nwant:\n%s", got, want)
	}
}
