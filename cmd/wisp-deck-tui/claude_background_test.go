package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackuait/wisp-deck/internal/attention"
)

func TestClaudeBackgroundCommandRunsValidatedCandidate(t *testing.T) {
	dir := t.TempDir()
	want := claudeBackgroundOptions{
		Claude:        filepath.Join(dir, "bin", "claude"),
		ConfigDir:     filepath.Join(dir, "claude-account"),
		WispConfigDir: filepath.Join(dir, "wisp-config"),
		OwnerRoot:     filepath.Join(dir, "wisp-deck-attention.owner"),
		DefaultConfig: true,
	}
	var got claudeBackgroundOptions
	cmd := newClaudeBackgroundCommand(func(_ context.Context, options claudeBackgroundOptions) error {
		got = options
		return nil
	})
	cmd.SetArgs([]string{
		"--claude", want.Claude,
		"--config-dir", want.ConfigDir,
		"--wisp-config-dir", want.WispConfigDir,
		"--owner-root", want.OwnerRoot,
		"--default-config",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("runner options = %#v, want %#v", got, want)
	}
}

func TestClaudeBackgroundCommandRejectsMissingOrRelativeIdentityPaths(t *testing.T) {
	dir := t.TempDir()
	base := []string{
		"--claude", filepath.Join(dir, "claude"),
		"--config-dir", filepath.Join(dir, "account"),
		"--wisp-config-dir", filepath.Join(dir, "wisp"),
		"--owner-root", filepath.Join(dir, "owner"),
	}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing claude", args: base[2:], want: "--claude is required"},
		{name: "relative claude", args: append([]string{"--claude", "claude"}, base[2:]...), want: "--claude must be absolute"},
		{name: "relative config", args: replaceClaudeBackgroundFlag(base, "--config-dir", "relative"), want: "--config-dir must be absolute"},
		{name: "relative wisp config", args: replaceClaudeBackgroundFlag(base, "--wisp-config-dir", "relative"), want: "--wisp-config-dir must be absolute"},
		{name: "relative owner", args: replaceClaudeBackgroundFlag(base, "--owner-root", "relative"), want: "--owner-root must be absolute"},
		{name: "unclean claude", args: replaceClaudeBackgroundFlag(base, "--claude", dir+"/bin/../claude"), want: "--claude must be lexically clean"},
		{name: "unclean config", args: replaceClaudeBackgroundFlag(base, "--config-dir", dir+"/account/../account"), want: "--config-dir must be lexically clean"},
		{name: "unclean wisp config", args: replaceClaudeBackgroundFlag(base, "--wisp-config-dir", dir+"/wisp/./"), want: "--wisp-config-dir must be lexically clean"},
		{name: "unclean owner", args: replaceClaudeBackgroundFlag(base, "--owner-root", dir+"/owner/../owner"), want: "--owner-root must be lexically clean"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			cmd := newClaudeBackgroundCommand(func(context.Context, claudeBackgroundOptions) error {
				called = true
				return nil
			})
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Execute error = %v, want substring %q", err, tt.want)
			}
			if called {
				t.Fatal("runner called for invalid options")
			}
		})
	}
}

func TestClaudeBackgroundLeaseRecoversOwnerlessAndMalformedTargets(t *testing.T) {
	for _, setup := range []struct {
		name string
		run  func(*testing.T, string)
	}{
		{
			name: "ownerless",
			run: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "malformed owner",
			run: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "owner"), []byte("partial"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(setup.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "broker.lock")
			setup.run(t, dir)
			identity := claudeBackgroundProcessIdentity{PID: 707, Start: "Mon Jul 13 12:07:00 2026"}

			lease, acquired, err := acquireClaudeBackgroundLease(
				context.Background(),
				dir,
				identity,
				func(context.Context, claudeBackgroundProcessIdentity) bool { return true },
			)
			if err != nil || !acquired || lease == nil {
				t.Fatalf("acquire from interrupted publication = (%#v, %v, %v), want acquired", lease, acquired, err)
			}
			owner, err := readClaudeBackgroundLeaseOwner(filepath.Join(dir, "owner"))
			if err != nil || owner != identity {
				t.Fatalf("published owner = (%#v, %v), want %#v", owner, err, identity)
			}
			if err := lease.Release(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestClaudeBackgroundLeaseDoesNotBlockBehindGuard(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "broker.lock")
	guard, err := lockClaudeBackgroundGuard(dir + ".guard")
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		lease    *claudeBackgroundLease
		acquired bool
		err      error
	}
	resultChannel := make(chan result, 1)
	go func() {
		lease, acquired, acquireErr := acquireClaudeBackgroundLease(
			context.Background(),
			dir,
			claudeBackgroundProcessIdentity{PID: 808, Start: "Mon Jul 13 12:08:00 2026"},
			func(context.Context, claudeBackgroundProcessIdentity) bool { return true },
		)
		resultChannel <- result{lease: lease, acquired: acquired, err: acquireErr}
	}()

	select {
	case got := <-resultChannel:
		if got.err != nil || got.acquired || got.lease != nil {
			t.Fatalf("guard contention = (%#v, %v, %v), want immediately unavailable", got.lease, got.acquired, got.err)
		}
	case <-time.After(100 * time.Millisecond):
		_ = guard.Close()
		got := <-resultChannel
		t.Fatalf("acquire blocked behind guard; eventual result = (%#v, %v, %v)", got.lease, got.acquired, got.err)
	}
	if err := guard.Close(); err != nil {
		t.Fatal(err)
	}
}

func replaceClaudeBackgroundFlag(args []string, flag, value string) []string {
	result := append([]string(nil), args...)
	for i := 0; i+1 < len(result); i++ {
		if result[i] == flag {
			result[i+1] = value
			return result
		}
	}
	return result
}

func TestClaudeBackgroundLeaseRejectsLiveOwnerAndTransfersAfterExactOwnerDies(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "broker.lock")
	first := claudeBackgroundProcessIdentity{PID: 101, Start: "Mon Jul 13 12:00:00 2026"}
	second := claudeBackgroundProcessIdentity{PID: 202, Start: "Mon Jul 13 12:00:01 2026"}
	live := map[claudeBackgroundProcessIdentity]bool{first: true, second: true}
	isLive := func(_ context.Context, identity claudeBackgroundProcessIdentity) bool {
		return live[identity]
	}

	leaseOne, acquired, err := acquireClaudeBackgroundLease(context.Background(), dir, first, isLive)
	if err != nil || !acquired {
		t.Fatalf("first acquire = (%v, %v), want acquired", acquired, err)
	}
	if leaseOne == nil {
		t.Fatal("first lease is nil")
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("lock directory mode = %v, %v; want 0700", func() any {
			if info == nil {
				return nil
			}
			return info.Mode().Perm()
		}(), err)
	}
	if info, err := os.Stat(filepath.Join(dir, "owner")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("owner mode = %v, %v; want 0600", func() any {
			if info == nil {
				return nil
			}
			return info.Mode().Perm()
		}(), err)
	}

	if lease, acquired, err := acquireClaudeBackgroundLease(context.Background(), dir, second, isLive); err != nil || acquired || lease != nil {
		t.Fatalf("second acquire while owner live = (%#v, %v, %v), want unavailable", lease, acquired, err)
	}

	// A recycled PID with another start stamp is not the recorded owner.
	live[first] = false
	live[claudeBackgroundProcessIdentity{PID: first.PID, Start: second.Start}] = true
	leaseTwo, acquired, err := acquireClaudeBackgroundLease(context.Background(), dir, second, isLive)
	if err != nil || !acquired || leaseTwo == nil {
		t.Fatalf("second acquire after exact owner death = (%#v, %v, %v), want acquired", leaseTwo, acquired, err)
	}

	// A stale owner's deferred cleanup must not remove its successor's lock.
	if err := leaseOne.Release(); err != nil {
		t.Fatalf("stale release: %v", err)
	}
	got, err := readClaudeBackgroundLeaseOwner(filepath.Join(dir, "owner"))
	if err != nil {
		t.Fatal(err)
	}
	if got != second {
		t.Fatalf("owner after stale release = %#v, want %#v", got, second)
	}
	if err := leaseTwo.Release(); err != nil {
		t.Fatalf("current release: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("lock remains after current release: %v", err)
	}
}

func TestClaudeBackgroundLeaseHasExactlyOneConcurrentWinner(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "broker.lock")
	const contenders = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	winners := make([]claudeBackgroundProcessIdentity, 0, 1)
	leases := make([]*claudeBackgroundLease, 0, 1)
	for index := 1; index <= contenders; index++ {
		identity := claudeBackgroundProcessIdentity{
			PID:   index,
			Start: fmt.Sprintf("Mon Jul 13 12:00:%02d 2026", index),
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			lease, acquired, err := acquireClaudeBackgroundLease(context.Background(), dir, identity,
				func(context.Context, claudeBackgroundProcessIdentity) bool { return true })
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			if acquired {
				mu.Lock()
				winners = append(winners, identity)
				leases = append(leases, lease)
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	if len(winners) != 1 {
		t.Fatalf("winners = %#v, want exactly one", winners)
	}
	owner, err := readClaudeBackgroundLeaseOwner(filepath.Join(dir, "owner"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(owner, winners[0]) {
		t.Fatalf("owner = %#v, winner = %#v", owner, winners[0])
	}
	if err := leases[0].Release(); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeBackgroundStorageKeyUsesExactConfigRootWithoutExposingIt(t *testing.T) {
	rootA := "/Users/example/.claude/accounts/A"
	rootB := "/Users/example/.claude/accounts/a"
	keyA := claudeBackgroundStorageKey(rootA)
	keyB := claudeBackgroundStorageKey(rootB)
	if keyA == keyB {
		t.Fatalf("case-distinct exact roots share key %q", keyA)
	}
	for _, key := range []string{keyA, keyB} {
		if len(key) != 64 || strings.Contains(key, "Users") || strings.ContainsAny(key, "/\\") {
			t.Fatalf("unsafe storage key %q", key)
		}
	}
}

func TestClaudeBackgroundOwnerRecordIsStrictAndCanonical(t *testing.T) {
	want := claudeBackgroundProcessIdentity{PID: 321, Start: "Mon Jul 13 12:34:56 2026"}
	got, err := parseClaudeBackgroundOwnerRecord([]byte("1\t321\tMon Jul 13 12:34:56 2026\n"))
	if err != nil {
		t.Fatalf("parseClaudeBackgroundOwnerRecord() error = %v", err)
	}
	if got != want {
		t.Fatalf("owner = %#v, want %#v", got, want)
	}

	for name, record := range map[string]string{
		"empty":            "",
		"missing newline":  "1\t321\tMon Jul 13 12:34:56 2026",
		"extra newline":    "1\t321\tMon Jul 13 12:34:56 2026\n\n",
		"version":          "2\t321\tMon Jul 13 12:34:56 2026\n",
		"zero pid":         "1\t0\tMon Jul 13 12:34:56 2026\n",
		"noncanonical pid": "1\t0321\tMon Jul 13 12:34:56 2026\n",
		"bad start":        "1\t321\tnot-a-start\n",
		"extra field":      "1\t321\tMon Jul 13 12:34:56 2026\textra\n",
	} {
		t.Run(name, func(t *testing.T) {
			if owner, err := parseClaudeBackgroundOwnerRecord([]byte(record)); err == nil {
				t.Fatalf("parseClaudeBackgroundOwnerRecord() = %#v, want error", owner)
			}
		})
	}
}

func TestClaudeBackgroundProcessLivenessFailsClosedWhenStartLookupFails(t *testing.T) {
	identity := claudeBackgroundProcessIdentity{
		PID:   os.Getpid(),
		Start: "Mon Jul 13 13:00:00 2026",
	}
	failingStart := func(context.Context, int) (string, error) {
		return "", errors.New("injected ps failure")
	}
	if !claudeBackgroundProcessLiveWithStart(context.Background(), identity, failingStart) {
		t.Fatal("live PID was reclaimed after an ambiguous ps failure")
	}

	identity.PID = 1<<31 - 1
	if claudeBackgroundProcessLiveWithStart(context.Background(), identity, failingStart) {
		t.Fatal("definitely absent PID was treated as live")
	}
}

func TestClaudeBackgroundAgentsCommandUsesExactArgvEnvironmentAndBoundedOutput(t *testing.T) {
	dir := t.TempDir()
	argsLog := filepath.Join(dir, "args")
	envLog := filepath.Join(dir, "env")
	stdinLog := filepath.Join(dir, "stdin")
	claude := filepath.Join(dir, "claude")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" > %q
printf '%%s\n' "$CLAUDE_CONFIG_DIR" > %q
if IFS= read -r line; then printf 'read:%%s\n' "$line" > %q; else printf 'eof\n' > %q; fi
printf '[{"kind":"background","id":"job-a","state":"working"}]'
printf 'must-not-reach-terminal' >&2
`, argsLog, envLog, stdinLog, stdinLog)
	if err := os.WriteFile(claude, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "/wrong/inherited/root")

	output, err := runClaudeBackgroundAgents(context.Background(), claude, filepath.Join(dir, "exact-account"), false, 1024)
	if err != nil {
		t.Fatalf("runClaudeBackgroundAgents() error = %v", err)
	}
	if got, want := string(output), `[{"kind":"background","id":"job-a","state":"working"}]`; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, _ := os.ReadFile(argsLog); string(got) != "agents\n--json\n--all\n" {
		t.Fatalf("argv log = %q", got)
	}
	if got, _ := os.ReadFile(envLog); string(got) != filepath.Join(dir, "exact-account")+"\n" {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q", got)
	}
	if got, _ := os.ReadFile(stdinLog); string(got) != "eof\n" {
		t.Fatalf("stdin behavior = %q, want EOF", got)
	}

	tooMuch := filepath.Join(dir, "claude-too-much")
	if err := os.WriteFile(tooMuch, []byte("#!/bin/sh\nprintf '12345'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if output, err := runClaudeBackgroundAgents(context.Background(), tooMuch, filepath.Join(dir, "exact-account"), false, 4); err == nil {
		t.Fatalf("oversized command output = %q, nil error", output)
	}

	slow := filepath.Join(dir, "claude-slow")
	if err := os.WriteFile(slow, []byte("#!/bin/sh\nsleep 5\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := runClaudeBackgroundAgents(ctx, slow, filepath.Join(dir, "exact-account"), false, 1024); err == nil {
		t.Fatal("timed-out Claude command returned nil error")
	}
}

func TestClaudeBackgroundAgentsCommandUnsetsConfigForDefaultAccount(t *testing.T) {
	dir := t.TempDir()
	environmentLog := filepath.Join(dir, "env")
	claude := filepath.Join(dir, "claude")
	script := fmt.Sprintf(`#!/bin/sh
if env | grep '^CLAUDE_CONFIG_DIR=' > %q; then :; else printf 'UNSET\n' > %q; fi
printf '[]'
`, environmentLog, environmentLog)
	if err := os.WriteFile(claude, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "/wrong/inherited/root")

	if _, err := runClaudeBackgroundAgents(context.Background(), claude, filepath.Join(dir, ".claude"), true, 1024); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(environmentLog); err != nil || string(got) != "UNSET\n" {
		t.Fatalf("default CLAUDE_CONFIG_DIR = %q, %v; want absent", got, err)
	}
}

func TestClaudeBackgroundAgentsTimeoutKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "child.pid")
	claude := filepath.Join(dir, "claude")
	script := fmt.Sprintf("#!/bin/sh\nsleep 30 &\nchild=$!\nprintf '%%s' \"$child\" > %q\nwait\n", pidPath)
	if err := os.WriteFile(claude, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := runClaudeBackgroundAgents(ctx, claude, filepath.Join(dir, "account"), false, 1024)
		result <- err
	}()
	var data []byte
	deadline := time.Now().Add(10 * time.Second)
	for len(data) == 0 && time.Now().Before(deadline) {
		data, _ = os.ReadFile(pidPath)
		if len(data) == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if len(data) == 0 {
		cancel()
		t.Fatal("poll command did not publish its grandchild PID")
	}
	cancel()
	if err := <-result; err == nil {
		t.Fatal("canceled process group returned nil error")
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Kill(pid, syscall.SIGKILL) }()
	deadline = time.Now().Add(time.Second)
	for {
		err = syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("poll grandchild PID %d survived timeout: %v", pid, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestClaudeBackgroundCandidatePersistsBeforeNotifyingAndStopsWithOwner(t *testing.T) {
	dir := t.TempDir()
	ownerRoot := filepath.Join(dir, "owner-root")
	configDir := filepath.Join(dir, "account")
	wispDir := filepath.Join(dir, "wisp")
	for _, path := range []string{ownerRoot, configDir, wispDir} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	owner := claudeBackgroundProcessIdentity{PID: 77, Start: "Mon Jul 13 13:00:00 2026"}
	writeTestClaudeBackgroundOwner(t, filepath.Join(ownerRoot, "owner"), owner)
	self := claudeBackgroundProcessIdentity{PID: 88, Start: "Mon Jul 13 13:00:01 2026"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	polls := 0
	var notified []attention.ClaudeBackgroundEvent
	dependencies := claudeBackgroundDependencies{
		SelfIdentity: func(context.Context) (claudeBackgroundProcessIdentity, error) { return self, nil },
		IsLive: func(_ context.Context, identity claudeBackgroundProcessIdentity) bool {
			return identity == owner || identity == self
		},
		RunAgents: func(context.Context, string, string, bool, int64) ([]byte, error) {
			mu.Lock()
			defer mu.Unlock()
			polls++
			if polls == 1 {
				return []byte(`[{"kind":"background","id":"job-a","state":"working"}]`), nil
			}
			return []byte(`[{"kind":"background","id":"job-a","state":"blocked"}]`), nil
		},
		Notify: func(_ context.Context, event attention.ClaudeBackgroundEvent) {
			statePath := filepath.Join(wispDir, "attention", "claude-background", claudeBackgroundStorageKey(configDir), "jobs.json")
			restarted, err := attention.NewClaudeBackgroundTracker(statePath, configDir)
			if err != nil {
				t.Errorf("reopen tracker before notification: %v", err)
			} else if replay, replayErr := restarted.ObserveSnapshot([]byte(`[{
				"kind":"background","id":"job-a","state":"blocked"
			}]`)); replayErr != nil || len(replay) != 0 {
				t.Errorf("transition was not persisted before notification: replay=%#v err=%v", replay, replayErr)
			}
			notified = append(notified, event)
			cancel()
		},
		PollInterval:   time.Millisecond,
		RetryMax:       4 * time.Millisecond,
		HealthInterval: time.Millisecond,
		CommandTimeout: time.Second,
	}
	options := claudeBackgroundOptions{
		Claude:        filepath.Join(dir, "claude"),
		ConfigDir:     configDir,
		WispConfigDir: wispDir,
		OwnerRoot:     ownerRoot,
	}
	if err := runClaudeBackgroundWithDependencies(ctx, options, dependencies); err != nil {
		t.Fatalf("runClaudeBackgroundWithDependencies() error = %v", err)
	}
	if len(notified) != 1 || notified[0].Status != attention.ClaudeBackgroundBlocked || notified[0].JobID != "job-a" {
		t.Fatalf("notifications = %#v", notified)
	}

	storageDir := filepath.Join(wispDir, "attention", "claude-background", claudeBackgroundStorageKey(configDir))
	if info, err := os.Stat(storageDir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("storage directory = %v, %v; want mode 0700", info, err)
	}
	if info, err := os.Stat(filepath.Join(storageDir, "jobs.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("tracker file = %v, %v; want mode 0600", info, err)
	}
}

func TestClaudeBackgroundFollowerRetriesElectionAtPollInterval(t *testing.T) {
	dir := t.TempDir()
	ownerRoot := filepath.Join(dir, "owner-root")
	configDir := filepath.Join(dir, "account")
	wispDir := filepath.Join(dir, "wisp")
	for _, path := range []string{ownerRoot, configDir, wispDir} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	owner := claudeBackgroundProcessIdentity{PID: 77, Start: "Mon Jul 13 13:00:00 2026"}
	self := claudeBackgroundProcessIdentity{PID: 88, Start: "Mon Jul 13 13:00:01 2026"}
	incumbent := claudeBackgroundProcessIdentity{PID: 99, Start: "Mon Jul 13 13:00:02 2026"}
	writeTestClaudeBackgroundOwner(t, filepath.Join(ownerRoot, "owner"), owner)
	storageDir := filepath.Join(wispDir, "attention", "claude-background", claudeBackgroundStorageKey(configDir))
	if err := ensureClaudeBackgroundPrivateDir(storageDir); err != nil {
		t.Fatal(err)
	}
	incumbentLease, acquired, err := acquireClaudeBackgroundLease(
		context.Background(),
		filepath.Join(storageDir, "leader.lock"),
		incumbent,
		func(context.Context, claudeBackgroundProcessIdentity) bool { return true },
	)
	if err != nil || !acquired {
		t.Fatalf("incumbent acquire = (%v, %v), want acquired", acquired, err)
	}
	defer func() { _ = incumbentLease.Release() }()

	incumbentChecks := 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	polls := 0
	var electionWaits []time.Duration
	dependencies := claudeBackgroundDependencies{
		SelfIdentity: func(context.Context) (claudeBackgroundProcessIdentity, error) { return self, nil },
		IsLive: func(_ context.Context, identity claudeBackgroundProcessIdentity) bool {
			switch identity {
			case owner, self:
				return true
			case incumbent:
				incumbentChecks++
				return incumbentChecks <= 6
			default:
				return false
			}
		},
		RunAgents: func(context.Context, string, string, bool, int64) ([]byte, error) {
			polls++
			cancel()
			return []byte(`[]`), nil
		},
		WaitHealthy: func(
			ctx context.Context,
			duration time.Duration,
			_ time.Duration,
			healthy func(context.Context) bool,
		) bool {
			electionWaits = append(electionWaits, duration)
			return healthy(ctx)
		},
		Notify:         func(context.Context, attention.ClaudeBackgroundEvent) {},
		PollInterval:   5 * time.Second,
		RetryMax:       time.Minute,
		HealthInterval: 5 * time.Second,
		CommandTimeout: time.Second,
	}
	options := claudeBackgroundOptions{
		Claude:        filepath.Join(dir, "claude"),
		ConfigDir:     configDir,
		WispConfigDir: wispDir,
		OwnerRoot:     ownerRoot,
	}
	if err := runClaudeBackgroundWithDependencies(ctx, options, dependencies); err != nil {
		t.Fatal(err)
	}
	if polls != 1 {
		t.Fatalf("polls after incumbent stopped = %d, want prompt handoff before timeout", polls)
	}
	if incumbentChecks < 7 {
		t.Fatalf("leader checks = %d, want election retried through stopped incumbent", incumbentChecks)
	}
	if len(electionWaits) < 6 {
		t.Fatalf("election waits = %v, want at least six contention waits", electionWaits)
	}
	for index, delay := range electionWaits[:6] {
		if delay != dependencies.PollInterval {
			t.Fatalf("contention wait %d = %s, want fixed %s; all waits %v", index, delay, dependencies.PollInterval, electionWaits)
		}
	}
}

func TestClaudeBackgroundCandidatePinsOwnerAndConfigDirectoryIdentity(t *testing.T) {
	dir := t.TempDir()
	ownerRoot := filepath.Join(dir, "owner-root")
	configDir := filepath.Join(dir, "account")
	wispDir := filepath.Join(dir, "wisp")
	for _, path := range []string{ownerRoot, configDir, wispDir} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	owner := claudeBackgroundProcessIdentity{PID: 77, Start: "Mon Jul 13 13:00:00 2026"}
	writeTestClaudeBackgroundOwner(t, filepath.Join(ownerRoot, "owner"), owner)
	self := claudeBackgroundProcessIdentity{PID: 88, Start: "Mon Jul 13 13:00:01 2026"}
	polls := 0
	dependencies := claudeBackgroundDependencies{
		SelfIdentity: func(context.Context) (claudeBackgroundProcessIdentity, error) { return self, nil },
		IsLive: func(_ context.Context, identity claudeBackgroundProcessIdentity) bool {
			return identity == owner || identity == self
		},
		RunAgents: func(context.Context, string, string, bool, int64) ([]byte, error) {
			polls++
			if polls == 1 {
				if err := os.Rename(configDir, configDir+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(configDir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			return []byte(`[]`), nil
		},
		Notify:         func(context.Context, attention.ClaudeBackgroundEvent) {},
		PollInterval:   time.Millisecond,
		RetryMax:       4 * time.Millisecond,
		HealthInterval: time.Millisecond,
		CommandTimeout: time.Second,
	}
	options := claudeBackgroundOptions{Claude: filepath.Join(dir, "claude"), ConfigDir: configDir, WispConfigDir: wispDir, OwnerRoot: ownerRoot}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runClaudeBackgroundWithDependencies(ctx, options, dependencies); err != nil {
		t.Fatal(err)
	}
	if polls != 1 {
		t.Fatalf("polls after config inode replacement = %d, want 1", polls)
	}
}

func TestClaudeBackgroundCandidatePinsPrivateCandidateDirectoryIdentity(t *testing.T) {
	dir := t.TempDir()
	ownerRoot := filepath.Join(dir, "owner-root")
	configDir := filepath.Join(dir, "account")
	wispDir := filepath.Join(dir, "wisp")
	for _, path := range []string{ownerRoot, configDir, wispDir} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	owner := claudeBackgroundProcessIdentity{PID: 77, Start: "Mon Jul 13 13:00:00 2026"}
	writeTestClaudeBackgroundOwner(t, filepath.Join(ownerRoot, "owner"), owner)
	self := claudeBackgroundProcessIdentity{PID: 88, Start: "Mon Jul 13 13:00:01 2026"}
	polls := 0
	dependencies := claudeBackgroundDependencies{
		SelfIdentity: func(context.Context) (claudeBackgroundProcessIdentity, error) { return self, nil },
		IsLive: func(_ context.Context, identity claudeBackgroundProcessIdentity) bool {
			return identity == owner || identity == self
		},
		RunAgents: func(context.Context, string, string, bool, int64) ([]byte, error) {
			polls++
			if polls == 1 {
				candidateDir := filepath.Join(ownerRoot, "claude-background-candidates")
				if err := os.Rename(candidateDir, candidateDir+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(candidateDir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			return []byte(`[]`), nil
		},
		Notify:         func(context.Context, attention.ClaudeBackgroundEvent) {},
		PollInterval:   time.Millisecond,
		RetryMax:       4 * time.Millisecond,
		HealthInterval: time.Millisecond,
		CommandTimeout: time.Second,
	}
	options := claudeBackgroundOptions{Claude: filepath.Join(dir, "claude"), ConfigDir: configDir, WispConfigDir: wispDir, OwnerRoot: ownerRoot}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runClaudeBackgroundWithDependencies(ctx, options, dependencies); err != nil {
		t.Fatal(err)
	}
	if polls != 1 {
		t.Fatalf("polls after candidate-dir inode replacement = %d, want 1", polls)
	}
}

func TestClaudeBackgroundCandidatePinsActiveLeaseAndStorageIdentity(t *testing.T) {
	for _, replacement := range []string{
		"wrapper owner", "config root", "candidate parent", "candidate lease",
		"leader lease", "storage directory", "wisp root",
	} {
		t.Run(replacement, func(t *testing.T) {
			dir := t.TempDir()
			ownerRoot := filepath.Join(dir, "owner-root")
			configDir := filepath.Join(dir, "account")
			wispDir := filepath.Join(dir, "wisp")
			for _, path := range []string{ownerRoot, configDir, wispDir} {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			owner := claudeBackgroundProcessIdentity{PID: 77, Start: "Mon Jul 13 13:00:00 2026"}
			self := claudeBackgroundProcessIdentity{PID: 88, Start: "Mon Jul 13 13:00:01 2026"}
			other := claudeBackgroundProcessIdentity{PID: 99, Start: "Mon Jul 13 13:00:02 2026"}
			writeTestClaudeBackgroundOwner(t, filepath.Join(ownerRoot, "owner"), owner)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			polls := 0
			notifications := 0
			storageKey := claudeBackgroundStorageKey(configDir)
			dependencies := claudeBackgroundDependencies{
				SelfIdentity: func(context.Context) (claudeBackgroundProcessIdentity, error) { return self, nil },
				IsLive: func(_ context.Context, identity claudeBackgroundProcessIdentity) bool {
					return identity == owner || identity == self || identity == other
				},
				RunAgents: func(context.Context, string, string, bool, int64) ([]byte, error) {
					polls++
					if polls == 1 {
						return []byte(`[{"kind":"background","id":"job-a","state":"working"}]`), nil
					}
					if polls == 2 {
						candidateParent := filepath.Join(ownerRoot, "claude-background-candidates")
						candidateLease := filepath.Join(ownerRoot, "claude-background-candidates", storageKey+".lock")
						storageDir := filepath.Join(wispDir, "attention", "claude-background", storageKey)
						leaderLease := filepath.Join(storageDir, "leader.lock")
						switch replacement {
						case "wrapper owner":
							writeTestClaudeBackgroundOwner(t, filepath.Join(ownerRoot, "owner"), other)
						case "config root":
							if err := os.Rename(configDir, configDir+".old"); err != nil {
								t.Fatal(err)
							}
							if err := os.Mkdir(configDir, 0o700); err != nil {
								t.Fatal(err)
							}
						case "candidate parent":
							if err := os.Rename(candidateParent, candidateParent+".old"); err != nil {
								t.Fatal(err)
							}
							if err := os.Mkdir(candidateParent, 0o700); err != nil {
								t.Fatal(err)
							}
						case "candidate lease":
							replaceTestClaudeBackgroundLease(t, candidateLease, other)
						case "leader lease":
							replaceTestClaudeBackgroundLease(t, leaderLease, other)
						case "storage directory":
							if err := os.Rename(storageDir, storageDir+".old"); err != nil {
								t.Fatal(err)
							}
							if err := os.Mkdir(storageDir, 0o700); err != nil {
								t.Fatal(err)
							}
						case "wisp root":
							if err := os.Rename(wispDir, wispDir+".old"); err != nil {
								t.Fatal(err)
							}
							if err := os.Mkdir(wispDir, 0o700); err != nil {
								t.Fatal(err)
							}
						}
						return []byte(`[{"kind":"background","id":"job-a","state":"blocked"}]`), nil
					}
					cancel()
					return []byte(`[]`), nil
				},
				Notify: func(context.Context, attention.ClaudeBackgroundEvent) {
					notifications++
				},
				WaitHealthy: func(
					ctx context.Context,
					_ time.Duration,
					_ time.Duration,
					healthy func(context.Context) bool,
				) bool {
					return healthy(ctx)
				},
				PollInterval:   time.Millisecond,
				RetryMax:       4 * time.Millisecond,
				HealthInterval: time.Millisecond,
				CommandTimeout: time.Second,
			}
			options := claudeBackgroundOptions{
				Claude:        filepath.Join(dir, "claude"),
				ConfigDir:     configDir,
				WispConfigDir: wispDir,
				OwnerRoot:     ownerRoot,
			}
			if err := runClaudeBackgroundWithDependencies(ctx, options, dependencies); err != nil {
				t.Fatal(err)
			}
			if polls != 2 || notifications != 0 {
				t.Fatalf("after %s replacement: polls=%d notifications=%d, want 2 and 0", replacement, polls, notifications)
			}
		})
	}
}

func TestClaudeBackgroundNotifierUsesGenericBodyAndLiveAllowedSound(t *testing.T) {
	dir := t.TempDir()
	features := filepath.Join(dir, "claude-features.json")
	if err := os.WriteFile(features, []byte(`{"sound":true,"sound_name":"Glass"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	type call struct {
		name string
		args []string
		env  []string
	}
	var calls []call
	notifier := claudeBackgroundNotifier{
		WispConfigDir: dir,
		GOOS:          "darwin",
		Run: func(_ context.Context, name string, args, env []string) error {
			calls = append(calls, call{name: name, args: append([]string(nil), args...), env: append([]string(nil), env...)})
			return nil
		},
	}
	event := attention.ClaudeBackgroundEvent{
		JobID:      "private-job-id",
		Status:     attention.ClaudeBackgroundBlocked,
		WaitingFor: "private question text",
	}
	notifier.Notify(context.Background(), event)
	if len(calls) != 2 {
		t.Fatalf("command calls = %#v, want osascript and afplay", calls)
	}
	if calls[0].name != "/usr/bin/osascript" {
		t.Fatalf("notification command = %q", calls[0].name)
	}
	joined := strings.Join(append(append([]string(nil), calls[0].args...), calls[0].env...), "\n")
	if !strings.Contains(joined, "Claude background") || !strings.Contains(joined, "needs input") {
		t.Fatalf("generic notification missing from %#v", calls[0])
	}
	if strings.Contains(joined, event.JobID) || strings.Contains(joined, event.WaitingFor) {
		t.Fatalf("private event detail leaked into notification: %q", joined)
	}
	if calls[1].name != "/usr/bin/afplay" || !reflect.DeepEqual(calls[1].args, []string{"/System/Library/Sounds/Glass.aiff"}) {
		t.Fatalf("sound call = %#v", calls[1])
	}

	if err := os.WriteFile(features, []byte(`{"sound":false,"sound_name":"../../private"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	calls = nil
	notifier.Notify(context.Background(), attention.ClaudeBackgroundEvent{Status: attention.ClaudeBackgroundCompleted})
	if len(calls) != 1 || calls[0].name != "/usr/bin/osascript" {
		t.Fatalf("disabled sound calls = %#v", calls)
	}

	if err := os.WriteFile(features, []byte(`{"sound":true,"sound_name":"../../private"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	calls = nil
	notifier.Notify(context.Background(), attention.ClaudeBackgroundEvent{Status: attention.ClaudeBackgroundFailed})
	if len(calls) != 2 || !reflect.DeepEqual(calls[1].args, []string{"/System/Library/Sounds/Bottle.aiff"}) {
		t.Fatalf("unsafe sound preference calls = %#v", calls)
	}
}

func TestClaudeBackgroundSoundPreferenceFailsClosedWithoutExplicitOptIn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claude-features.json")
	tests := []struct {
		name, content string
		write         bool
	}{
		{name: "missing"},
		{name: "invalid", content: `{"sound":true`, write: true},
		{name: "missing flag", content: `{"sound_name":"Glass"}`, write: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = os.Remove(path)
			if tt.write {
				if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if got := claudeBackgroundSoundPreference(path); got != "" {
				t.Fatalf("ambiguous preference played %q, want silence", got)
			}
		})
	}
}

func TestClaudeBackgroundNotifierHoldsPreferenceLockThroughPlayback(t *testing.T) {
	if _, err := os.Stat("/usr/bin/lockf"); err != nil {
		t.Skip("macOS lockf is required for the cross-process sound lock")
	}
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "claude-features.json"),
		[]byte(`{"sound":true,"sound_name":"Glass"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	playbackStarted := make(chan struct{})
	releasePlayback := make(chan struct{})
	notifyDone := make(chan struct{})
	notifier := claudeBackgroundNotifier{
		WispConfigDir: dir,
		GOOS:          "darwin",
		Run: func(_ context.Context, name string, _ []string, _ []string) error {
			if name == "/usr/bin/afplay" {
				close(playbackStarted)
				<-releasePlayback
			}
			return nil
		},
	}
	go func() {
		notifier.Notify(context.Background(), attention.ClaudeBackgroundEvent{Status: attention.ClaudeBackgroundCompleted})
		close(notifyDone)
	}()
	select {
	case <-playbackStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("background sound did not start")
	}

	setter := exec.Command(
		"/usr/bin/lockf", "-k", filepath.Join(dir, ".claude-features.json.lock"),
		"/bin/sh", "-c", ":",
	)
	setterDone := make(chan error, 1)
	if err := setter.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { setterDone <- setter.Wait() }()
	completedEarly := false
	select {
	case err := <-setterDone:
		if err != nil {
			t.Fatal(err)
		}
		completedEarly = true
	case <-time.After(200 * time.Millisecond):
	}
	close(releasePlayback)
	if !completedEarly {
		select {
		case err := <-setterDone:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("preference lock did not release after playback")
		}
	}
	select {
	case <-notifyDone:
	case <-time.After(2 * time.Second):
		t.Fatal("background notifier did not finish")
	}
	if completedEarly {
		t.Fatal("preference writer crossed an in-flight background sound")
	}
}

func TestClaudeBackgroundNotifierGivesSoundAnIndependentDeadline(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "claude-features.json"),
		[]byte(`{"sound":true,"sound_name":"Glass"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	calls := 0
	soundContextLive := false
	notifier := claudeBackgroundNotifier{
		WispConfigDir: dir,
		GOOS:          "darwin",
		Timeout:       5 * time.Millisecond,
		Run: func(ctx context.Context, _ string, _ []string, _ []string) error {
			calls++
			if calls == 1 {
				<-ctx.Done()
				return ctx.Err()
			}
			soundContextLive = ctx.Err() == nil
			return nil
		},
	}
	notifier.Notify(context.Background(), attention.ClaudeBackgroundEvent{Status: attention.ClaudeBackgroundCompleted})
	if calls != 2 || !soundContextLive {
		t.Fatalf("notification calls = %d, sound context live = %v; want independent sound attempt", calls, soundContextLive)
	}
}

func TestClaudeBackgroundRetryBackoffIsBounded(t *testing.T) {
	if got := claudeBackgroundNextRetry(0, 5*time.Second, time.Minute); got != 5*time.Second {
		t.Fatalf("initial retry = %s", got)
	}
	if got := claudeBackgroundNextRetry(5*time.Second, 5*time.Second, time.Minute); got != 10*time.Second {
		t.Fatalf("second retry = %s", got)
	}
	if got := claudeBackgroundNextRetry(40*time.Second, 5*time.Second, time.Minute); got != time.Minute {
		t.Fatalf("capped retry = %s", got)
	}
}

func TestClaudeBackgroundDefaultHealthCadenceMatchesPolling(t *testing.T) {
	dependencies := defaultClaudeBackgroundDependencies(claudeBackgroundDependencies{
		PollInterval: 7 * time.Second,
	})
	if dependencies.HealthInterval != dependencies.PollInterval {
		t.Fatalf("health interval = %s, poll interval = %s", dependencies.HealthInterval, dependencies.PollInterval)
	}
}

func TestClaudeBackgroundDetachedRunnerWaitsForCommandSideEffect(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	command := filepath.Join(dir, "command")
	if err := os.WriteFile(command, []byte(fmt.Sprintf("#!/bin/sh\nsleep 0.02\nprintf done > %q\n", marker)), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := runClaudeBackgroundDetached(ctx, command, nil, os.Environ()); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "done" {
		t.Fatalf("side effect after runner return = %q, %v", data, err)
	}
}

func writeTestClaudeBackgroundOwner(t *testing.T, path string, identity claudeBackgroundProcessIdentity) {
	t.Helper()
	record := fmt.Sprintf("1\t%d\t%s\n", identity.PID, identity.Start)
	if err := os.WriteFile(path, []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
}

func replaceTestClaudeBackgroundLease(t *testing.T, path string, identity claudeBackgroundProcessIdentity) {
	t.Helper()
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestClaudeBackgroundOwner(t, filepath.Join(path, "owner"), identity)
}
