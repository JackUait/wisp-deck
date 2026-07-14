package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/jackuait/wisp-deck/internal/attention"
	"github.com/jackuait/wisp-deck/internal/soundpref"
)

const (
	claudeBackgroundMaxOutputBytes = int64(1024 * 1024)
	claudeBackgroundOwnerMaxBytes  = 1024

	defaultClaudeBackgroundPollInterval   = 5 * time.Second
	defaultClaudeBackgroundRetryMax       = time.Minute
	defaultClaudeBackgroundCommandTimeout = 3 * time.Second
	defaultClaudeBackgroundNotifyTimeout  = 2 * time.Second
)

type claudeBackgroundOptions struct {
	Claude        string
	ConfigDir     string
	WispConfigDir string
	OwnerRoot     string
	DefaultConfig bool
}

type claudeBackgroundRunner func(context.Context, claudeBackgroundOptions) error

func init() {
	rootCmd.AddCommand(newClaudeBackgroundCommand(runClaudeBackground))
}

func newClaudeBackgroundCommand(run claudeBackgroundRunner) *cobra.Command {
	var options claudeBackgroundOptions
	cmd := &cobra.Command{
		Use:           "claude-background --claude PATH --config-dir DIR --wisp-config-dir DIR --owner-root DIR [--default-config]",
		Short:         "Monitor account-global Claude background jobs",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateClaudeBackgroundOptions(options); err != nil {
				return err
			}
			if run == nil {
				return fmt.Errorf("Claude background runner is unavailable")
			}
			return run(cmd.Context(), options)
		},
	}
	cmd.Flags().StringVar(&options.Claude, "claude", "", "absolute Claude executable path")
	cmd.Flags().StringVar(&options.ConfigDir, "config-dir", "", "exact Claude account config directory")
	cmd.Flags().StringVar(&options.WispConfigDir, "wisp-config-dir", "", "Wisp Deck configuration directory")
	cmd.Flags().StringVar(&options.OwnerRoot, "owner-root", "", "owning Wisp attention runtime root")
	cmd.Flags().BoolVar(&options.DefaultConfig, "default-config", false, "query the default Keychain account with CLAUDE_CONFIG_DIR unset")
	return cmd
}

func validateClaudeBackgroundOptions(options claudeBackgroundOptions) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "--claude", value: options.Claude},
		{name: "--config-dir", value: options.ConfigDir},
		{name: "--wisp-config-dir", value: options.WispConfigDir},
		{name: "--owner-root", value: options.OwnerRoot},
	} {
		if field.value == "" {
			return fmt.Errorf("%s is required", field.name)
		}
		if !filepath.IsAbs(field.value) {
			return fmt.Errorf("%s must be absolute", field.name)
		}
		if filepath.Clean(field.value) != field.value {
			return fmt.Errorf("%s must be lexically clean", field.name)
		}
	}
	return nil
}

type claudeBackgroundProcessIdentity struct {
	PID   int
	Start string
}

type claudeBackgroundLiveFunc func(context.Context, claudeBackgroundProcessIdentity) bool

// claudeBackgroundLease is a PID/start-identified directory lease. A private
// flock guard serializes every ownership change; the durable owner record makes
// an abandoned lease reclaimable without trusting a recycled PID.
type claudeBackgroundLease struct {
	dir      string
	identity claudeBackgroundProcessIdentity
}

func acquireClaudeBackgroundLease(
	ctx context.Context,
	dir string,
	identity claudeBackgroundProcessIdentity,
	isLive claudeBackgroundLiveFunc,
) (*claudeBackgroundLease, bool, error) {
	if err := validateClaudeBackgroundProcessIdentity(identity); err != nil {
		return nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	guard, err := lockClaudeBackgroundGuard(dir + ".guard")
	if err != nil {
		if errors.Is(err, errClaudeBackgroundGuardBusy) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer guard.Close()
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}

	ownerPath := filepath.Join(dir, "owner")
	info, statErr := os.Stat(dir)
	switch {
	case statErr == nil && !info.IsDir():
		return nil, false, fmt.Errorf("Claude background lease %q is not a directory", dir)
	case statErr == nil:
		owner, readErr := readClaudeBackgroundLeaseOwner(ownerPath)
		if readErr == nil {
			if owner == identity {
				return nil, false, nil
			}
			if isLive == nil {
				isLive = claudeBackgroundProcessLive
			}
			if isLive(ctx, owner) {
				return nil, false, nil
			}
		} else if !os.IsNotExist(readErr) && !errors.Is(readErr, errClaudeBackgroundInvalidOwner) {
			// Unknown I/O is not proof that a live owner died. Fail closed so a
			// transient descriptor/permission failure cannot create two brokers.
			return nil, false, readErr
		}
		// Published leases always contain a complete owner record because the
		// directory is assembled under a sibling name and renamed atomically.
		// An ownerless or malformed target can therefore only be an interrupted
		// legacy publication (or corruption) and is safe to retire under guard.
		stale := fmt.Sprintf("%s.stale.%d.%d", dir, os.Getpid(), time.Now().UnixNano())
		if err := os.Rename(dir, stale); err != nil {
			return nil, false, fmt.Errorf("retire stale Claude background lease: %w", err)
		}
		defer os.RemoveAll(stale)
	case os.IsNotExist(statErr):
		// The serialized creator below publishes the complete owner before the
		// guard is released, so another candidate cannot observe ownerless state.
	default:
		return nil, false, fmt.Errorf("stat Claude background lease: %w", statErr)
	}

	if err := createClaudeBackgroundLease(dir, identity); err != nil {
		return nil, false, err
	}
	return &claudeBackgroundLease{dir: dir, identity: identity}, true, nil
}

func (l *claudeBackgroundLease) Release() error {
	if l == nil || l.dir == "" {
		return nil
	}
	guard, err := lockClaudeBackgroundGuard(l.dir + ".guard")
	if err != nil {
		if os.IsNotExist(err) || errors.Is(err, errClaudeBackgroundGuardBusy) {
			return nil
		}
		return err
	}
	defer guard.Close()

	owner, err := readClaudeBackgroundLeaseOwner(filepath.Join(l.dir, "owner"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if owner != l.identity {
		return nil
	}
	tombstone := fmt.Sprintf("%s.released.%d.%d", l.dir, os.Getpid(), time.Now().UnixNano())
	if err := os.Rename(l.dir, tombstone); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("release Claude background lease: %w", err)
	}
	return os.RemoveAll(tombstone)
}

type claudeBackgroundGuard struct {
	file *os.File
}

var errClaudeBackgroundGuardBusy = errors.New("Claude background lease guard is busy")
var errClaudeBackgroundInvalidOwner = errors.New("invalid Claude background owner")

func lockClaudeBackgroundGuard(path string) (*claudeBackgroundGuard, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open Claude background lease guard: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("chmod Claude background lease guard: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, errClaudeBackgroundGuardBusy
		}
		return nil, fmt.Errorf("lock Claude background lease guard: %w", err)
	}
	return &claudeBackgroundGuard{file: file}, nil
}

func (g *claudeBackgroundGuard) Close() error {
	if g == nil || g.file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(g.file.Fd()), syscall.LOCK_UN)
	closeErr := g.file.Close()
	g.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

func createClaudeBackgroundLease(dir string, identity claudeBackgroundProcessIdentity) error {
	parent := filepath.Dir(dir)
	temporary, err := os.MkdirTemp(parent, "."+filepath.Base(dir)+".pending-")
	if err != nil {
		return fmt.Errorf("create temporary Claude background lease: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(temporary)
		}
	}()
	if err := os.Chmod(temporary, 0o700); err != nil {
		return fmt.Errorf("chmod Claude background lease: %w", err)
	}
	if err := writeClaudeBackgroundOwner(filepath.Join(temporary, "owner"), identity); err != nil {
		return err
	}
	if err := os.Rename(temporary, dir); err != nil {
		return fmt.Errorf("publish Claude background lease: %w", err)
	}
	published = true
	return nil
}

func writeClaudeBackgroundOwner(path string, identity claudeBackgroundProcessIdentity) error {
	record := []byte(fmt.Sprintf("1\t%d\t%s\n", identity.PID, identity.Start))
	temporary, err := os.CreateTemp(filepath.Dir(path), ".owner-*")
	if err != nil {
		return fmt.Errorf("create Claude background owner: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(record); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish Claude background owner: %w", err)
	}
	return nil
}

func readClaudeBackgroundLeaseOwner(path string) (claudeBackgroundProcessIdentity, error) {
	file, err := os.Open(path)
	if err != nil {
		return claudeBackgroundProcessIdentity{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return claudeBackgroundProcessIdentity{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() > claudeBackgroundOwnerMaxBytes {
		return claudeBackgroundProcessIdentity{}, fmt.Errorf("%w: invalid file metadata", errClaudeBackgroundInvalidOwner)
	}
	data, err := io.ReadAll(io.LimitReader(file, claudeBackgroundOwnerMaxBytes+1))
	if err != nil {
		return claudeBackgroundProcessIdentity{}, err
	}
	if len(data) > claudeBackgroundOwnerMaxBytes {
		return claudeBackgroundProcessIdentity{}, fmt.Errorf("%w: file exceeds size limit", errClaudeBackgroundInvalidOwner)
	}
	owner, err := parseClaudeBackgroundOwnerRecord(data)
	if err != nil {
		return claudeBackgroundProcessIdentity{}, fmt.Errorf("%w: %v", errClaudeBackgroundInvalidOwner, err)
	}
	return owner, nil
}

func parseClaudeBackgroundOwnerRecord(data []byte) (claudeBackgroundProcessIdentity, error) {
	if len(data) == 0 || len(data) > claudeBackgroundOwnerMaxBytes {
		return claudeBackgroundProcessIdentity{}, errors.New("invalid Claude background owner record size")
	}
	if data[len(data)-1] != '\n' || bytes.Count(data, []byte{'\n'}) != 1 || bytes.ContainsRune(data, '\r') {
		return claudeBackgroundProcessIdentity{}, errors.New("Claude background owner must have one trailing newline")
	}
	fields := strings.Split(string(data[:len(data)-1]), "\t")
	if len(fields) != 3 || fields[0] != "1" {
		return claudeBackgroundProcessIdentity{}, errors.New("invalid Claude background owner fields")
	}
	pid, err := strconv.Atoi(fields[1])
	if err != nil || pid <= 0 || strconv.Itoa(pid) != fields[1] {
		return claudeBackgroundProcessIdentity{}, errors.New("invalid Claude background owner PID")
	}
	identity := claudeBackgroundProcessIdentity{PID: pid, Start: fields[2]}
	if err := validateClaudeBackgroundProcessIdentity(identity); err != nil {
		return claudeBackgroundProcessIdentity{}, err
	}
	return identity, nil
}

func validateClaudeBackgroundProcessIdentity(identity claudeBackgroundProcessIdentity) error {
	if identity.PID <= 0 {
		return errors.New("Claude background process PID must be positive")
	}
	if strings.ContainsAny(identity.Start, "\t\r\n") {
		return errors.New("Claude background process start contains a delimiter")
	}
	if _, err := time.Parse("Mon Jan _2 15:04:05 2006", identity.Start); err != nil {
		return fmt.Errorf("invalid Claude background process start: %w", err)
	}
	return nil
}

func claudeBackgroundStorageKey(configRoot string) string {
	digest := sha256.Sum256([]byte(configRoot))
	return hex.EncodeToString(digest[:])
}

type claudeBackgroundDependencies struct {
	SelfIdentity func(context.Context) (claudeBackgroundProcessIdentity, error)
	IsLive       claudeBackgroundLiveFunc
	RunAgents    func(context.Context, string, string, bool, int64) ([]byte, error)
	Notify       func(context.Context, attention.ClaudeBackgroundEvent)
	WaitHealthy  func(context.Context, time.Duration, time.Duration, func(context.Context) bool) bool

	PollInterval   time.Duration
	RetryMax       time.Duration
	HealthInterval time.Duration
	CommandTimeout time.Duration
}

func runClaudeBackground(ctx context.Context, options claudeBackgroundOptions) error {
	notifier := claudeBackgroundNotifier{
		WispConfigDir: options.WispConfigDir,
		GOOS:          runtime.GOOS,
		Run:           runClaudeBackgroundDetached,
	}
	return runClaudeBackgroundWithDependencies(ctx, options, claudeBackgroundDependencies{
		Notify: notifier.Notify,
	})
}

func runClaudeBackgroundWithDependencies(
	ctx context.Context,
	options claudeBackgroundOptions,
	dependencies claudeBackgroundDependencies,
) error {
	dependencies = defaultClaudeBackgroundDependencies(dependencies)

	ownerPinned, err := pinClaudeBackgroundDirectory(options.OwnerRoot)
	if err != nil {
		return nil
	}
	configPinned, err := pinClaudeBackgroundDirectory(options.ConfigDir)
	if err != nil {
		return nil
	}
	wispPinned, err := pinClaudeBackgroundDirectory(options.WispConfigDir)
	if err != nil {
		return nil
	}
	owner, err := readClaudeBackgroundLeaseOwner(filepath.Join(options.OwnerRoot, "owner"))
	if err != nil || !dependencies.IsLive(ctx, owner) {
		return nil
	}
	self, err := dependencies.SelfIdentity(ctx)
	if err != nil || validateClaudeBackgroundProcessIdentity(self) != nil {
		return nil
	}

	storageKey := claudeBackgroundStorageKey(options.ConfigDir)
	candidateParent := filepath.Join(options.OwnerRoot, "claude-background-candidates")
	if err := ensureClaudeBackgroundPrivateDir(candidateParent); err != nil {
		return nil
	}
	candidatePinned, err := pinClaudeBackgroundDirectory(candidateParent)
	if err != nil {
		return nil
	}
	candidateLease, acquired, err := acquireClaudeBackgroundLease(
		ctx,
		filepath.Join(candidateParent, storageKey+".lock"),
		self,
		dependencies.IsLive,
	)
	if err != nil || !acquired {
		return nil
	}
	defer func() { _ = candidateLease.Release() }()
	candidateLeasePinned, err := pinClaudeBackgroundDirectory(candidateLease.dir)
	if err != nil {
		return nil
	}

	storageDir := filepath.Join(options.WispConfigDir, "attention", "claude-background", storageKey)
	if err := ensureClaudeBackgroundPrivateDir(storageDir); err != nil {
		return nil
	}
	storagePinned, err := pinClaudeBackgroundDirectory(storageDir)
	if err != nil {
		return nil
	}
	healthy := func(checkContext context.Context) bool {
		if checkContext.Err() != nil || !ownerPinned.Matches() ||
			!configPinned.Matches() || !wispPinned.Matches() ||
			!candidatePinned.Matches() || !candidateLeasePinned.Matches() ||
			!storagePinned.Matches() || !claudeBackgroundLeaseIdentityMatches(candidateLease) {
			return false
		}
		observed, readErr := readClaudeBackgroundLeaseOwner(filepath.Join(options.OwnerRoot, "owner"))
		return readErr == nil && observed == owner && dependencies.IsLive(checkContext, owner)
	}

	retry := time.Duration(0)
	for healthy(ctx) {
		leader, leaderAcquired, leaseErr := acquireClaudeBackgroundLease(
			ctx,
			filepath.Join(storageDir, "leader.lock"),
			self,
			dependencies.IsLive,
		)
		if leaseErr != nil {
			retry = claudeBackgroundNextRetry(retry, dependencies.PollInterval, dependencies.RetryMax)
			if !dependencies.WaitHealthy(ctx, retry, dependencies.HealthInterval, healthy) {
				return nil
			}
			continue
		}
		if !leaderAcquired {
			// A healthy incumbent is ordinary contention, not a failure. Keep
			// followers at the low-frequency poll cadence so handoff is prompt
			// even after the incumbent has been active for a long time.
			retry = 0
			if !dependencies.WaitHealthy(ctx, dependencies.PollInterval, dependencies.HealthInterval, healthy) {
				return nil
			}
			continue
		}
		leaderPinned, pinErr := pinClaudeBackgroundDirectory(leader.dir)
		if pinErr != nil {
			_ = leader.Release()
			retry = claudeBackgroundNextRetry(retry, dependencies.PollInterval, dependencies.RetryMax)
			if !dependencies.WaitHealthy(ctx, retry, dependencies.HealthInterval, healthy) {
				return nil
			}
			continue
		}
		leaderHealthy := func(checkContext context.Context) bool {
			return healthy(checkContext) && leaderPinned.Matches() && claudeBackgroundLeaseIdentityMatches(leader)
		}

		tracker, trackerErr := attention.NewClaudeBackgroundTracker(
			filepath.Join(storageDir, "jobs.json"),
			options.ConfigDir,
		)
		if trackerErr != nil {
			_ = leader.Release()
			retry = claudeBackgroundNextRetry(retry, dependencies.PollInterval, dependencies.RetryMax)
			if !dependencies.WaitHealthy(ctx, retry, dependencies.HealthInterval, healthy) {
				return nil
			}
			continue
		}

		retry = 0
		for leaderHealthy(ctx) {
			pollContext, cancelPoll := context.WithTimeout(ctx, dependencies.CommandTimeout)
			data, pollErr := dependencies.RunAgents(
				pollContext,
				options.Claude,
				options.ConfigDir,
				options.DefaultConfig,
				claudeBackgroundMaxOutputBytes,
			)
			cancelPoll()
			if pollErr == nil && !leaderHealthy(ctx) {
				_ = leader.Release()
				return nil
			}
			if pollErr == nil {
				events, observeErr := tracker.ObserveSnapshot(data)
				if observeErr == nil {
					for _, event := range events {
						if !leaderHealthy(ctx) {
							_ = leader.Release()
							return nil
						}
						dependencies.Notify(ctx, event)
					}
					retry = 0
				} else {
					pollErr = observeErr
				}
			}

			delay := dependencies.PollInterval
			if pollErr != nil {
				retry = claudeBackgroundNextRetry(retry, dependencies.PollInterval, dependencies.RetryMax)
				delay = retry
			}
			if !dependencies.WaitHealthy(ctx, delay, dependencies.HealthInterval, leaderHealthy) {
				_ = leader.Release()
				return nil
			}
		}
		_ = leader.Release()
		return nil
	}
	return nil
}

func defaultClaudeBackgroundDependencies(dependencies claudeBackgroundDependencies) claudeBackgroundDependencies {
	if dependencies.SelfIdentity == nil {
		dependencies.SelfIdentity = claudeBackgroundSelfIdentity
	}
	if dependencies.IsLive == nil {
		dependencies.IsLive = claudeBackgroundProcessLive
	}
	if dependencies.RunAgents == nil {
		dependencies.RunAgents = runClaudeBackgroundAgents
	}
	if dependencies.Notify == nil {
		dependencies.Notify = func(context.Context, attention.ClaudeBackgroundEvent) {}
	}
	if dependencies.WaitHealthy == nil {
		dependencies.WaitHealthy = waitClaudeBackgroundHealthy
	}
	if dependencies.PollInterval <= 0 {
		dependencies.PollInterval = defaultClaudeBackgroundPollInterval
	}
	if dependencies.RetryMax < dependencies.PollInterval {
		dependencies.RetryMax = defaultClaudeBackgroundRetryMax
	}
	if dependencies.HealthInterval <= 0 {
		dependencies.HealthInterval = dependencies.PollInterval
	}
	if dependencies.CommandTimeout <= 0 {
		dependencies.CommandTimeout = defaultClaudeBackgroundCommandTimeout
	}
	return dependencies
}

type claudeBackgroundPinnedDirectory struct {
	path string
	info os.FileInfo
}

func pinClaudeBackgroundDirectory(path string) (claudeBackgroundPinnedDirectory, error) {
	info, err := os.Stat(path)
	if err != nil {
		return claudeBackgroundPinnedDirectory{}, err
	}
	if !info.IsDir() {
		return claudeBackgroundPinnedDirectory{}, fmt.Errorf("%q is not a directory", path)
	}
	return claudeBackgroundPinnedDirectory{path: path, info: info}, nil
}

func (p claudeBackgroundPinnedDirectory) Matches() bool {
	current, err := os.Stat(p.path)
	return err == nil && current.IsDir() && os.SameFile(p.info, current)
}

func claudeBackgroundLeaseIdentityMatches(lease *claudeBackgroundLease) bool {
	if lease == nil {
		return false
	}
	owner, err := readClaudeBackgroundLeaseOwner(filepath.Join(lease.dir, "owner"))
	return err == nil && owner == lease.identity
}

func ensureClaudeBackgroundPrivateDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func waitClaudeBackgroundHealthy(
	ctx context.Context,
	duration time.Duration,
	healthInterval time.Duration,
	healthy func(context.Context) bool,
) bool {
	if duration <= 0 {
		return healthy(ctx)
	}
	deadline := time.Now().Add(duration)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return healthy(ctx)
		}
		step := remaining
		if healthInterval > 0 && step > healthInterval {
			step = healthInterval
		}
		timer := time.NewTimer(step)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return false
		case <-timer.C:
			if !healthy(ctx) {
				return false
			}
		}
	}
}

func claudeBackgroundNextRetry(current, base, maximum time.Duration) time.Duration {
	if base <= 0 {
		base = defaultClaudeBackgroundPollInterval
	}
	if maximum < base {
		maximum = base
	}
	if current < base {
		return base
	}
	if current >= maximum/2 {
		return maximum
	}
	return current * 2
}

func claudeBackgroundSelfIdentity(ctx context.Context) (claudeBackgroundProcessIdentity, error) {
	start, err := claudeBackgroundProcessStart(ctx, os.Getpid())
	if err != nil {
		return claudeBackgroundProcessIdentity{}, err
	}
	return claudeBackgroundProcessIdentity{PID: os.Getpid(), Start: start}, nil
}

func claudeBackgroundProcessLive(ctx context.Context, identity claudeBackgroundProcessIdentity) bool {
	return claudeBackgroundProcessLiveWithStart(ctx, identity, claudeBackgroundProcessStart)
}

func claudeBackgroundProcessLiveWithStart(
	ctx context.Context,
	identity claudeBackgroundProcessIdentity,
	processStart func(context.Context, int) (string, error),
) bool {
	if validateClaudeBackgroundProcessIdentity(identity) != nil {
		return false
	}
	start, err := processStart(ctx, identity.PID)
	if err == nil {
		return start == identity.Start
	}
	// Failure to execute or parse ps is not proof that the exact owner died.
	// Prefer delayed recovery over duplicate brokers: only ESRCH proves that no
	// process currently has this PID. A later successful ps call can still
	// distinguish a recycled PID by its start stamp.
	return !errors.Is(syscall.Kill(identity.PID, 0), syscall.ESRCH)
}

func claudeBackgroundProcessStart(ctx context.Context, pid int) (string, error) {
	if pid <= 0 {
		return "", errors.New("process PID must be positive")
	}
	cmd := exec.CommandContext(ctx, "/bin/ps", "-p", strconv.Itoa(pid), "-o", "lstart=")
	cmd.Env = claudeBackgroundEnvironment(os.Environ(), map[string]string{
		"LC_ALL": "C",
		"TZ":     "UTC",
	})
	cmd.Stdin = nil
	cmd.Stderr = io.Discard
	var output claudeBackgroundBoundedBuffer
	output.maximum = claudeBackgroundOwnerMaxBytes
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		return "", err
	}
	start := strings.TrimSpace(output.String())
	if _, err := time.Parse("Mon Jan _2 15:04:05 2006", start); err != nil {
		return "", err
	}
	return start, nil
}

func runClaudeBackgroundAgents(
	ctx context.Context,
	claude string,
	configDir string,
	defaultConfig bool,
	maximum int64,
) ([]byte, error) {
	if maximum <= 0 {
		return nil, errors.New("Claude background output limit must be positive")
	}
	cmd := exec.CommandContext(ctx, claude, "agents", "--json", "--all")
	if defaultConfig {
		cmd.Env = claudeBackgroundEnvironmentWithout(os.Environ(), "CLAUDE_CONFIG_DIR")
	} else {
		cmd.Env = claudeBackgroundEnvironment(os.Environ(), map[string]string{
			"CLAUDE_CONFIG_DIR": configDir,
		})
	}
	cmd.Stdin = nil
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 100 * time.Millisecond
	configureClaudeBackgroundProcessGroup(cmd)
	output := &claudeBackgroundBoundedBuffer{maximum: maximum}
	cmd.Stdout = output
	err := cmd.Run()
	if output.overflow {
		return nil, fmt.Errorf("Claude background output exceeds %d bytes", maximum)
	}
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), output.Bytes()...), nil
}

type claudeBackgroundBoundedBuffer struct {
	buffer   bytes.Buffer
	maximum  int64
	overflow bool
}

func (b *claudeBackgroundBoundedBuffer) Write(data []byte) (int, error) {
	if int64(b.buffer.Len())+int64(len(data)) > b.maximum {
		b.overflow = true
		return 0, fmt.Errorf("bounded output overflow")
	}
	return b.buffer.Write(data)
}

func (b *claudeBackgroundBoundedBuffer) Bytes() []byte {
	return b.buffer.Bytes()
}

func (b *claudeBackgroundBoundedBuffer) String() string {
	return b.buffer.String()
}

func claudeBackgroundEnvironment(inherited []string, overrides map[string]string) []string {
	environment := make([]string, 0, len(inherited)+len(overrides))
	for _, entry := range inherited {
		key, _, found := strings.Cut(entry, "=")
		if found {
			if _, overridden := overrides[key]; overridden {
				continue
			}
		}
		environment = append(environment, entry)
	}
	for key, value := range overrides {
		environment = append(environment, key+"="+value)
	}
	return environment
}

func claudeBackgroundEnvironmentWithout(inherited []string, removedKey string) []string {
	environment := make([]string, 0, len(inherited))
	for _, entry := range inherited {
		key, _, found := strings.Cut(entry, "=")
		if found && key == removedKey {
			continue
		}
		environment = append(environment, entry)
	}
	return environment
}

func configureClaudeBackgroundProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}

type claudeBackgroundExecFunc func(context.Context, string, []string, []string) error

type claudeBackgroundNotifier struct {
	WispConfigDir string
	GOOS          string
	Timeout       time.Duration
	Run           claudeBackgroundExecFunc
}

func (n claudeBackgroundNotifier) Notify(ctx context.Context, event attention.ClaudeBackgroundEvent) {
	if n.GOOS != "darwin" {
		return
	}
	run := n.Run
	if run == nil {
		run = runClaudeBackgroundDetached
	}
	timeout := n.Timeout
	if timeout <= 0 {
		timeout = defaultClaudeBackgroundNotifyTimeout
	}
	body := claudeBackgroundNotificationBody(event.Status)
	notifyContext, cancelNotify := context.WithTimeout(ctx, timeout)
	environment := claudeBackgroundEnvironment(os.Environ(), map[string]string{
		"WISP_DECK_NOTIFICATION_TITLE": "Claude background",
		"WISP_DECK_NOTIFICATION_BODY":  body,
	})
	_ = run(notifyContext, "/usr/bin/osascript", []string{
		"-e",
		`display notification (system attribute "WISP_DECK_NOTIFICATION_BODY") with title (system attribute "WISP_DECK_NOTIFICATION_TITLE")`,
	}, environment)
	cancelNotify()

	features := filepath.Join(n.WispConfigDir, "claude-features.json")
	_ = soundpref.WithExclusiveLock(features, func() error {
		sound := claudeBackgroundSoundPreference(features)
		if sound == "" {
			return nil
		}
		soundContext, cancelSound := context.WithTimeout(ctx, timeout)
		defer cancelSound()
		return run(soundContext, "/usr/bin/afplay", []string{
			filepath.Join("/System/Library/Sounds", sound+".aiff"),
		}, os.Environ())
	})
}

func claudeBackgroundNotificationBody(status attention.ClaudeBackgroundStatus) string {
	switch status {
	case attention.ClaudeBackgroundBlocked:
		return "Background agent needs input"
	case attention.ClaudeBackgroundCompleted:
		return "Background agent completed"
	case attention.ClaudeBackgroundFailed:
		return "Background agent failed"
	case attention.ClaudeBackgroundStopped:
		return "Background agent stopped"
	default:
		return "Background agent needs attention"
	}
}

func claudeBackgroundSoundPreference(path string) string {
	return soundpref.Read(path)
}

func readClaudeBackgroundSmallFile(path string, maximum int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maximum {
		return nil, errors.New("invalid Claude background preference file")
	}
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, errors.New("Claude background preference file exceeds size limit")
	}
	return data, nil
}

func runClaudeBackgroundDetached(ctx context.Context, name string, args, environment []string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = environment
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 100 * time.Millisecond
	configureClaudeBackgroundProcessGroup(cmd)
	return cmd.Run()
}
