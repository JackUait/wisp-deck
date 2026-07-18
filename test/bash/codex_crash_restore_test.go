package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func codexCrashTmuxMock(t *testing.T, dir, identityA, identityB string, staleStampedIDs bool) string {
	t.Helper()
	stampedA, stampedB := "", ""
	if staleStampedIDs {
		stampedA = "WISP_DECK_CODEX_SESSION=cccccccc-cccc-4ccc-8ccc-cccccccccccc\\n"
		stampedB = "WISP_DECK_CODEX_SESSION=dddddddd-dddd-4ddd-8ddd-dddddddddddd\\n"
	}
	return mockCommand(t, dir, "tmux", fmt.Sprintf(`
case "$1" in
  list-sessions)
    printf '100 dev-app-1\n200 dev-app-2\n'
    ;;
  show-environment)
    case "$3" in
      dev-app-1)
        printf 'WISP_DECK=1\nWISP_DECK_BOOT=old-boot\nWISP_DECK_PROJECT=app\nWISP_DECK_PATH=/p/app\nWISP_DECK_TOOL=codex\nWISP_DECK_TERMINAL=ghostty\nWISP_DECK_CLAUDE_SESSION=aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa\n%sWISP_DECK_CODEX_SESSION_FILE=%s\n'
        ;;
      dev-app-2)
        printf 'WISP_DECK=1\nWISP_DECK_BOOT=old-boot\nWISP_DECK_PROJECT=app\nWISP_DECK_PATH=/p/app\nWISP_DECK_TOOL=codex\nWISP_DECK_TERMINAL=ghostty\nWISP_DECK_CLAUDE_SESSION=bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb\n%sWISP_DECK_CODEX_SESSION_FILE=%s\n'
        ;;
    esac
    ;;
  display-message) :
    ;;
esac
`, stampedA, identityA, stampedB, identityB))
}

func runCodexCrashQueueAndBuild(t *testing.T, configDir string) string {
	t.Helper()
	root := projectRoot(t)
	script := fmt.Sprintf(`
source %q
source %q
maybe_restore_session %q new-boot
first="$(restore_queue_pop %q new-boot)"
second="$(restore_queue_pop %q new-boot)"
build_restore() {
  local entry="$1" index="$2" path tool sid layout account identity_key
  IFS='|' read -r path tool sid layout account identity_key <<< "$entry"
  WISP_DECK_ATTENTION_FILE=%q
  WISP_DECK_ATTENTION_GENERATION=generation.Crash
  WISP_DECK_CODEX_SESSION_FILE=%q/new-"$index".codex
  WISP_DECK_RESUME=1
  WISP_DECK_RESUME_SESSION="$sid"
  build_ai_launch_cmd "$tool" /usr/bin/codex
}
printf 'ENTRY1=%%s\n' "$first"
printf 'ENTRY2=%%s\n' "$second"
printf 'CMD1=%%s\n' "$(build_restore "$first" 1)"
printf 'CMD2=%%s\n' "$(build_restore "$second" 2)"
`,
		filepath.Join(root, "lib", "session-restore.sh"),
		filepath.Join(root, "lib", "tmux-session.sh"),
		configDir, configDir, configDir,
		filepath.Join(configDir, "generation.Crash", "state"),
		filepath.Join(configDir, "session-identities"),
	)
	out, code := runBashSnippet(t, script, buildEnv(t, nil, "HOME="+t.TempDir()))
	assertExitCode(t, code, 0)
	return out
}

func TestCodexCrashRestorePreservesDistinctSameProjectThreads(t *testing.T) {
	dir := t.TempDir()
	identityDir := filepath.Join(dir, "session-identities")
	if err := os.Mkdir(identityDir, 0o700); err != nil {
		t.Fatal(err)
	}
	identityA := filepath.Join(identityDir, "dev-app-1.codex")
	identityB := filepath.Join(identityDir, "dev-app-2.codex")
	if err := os.WriteFile(identityA, []byte(codexSessionA+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identityB, []byte(codexSessionB+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	binDir := codexCrashTmuxMock(t, dir, identityA, identityB, true)
	snapshot := filepath.Join(dir, "last-session")
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snapshot}, buildEnv(t, []string{binDir}))
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	gotSnapshot := strings.TrimSpace(string(data))
	wantSnapshot := "old-boot|app|/p/app|codex|ghostty|" + codexSessionA + "|||dev-app-1.codex\n" +
		"old-boot|app|/p/app|codex|ghostty|" + codexSessionB + "|||dev-app-2.codex"
	if gotSnapshot != wantSnapshot {
		t.Fatalf("same-project snapshot:\n got %q\nwant %q", gotSnapshot, wantSnapshot)
	}
	assertNotContains(t, gotSnapshot, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	assertNotContains(t, gotSnapshot, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	assertNotContains(t, gotSnapshot, "cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	assertNotContains(t, gotSnapshot, "dddddddd-dddd-4ddd-8ddd-dddddddddddd")

	out := runCodexCrashQueueAndBuild(t, dir)
	assertSubstringsInOrder(t, out,
		"ENTRY1=/p/app|codex|"+codexSessionA,
		"ENTRY2=/p/app|codex|"+codexSessionB,
		"CMD1=wisp-deck-tui codex-adapter",
		"--resume-session "+codexSessionA,
		"CMD2=wisp-deck-tui codex-adapter",
		"--resume-session "+codexSessionB,
	)
	assertNotContains(t, out, "--resume-picker")
}

func TestCodexCrashRestoreMissingOneSidecarDegradesOnlyThatTabToPicker(t *testing.T) {
	dir := t.TempDir()
	identityDir := filepath.Join(dir, "session-identities")
	if err := os.Mkdir(identityDir, 0o700); err != nil {
		t.Fatal(err)
	}
	identityA := filepath.Join(identityDir, "dev-app-1.codex")
	identityB := filepath.Join(identityDir, "dev-app-2.codex")
	binDir := codexCrashTmuxMock(t, dir, identityA, identityB, false)
	snapshot := filepath.Join(dir, "last-session")
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snapshot}, buildEnv(t, []string{binDir}))
	assertExitCode(t, code, 0)
	if err := os.WriteFile(identityA, []byte(codexSessionA+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out := runCodexCrashQueueAndBuild(t, dir)
	assertContains(t, out, "ENTRY1=/p/app|codex|"+codexSessionA)
	assertContains(t, out, "ENTRY2=/p/app|codex||||dev-app-2.codex")
	assertContains(t, out, "CMD1=wisp-deck-tui codex-adapter")
	assertContains(t, out, "--resume-session "+codexSessionA)
	assertContains(t, out, "CMD2=wisp-deck-tui codex-adapter")
	if got := strings.Count(out, "--resume-picker"); got != 1 {
		t.Fatalf("resume picker count = %d, want exactly one\n%s", got, out)
	}
}

func TestCodexCrashRestoreQueuePrefersSidecarOverFrozenEmbeddedIdentity(t *testing.T) {
	dir := t.TempDir()
	identityDir := filepath.Join(dir, "session-identities")
	if err := os.Mkdir(identityDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(identityDir, "dev-app-1.codex"),
		[]byte(codexSessionB+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, dir, "last-session",
		"old-boot|app|/p/app|codex|ghostty|cccccccc-cccc-4ccc-8ccc-cccccccccccc|||dev-app-1.codex\n")

	out := runCodexCrashQueueAndBuild(t, dir)
	assertContains(t, out, "ENTRY1=/p/app|codex|"+codexSessionB)
	assertContains(t, out, "--resume-session "+codexSessionB)
	assertNotContains(t, out, "cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	assertNotContains(t, out, "--resume-picker")
}

func TestCodexCrashRestoreLegacySnapshotUsesPicker(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "last-session", "old-boot|app|/p/app|codex|ghostty|||\n")

	out := runCodexCrashQueueAndBuild(t, dir)
	assertContains(t, out, "ENTRY1=/p/app|codex||||")
	assertContains(t, out, "CMD1=wisp-deck-tui codex-adapter")
	assertContains(t, out, "--resume-picker")
	assertNotContains(t, out, "--resume-session")
}
