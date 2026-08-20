package bash_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func quote(s string) string { return "\"" + s + "\"" }

// A window_layout string exactly reproduces every pane's position and size.
// It contains commas and braces but never '|', so it is a safe trailing field.
const sampleLayout = "bdba,204x50,0,0{152x50,0,0,1,51x50,153,0,2}"

const (
	codexSessionA = "11111111-1111-4111-8111-111111111111"
	codexSessionB = "22222222-2222-4222-8222-222222222222"
)

func TestPruneCodexSessionIdentitiesRemovesOnlyOldUnreferencedFiles(t *testing.T) {
	dir := t.TempDir()
	identityDir := filepath.Join(dir, "session-identities")
	if err := os.Mkdir(identityDir, 0o700); err != nil {
		t.Fatal(err)
	}
	keys := []string{
		"stale.codex",
		"recent.codex",
		"snapshot.codex",
		"previous.codex",
		"queued.codex",
		"live.codex",
	}
	old := time.Now().Add(-45 * 24 * time.Hour)
	for _, key := range keys {
		path := filepath.Join(identityDir, key)
		if err := os.WriteFile(path, []byte(codexSessionA+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if key != "recent.codex" {
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	writeTempFile(t, dir, "last-session",
		"old|app|/p/app|codex|ghostty|"+codexSessionA+"|||snapshot.codex\n")
	writeTempFile(t, dir, "last-session.prev",
		"old|app|/p/app|codex|ghostty|"+codexSessionA+"|||previous.codex\n")
	writeTempFile(t, dir, "restore-queue",
		"/p/app|codex|"+codexSessionA+"|||queued.codex\n")

	tmuxBody := `
case "$1" in
  list-sessions) echo "dev-live-1" ;;
  show-environment)
    printf 'WISP_DECK=1\nWISP_DECK_CODEX_SESSION_FILE=` + filepath.Join(identityDir, "live.codex") + `\n' ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	_, code := runBashFunc(
		t,
		"lib/session-restore.sh",
		"prune_codex_session_identities",
		[]string{"tmux", dir, "30"},
		buildEnv(t, []string{binDir}),
	)
	assertExitCode(t, code, 0)

	if _, err := os.Stat(filepath.Join(identityDir, "stale.codex")); !os.IsNotExist(err) {
		t.Fatalf("old unreferenced identity survived pruning: %v", err)
	}
	for _, key := range keys[1:] {
		if _, err := os.Stat(filepath.Join(identityDir, key)); err != nil {
			t.Fatalf("protected identity %q was pruned: %v", key, err)
		}
	}
}

func TestPruneCodexSessionIdentitiesDefersWhenTmuxInspectionFails(t *testing.T) {
	dir := t.TempDir()
	identityDir := filepath.Join(dir, "session-identities")
	if err := os.Mkdir(identityDir, 0o700); err != nil {
		t.Fatal(err)
	}
	identityFile := filepath.Join(identityDir, "possibly-live.codex")
	if err := os.WriteFile(identityFile, []byte(codexSessionA+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-45 * 24 * time.Hour)
	if err := os.Chtimes(identityFile, old, old); err != nil {
		t.Fatal(err)
	}
	binDir := mockCommand(t, dir, "tmux", "exit 1")

	_, code := runBashFunc(
		t,
		"lib/session-restore.sh",
		"prune_codex_session_identities",
		[]string{"tmux", dir, "30"},
		buildEnv(t, []string{binDir}),
	)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(identityFile); err != nil {
		t.Fatalf("identity was pruned while tmux inspection failed: %v", err)
	}
}

func TestWriteSessionSnapshot_captures_window_layout(t *testing.T) {
	// The snapshot must record each session's exact pane geometry (tmux's
	// #{window_layout}) as a 7th field so restore can reproduce the panes at
	// the positions they held when Wisp Deck was closed.
	dir := t.TempDir()
	tmuxBody := `
case "$1" in
  list-sessions) echo "100 dev-app-1" ;;
  show-environment)
    printf 'WISP_DECK=1\nWISP_DECK_BOOT=111\nWISP_DECK_PROJECT=app\nWISP_DECK_PATH=/p/app\nWISP_DECK_TOOL=claude\nWISP_DECK_TERMINAL=ghostty\n' ;;
  display-message) echo "` + sampleLayout + `" ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	env := buildEnv(t, []string{binDir})
	snap := filepath.Join(dir, "last-session")
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(snap)
	if err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "111|app|/p/app|claude|ghostty||" + sampleLayout + "||"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteSessionSnapshot_empty_layout_when_unavailable(t *testing.T) {
	// Old tmux, a race, or an unreachable window may yield no layout. The 7th
	// field is then empty and restore falls back to the default pane split.
	dir := t.TempDir()
	tmuxBody := `
case "$1" in
  list-sessions) echo "100 dev-app-1" ;;
  show-environment)
    printf 'WISP_DECK=1\nWISP_DECK_BOOT=111\nWISP_DECK_PROJECT=app\nWISP_DECK_PATH=/p/app\nWISP_DECK_TOOL=claude\nWISP_DECK_TERMINAL=ghostty\n' ;;
  display-message) : ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	env := buildEnv(t, []string{binDir})
	snap := filepath.Join(dir, "last-session")
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(snap)
	if err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "111|app|/p/app|claude|ghostty||||"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteSessionSnapshot_excludesAttentionRuntimeFields(t *testing.T) {
	dir := t.TempDir()
	tmuxBody := `
case "$1" in
  list-sessions) echo "100 dev-app-1" ;;
  show-environment)
    printf 'WISP_DECK=1\nWISP_DECK_BOOT=111\nWISP_DECK_PROJECT=app\nWISP_DECK_PATH=/p/app\nWISP_DECK_TOOL=claude\nWISP_DECK_TERMINAL=ghostty\nWISP_DECK_ATTENTION_ROOT=/poison/root\nWISP_DECK_ATTENTION_DESCRIPTOR=/poison/descriptor\nWISP_DECK_ATTENTION_GENERATION=generation.poison\nWISP_DECK_ATTENTION_FILE=/poison/state\n' ;;
  display-message) : ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	snapshot := filepath.Join(dir, "last-session")
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snapshot}, buildEnv(t, []string{binDir}))
	assertExitCode(t, code, 0)

	data, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}
	got := strings.TrimSpace(string(data))
	if want := "111|app|/p/app|claude|ghostty||||"; got != want {
		t.Fatalf("snapshot imported attention runtime fields: got %q, want %q", got, want)
	}
	for _, poison := range []string{"/poison/root", "/poison/descriptor", "generation.poison", "/poison/state"} {
		if strings.Contains(got, poison) {
			t.Errorf("snapshot contains prior attention value %q", poison)
		}
	}
}

func TestMaybeRestore_carries_layout_into_queue(t *testing.T) {
	// The captured layout must ride the snapshot through queue construction —
	// including the unstamped-duplicate dedup pass — into the queue entry as a
	// trailing field, so the restoring wrapper can apply it.
	dir := t.TempDir()
	home := t.TempDir()
	writeTranscript(t, home, "/p/app", "sid-a", 1*time.Hour)
	writeTempFile(t, dir, "last-session",
		"111|app|/p/app|claude|ghostty|sid-a|"+sampleLayout+"\n")
	_, code := runMaybeRestoreHome(t, dir, "222", home)
	assertExitCode(t, code, 0)
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatalf("restore-queue not written: %v", err)
	}
	got := strings.TrimSpace(string(queue))
	want := "222|/p/app|claude|sid-a|" + sampleLayout + "||"
	if got != want {
		t.Errorf("queue:\n got %q\nwant %q", got, want)
	}
}

func TestCurrentBootId_uses_bootsessionuuid(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "sysctl", `
case "$*" in
  *kern.bootsessionuuid*) echo "996F1E8F-46BF-4D0A-8D21-FD8D13555B47" ;;
  *kern.boottime*) echo "{ sec = 1700000000, usec = 123456 } Thu Jan  1 00:00:00 2024" ;;
  *) exit 1 ;;
esac`)
	env := buildEnv(t, []string{binDir})
	out, code := runBashFunc(t, "lib/session-restore.sh", "current_boot_id", nil, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "996F1E8F-46BF-4D0A-8D21-FD8D13555B47" {
		t.Errorf("got %q, want bootsessionuuid", strings.TrimSpace(out))
	}
}

// Regression: kern.boottime is now-minus-uptime, so an NTP clock step right
// after login shifts it (observed drifting 1s between two wrapper launches of
// the same boot). A boottime-derived id made the second wrapper treat the same
// boot as a NEW boot: it rebuilt the restore queue from the snapshot and
// re-restored sessions the first chain had already opened — duplicate tabs.
// The id must come from kern.bootsessionuuid, which never moves within a boot.
func TestCurrentBootId_stable_when_boottime_drifts(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, "calls")
	binDir := mockCommand(t, dir, "sysctl", `
case "$*" in
  *kern.bootsessionuuid*) echo "996F1E8F-46BF-4D0A-8D21-FD8D13555B47" ;;
  *kern.boottime*)
    n=$(cat `+quote(counter)+` 2>/dev/null || echo 0)
    echo $((n + 1)) > `+quote(counter)+`
    echo "{ sec = $((1783268852 + n)), usec = 135586 } Sun Jul  5 19:27:33 2026" ;;
  *) exit 1 ;;
esac`)
	env := buildEnv(t, []string{binDir})
	first, code := runBashFunc(t, "lib/session-restore.sh", "current_boot_id", nil, env)
	assertExitCode(t, code, 0)
	second, code := runBashFunc(t, "lib/session-restore.sh", "current_boot_id", nil, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(first) == "" {
		t.Fatal("boot id is empty")
	}
	if strings.TrimSpace(first) != strings.TrimSpace(second) {
		t.Errorf("boot id changed within one boot: %q then %q",
			strings.TrimSpace(first), strings.TrimSpace(second))
	}
}

func TestCurrentBootId_falls_back_to_boottime_sec(t *testing.T) {
	dir := t.TempDir()
	// Older macOS without kern.bootsessionuuid: only kern.boottime answers.
	binDir := mockCommand(t, dir, "sysctl", `
case "$*" in
  *kern.bootsessionuuid*) exit 1 ;;
  *kern.boottime*) echo "{ sec = 1700000000, usec = 123456 } Thu Jan  1 00:00:00 2024" ;;
  *) exit 1 ;;
esac`)
	env := buildEnv(t, []string{binDir})
	out, code := runBashFunc(t, "lib/session-restore.sh", "current_boot_id", nil, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "1700000000" {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), "1700000000")
	}
}

func TestCurrentBootId_empty_when_sysctl_fails(t *testing.T) {
	dir := t.TempDir()
	binDir := mockCommand(t, dir, "sysctl", `exit 1`)
	env := buildEnv(t, []string{binDir})
	out, _ := runBashFunc(t, "lib/session-restore.sh", "current_boot_id", nil, env)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty, got %q", strings.TrimSpace(out))
	}
}

// A sysctl mock for drift scenarios: bootsessionuuid answers with a fixed
// uuid, boottime with the given sec value.
func mockSysctl(t *testing.T, dir string, boottimeSec string) string {
	t.Helper()
	return mockCommand(t, dir, "sysctl", `
case "$*" in
  *kern.bootsessionuuid*) echo "996F1E8F-46BF-4D0A-8D21-FD8D13555B47" ;;
  *kern.boottime*) echo "{ sec = `+boottimeSec+`, usec = 135586 } Sun Jul  5 19:27:33 2026" ;;
  *) exit 1 ;;
esac`)
}

// Upgrade transition: sessions started before the uuid-based boot id shipped
// carry a NUMERIC WISP_DECK_BOOT from the CURRENT boot (kern.boottime sec,
// possibly drifted by an NTP step). Such entries must not be queued — they are
// alive right now, and restoring them duplicates their tabs.
func TestMaybeRestore_skips_legacy_numeric_ids_of_current_boot(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	writeTempFile(t, dir, "last-session",
		"1783268852|app|/p/app|claude|ghostty||\n"+ // this boot, drifted legacy id
			"1783091438|web|/p/web|claude|ghostty||\n") // genuinely previous boot
	env := buildEnv(t, []string{mockSysctl(t, dir, "1783268853")}, "HOME="+home)
	_, code := runMaybeRestoreEnv(t, dir, "996F1E8F-46BF-4D0A-8D21-FD8D13555B47", env)
	assertExitCode(t, code, 0)
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatalf("restore-queue not written: %v", err)
	}
	got := strings.TrimSpace(string(queue))
	want := "996F1E8F-46BF-4D0A-8D21-FD8D13555B47|/p/web|claude||||"
	if got != want {
		t.Errorf("queue:\n got %q\nwant %q", got, want)
	}
}

// Legacy fallback systems (no kern.bootsessionuuid) still derive the id from
// kern.boottime, which an NTP clock step shifts. A marker stamped with the
// pre-step value must still gate the post-step launch of the SAME boot —
// rebuilding the queue here re-restored already-open sessions (duplicate tabs).
func TestMaybeRestore_marker_gate_tolerates_boottime_drift(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	writeTempFile(t, dir, "last-session", "1783091438|web|/p/web|claude|ghostty||\n")
	writeTempFile(t, dir, "last-restore-boot", "1783268852\n")
	env := buildEnv(t, []string{mockSysctl(t, dir, "1783268853")}, "HOME="+home)
	_, code := runMaybeRestoreEnv(t, dir, "1783268853", env)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(filepath.Join(dir, "restore-queue")); err == nil {
		t.Error("queue was rebuilt within the same boot (marker gate failed on drift)")
	}
}

// Same drift on legacy systems mid-chain: a queue built with the pre-step id
// must still be consumable by a tab that computed the post-step id, instead of
// being discarded as another boot's queue (which broke the restore chain).
func TestRestoreQueuePop_tolerates_boottime_drift(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "restore-queue", "1783268852|/p/app|claude|sid-a|\n")
	env := buildEnv(t, []string{mockSysctl(t, dir, "1783268853")})
	out, code := runBashFunc(t, "lib/session-restore.sh", "restore_queue_pop",
		[]string{dir, "1783268853"}, env)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "/p/app|claude|sid-a|" {
		t.Errorf("got %q, want popped entry", strings.TrimSpace(out))
	}
	if _, err := os.Stat(filepath.Join(dir, "restore-queue")); err == nil {
		t.Error("single-entry queue should be removed after pop")
	}
}

// A snapshot must never yield two queue entries for the same conversation:
// whatever upstream failure duplicates a snapshot line (heartbeat race, a
// re-merged store), restoring the same sid twice opens duplicate tabs.
func TestMaybeRestore_dedupes_duplicate_sids(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	writeTranscript(t, home, "/p/app", "sid-a", 1*time.Hour)
	writeTempFile(t, dir, "last-session",
		"111|app|/p/app|claude|ghostty|sid-a|\n"+
			"111|app|/p/app|claude|ghostty|sid-a|\n")
	_, code := runMaybeRestoreHome(t, dir, "222", home)
	assertExitCode(t, code, 0)
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatalf("restore-queue not written: %v", err)
	}
	got := strings.TrimSpace(string(queue))
	want := "222|/p/app|claude|sid-a|||"
	if got != want {
		t.Errorf("queue:\n got %q\nwant %q", got, want)
	}
}

func TestMaybeRestoreDoesNotDedupeSameIDAcrossTools(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	writeTranscript(t, home, "/p/claude", codexSessionA, time.Hour)
	writeTempFile(t, dir, "last-session",
		"111|claude|/p/claude|claude|ghostty|"+codexSessionA+"|||\n"+
			"111|codex|/p/codex|codex|ghostty|"+codexSessionA+"|||\n")

	_, code := runMaybeRestoreHome(t, dir, "222", home)
	assertExitCode(t, code, 0)
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(queue))
	want := "222|/p/claude|claude|" + codexSessionA + "|||\n" +
		"222|/p/codex|codex|" + codexSessionA + "|||"
	if got != want {
		t.Fatalf("cross-tool identities were deduplicated:\n got %q\nwant %q", got, want)
	}
}

// If a claim file from THIS boot exists under a different id form (a legacy
// numeric claim left by a pre-uuid wrapper, or a drifted boottime id), the
// queue was already built this boot. Rebuilding it would resurrect entries
// other tabs already popped — duplicate tabs. The claim must gate regardless
// of which id form stamped it.
func TestMaybeRestore_blocked_by_current_boot_claim_of_other_id_form(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	writeTempFile(t, dir, "last-session", "1783091438|web|/p/web|claude|ghostty||\n")
	writeTempFile(t, dir, "last-restore-boot.1783268852", "")
	env := buildEnv(t, []string{mockSysctl(t, dir, "1783268853")}, "HOME="+home)
	_, code := runMaybeRestoreEnv(t, dir, "996F1E8F-46BF-4D0A-8D21-FD8D13555B47", env)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(filepath.Join(dir, "restore-queue")); err == nil {
		t.Error("queue rebuilt despite an existing current-boot claim (other id form)")
	}
}

// A prior-boot claim must still be swept so it can never block a real new
// boot's restore.
func TestMaybeRestore_sweeps_prior_boot_claims(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	writeTempFile(t, dir, "last-session", "1783091438|web|/p/web|claude|ghostty||\n")
	writeTempFile(t, dir, "last-restore-boot.1783091440", "") // prior boot's claim
	env := buildEnv(t, []string{mockSysctl(t, dir, "1783268853")}, "HOME="+home)
	_, code := runMaybeRestoreEnv(t, dir, "996F1E8F-46BF-4D0A-8D21-FD8D13555B47", env)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(filepath.Join(dir, "last-restore-boot.1783091440")); err == nil {
		t.Error("prior-boot claim not swept")
	}
	if _, err := os.Stat(filepath.Join(dir, "restore-queue")); err != nil {
		t.Error("queue must be built after sweeping a prior-boot claim")
	}
}

// Last-line duplicate defense: an entry whose conversation is already open in
// an alive Wisp Deck session must be refused, no matter how it got queued.
func TestRestoreEntryWanted_refuses_sid_already_open(t *testing.T) {
	dir := t.TempDir()
	projDir := t.TempDir()
	tmuxBody := `
case "$1" in
  list-sessions) echo "dev-app-1" ;;
  show-environment)
    printf 'WISP_DECK=1\nWISP_DECK_CLAUDE_SESSION=sid-open\n' ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	env := buildEnv(t, []string{binDir})

	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_entry_wanted",
		[]string{"tmux", projDir + "|claude|sid-open|"}, env)
	if code == 0 {
		t.Error("entry with an already-open sid must be refused")
	}
	_, code = runBashFunc(t, "lib/session-restore.sh", "restore_entry_wanted",
		[]string{"tmux", projDir + "|claude|sid-fresh|"}, env)
	if code != 0 {
		t.Error("entry with a fresh sid must be accepted")
	}
	// Empty sid carries no identity — must not be refused (legit multi-tab
	// projects on old snapshots would otherwise be dropped).
	_, code = runBashFunc(t, "lib/session-restore.sh", "restore_entry_wanted",
		[]string{"tmux", projDir + "|claude||"}, env)
	if code != 0 {
		t.Error("entry with an empty sid must be accepted")
	}
	// Missing project directory is still refused.
	_, code = runBashFunc(t, "lib/session-restore.sh", "restore_entry_wanted",
		[]string{"tmux", "/nonexistent/gone|claude|sid-fresh|"}, env)
	if code == 0 {
		t.Error("entry with a missing project dir must be refused")
	}
}

func TestRestoreEntryWantedCodexConsultsLiveSidecar(t *testing.T) {
	dir := t.TempDir()
	projDir := t.TempDir()
	identityFile := filepath.Join(dir, "live.codex")
	if err := os.WriteFile(identityFile, []byte(codexSessionA+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tmuxBody := `
case "$1" in
  list-sessions) echo "dev-app-1" ;;
  show-environment)
    printf 'WISP_DECK=1\nWISP_DECK_TOOL=codex\nWISP_DECK_CODEX_SESSION_FILE=` + identityFile + `\n' ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	env := buildEnv(t, []string{binDir})

	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_entry_wanted",
		[]string{"tmux", projDir + "|codex|" + codexSessionA + "|||live.codex"}, env)
	if code == 0 {
		t.Fatal("Codex entry already open through its live sidecar was accepted")
	}
	_, code = runBashFunc(t, "lib/session-restore.sh", "restore_entry_wanted",
		[]string{"tmux", projDir + "|codex|" + codexSessionB + "|||other.codex"}, env)
	if code != 0 {
		t.Fatal("different Codex identity was refused")
	}
}

// Restore decisions must be reconstructable after the fact: queue builds and
// pops append to restore.log.
func TestRestoreLog_records_build_and_pop(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	writeTempFile(t, dir, "last-session", "111|web|/p/web|claude|ghostty||\n")
	// Build and first pop happen in ONE process, as in the real wrapper — the
	// builder pre-acquires the pop lock at build time and its own pop consumes
	// the handoff (see maybe_restore_session).
	root := projectRoot(t)
	script := `
source ` + quote(filepath.Join(root, "lib", "session-restore.sh")) + `
maybe_restore_session ` + quote(dir) + ` 222
restore_queue_pop ` + quote(dir) + ` 222
`
	out, code := runBashSnippet(t, script, buildEnv(t, nil, "HOME="+home))
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) == "" {
		t.Fatal("pop returned nothing")
	}
	log, err := os.ReadFile(filepath.Join(dir, "restore.log"))
	if err != nil {
		t.Fatalf("restore.log not written: %v", err)
	}
	assertContains(t, string(log), "queue-built")
	assertContains(t, string(log), "popped")
}

// runMaybeRestoreEnv runs maybe_restore_session with a caller-built env
// (mocked sysctl in PATH, HOME override).
func runMaybeRestoreEnv(t *testing.T, configDir, curBoot string, env []string) (string, int) {
	t.Helper()
	root := projectRoot(t)
	mod := filepath.Join(root, "lib", "session-restore.sh")
	script := `
source ` + quote(mod) + `
maybe_restore_session ` + quote(configDir) + ` ` + quote(curBoot) + `
`
	return runBashSnippet(t, script, env)
}

// referenced by later tasks; keep import of filepath/os used
var _ = filepath.Join
var _ = os.Environ

// Helper: run maybe_restore_session with a stub launch_restore_window that
// records every call (one line per spawn) to recFile. The new queue-based
// restore must never spawn windows from maybe_restore_session.
func runMaybeRestore(t *testing.T, configDir, curBoot, recFile string) (string, int) {
	t.Helper()
	root := projectRoot(t)
	mod := filepath.Join(root, "lib", "session-restore.sh")
	script := `
source ` + quote(mod) + `
launch_restore_window() { echo "$1|$2|$3|$4" >> ` + quote(recFile) + `; }
maybe_restore_session ` + quote(configDir) + ` ` + quote(curBoot) + ` "/w/wrapper.sh"
`
	return runBashSnippet(t, script, nil)
}

func TestMaybeRestore_writes_ordered_queue_and_marker(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	// First line carries a Claude conversation id (6th field, backed by a
	// resumable transcript); second is an old-format 5-field line — its queue
	// entry gets an empty id.
	writeTranscript(t, home, "/p/app", "sid-a", 1*time.Hour)
	writeTempFile(t, dir, "last-session",
		"111|app|/p/app|claude|ghostty|sid-a\n111|web|/p/web|opencode|ghostty\n")
	_, code := runMaybeRestoreHome(t, dir, "222", home)
	assertExitCode(t, code, 0)
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatalf("restore-queue not written: %v", err)
	}
	got := strings.TrimSpace(string(queue))
	want := "222|/p/app|claude|sid-a|||\n222|/p/web|opencode||||"
	if got != want {
		t.Errorf("queue:\n got %q\nwant %q", got, want)
	}
	marker, _ := os.ReadFile(filepath.Join(dir, "last-restore-boot"))
	if strings.TrimSpace(string(marker)) != "222" {
		t.Errorf("marker = %q, want 222", string(marker))
	}
}

func TestMaybeRestore_noop_when_already_restored_this_boot(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "last-session", "111|app|/p/app|claude|ghostty\n")
	writeTempFile(t, dir, "last-restore-boot", "222\n")
	rec := filepath.Join(dir, "rec")
	_, code := runMaybeRestore(t, dir, "222", rec)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(rec); err == nil {
		t.Error("expected no spawns when boot already restored")
	}
	if _, err := os.Stat(filepath.Join(dir, "restore-queue")); err == nil {
		t.Error("expected no queue when boot already restored")
	}
}

func TestMaybeRestore_noop_when_no_snapshot(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	_, code := runMaybeRestore(t, dir, "222", rec)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(rec); err == nil {
		t.Error("expected no spawns when snapshot missing")
	}
}

func TestMaybeRestore_skips_current_boot_lines(t *testing.T) {
	dir := t.TempDir()
	// All lines are from the current boot -> nothing to restore.
	writeTempFile(t, dir, "last-session", "222|app|/p/app|claude|ghostty\n")
	rec := filepath.Join(dir, "rec")
	_, code := runMaybeRestore(t, dir, "222", rec)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(filepath.Join(dir, "restore-queue")); err == nil {
		t.Error("expected no queue when all lines are current-boot")
	}
	// marker must NOT be written when nothing was restored
	if _, err := os.Stat(filepath.Join(dir, "last-restore-boot")); err == nil {
		t.Error("marker should not be written when nothing restored")
	}
}

func TestMaybeRestore_noop_when_claim_already_taken(t *testing.T) {
	// Two wrappers may start simultaneously at login. The claim file is the
	// atomic gate: if it already exists for this boot, a second caller must
	// not rebuild the queue (which would resurrect already-popped entries).
	dir := t.TempDir()
	writeTempFile(t, dir, "last-session", "111|app|/p/app|claude|ghostty\n")
	writeTempFile(t, dir, "last-restore-boot.222", "")
	rec := filepath.Join(dir, "rec")
	_, code := runMaybeRestore(t, dir, "222", rec)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(filepath.Join(dir, "restore-queue")); err == nil {
		t.Error("expected no queue when claim for this boot already exists")
	}
}

func TestWriteSessionSnapshot_writes_ghost_sessions_only(t *testing.T) {
	dir := t.TempDir()
	// Mock tmux: list two sessions; only dev-app-1 carries WISP_DECK=1.
	tmuxBody := `
case "$1" in
  list-sessions) echo "100 dev-app-1"; echo "200 other-sess" ;;
  show-environment)
    if [ "$3" = "dev-app-1" ]; then
      printf 'WISP_DECK=1\nWISP_DECK_BOOT=111\nWISP_DECK_PROJECT=app\nWISP_DECK_PATH=/p/app\nWISP_DECK_TOOL=claude\nWISP_DECK_TERMINAL=ghostty\n'
    else
      printf 'SOMEVAR=1\n'
    fi ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	env := buildEnv(t, []string{binDir})
	snap := filepath.Join(dir, "last-session")
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(snap)
	if err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "111|app|/p/app|claude|ghostty||||"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteSessionSnapshot_includes_claude_session_id(t *testing.T) {
	// The statusline stamps WISP_DECK_CLAUDE_SESSION into the tmux session
	// env; the snapshot must carry it (6th field) so restore can reopen each
	// tab's own conversation instead of `claude -c` (which resumes the same,
	// most recent one for every tab of a project).
	dir := t.TempDir()
	tmuxBody := `
case "$1" in
  list-sessions) echo "100 dev-app-1" ;;
  show-environment)
    printf 'WISP_DECK=1\nWISP_DECK_BOOT=111\nWISP_DECK_PROJECT=app\nWISP_DECK_PATH=/p/app\nWISP_DECK_TOOL=claude\nWISP_DECK_TERMINAL=ghostty\nWISP_DECK_CLAUDE_SESSION=sid-42\n' ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	env := buildEnv(t, []string{binDir})
	snap := filepath.Join(dir, "last-session")
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(snap)
	if err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "111|app|/p/app|claude|ghostty|sid-42|||"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteSessionSnapshotCodexUsesCodexIDNotClaudeID(t *testing.T) {
	dir := t.TempDir()
	tmuxBody := `
case "$1" in
  list-sessions) echo "100 dev-app-1" ;;
  show-environment)
    printf 'WISP_DECK=1\nWISP_DECK_BOOT=111\nWISP_DECK_PROJECT=app\nWISP_DECK_PATH=/p/app\nWISP_DECK_TOOL=codex\nWISP_DECK_TERMINAL=ghostty\nWISP_DECK_CLAUDE_SESSION=aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa\nWISP_DECK_CODEX_SESSION=` + codexSessionA + `\n' ;;
  display-message) : ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	snap := filepath.Join(dir, "last-session")
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, buildEnv(t, []string{binDir}))
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(snap)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(data)),
		"111|app|/p/app|codex|ghostty|"+codexSessionA+"|||"; got != want {
		t.Fatalf("Codex snapshot = %q, want %q", got, want)
	}
}

func TestWriteSessionSnapshotCodexReadsDurableIdentity(t *testing.T) {
	dir := t.TempDir()
	identityDir := filepath.Join(dir, "session-identities")
	if err := os.Mkdir(identityDir, 0o700); err != nil {
		t.Fatal(err)
	}
	identityFile := filepath.Join(identityDir, "dev-app-1.codex")
	if err := os.WriteFile(identityFile, []byte(codexSessionB+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tmuxBody := `
case "$1" in
  list-sessions) echo "100 dev-app-1" ;;
  show-environment)
    printf 'WISP_DECK=1\nWISP_DECK_BOOT=111\nWISP_DECK_PROJECT=app\nWISP_DECK_PATH=/p/app\nWISP_DECK_TOOL=codex\nWISP_DECK_TERMINAL=ghostty\nWISP_DECK_CODEX_SESSION_FILE=` + identityFile + `\n' ;;
  display-message) : ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	snap := filepath.Join(dir, "last-session")
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, buildEnv(t, []string{binDir}))
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(snap)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(data)),
		"111|app|/p/app|codex|ghostty|"+codexSessionB+"|||dev-app-1.codex"; got != want {
		t.Fatalf("sidecar-backed snapshot = %q, want %q", got, want)
	}
}

func TestWriteSessionSnapshotCodexRejectsMalformedIdentity(t *testing.T) {
	dir := t.TempDir()
	identityDir := filepath.Join(dir, "session-identities")
	if err := os.Mkdir(identityDir, 0o700); err != nil {
		t.Fatal(err)
	}
	identityFile := filepath.Join(identityDir, "dev-app-1.codex")
	if err := os.WriteFile(identityFile, []byte("NOT-A-CODEX-UUID\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tmuxBody := `
case "$1" in
  list-sessions) echo "100 dev-app-1" ;;
  show-environment)
    printf 'WISP_DECK=1\nWISP_DECK_BOOT=111\nWISP_DECK_PROJECT=app\nWISP_DECK_PATH=/p/app\nWISP_DECK_TOOL=codex\nWISP_DECK_TERMINAL=ghostty\nWISP_DECK_CODEX_SESSION=AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA\nWISP_DECK_CODEX_SESSION_FILE=` + identityFile + `\n' ;;
  display-message) : ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	snap := filepath.Join(dir, "last-session")
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, buildEnv(t, []string{binDir}))
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(snap)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(data)),
		"111|app|/p/app|codex|ghostty||||dev-app-1.codex"; got != want {
		t.Fatalf("malformed Codex snapshot = %q, want %q", got, want)
	}
}

func TestMaybeRestoreCodexResolvesIdentityKeyIntoQueue(t *testing.T) {
	dir := t.TempDir()
	identityDir := filepath.Join(dir, "session-identities")
	if err := os.Mkdir(identityDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(identityDir, "dev-app-1.codex"), []byte(codexSessionA+"\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	writeTempFile(t, dir, "last-session",
		"111|app|/p/app|codex|ghostty||||dev-app-1.codex\n")

	_, code := runMaybeRestoreHome(t, dir, "222", t.TempDir())
	assertExitCode(t, code, 0)
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(queue)),
		"222|/p/app|codex|"+codexSessionA+"|||dev-app-1.codex"; got != want {
		t.Fatalf("Codex restore queue = %q, want %q", got, want)
	}
}

func TestMaybeRestoreCodexKeepsMissingLegacyIDForPicker(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "last-session", "111|app|/p/app|codex|ghostty\n")

	_, code := runMaybeRestoreHome(t, dir, "222", t.TempDir())
	assertExitCode(t, code, 0)
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(queue)), "222|/p/app|codex||||"; got != want {
		t.Fatalf("legacy Codex restore queue = %q, want picker entry %q", got, want)
	}
}

func TestWriteSessionSnapshot_preserves_file_when_tmux_dead(t *testing.T) {
	dir := t.TempDir()
	snap := writeTempFile(t, dir, "last-session", "111|app|/p/app|claude|ghostty\n")
	// tmux server is dead: list-sessions exits 1.
	tmuxBody := `
case "$1" in
  list-sessions) exit 1 ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	env := buildEnv(t, []string{binDir})
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(snap)
	if err != nil {
		t.Fatalf("snapshot disappeared: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "111|app|/p/app|claude|ghostty"
	if got != want {
		t.Errorf("snapshot was wiped when tmux dead: got %q, want %q", got, want)
	}
}

// runMaybeRestoreHome is runMaybeRestore with a HOME override so the
// transcript lookup under ~/.claude/projects/ can be faked.
func runMaybeRestoreHome(t *testing.T, configDir, curBoot, home string) (string, int) {
	t.Helper()
	root := projectRoot(t)
	mod := filepath.Join(root, "lib", "session-restore.sh")
	script := `
source ` + quote(mod) + `
maybe_restore_session ` + quote(configDir) + ` ` + quote(curBoot) + `
`
	return runBashSnippet(t, script, buildEnv(t, nil, "HOME="+home))
}

// writeTranscript creates a fake Claude conversation transcript for a project
// path (munged as Claude does: every non-alphanumeric byte becomes '-') with
// the given mtime age. The content includes a model turn ("type":"assistant")
// — that is what makes a real transcript resumable.
func writeTranscript(t *testing.T, home, projPath, sid string, age time.Duration) {
	t.Helper()
	writeTranscriptRaw(t, home, projPath, sid,
		"{\"type\":\"user\"}\n{\"type\":\"assistant\"}\n", age)
}

// writeTranscriptRaw is writeTranscript with explicit file content, for
// faking transcripts that are NOT resumable (no model turn).
func writeTranscriptRaw(t *testing.T, home, projPath, sid, content string, age time.Duration) {
	t.Helper()
	munged := ""
	for _, r := range projPath {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			munged += string(r)
		} else {
			munged += "-"
		}
	}
	dir := filepath.Join(home, ".claude", "projects", munged)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir transcripts: %v", err)
	}
	f := filepath.Join(dir, sid+".jsonl")
	if err := os.WriteFile(f, []byte(content), 0644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	ts := time.Now().Add(-age)
	if err := os.Chtimes(f, ts, ts); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
}

func TestMaybeRestore_assigns_distinct_transcripts_to_unstamped_duplicates(t *testing.T) {
	// Two tabs of the same project whose conversation ids were never stamped
	// (e.g. claude launched before the stamping update and sat idle). Plain
	// `-c` would open the SAME most-recent conversation in both. The queue
	// builder must pin each to a distinct recent transcript instead.
	dir := t.TempDir()
	home := t.TempDir()
	writeTranscript(t, home, "/p/app", "sid-new", 1*time.Hour)
	writeTranscript(t, home, "/p/app", "sid-old", 2*time.Hour)
	writeTempFile(t, dir, "last-session",
		"111|app|/p/app|claude|ghostty|\n111|app|/p/app|claude|ghostty|\n")
	_, code := runMaybeRestoreHome(t, dir, "222", home)
	assertExitCode(t, code, 0)
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatalf("restore-queue not written: %v", err)
	}
	got := strings.TrimSpace(string(queue))
	want := "222|/p/app|claude|sid-new|||\n222|/p/app|claude|sid-old|||"
	if got != want {
		t.Errorf("queue:\n got %q\nwant %q", got, want)
	}
}

func TestMaybeRestore_duplicate_fill_skips_stamped_sids(t *testing.T) {
	// One tab of the pair did stamp its id (the most recent transcript);
	// the unstamped one must get the NEXT transcript, not the same one.
	dir := t.TempDir()
	home := t.TempDir()
	writeTranscript(t, home, "/p/app", "sid-new", 1*time.Hour)
	writeTranscript(t, home, "/p/app", "sid-old", 2*time.Hour)
	writeTempFile(t, dir, "last-session",
		"111|app|/p/app|claude|ghostty|sid-new\n111|app|/p/app|claude|ghostty|\n")
	_, code := runMaybeRestoreHome(t, dir, "222", home)
	assertExitCode(t, code, 0)
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatalf("restore-queue not written: %v", err)
	}
	got := strings.TrimSpace(string(queue))
	want := "222|/p/app|claude|sid-new|||\n222|/p/app|claude|sid-old|||"
	if got != want {
		t.Errorf("queue:\n got %q\nwant %q", got, want)
	}
}

func TestMaybeRestore_single_unstamped_entry_keeps_c_fallback(t *testing.T) {
	// A lone tab of a project needs no pinning — `claude -c` already reopens
	// its most recent conversation, and guessing a transcript adds risk.
	dir := t.TempDir()
	home := t.TempDir()
	writeTranscript(t, home, "/p/app", "sid-new", 1*time.Hour)
	writeTempFile(t, dir, "last-session",
		"111|app|/p/app|claude|ghostty|\n")
	_, code := runMaybeRestoreHome(t, dir, "222", home)
	assertExitCode(t, code, 0)
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatalf("restore-queue not written: %v", err)
	}
	got := strings.TrimSpace(string(queue))
	want := "222|/p/app|claude||||"
	if got != want {
		t.Errorf("queue:\n got %q\nwant %q", got, want)
	}
}

func TestMaybeRestore_duplicate_fill_survives_missing_transcript_dir(t *testing.T) {
	// No transcript store for the project (e.g. brand-new install): the
	// duplicates keep the empty id and both fall back to `claude -c`.
	dir := t.TempDir()
	home := t.TempDir()
	writeTempFile(t, dir, "last-session",
		"111|app|/p/app|claude|ghostty|\n111|app|/p/app|claude|ghostty|\n")
	_, code := runMaybeRestoreHome(t, dir, "222", home)
	assertExitCode(t, code, 0)
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatalf("restore-queue not written: %v", err)
	}
	got := strings.TrimSpace(string(queue))
	want := "222|/p/app|claude||||\n222|/p/app|claude||||"
	if got != want {
		t.Errorf("queue:\n got %q\nwant %q", got, want)
	}
}

func TestMaybeRestore_blanks_stamped_sid_without_transcript(t *testing.T) {
	// The statusline stamps whatever session_id claude currently shows — for a
	// brand-new or just-/clear'd session no transcript exists yet, and
	// `claude --resume <id>` fails hard ("No conversation found") and exits to
	// a bare shell. A stamped id with no transcript on disk must be blanked so
	// the tab falls back to the safe `claude -c`.
	dir := t.TempDir()
	home := t.TempDir()
	writeTranscript(t, home, "/p/app", "sid-real", 1*time.Hour)
	writeTempFile(t, dir, "last-session",
		"111|app|/p/app|claude|ghostty|sid-dead\n")
	_, code := runMaybeRestoreHome(t, dir, "222", home)
	assertExitCode(t, code, 0)
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatalf("restore-queue not written: %v", err)
	}
	got := strings.TrimSpace(string(queue))
	want := "222|/p/app|claude||||"
	if got != want {
		t.Errorf("queue:\n got %q\nwant %q", got, want)
	}
}

func TestMaybeRestore_blanks_stamped_sid_without_model_turn(t *testing.T) {
	// A transcript can exist yet be unresumable: claude marks sessions with no
	// model turn (no assistant reply yet) as non-resumable, and --resume fails
	// on them exactly like on a missing file. Such an id must be blanked too.
	dir := t.TempDir()
	home := t.TempDir()
	writeTranscriptRaw(t, home, "/p/app", "sid-empty",
		"{\"type\":\"user\"}\n", 1*time.Hour)
	writeTempFile(t, dir, "last-session",
		"111|app|/p/app|claude|ghostty|sid-empty\n")
	_, code := runMaybeRestoreHome(t, dir, "222", home)
	assertExitCode(t, code, 0)
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatalf("restore-queue not written: %v", err)
	}
	got := strings.TrimSpace(string(queue))
	want := "222|/p/app|claude||||"
	if got != want {
		t.Errorf("queue:\n got %q\nwant %q", got, want)
	}
}

func TestMaybeRestore_keeps_stamped_sid_with_resumable_transcript(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	writeTranscript(t, home, "/p/app", "sid-good", 1*time.Hour)
	writeTempFile(t, dir, "last-session",
		"111|app|/p/app|claude|ghostty|sid-good\n")
	_, code := runMaybeRestoreHome(t, dir, "222", home)
	assertExitCode(t, code, 0)
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatalf("restore-queue not written: %v", err)
	}
	got := strings.TrimSpace(string(queue))
	want := "222|/p/app|claude|sid-good|||"
	if got != want {
		t.Errorf("queue:\n got %q\nwant %q", got, want)
	}
}

func TestMaybeRestore_dead_stamped_duplicate_gets_pinned_distinct(t *testing.T) {
	// Two tabs of one project: one stamped with a dead id, one unstamped.
	// After blanking the dead id both are unstamped duplicates, so the pinning
	// logic must give each a distinct resumable transcript.
	dir := t.TempDir()
	home := t.TempDir()
	writeTranscript(t, home, "/p/app", "sid-new", 1*time.Hour)
	writeTranscript(t, home, "/p/app", "sid-old", 2*time.Hour)
	writeTempFile(t, dir, "last-session",
		"111|app|/p/app|claude|ghostty|sid-dead\n111|app|/p/app|claude|ghostty|\n")
	_, code := runMaybeRestoreHome(t, dir, "222", home)
	assertExitCode(t, code, 0)
	queue, err := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if err != nil {
		t.Fatalf("restore-queue not written: %v", err)
	}
	got := strings.TrimSpace(string(queue))
	want := "222|/p/app|claude|sid-new|||\n222|/p/app|claude|sid-old|||"
	if got != want {
		t.Errorf("queue:\n got %q\nwant %q", got, want)
	}
}

func TestMaybeRestore_backs_up_prior_snapshot(t *testing.T) {
	// The heartbeat rewrites last-session from currently-alive sessions soon
	// after restore starts, destroying the only pointers to the pre-reboot
	// tabs. Keep a copy so a broken restore chain stays recoverable.
	dir := t.TempDir()
	snapContent := "111|app|/p/app|claude|ghostty|sid-a\n"
	writeTempFile(t, dir, "last-session", snapContent)
	rec := filepath.Join(dir, "rec")
	_, code := runMaybeRestore(t, dir, "222", rec)
	assertExitCode(t, code, 0)
	prev, err := os.ReadFile(filepath.Join(dir, "last-session.prev"))
	if err != nil {
		t.Fatalf("last-session.prev not written: %v", err)
	}
	if string(prev) != snapContent {
		t.Errorf("backup = %q, want %q", string(prev), snapContent)
	}
}

func TestClaudePickTranscript_skips_transcript_without_model_turn(t *testing.T) {
	// The mtime-based pick must never pin a tab to an unresumable transcript —
	// `claude --resume` would fail and dump the tab to a bare shell.
	home := t.TempDir()
	writeTranscriptRaw(t, home, "/p/app", "sid-unresumable",
		"{\"type\":\"user\"}\n", 1*time.Hour)
	writeTranscript(t, home, "/p/app", "sid-resumable", 2*time.Hour)
	out, code := runBashFunc(t, "lib/session-restore.sh", "claude_pick_transcript",
		[]string{"/p/app", ""}, buildEnv(t, nil, "HOME="+home))
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "sid-resumable" {
		t.Errorf("picked %q, want %q", strings.TrimSpace(out), "sid-resumable")
	}
}

func TestWriteSessionSnapshot_noop_while_restore_queue_fresh(t *testing.T) {
	// While a restore chain is draining (fresh restore-queue), the heartbeat
	// must not rewrite the snapshot: the alive sessions are only the
	// restored-so-far subset, and overwriting would lose the rest.
	dir := t.TempDir()
	snap := writeTempFile(t, dir, "last-session", "111|app|/p/app|claude|ghostty|sid-a\n")
	writeTempFile(t, dir, "restore-queue", "222|/p/web|claude|sid-b\n")
	tmuxBody := `
case "$1" in
  list-sessions) echo "100 dev-app-1" ;;
  show-environment)
    printf 'WISP_DECK=1\nWISP_DECK_BOOT=222\nWISP_DECK_PROJECT=app\nWISP_DECK_PATH=/p/app\nWISP_DECK_TOOL=claude\nWISP_DECK_TERMINAL=ghostty\n' ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	env := buildEnv(t, []string{binDir})
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, env)
	assertExitCode(t, code, 0)
	data, _ := os.ReadFile(snap)
	got := strings.TrimSpace(string(data))
	want := "111|app|/p/app|claude|ghostty|sid-a"
	if got != want {
		t.Errorf("snapshot rewritten during restore: got %q, want %q", got, want)
	}
}

func TestWriteSessionSnapshot_writes_after_restore_queue_stale(t *testing.T) {
	// A stale queue (>5 min — the chain broke) must not freeze the snapshot
	// forever; normal heartbeat snapshotting resumes.
	dir := t.TempDir()
	snap := writeTempFile(t, dir, "last-session", "111|app|/p/app|claude|ghostty|sid-a\n")
	queue := writeTempFile(t, dir, "restore-queue", "222|/p/web|claude|sid-b\n")
	old := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(queue, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	tmuxBody := `
case "$1" in
  list-sessions) echo "100 dev-app-1" ;;
  show-environment)
    printf 'WISP_DECK=1\nWISP_DECK_BOOT=222\nWISP_DECK_PROJECT=app\nWISP_DECK_PATH=/p/app\nWISP_DECK_TOOL=claude\nWISP_DECK_TERMINAL=ghostty\n' ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	env := buildEnv(t, []string{binDir})
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, env)
	assertExitCode(t, code, 0)
	data, _ := os.ReadFile(snap)
	got := strings.TrimSpace(string(data))
	want := "222|app|/p/app|claude|ghostty||||"
	if got != want {
		t.Errorf("snapshot not rewritten after queue went stale: got %q, want %q", got, want)
	}
}

func TestWriteSessionSnapshot_removes_stale_tmp_files(t *testing.T) {
	// A heartbeat SIGKILL'd mid-write (e.g. at shutdown) leaves its
	// last-session.tmp.<pid> behind forever. The next snapshot write must
	// sweep such debris — but only stale files, never a fresh tmp that a
	// concurrent writer is about to mv into place.
	dir := t.TempDir()
	snap := writeTempFile(t, dir, "last-session", "111|app|/p/app|claude|ghostty|\n")
	staleTmp := writeTempFile(t, dir, "last-session.tmp.12345", "")
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(staleTmp, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	freshTmp := writeTempFile(t, dir, "last-session.tmp.67890", "")
	// tmux server dead: the function returns before writing, but the sweep
	// must still have happened.
	binDir := mockCommand(t, dir, "tmux", `exit 1`)
	env := buildEnv(t, []string{binDir})
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, env)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(staleTmp); err == nil {
		t.Error("stale tmp file not removed")
	}
	if _, err := os.Stat(freshTmp); err != nil {
		t.Error("fresh tmp file must be kept (concurrent writer)")
	}
}

func TestRestoreQueuePop_pops_first_line_and_keeps_rest(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "restore-queue",
		"222|/p/app|claude|sid-a\n222|/p/web|opencode\n")
	out, code := runBashFunc(t, "lib/session-restore.sh", "restore_queue_pop",
		[]string{dir, "222"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "/p/app|claude|sid-a" {
		t.Errorf("pop = %q, want %q", strings.TrimSpace(out), "/p/app|claude|sid-a")
	}
	rest, _ := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if strings.TrimSpace(string(rest)) != "222|/p/web|opencode" {
		t.Errorf("remaining queue = %q, want %q", strings.TrimSpace(string(rest)), "222|/p/web|opencode")
	}
}

func TestRestoreQueuePop_removes_file_after_last_entry(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "restore-queue", "222|/p/app|claude\n")
	out, code := runBashFunc(t, "lib/session-restore.sh", "restore_queue_pop",
		[]string{dir, "222"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "/p/app|claude" {
		t.Errorf("pop = %q, want %q", strings.TrimSpace(out), "/p/app|claude")
	}
	if _, err := os.Stat(filepath.Join(dir, "restore-queue")); err == nil {
		t.Error("queue file should be removed after last entry is popped")
	}
}

func TestRestoreQueuePop_empty_when_no_queue(t *testing.T) {
	dir := t.TempDir()
	out, code := runBashFunc(t, "lib/session-restore.sh", "restore_queue_pop",
		[]string{dir, "222"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty, got %q", strings.TrimSpace(out))
	}
}

func TestRestoreQueuePop_discards_queue_on_boot_mismatch(t *testing.T) {
	// A queue left over from a previous boot must never be consumed.
	dir := t.TempDir()
	writeTempFile(t, dir, "restore-queue", "111|/p/app|claude\n")
	out, code := runBashFunc(t, "lib/session-restore.sh", "restore_queue_pop",
		[]string{dir, "222"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty on boot mismatch, got %q", strings.TrimSpace(out))
	}
	if _, err := os.Stat(filepath.Join(dir, "restore-queue")); err == nil {
		t.Error("stale-boot queue should be deleted")
	}
}

func TestRestoreQueuePop_discards_stale_queue(t *testing.T) {
	// A broken chain must not hijack a tab the user opens much later.
	dir := t.TempDir()
	q := writeTempFile(t, dir, "restore-queue", "222|/p/app|claude\n")
	old := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(q, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	out, code := runBashFunc(t, "lib/session-restore.sh", "restore_queue_pop",
		[]string{dir, "222"}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty for stale queue, got %q", strings.TrimSpace(out))
	}
	if _, err := os.Stat(filepath.Join(dir, "restore-queue")); err == nil {
		t.Error("stale queue should be deleted")
	}
}

// Helper: run restore_advance with stubbed restore_trigger_tab and
// terminal_launch_window hooks recording to trigFile/winFile. The trigger stub
// stands in for a tab that really started: it copies the chain ticket to
// ticket-at-spawn and then claims it, which is what stops restore_advance from
// falling back to a window.
func runRestoreAdvance(t *testing.T, configDir, trigFile, winFile string, trigExit int) (string, int) {
	t.Helper()
	root := projectRoot(t)
	mod := filepath.Join(root, "lib", "session-restore.sh")
	ticket := quote(filepath.Join(configDir, "restore-chain-ticket"))
	script := `
source ` + quote(mod) + `
restore_trigger_tab() { echo triggered >> ` + quote(trigFile) + `; cat ` + ticket + ` > ` + quote(filepath.Join(configDir, "ticket-at-spawn")) + ` 2>/dev/null; rm -f ` + ticket + `; return ` + strconv.Itoa(trigExit) + `; }
terminal_launch_window() { echo window >> ` + quote(winFile) + `; }
restore_advance ` + quote(configDir) + `
`
	return runBashSnippet(t, script, nil)
}

func TestRestoreAdvance_triggers_one_tab_when_queue_nonempty(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "restore-queue",
		"222|/p/app|claude\n222|/p/web|opencode\n")
	trig := filepath.Join(dir, "trig")
	win := filepath.Join(dir, "win")
	_, code := runRestoreAdvance(t, dir, trig, win, 0)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(trig)
	if err != nil {
		t.Fatalf("restore_trigger_tab not called: %v", err)
	}
	if got := strings.Count(string(data), "triggered"); got != 1 {
		t.Errorf("trigger called %d times, want 1", got)
	}
	if _, err := os.Stat(win); err == nil {
		t.Error("no windows must be spawned when the tab trigger succeeds")
	}
	// Queue must stay intact for the next tab to pop.
	q, _ := os.ReadFile(filepath.Join(dir, "restore-queue"))
	if !strings.Contains(string(q), "/p/app") || !strings.Contains(string(q), "/p/web") {
		t.Errorf("queue must be untouched by advance, got %q", string(q))
	}
}

func TestRestoreAdvance_noop_when_queue_missing(t *testing.T) {
	dir := t.TempDir()
	trig := filepath.Join(dir, "trig")
	win := filepath.Join(dir, "win")
	_, code := runRestoreAdvance(t, dir, trig, win, 0)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(trig); err == nil {
		t.Error("must not trigger a tab when queue is missing")
	}
	if _, err := os.Stat(win); err == nil {
		t.Error("must not spawn windows when queue is missing")
	}
}

func TestRestoreAdvance_falls_back_to_single_window_when_trigger_fails(t *testing.T) {
	// osascript needs the Accessibility permission; when it fails, restore
	// degrades to exactly ONE plain window. That window runs the wrapper via
	// Ghostty's configured command, pops the next entry, and advances the
	// chain itself — spawning one window per remaining entry here multiplied
	// with each window's own advance call and opened surplus empty windows.
	dir := t.TempDir()
	writeTempFile(t, dir, "restore-queue",
		"222|/p/app|claude\n222|/p/web|opencode\n222|/p/api|claude\n")
	trig := filepath.Join(dir, "trig")
	win := filepath.Join(dir, "win")
	_, code := runRestoreAdvance(t, dir, trig, win, 1)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(win)
	if err != nil {
		t.Fatalf("fallback window not spawned: %v", err)
	}
	if got := strings.Count(string(data), "window"); got != 1 {
		t.Errorf("spawned %d windows, want exactly 1 (the chain continues itself)", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "restore-queue")); err != nil {
		t.Error("queue must survive for the fallback window to pop")
	}
}

func TestRestoreTriggerTab_invokes_osascript_and_propagates_failure(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	binDir := mockCommand(t, dir, "osascript", `echo "$@" >> `+quote(rec)+`; exit 0`)
	env := buildEnv(t, []string{binDir})
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_trigger_tab", nil, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("osascript not invoked: %v", err)
	}
	if !strings.Contains(string(data), "keystroke") {
		t.Errorf("expected a keystroke script, got %q", string(data))
	}

	failDir := t.TempDir()
	failBin := mockCommand(t, failDir, "osascript", `exit 1`)
	failEnv := buildEnv(t, []string{failBin})
	_, code = runBashFunc(t, "lib/session-restore.sh", "restore_trigger_tab", nil, failEnv)
	if code == 0 {
		t.Error("restore_trigger_tab must propagate osascript failure")
	}
}

func TestWriteSessionSnapshot_orders_by_creation_time(t *testing.T) {
	// tmux list-sessions returns sessions alphabetically; the snapshot must
	// be ordered by creation time so restore reproduces the tab order.
	dir := t.TempDir()
	tmuxBody := `
case "$1" in
  list-sessions) echo "200 dev-a-1"; echo "100 dev-b-2" ;;
  show-environment)
    if [ "$3" = "dev-a-1" ]; then
      printf 'WISP_DECK=1\nWISP_DECK_BOOT=111\nWISP_DECK_PROJECT=a\nWISP_DECK_PATH=/p/a\nWISP_DECK_TOOL=claude\nWISP_DECK_TERMINAL=ghostty\n'
    else
      printf 'WISP_DECK=1\nWISP_DECK_BOOT=111\nWISP_DECK_PROJECT=b\nWISP_DECK_PATH=/p/b\nWISP_DECK_TOOL=claude\nWISP_DECK_TERMINAL=ghostty\n'
    fi ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	env := buildEnv(t, []string{binDir})
	snap := filepath.Join(dir, "last-session")
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(snap)
	if err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "111|b|/p/b|claude|ghostty||||\n111|a|/p/a|claude|ghostty||||"
	if got != want {
		t.Errorf("snapshot order:\n got %q\nwant %q", got, want)
	}
}

func TestWriteSessionSnapshot_handles_session_name_with_spaces(t *testing.T) {
	dir := t.TempDir()
	// Session name contains a space: "dev-My Project-1".
	// The word-splitting bug in the old for loop would split this into two tokens.
	tmuxBody := `
case "$1" in
  list-sessions) echo "100 dev-My Project-1" ;;
  show-environment)
    if [ "$3" = "dev-My Project-1" ]; then
      printf 'WISP_DECK=1\nWISP_DECK_BOOT=111\nWISP_DECK_PROJECT=My Project\nWISP_DECK_PATH=/p/app\nWISP_DECK_TOOL=claude\nWISP_DECK_TERMINAL=ghostty\n'
    else
      printf 'SOMEVAR=1\n'
    fi ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	env := buildEnv(t, []string{binDir})
	snap := filepath.Join(dir, "last-session")
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, env)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(snap)
	if err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "111|My Project|/p/app|claude|ghostty||||"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteSessionSnapshot_empty_when_no_ghost_sessions(t *testing.T) {
	dir := t.TempDir()
	tmuxBody := `
case "$1" in
  list-sessions) echo "100 other-sess" ;;
  show-environment) printf 'SOMEVAR=1\n' ;;
esac
`
	binDir := mockCommand(t, dir, "tmux", tmuxBody)
	env := buildEnv(t, []string{binDir})
	snap := filepath.Join(dir, "last-session")
	_, code := runBashFunc(t, "lib/session-restore.sh", "write_session_snapshot",
		[]string{"tmux", snap}, env)
	assertExitCode(t, code, 0)
	data, _ := os.ReadFile(snap)
	if strings.TrimSpace(string(data)) != "" {
		t.Errorf("expected empty snapshot, got %q", string(data))
	}
}
