package bash_test

// The restore chain opens one Ghostty tab per queue entry via Cmd+T — but a
// bare tab spawned by the chain used to be indistinguishable from a tab the
// USER opens during the drain. Any interactive launch popped the next queue
// entry, so a user's fresh tab was hijacked into restoring some queued
// project while their intended picker/session landed elsewhere ("session
// opens in a wrong tab"). The fix: restore_advance issues a one-shot chain
// ticket right before spawning the next tab, and only the queue builder or a
// launch that atomically claims that ticket may pop the queue.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// seedChainTicket plants a fresh chain ticket, modelling a tab that was
// spawned by a previous tab's restore_advance. Wrapper-level restore tests
// that expect a queue pop need one — a ticket-less launch is a user tab and
// must not pop.
func seedChainTicket(t *testing.T, confDir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(confDir, "restore-chain-ticket"),
		[]byte(strconv.FormatInt(time.Now().Unix(), 10)+"\n"), 0644); err != nil {
		t.Fatalf("write chain ticket: %v", err)
	}
}

func TestRestoreClaimChainTicket_fails_without_ticket(t *testing.T) {
	dir := t.TempDir()
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_claim_chain_ticket",
		[]string{dir}, nil)
	if code == 0 {
		t.Error("claim must fail when no chain ticket was issued")
	}
}

func TestRestoreChainTicket_issue_then_claim_is_one_shot(t *testing.T) {
	dir := t.TempDir()
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_issue_chain_ticket",
		[]string{dir}, nil)
	assertExitCode(t, code, 0)
	_, code = runBashFunc(t, "lib/session-restore.sh", "restore_claim_chain_ticket",
		[]string{dir}, nil)
	assertExitCode(t, code, 0)
	// The ticket is consumed by the claim: a second launch must not also pop.
	_, code = runBashFunc(t, "lib/session-restore.sh", "restore_claim_chain_ticket",
		[]string{dir}, nil)
	if code == 0 {
		t.Error("a chain ticket must be claimable exactly once")
	}
}

func TestRestoreClaimChainTicket_ignores_stale_ticket(t *testing.T) {
	// A ticket orphaned by a broken chain must not hijack a tab the user
	// opens minutes later.
	dir := t.TempDir()
	stale := strconv.FormatInt(time.Now().Add(-5*time.Minute).Unix(), 10)
	writeTempFile(t, dir, "restore-chain-ticket", stale+"\n")
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_claim_chain_ticket",
		[]string{dir}, nil)
	if code == 0 {
		t.Error("a stale chain ticket must not be claimable")
	}
	if _, err := os.Stat(filepath.Join(dir, "restore-chain-ticket")); err == nil {
		t.Error("a stale chain ticket must be swept")
	}
}

// restore_advance must issue the ticket BEFORE spawning the next tab, for
// both the Cmd+T path and the plain-window fallback — the spawned launch
// claims it to earn its queue pop.
func TestRestoreAdvance_issues_chain_ticket_before_spawning_tab(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "restore-queue",
		"222|/p/app|claude\n222|/p/web|opencode\n")
	trig := filepath.Join(dir, "trig")
	win := filepath.Join(dir, "win")
	_, code := runRestoreAdvance(t, dir, trig, win, 0)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(filepath.Join(dir, "restore-chain-ticket"))
	if err != nil {
		t.Fatalf("restore_advance did not issue a chain ticket: %v", err)
	}
	stamp := strings.TrimSpace(string(data))
	if stamp == "" || strings.ContainsFunc(stamp, func(r rune) bool { return r < '0' || r > '9' }) {
		t.Errorf("chain ticket must hold a numeric epoch, got %q", stamp)
	}
}

func TestRestoreAdvance_issues_no_ticket_when_queue_empty(t *testing.T) {
	dir := t.TempDir()
	trig := filepath.Join(dir, "trig")
	win := filepath.Join(dir, "win")
	_, code := runRestoreAdvance(t, dir, trig, win, 0)
	assertExitCode(t, code, 0)
	if _, err := os.Stat(filepath.Join(dir, "restore-chain-ticket")); err == nil {
		t.Error("no ticket may be issued when there is nothing left to restore")
	}
}

// --- restore_pop_authorized ---

// The pop gate: builder always may; a chain ticket may; and a launch that
// STARTED within the grace window of the queue build may (that's the macOS
// crash-resume storm — every resumed window launches at once with the
// builder, before any ticket exists, and must still drain the queue).

func TestRestorePopAuthorized_builder_always_allowed(t *testing.T) {
	dir := t.TempDir()
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_pop_authorized",
		[]string{dir, "1", strconv.FormatInt(time.Now().Unix(), 10)}, nil)
	assertExitCode(t, code, 0)
}

func TestRestorePopAuthorized_ticket_holder_allowed(t *testing.T) {
	dir := t.TempDir()
	seedChainTicket(t, dir)
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_pop_authorized",
		[]string{dir, "0", strconv.FormatInt(time.Now().Unix(), 10)}, nil)
	assertExitCode(t, code, 0)
}

func TestRestorePopAuthorized_storm_launch_within_build_grace_allowed(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Unix()
	writeTempFile(t, dir, "restore-queue-built-at", strconv.FormatInt(now, 10)+"\n")
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_pop_authorized",
		[]string{dir, "0", strconv.FormatInt(now+2, 10)}, nil)
	assertExitCode(t, code, 0)
}

func TestRestorePopAuthorized_late_launch_without_ticket_denied(t *testing.T) {
	// A tab the user opens mid-drain: queue built a while ago, no ticket.
	dir := t.TempDir()
	built := time.Now().Add(-2 * time.Minute).Unix()
	writeTempFile(t, dir, "restore-queue-built-at", strconv.FormatInt(built, 10)+"\n")
	_, code := runBashFunc(t, "lib/session-restore.sh", "restore_pop_authorized",
		[]string{dir, "0", strconv.FormatInt(time.Now().Unix(), 10)}, nil)
	if code == 0 {
		t.Error("a late ticket-less launch must not be authorized to pop")
	}
}

func TestMaybeRestore_stamps_queue_build_time(t *testing.T) {
	dir := t.TempDir()
	proj := t.TempDir()
	writeTempFile(t, dir, "last-session",
		"old-boot|proj|"+proj+"|opencode|ghostty|||\n")
	_, code := runBashFunc(t, "lib/session-restore.sh", "maybe_restore_session",
		[]string{dir, "boot-new"}, nil)
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(filepath.Join(dir, "restore-queue-built-at"))
	if err != nil {
		t.Fatalf("queue build must stamp restore-queue-built-at: %v", err)
	}
	stamp := strings.TrimSpace(string(data))
	if stamp == "" || strings.ContainsFunc(stamp, func(r rune) bool { return r < '0' || r > '9' }) {
		t.Errorf("build stamp must be a numeric epoch, got %q", stamp)
	}
}

// A tab the user opens mid-drain (interactive launch, current-boot queue
// present, not the builder, no chain ticket) must NOT consume a queue entry:
// the entry belongs to the chain's own next tab. Pre-fix, this launch popped
// the entry and restored someone else's project into the user's tab.
func TestWrapperInteractive_without_chain_ticket_leaves_queue_alone(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	recPath := filepath.Join(home, "rec")
	mocks := map[string]string{
		"tmux":          "#!/bin/bash\nif [ \"$1\" = \"new-session\" ]; then printf '%s\\n' \"$*\" > \"$GT_REC\"; exit 0; fi\nexit 0\n",
		"claude":        "#!/bin/bash\nexit 0\n",
		"wisp-deck-tui": "#!/bin/bash\nexit 0\n",
		"sysctl":        "#!/bin/bash\necho \"{ sec = 12345, usec = 1 } Thu Jul  2 01:01:01 2026\"\n",
	}
	for name, body := range mocks {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0755); err != nil {
			t.Fatalf("write mock %s: %v", name, err)
		}
	}
	projDir := filepath.Join(home, "proj")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	confDir := filepath.Join(home, ".config", "wisp-deck")
	if err := os.MkdirAll(confDir, 0755); err != nil {
		t.Fatalf("mkdir conf: %v", err)
	}
	queue := filepath.Join(confDir, "restore-queue")
	entry := "12345|" + projDir + "|claude|sid-42\n"
	if err := os.WriteFile(queue, []byte(entry), 0644); err != nil {
		t.Fatalf("write queue: %v", err)
	}
	// Restore gate already ran this boot — this launch is not the builder.
	if err := os.WriteFile(filepath.Join(confDir, "last-restore-boot"),
		[]byte("12345\n"), 0644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	env := buildEnv(t, nil, "HOME="+home, "GT_REC="+recPath)
	_, code := runBashScript(t, "wrapper.sh", nil, env)
	assertExitCode(t, code, 0)

	if _, err := os.Stat(recPath); err == nil {
		t.Error("a ticket-less launch must not restore a queued project into its tab")
	}
	data, err := os.ReadFile(queue)
	if err != nil || string(data) != entry {
		t.Errorf("the queue entry belongs to the chain's own tab and must survive: err=%v content=%q", err, string(data))
	}
}
