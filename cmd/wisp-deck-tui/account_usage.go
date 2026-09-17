package main

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackuait/wisp-deck/internal/allin"
	"github.com/jackuait/wisp-deck/internal/subusage"
	"github.com/spf13/cobra"
)

// account-usage refreshes the cached 5h/7d reading for EVERY Claude login on
// the machine, not just the session's own. Claude Code puts the running
// session's rate limits in its statusline payload; the All-In picker offers a
// row per login, so it needs a reading for logins no pane is running — which
// only this endpoint can answer.
//
// It carries the same throttle and single-flight lock as subscription-usage,
// per login, and never prints or fails its caller: every caller is a launch or
// a render, where error output would land in the user's pane.
var (
	accountUsageAccountsList string
	accountUsageAccountsDir  string
	accountUsageConfigsList  string
	accountUsageBaseURL      string
	accountUsageMinInterval  int
)

// usageRefreshOptions is what one round needs beyond the files on disk.
// anthropic is overridden only by tests.
type usageRefreshOptions struct {
	env         allin.Env
	anthropic   string
	minInterval int
}

// accountUsageToken is the Keychain read, as a seam so tests never touch the
// real Keychain.
var accountUsageToken = allin.AccountToken

var accountUsageCmd = &cobra.Command{
	Use:           "account-usage",
	Short:         "Refresh the cached 5h/7d usage snapshot for every Claude login",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runAccountUsage,
}

func init() {
	accountUsageCmd.Flags().StringVar(&accountUsageAccountsList, "accounts-list", "", "claude-accounts.list path")
	accountUsageCmd.Flags().StringVar(&accountUsageAccountsDir, "accounts-dir", "", "Directory holding each login's config dir")
	accountUsageCmd.Flags().StringVar(&accountUsageConfigsList, "configs-list", "", "claude-configs.list path (the caches sit beside it)")
	accountUsageCmd.Flags().StringVar(&accountUsageBaseURL, "base-url", subusage.AnthropicBaseURL, "Override the usage endpoint root (tests)")
	accountUsageCmd.Flags().IntVar(&accountUsageMinInterval, "min-interval", 300, "Seconds between refresh attempts per login")
	rootCmd.AddCommand(accountUsageCmd)
}

func runAccountUsage(cmd *cobra.Command, args []string) error {
	opts := usageRefreshOptions{
		env: allin.Env{
			AccountsList: accountUsageAccountsList,
			AccountsDir:  accountUsageAccountsDir,
			ConfigsList:  accountUsageConfigsList,
		},
		anthropic:   accountUsageBaseURL,
		minInterval: accountUsageMinInterval,
	}
	if opts.env.ConfigsList == "" {
		return nil
	}
	client := &http.Client{Timeout: 15 * time.Second}
	for _, login := range accountUsageLogins(opts.env.AccountsList) {
		refreshAccountUsage(client, opts, login)
	}
	return nil
}

// accountUsageLogins is every login the roster can offer: the implicit one
// first, then each "Label:dir" line of the accounts list. The dir is the id —
// the label is renameable and never names a file.
func accountUsageLogins(listFile string) []string {
	logins := []string{"default"}
	data, err := os.ReadFile(listFile)
	if err != nil {
		return logins
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		_, dir, ok := strings.Cut(line, ":")
		if ok && dir != "" {
			logins = append(logins, dir)
		}
	}
	return logins
}

func refreshAccountUsage(client *http.Client, opts usageRefreshOptions, login string) {
	refreshUsageCache(allin.AccountUsageFile(opts.env.ConfigsList, login), opts.minInterval,
		func() (subusage.Snapshot, error) {
			snap := subusage.Snapshot{Provider: "claude"}
			token, err := accountUsageToken(opts.env.AccountsDir, login)
			if err != nil {
				return snap, err
			}
			snap.RateLimits, err = subusage.FetchClaudeAccount(client, opts.anthropic, token)
			return snap, err
		})
}
