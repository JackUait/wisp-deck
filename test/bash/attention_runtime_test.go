package bash_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runAttentionBash(t *testing.T, body string, args ...string) (string, int) {
	t.Helper()
	module := filepath.Join(projectRoot(t), "lib", "attention.sh")
	script := "source \"$1\"\nshift\n" + body
	cmdArgs := append([]string{"-c", script, "attention-test", module}, args...)
	cmd := exec.Command("bash", cmdArgs...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return string(out), exitErr.ExitCode()
	}
	t.Fatalf("run attention bash: %v", err)
	return "", -1
}

func parseKeyLines(t *testing.T, output string) map[string]string {
	t.Helper()
	values := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("malformed output line %q in %q", line, output)
		}
		values[key] = value
	}
	return values
}

func assertPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode %s = %04o, want %04o", path, got, want)
	}
}

func createAttentionFixture(t *testing.T, base, tool string) map[string]string {
	t.Helper()
	out, code := runAttentionBash(t, `
attention_session_create "$1" >/dev/null || exit 50
attention_begin_generation "$WISP_DECK_ATTENTION_ROOT" "$2" >/dev/null || exit 51
printf 'root=%s\n' "$WISP_DECK_ATTENTION_ROOT"
printf 'descriptor=%s\n' "$WISP_DECK_ATTENTION_DESCRIPTOR"
printf 'generation=%s\n' "$WISP_DECK_ATTENTION_GENERATION"
printf 'state=%s\n' "$WISP_DECK_ATTENTION_FILE"
`, base, tool)
	if code != 0 {
		t.Fatalf("create attention fixture failed with %d: %s", code, out)
	}
	return parseKeyLines(t, out)
}

func TestAttentionRuntimeCreatesPrivateSessionAndGeneration(t *testing.T) {
	base := t.TempDir()
	out, code := runAttentionBash(t, `
set -u
attention_session_create "$1" >"$1/session.out"
root=$WISP_DECK_ATTENTION_ROOT
attention_begin_generation "$root" claude >"$1/generation.out"
printf 'root=%s\n' "$root"
printf 'descriptor=%s\n' "$WISP_DECK_ATTENTION_DESCRIPTOR"
printf 'generation=%s\n' "$WISP_DECK_ATTENTION_GENERATION"
printf 'state=%s\n' "$WISP_DECK_ATTENTION_FILE"
printf 'session_output=%s\n' "$(cat "$1/session.out")"
printf 'generation_output=%s\n' "$(cat "$1/generation.out")"
printf 'read_output=%s\n' "$(attention_read_descriptor "$WISP_DECK_ATTENTION_DESCRIPTOR")"
`, base)
	if code != 0 {
		t.Fatalf("attention runtime failed with %d: %s", code, out)
	}
	got := parseKeyLines(t, out)
	root := got["root"]
	descriptor := got["descriptor"]
	generation := got["generation"]
	state := got["state"]
	if root == "" || descriptor == "" || generation == "" || state == "" {
		t.Fatalf("runtime returned empty fields: %#v", got)
	}
	if got["session_output"] != root {
		t.Fatalf("attention_session_create output = %q, want %q", got["session_output"], root)
	}
	wantGenerationOutput := strings.Join([]string{generation, state, descriptor}, "\t")
	if got["generation_output"] != wantGenerationOutput {
		t.Fatalf("attention_begin_generation output = %q, want %q", got["generation_output"], wantGenerationOutput)
	}
	wantReadOutput := strings.Join([]string{generation, "claude", state}, "\t")
	if got["read_output"] != wantReadOutput {
		t.Fatalf("attention_read_descriptor output = %q, want %q", got["read_output"], wantReadOutput)
	}
	if descriptor != filepath.Join(root, "descriptor") {
		t.Fatalf("descriptor = %q, want session-local descriptor", descriptor)
	}
	if filepath.Dir(filepath.Dir(state)) != root || filepath.Base(state) != "state" {
		t.Fatalf("state path %q is not an immutable generation child of %q", state, root)
	}
	if filepath.Base(filepath.Dir(state)) != generation {
		t.Fatalf("state parent = %q, generation = %q", filepath.Base(filepath.Dir(state)), generation)
	}

	assertPerm(t, root, 0o700)
	owner := filepath.Join(root, "owner")
	assertPerm(t, owner, 0o600)
	assertPerm(t, filepath.Dir(state), 0o700)
	assertPerm(t, state, 0o600)
	assertPerm(t, descriptor, 0o600)
	stateRecord, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("1\t%s\t0\tunknown\t-\n", generation); string(stateRecord) != want {
		t.Fatalf("initial state = %q, want %q", stateRecord, want)
	}
	descriptorRecord, err := os.ReadFile(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("1\t%s\tclaude\t%s\n", generation, state); string(descriptorRecord) != want {
		t.Fatalf("descriptor = %q, want %q", descriptorRecord, want)
	}
	ownerRecord, err := os.ReadFile(owner)
	if err != nil {
		t.Fatal(err)
	}
	ownerFields := strings.Split(strings.TrimSuffix(string(ownerRecord), "\n"), "\t")
	if len(ownerFields) != 3 || ownerFields[0] != "1" || ownerFields[1] == "" {
		t.Fatalf("owner record = %q, want version, PID, and UTC process start", ownerRecord)
	}
	if _, err := time.Parse("Mon Jan _2 15:04:05 2006", ownerFields[2]); err != nil {
		t.Fatalf("owner start = %q: %v", ownerFields[2], err)
	}
}

func TestAttentionRuntimeStartsClaudeBackgroundCandidateDetached(t *testing.T) {
	dir := t.TempDir()
	attention := createAttentionFixture(t, dir, "claude")
	configRoot := filepath.Join(dir, "claude-account")
	wispConfig := filepath.Join(dir, "wisp-config")
	for _, path := range []string{configRoot, wispConfig} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	callLog := filepath.Join(dir, "candidate.args")
	errorLog := filepath.Join(dir, "candidate.err")
	fd3Log := filepath.Join(dir, "candidate.fd3")
	bin := mockCommand(t, dir, "wisp-deck-tui", `
printf '%s\n' "$@" > "$CALL_LOG"
if IFS= read -r leaked; then printf 'stdin-leak:%s\n' "$leaked" >> "$CALL_LOG"; fi
printf 'fd3-leak\n' >&3 2>/dev/null || true
printf 'stdout-must-be-dropped\n'
printf 'stderr-goes-to-log\n' >&2
`)
	body := fmt.Sprintf(`
exec 3>%q
attention_start_claude_background_candidate %q %q %q %q isolated %q
printf 'caller-continued\n'
`, fd3Log, filepath.Join(dir, "bin", "claude"), configRoot, wispConfig, attention["root"], errorLog)
	out, code := runBashSnippet(t,
		fmt.Sprintf("source %q\n%s", filepath.Join(projectRoot(t), "lib", "attention.sh"), body),
		buildEnv(t, []string{bin}, "CALL_LOG="+callLog))
	assertExitCode(t, code, 0)
	if out != "caller-continued\n" {
		t.Fatalf("candidate leaked terminal output: %q", out)
	}
	args := waitForFile(t, callLog, "background candidate did not start")
	wantArgs := strings.Join([]string{
		"claude-background",
		"--claude", filepath.Join(dir, "bin", "claude"),
		"--config-dir", configRoot,
		"--wisp-config-dir", wispConfig,
		"--owner-root", attention["root"],
	}, "\n") + "\n"
	if args != wantArgs {
		t.Fatalf("candidate argv = %q, want %q", args, wantArgs)
	}
	if strings.Contains(args, "stdin-leak") {
		t.Fatalf("candidate inherited terminal stdin: %q", args)
	}
	if got := waitForFile(t, errorLog, "candidate stderr was not isolated"); !strings.Contains(got, "stderr-goes-to-log") {
		t.Fatalf("candidate stderr log = %q", got)
	}
	if leaked, err := os.ReadFile(fd3Log); err != nil || len(leaked) != 0 {
		t.Fatalf("candidate inherited wrapper fd3: %q, %v", leaked, err)
	}
	candidateDir := filepath.Join(attention["root"], "claude-background-candidates")
	assertPerm(t, candidateDir, 0o700)
}

func TestAttentionRuntimeDefaultClaudeBackgroundCandidateUnsetsConfigMode(t *testing.T) {
	dir := t.TempDir()
	attention := createAttentionFixture(t, dir, "claude")
	configRoot := filepath.Join(dir, ".claude")
	wispConfig := filepath.Join(dir, "wisp-config")
	for _, path := range []string{configRoot, wispConfig} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	callLog := filepath.Join(dir, "candidate.args")
	bin := mockCommand(t, dir, "wisp-deck-tui", `printf '%s\n' "$@" > "$CALL_LOG"`)
	body := fmt.Sprintf(`
attention_start_claude_background_candidate %q %q %q %q default /dev/null
printf 'caller-continued\n'
`, filepath.Join(dir, "bin", "claude"), configRoot, wispConfig, attention["root"])
	out, code := runBashSnippet(t,
		fmt.Sprintf("source %q\n%s", filepath.Join(projectRoot(t), "lib", "attention.sh"), body),
		buildEnv(t, []string{bin}, "CALL_LOG="+callLog))
	assertExitCode(t, code, 0)
	if out != "caller-continued\n" {
		t.Fatalf("candidate leaked terminal output: %q", out)
	}
	args := waitForFile(t, callLog, "default background candidate did not start")
	wantArgs := strings.Join([]string{
		"claude-background",
		"--claude", filepath.Join(dir, "bin", "claude"),
		"--config-dir", configRoot,
		"--wisp-config-dir", wispConfig,
		"--owner-root", attention["root"],
		"--default-config",
	}, "\n") + "\n"
	if args != wantArgs {
		t.Fatalf("default candidate argv = %q, want %q", args, wantArgs)
	}
}

func TestAttentionRuntimeSeparatesSessionsAndFencesRotation(t *testing.T) {
	base := t.TempDir()
	out, code := runAttentionBash(t, `
set -u
attention_session_create "$1" >/dev/null
root_one=$WISP_DECK_ATTENTION_ROOT
attention_session_create "$1" >/dev/null
root_two=$WISP_DECK_ATTENTION_ROOT
attention_begin_generation "$root_one" claude >/dev/null
old_generation=$WISP_DECK_ATTENTION_GENERATION
old_state=$WISP_DECK_ATTENTION_FILE
attention_begin_generation "$root_one" codex >/dev/null
printf 'root_one=%s\n' "$root_one"
printf 'root_two=%s\n' "$root_two"
printf 'old_generation=%s\n' "$old_generation"
printf 'new_generation=%s\n' "$WISP_DECK_ATTENTION_GENERATION"
printf 'old_state=%s\n' "$old_state"
printf 'new_state=%s\n' "$WISP_DECK_ATTENTION_FILE"
printf 'read_output=%s\n' "$(attention_read_descriptor "$WISP_DECK_ATTENTION_DESCRIPTOR")"
`, base)
	if code != 0 {
		t.Fatalf("attention rotation failed with %d: %s", code, out)
	}
	got := parseKeyLines(t, out)
	if got["root_one"] == got["root_two"] {
		t.Fatalf("two sessions collided at %q", got["root_one"])
	}
	if got["old_generation"] == got["new_generation"] || got["old_state"] == got["new_state"] {
		t.Fatalf("rotation reused generation: %#v", got)
	}
	if _, err := os.Stat(filepath.Dir(got["old_state"])); !os.IsNotExist(err) {
		t.Fatalf("old generation parent still exists: %v", err)
	}
	if err := os.WriteFile(got["old_state"], []byte("late"), 0o600); !os.IsNotExist(err) {
		t.Fatalf("late writer error = %v, want missing parent", err)
	}
	if _, err := os.Stat(filepath.Dir(got["old_state"])); !os.IsNotExist(err) {
		t.Fatalf("late writer recreated old generation parent: %v", err)
	}
	if _, err := os.Stat(got["new_state"]); err != nil {
		t.Fatalf("new state missing: %v", err)
	}
	wantRead := strings.Join([]string{got["new_generation"], "codex", got["new_state"]}, "\t")
	if got["read_output"] != wantRead {
		t.Fatalf("rotated descriptor = %q, want %q", got["read_output"], wantRead)
	}
}

func TestAttentionRuntimeRejectsInvalidInputsAndDescriptors(t *testing.T) {
	base := t.TempDir()
	out, code := runAttentionBash(t, `
attention_session_create "$1" >/dev/null || exit 10
root=$WISP_DECK_ATTENTION_ROOT
if attention_begin_generation "$root" unsupported >/dev/null 2>&1; then exit 11; fi
[ ! -e "$root/descriptor" ] || exit 12
printf '%s\n' "$root"
`, base)
	if code != 0 {
		t.Fatalf("invalid tool check failed with %d: %s", code, out)
	}
	root := strings.TrimSpace(out)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "owner" {
		t.Fatalf("invalid tool left generation debris: %v", entries)
	}
	for name, tool := range map[string]string{
		"empty":   "",
		"case":    "Claude",
		"tab":     "claude\tother",
		"newline": "claude\nother",
	} {
		t.Run("tool "+name, func(t *testing.T) {
			if output, status := runAttentionBash(t, `attention_begin_generation "$1" "$2"`, root, tool); status == 0 {
				t.Fatalf("accepted invalid tool %q: %q", tool, output)
			}
		})
	}
	if output, status := runAttentionBash(t, `attention_begin_generation "$1" claude`, filepath.Join(base, "missing")); status == 0 {
		t.Fatalf("accepted missing root: %q", output)
	}
	nonDirectory := filepath.Join(base, "not-a-directory")
	if err := os.WriteFile(nonDirectory, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, status := runAttentionBash(t, `attention_begin_generation "$1" claude`, nonDirectory); status == 0 {
		t.Fatalf("accepted non-directory root: %q", output)
	}

	badBase := filepath.Join(base, "bad\tbase")
	if err := os.Mkdir(badBase, 0o700); err != nil {
		t.Fatal(err)
	}
	if output, status := runAttentionBash(t, `attention_session_create "$1"`, badBase); status == 0 {
		t.Fatalf("session creation accepted tab in path: %q", output)
	}

	descriptor := filepath.Join(base, "descriptor")
	if output, status := runAttentionBash(t, `attention_read_descriptor "$1"`, descriptor); status == 0 {
		t.Fatalf("accepted missing descriptor: %q", output)
	}
	badRecords := map[string]string{
		"empty":               "",
		"missing newline":     "1\tg\tclaude\t/tmp/state",
		"extra newline":       "1\tg\tclaude\t/tmp/state\n\n",
		"wrong version":       "2\tg\tclaude\t/tmp/state\n",
		"empty generation":    "1\t\tclaude\t/tmp/state\n",
		"unsupported tool":    "1\tg\tother\t/tmp/state\n",
		"empty state":         "1\tg\tclaude\t\n",
		"mismatched state":    "1\tgeneration.abcdef\tclaude\t/tmp/state\n",
		"extra field":         "1\tg\tclaude\t/tmp/state\textra\n",
		"carriage return":     "1\tg\tclaude\t/tmp/state\r\n",
		"second partial line": "1\tg\tclaude\t/tmp/state\ntrailer",
		"oversized":           "1\tg\tclaude\t/" + strings.Repeat("x", 4096) + "\n",
	}
	for name, record := range badRecords {
		name, record := name, record
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(descriptor, []byte(record), 0o600); err != nil {
				t.Fatal(err)
			}
			if output, status := runAttentionBash(t, `attention_read_descriptor "$1"`, descriptor); status == 0 {
				t.Fatalf("accepted malformed descriptor %q: %q", record, output)
			}
		})
	}
}

func TestAttentionRuntimeRejectsDescriptorThatEscapesSessionRoot(t *testing.T) {
	base := t.TempDir()
	attention := createAttentionFixture(t, base, "claude")
	victim := filepath.Join(base, "victim")
	sentinel := filepath.Join(victim, "keep")
	if err := os.Mkdir(victim, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sentinel, []byte("do not delete\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	escapingState := filepath.Join(attention["root"], "..", "victim", "state")
	forged := fmt.Sprintf("1\t../victim\tclaude\t%s\n", escapingState)
	if err := os.WriteFile(attention["descriptor"], []byte(forged), 0o600); err != nil {
		t.Fatal(err)
	}

	readOutput, readStatus := runAttentionBash(t, `attention_read_descriptor "$1"`, attention["descriptor"])
	rotateOutput, rotateStatus := runAttentionBash(t,
		`attention_begin_generation "$1" codex`, attention["root"])
	if readStatus == 0 {
		t.Errorf("reader accepted escaping descriptor: %q", readOutput)
	}
	if rotateStatus == 0 {
		t.Errorf("rotation accepted escaping descriptor: %q", rotateOutput)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "do not delete\n" {
		t.Fatalf("escaping descriptor deleted or changed sibling data: data=%q err=%v", data, err)
	}
}

func TestAttentionRuntimeCleanupRejectsUnownedLookalike(t *testing.T) {
	base := t.TempDir()
	lookalike := filepath.Join(base, "wisp-deck-attention.forged")
	sentinel := filepath.Join(lookalike, "keep")
	if err := os.Mkdir(lookalike, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sentinel, []byte("owned elsewhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	output, status := runAttentionBash(t, `attention_cleanup "$1"`, lookalike)
	if status == 0 {
		t.Errorf("cleanup accepted unowned lookalike: %q", output)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "owned elsewhere\n" {
		t.Fatalf("cleanup deleted or changed unowned data: data=%q err=%v", data, err)
	}
}

func TestAttentionRuntimeDescriptorReplacementIsAtomic(t *testing.T) {
	base := t.TempDir()
	attention := createAttentionFixture(t, base, "claude")
	oldDescriptor, err := os.ReadFile(attention["descriptor"])
	if err != nil {
		t.Fatal(err)
	}

	realMV, err := exec.LookPath("mv")
	if err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(base, "blocked")
	release := filepath.Join(base, "release")
	bin := mockCommand(t, base, "mv", `
last=""
for arg in "$@"; do last="$arg"; done
if [ "$last" = "$BLOCK_DESCRIPTOR" ]; then
  : > "$BLOCKED_FILE"
  while [ ! -e "$RELEASE_FILE" ]; do sleep 0.01; done
fi
exec "$REAL_MV" "$@"`)

	module := filepath.Join(projectRoot(t), "lib", "attention.sh")
	cmd := exec.Command("bash", "-c", `source "$1" && attention_begin_generation "$2" codex >/dev/null`,
		"attention-atomic", module, attention["root"])
	cmd.Env = buildEnv(t, []string{bin},
		"BLOCK_DESCRIPTOR="+attention["descriptor"],
		"BLOCKED_FILE="+blocked,
		"RELEASE_FILE="+release,
		"REAL_MV="+realMV,
	)
	var processOutput bytes.Buffer
	cmd.Stdout = &processOutput
	cmd.Stderr = &processOutput
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(blocked); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatalf("descriptor replacement never reached blocked rename: %s", processOutput.String())
		}
		time.Sleep(10 * time.Millisecond)
	}

	whileBlocked, err := os.ReadFile(attention["descriptor"])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(whileBlocked, oldDescriptor) {
		t.Fatalf("descriptor changed before atomic rename: got %q, want %q", whileBlocked, oldDescriptor)
	}
	if _, err := os.Stat(attention["state"]); err != nil {
		t.Fatalf("old state disappeared before descriptor replacement: %v", err)
	}
	read, status := runAttentionBash(t, `attention_read_descriptor "$1"`, attention["descriptor"])
	if status != 0 {
		t.Fatalf("reader failed while rename blocked: %s", read)
	}
	wantOldRead := strings.Join([]string{attention["generation"], "claude", attention["state"]}, "\t") + "\n"
	if read != wantOldRead {
		t.Fatalf("reader saw %q while blocked, want %q", read, wantOldRead)
	}

	if err := os.WriteFile(release, []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("rotation failed after release: %v: %s", err, processOutput.String())
		}
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Fatal("rotation did not finish after descriptor rename was released")
	}

	newDescriptor, err := os.ReadFile(attention["descriptor"])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(newDescriptor, oldDescriptor) {
		t.Fatal("descriptor still names the old generation after released rename")
	}
	fields := strings.Split(strings.TrimSuffix(string(newDescriptor), "\n"), "\t")
	if len(fields) != 4 || fields[0] != "1" || fields[2] != "codex" {
		t.Fatalf("new descriptor malformed: %q", newDescriptor)
	}
	if _, err := os.Stat(filepath.Dir(attention["state"])); !os.IsNotExist(err) {
		t.Fatalf("old generation remains after descriptor replacement: %v", err)
	}
}

func TestAttentionRuntimeRelaunchLockSerializesProcesses(t *testing.T) {
	base := t.TempDir()
	attention := createAttentionFixture(t, base, "claude")
	module := filepath.Join(projectRoot(t), "lib", "attention.sh")
	aAcquired := filepath.Join(base, "a-acquired")
	aRelease := filepath.Join(base, "a-release")
	bReady := filepath.Join(base, "b-ready")
	bAcquired := filepath.Join(base, "b-acquired")

	start := func(script string, args ...string) (*exec.Cmd, <-chan error, *bytes.Buffer) {
		t.Helper()
		cmdArgs := append([]string{"-c", script, "attention-lock"}, args...)
		cmd := exec.Command("bash", cmdArgs...)
		var output bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &output
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		return cmd, done, &output
	}
	waitForPath := func(path string, done <-chan error, output *bytes.Buffer) {
		t.Helper()
		deadline := time.NewTimer(15 * time.Second)
		defer deadline.Stop()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			if _, err := os.Stat(path); err == nil {
				return
			}
			select {
			case err := <-done:
				t.Fatalf("lock process exited before creating %s: %v: %s", filepath.Base(path), err, output.String())
			case <-deadline.C:
				t.Fatalf("timed out waiting for %s: %s", filepath.Base(path), output.String())
			case <-ticker.C:
			}
		}
	}
	waitDone := func(cmd *exec.Cmd, done <-chan error, output *bytes.Buffer) error {
		t.Helper()
		select {
		case err := <-done:
			return err
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Fatalf("lock process timed out: %s", output.String())
			return nil
		}
	}

	cmdA, doneA, outputA := start(`
source "$1" || exit 80
attention_relaunch_lock_acquire "$2" || exit 81
: > "$3"
while [ ! -e "$4" ]; do sleep 0.01; done
attention_relaunch_lock_release "$2" || exit 82
`, module, attention["root"], aAcquired, aRelease)
	waitForPath(aAcquired, doneA, outputA)

	cmdB, doneB, outputB := start(`
source "$1" || exit 90
: > "$3"
attention_relaunch_lock_acquire "$2" || exit 91
: > "$4"
attention_relaunch_lock_release "$2" || exit 92
`, module, attention["root"], bReady, bAcquired)
	waitForPath(bReady, doneB, outputB)
	time.Sleep(750 * time.Millisecond)
	_, earlyErr := os.Stat(bAcquired)

	if err := os.WriteFile(aRelease, []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := waitDone(cmdA, doneA, outputA); err != nil {
		t.Fatalf("first lock process failed: %v: %s", err, outputA.String())
	}
	if err := waitDone(cmdB, doneB, outputB); err != nil {
		t.Fatalf("second lock process failed: %v: %s", err, outputB.String())
	}
	if earlyErr == nil {
		t.Fatal("second process acquired relaunch lock before the first released it")
	}
	if _, err := os.Stat(bAcquired); err != nil {
		t.Fatalf("second process never acquired lock after release: %v", err)
	}
	if _, err := os.Stat(filepath.Join(attention["root"], ".relaunch-lock")); !os.IsNotExist(err) {
		t.Fatalf("relaunch lock leaked after both releases: %v", err)
	}
}

func TestAttentionRuntimeNeverReclaimsOwnerlessLiveLock(t *testing.T) {
	base := t.TempDir()
	attention := createAttentionFixture(t, base, "claude")
	module := filepath.Join(projectRoot(t), "lib", "attention.sh")
	lockDir := filepath.Join(attention["root"], ".relaunch-lock")
	chmodBlocked := filepath.Join(base, "chmod-blocked")
	chmodRelease := filepath.Join(base, "chmod-release")
	aAcquired := filepath.Join(base, "a-acquired-ownerless")
	aRelease := filepath.Join(base, "a-release-ownerless")
	bReady := filepath.Join(base, "b-ready-ownerless")
	bAcquired := filepath.Join(base, "b-acquired-ownerless")
	bRelease := filepath.Join(base, "b-release-ownerless")

	realChmod, err := exec.LookPath("chmod")
	if err != nil {
		t.Fatal(err)
	}
	bin := mockCommand(t, base, "chmod", `
last=""
for arg in "$@"; do last="$arg"; done
if [ "$last" = "$BLOCK_LOCK_DIR" ]; then
  : > "$CHMOD_BLOCKED"
  while [ ! -e "$CHMOD_RELEASE" ]; do sleep 0.01; done
fi
exec "$REAL_CHMOD" "$@"`)

	start := func(script string, env []string, args ...string) (*exec.Cmd, <-chan error, *bytes.Buffer) {
		t.Helper()
		cmdArgs := append([]string{"-c", script, "attention-ownerless"}, args...)
		cmd := exec.Command("bash", cmdArgs...)
		cmd.Env = env
		var output bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &output
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		return cmd, done, &output
	}
	waitFor := func(path string, done <-chan error, output *bytes.Buffer) {
		t.Helper()
		deadline := time.NewTimer(15 * time.Second)
		defer deadline.Stop()
		for {
			if _, err := os.Stat(path); err == nil {
				return
			}
			select {
			case err := <-done:
				t.Fatalf("process exited before %s: %v: %s", filepath.Base(path), err, output.String())
			case <-deadline.C:
				t.Fatalf("timed out waiting for %s: %s", filepath.Base(path), output.String())
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	waitDone := func(cmd *exec.Cmd, done <-chan error, output *bytes.Buffer) error {
		t.Helper()
		select {
		case err := <-done:
			return err
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			return fmt.Errorf("timed out: %s", output.String())
		}
	}

	envA := buildEnv(t, []string{bin},
		"BLOCK_LOCK_DIR="+lockDir,
		"CHMOD_BLOCKED="+chmodBlocked,
		"CHMOD_RELEASE="+chmodRelease,
		"REAL_CHMOD="+realChmod,
	)
	cmdA, doneA, outputA := start(`
source "$1" || exit 100
attention_relaunch_lock_acquire "$2" || exit 101
: > "$3"
while [ ! -e "$4" ]; do sleep 0.01; done
attention_relaunch_lock_release "$2" || exit 102
`, envA, module, attention["root"], aAcquired, aRelease)
	waitFor(chmodBlocked, doneA, outputA)

	cmdB, doneB, outputB := start(`
source "$1" || exit 110
: > "$3"
attention_relaunch_lock_acquire "$2" || exit 111
: > "$4"
while [ ! -e "$5" ]; do sleep 0.01; done
attention_relaunch_lock_release "$2" || exit 112
`, buildEnv(t, nil), module, attention["root"], bReady, bAcquired, bRelease)
	waitFor(bReady, doneB, outputB)
	time.Sleep(3 * time.Second)
	_, earlyErr := os.Stat(bAcquired)

	if err := os.WriteFile(chmodRelease, []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(aAcquired, doneA, outputA)
	if err := os.WriteFile(aRelease, []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	aErr := waitDone(cmdA, doneA, outputA)
	waitFor(bAcquired, doneB, outputB)
	if err := os.WriteFile(bRelease, []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bErr := waitDone(cmdB, doneB, outputB)

	if earlyErr == nil {
		t.Error("waiter reclaimed an ownerless lock while its creator was still alive")
	}
	if aErr != nil {
		t.Errorf("owner process failed: %v: %s", aErr, outputA.String())
	}
	if bErr != nil {
		t.Errorf("waiter process failed: %v: %s", bErr, outputB.String())
	}
}

func TestAttentionRuntimeCleanupRemovesOnlySessionRoot(t *testing.T) {
	base := t.TempDir()
	out, code := runAttentionBash(t, `
attention_session_create "$1" >/dev/null || exit 40
root_one=$WISP_DECK_ATTENTION_ROOT
attention_begin_generation "$root_one" opencode >/dev/null || exit 41
attention_session_create "$1" >/dev/null || exit 42
root_two=$WISP_DECK_ATTENTION_ROOT
attention_begin_generation "$root_two" claude >/dev/null || exit 43
attention_cleanup "$root_one" || exit 44
[ ! -e "$root_one" ] || exit 45
[ -d "$root_two" ] || exit 46
[ -d "$1" ] || exit 44
attention_cleanup "$root_one" || exit 47
attention_cleanup "$root_two" || exit 48
printf 'cleaned\n'
`, base)
	if code != 0 || strings.TrimSpace(out) != "cleaned" {
		t.Fatalf("attention cleanup failed with %d: %q", code, out)
	}
}

func TestAttentionRuntimeDoesNotLeakCallerUmask(t *testing.T) {
	base := t.TempDir()
	out, code := runAttentionBash(t, `
umask 000
before=$(umask)
attention_session_create "$1" >/dev/null || exit 60
attention_begin_generation "$WISP_DECK_ATTENTION_ROOT" claude >/dev/null || exit 61
after=$(umask)
printf 'before=%s\n' "$before"
printf 'after=%s\n' "$after"
printf 'root=%s\n' "$WISP_DECK_ATTENTION_ROOT"
printf 'state=%s\n' "$WISP_DECK_ATTENTION_FILE"
printf 'descriptor=%s\n' "$WISP_DECK_ATTENTION_DESCRIPTOR"
`, base)
	if code != 0 {
		t.Fatalf("umask runtime failed with %d: %s", code, out)
	}
	got := parseKeyLines(t, out)
	if got["before"] != got["after"] {
		t.Fatalf("caller umask changed from %q to %q", got["before"], got["after"])
	}
	assertPerm(t, got["root"], 0o700)
	assertPerm(t, filepath.Dir(got["state"]), 0o700)
	assertPerm(t, got["state"], 0o600)
	assertPerm(t, got["descriptor"], 0o600)
}

func TestAttentionRuntimeWorksWhenSourcedByZsh(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not available")
	}
	base := t.TempDir()
	module := filepath.Join(projectRoot(t), "lib", "attention.sh")
	cmd := exec.Command(zsh, "-c", `
source "$1" || exit 70
attention_session_create "$2" >/dev/null || exit 71
attention_begin_generation "$WISP_DECK_ATTENTION_ROOT" codex >/dev/null || exit 72
attention_relaunch_lock_acquire "$WISP_DECK_ATTENTION_ROOT" || exit 73
attention_relaunch_lock_release "$WISP_DECK_ATTENTION_ROOT" || exit 74
attention_read_descriptor "$WISP_DECK_ATTENTION_DESCRIPTOR"`, "attention-zsh", module, base)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("zsh attention runtime failed: %v: %s", err, out)
	}
	fields := strings.Split(strings.TrimSpace(string(out)), "\t")
	if len(fields) != 3 || fields[0] == "" || fields[1] != "codex" || fields[2] == "" {
		t.Fatalf("zsh descriptor output malformed: %q", out)
	}
}

func TestAttentionRuntimeWrapperLifecycleOrdering(t *testing.T) {
	wrapperPath := filepath.Join(projectRoot(t), "wrapper.sh")
	data, err := os.ReadFile(wrapperPath)
	if err != nil {
		t.Fatal(err)
	}
	wrapper := string(data)

	libsStart := strings.Index(wrapper, "_gt_libs=(")
	if libsStart < 0 {
		t.Fatal("wrapper library list not found")
	}
	libsEnd := strings.Index(wrapper[libsStart:], ")")
	if libsEnd < 0 {
		t.Fatal("wrapper library list not found")
	}
	libs := wrapper[libsStart : libsStart+libsEnd]
	attentionLib := strings.Index(libs, "attention")
	accountSwitchLib := strings.Index(libs, "account-switch")
	if attentionLib < 0 || accountSwitchLib < 0 || attentionLib > accountSwitchLib {
		t.Fatal("wrapper must source attention.sh before account-switch.sh")
	}

	create := strings.Index(wrapper, "attention_session_create")
	begin := strings.Index(wrapper, "attention_begin_generation")
	build := strings.Index(wrapper, `AI_TOOL_CMD="$(resolve_ai_tool_cmd`)
	if create < 0 || begin < 0 || build < 0 || !(create < begin && begin < build) {
		t.Fatalf("initial attention lifecycle order invalid: create=%d begin=%d build=%d", create, begin, build)
	}

	var newSessionLine string
	for _, line := range strings.Split(wrapper, "\n") {
		if strings.Contains(line, `"$TMUX_CMD" new-session`) {
			newSessionLine = line
			break
		}
	}
	if newSessionLine == "" {
		t.Fatal("tmux new-session line not found")
	}
	startWatcher := strings.Index(wrapper, "start_tab_title_watcher")
	newSession := strings.Index(wrapper, `"$TMUX_CMD" new-session`)
	if startWatcher < 0 || newSession < 0 || startWatcher > newSession {
		t.Fatalf("tab-title watcher must start before tmux new-session: watcher=%d new-session=%d", startWatcher, newSession)
	}
	for _, name := range []string{
		"WISP_DECK_ATTENTION_ROOT",
		"WISP_DECK_ATTENTION_DESCRIPTOR",
		"WISP_DECK_ATTENTION_GENERATION",
		"WISP_DECK_ATTENTION_FILE",
	} {
		if !strings.Contains(newSessionLine, name+"=") {
			t.Errorf("tmux new-session does not stamp %s", name)
		}
	}

	cleanupStart := strings.Index(wrapper, "cleanup() {")
	cleanupEnd := strings.Index(wrapper, "trap cleanup")
	if cleanupStart < 0 || cleanupEnd < cleanupStart {
		t.Fatal("wrapper cleanup block not found")
	}
	cleanup := wrapper[cleanupStart:cleanupEnd]
	titleCleanup := strings.Index(cleanup, "stop_tab_title_watcher")
	heartbeatCleanup := strings.Index(cleanup, `kill_tree "$HEARTBEAT_PID"`)
	tmuxCleanup := strings.Index(cleanup, "cleanup_tmux_session")
	attentionCleanup := strings.Index(cleanup, "attention_cleanup")
	if titleCleanup < 0 || heartbeatCleanup < 0 || tmuxCleanup < 0 || attentionCleanup < 0 ||
		!(titleCleanup < heartbeatCleanup && heartbeatCleanup < tmuxCleanup && tmuxCleanup < attentionCleanup) {
		t.Fatalf("cleanup must stop title, heartbeat, and focus helpers before removing attention root: title=%d heartbeat=%d focus=%d attention=%d",
			titleCleanup, heartbeatCleanup, tmuxCleanup, attentionCleanup)
	}
	if !strings.Contains(cleanup, `cleanup_tmux_session "$SESSION_NAME" "$WATCHER_PID" "$TMUX_CMD"`) {
		t.Fatal("wrapper cleanup does not pass the focus watcher to cleanup_tmux_session")
	}
}
