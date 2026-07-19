package main

import (
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jackuait/wisp-deck/internal/subusage"
	"github.com/spf13/cobra"
)

// subscription-usage refreshes the on-disk 5h/7d usage snapshot for one
// subscription config. The statusline spawns it disowned on every render and
// reads only the cache file, so this command carries the throttle itself:
// a recent checked_at (success OR failure) exits without touching the
// network, and a lock file keeps concurrent statusline ticks single-flight.
// It never prints and never fails the caller — a statusline is no place for
// error output.
var (
	subUsageConfigsDir  string
	subUsageList        string
	subUsageConfig      string
	subUsageCache       string
	subUsageCodexAuth   string
	subUsageMinInterval int
)

var subscriptionUsageCmd = &cobra.Command{
	Use:           "subscription-usage",
	Short:         "Refresh the cached 5h/7d usage snapshot for a subscription config",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runSubscriptionUsage,
}

func init() {
	subscriptionUsageCmd.Flags().StringVar(&subUsageConfigsDir, "configs-dir", "", "Directory holding the subscription settings JSONs")
	subscriptionUsageCmd.Flags().StringVar(&subUsageList, "list", "", "claude-configs.list path")
	subscriptionUsageCmd.Flags().StringVar(&subUsageConfig, "config", "", "Subscription config filename (e.g. zhipu-glm.json)")
	subscriptionUsageCmd.Flags().StringVar(&subUsageCache, "cache", "", "Snapshot cache file to maintain")
	subscriptionUsageCmd.Flags().StringVar(&subUsageCodexAuth, "codex-auth", "", "Override ~/.codex/auth.json (tests)")
	subscriptionUsageCmd.Flags().IntVar(&subUsageMinInterval, "min-interval", 300, "Seconds between refresh attempts")
	rootCmd.AddCommand(subscriptionUsageCmd)
}

func runSubscriptionUsage(cmd *cobra.Command, args []string) error {
	if subUsageConfig == "" || subUsageCache == "" || subUsageConfigsDir == "" {
		return nil
	}
	now := time.Now().Unix()
	prev, prevErr := subusage.ReadCache(subUsageCache)
	if prevErr == nil && now-prev.CheckedAt < int64(subUsageMinInterval) {
		return nil
	}

	// Single-flight across concurrent statusline ticks: O_EXCL lock next to
	// the cache; a lock older than 2 minutes is a crashed run's leftover.
	// The cache's directory must exist before the lock can.
	if err := os.MkdirAll(filepath.Dir(subUsageCache), 0o700); err != nil {
		return nil
	}
	lock := subUsageCache + ".lock"
	if info, err := os.Stat(lock); err == nil {
		if time.Since(info.ModTime()) < 2*time.Minute {
			return nil
		}
		_ = os.Remove(lock)
	}
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil
	}
	_ = f.Close()
	defer func() { _ = os.Remove(lock) }()

	client := &http.Client{Timeout: 15 * time.Second}
	snap, _, fetchErr := subusage.Fetch(client, subUsageConfigsDir, subUsageList, subUsageConfig, subUsageCodexAuth)
	snap.CheckedAt = time.Now().Unix()
	if fetchErr != nil {
		// Keep the last good data; only the attempt stamp advances.
		if prevErr == nil {
			snap.RateLimits = prev.RateLimits
			snap.FetchedAt = prev.FetchedAt
		}
	} else {
		snap.FetchedAt = snap.CheckedAt
	}
	_ = subusage.WriteCache(subUsageCache, snap)
	return nil
}
