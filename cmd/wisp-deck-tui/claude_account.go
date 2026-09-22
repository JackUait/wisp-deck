package main

import (
	"fmt"
	"os"

	"github.com/jackuait/wisp-deck/internal/allin"
	"github.com/jackuait/wisp-deck/internal/claudeaccount"
	"github.com/spf13/cobra"
)

var (
	caList       string
	caAccountDir string
	caPointer    string
	caDir        string
	caLabel      string
	caEmails     string
	caUsageDir   string
	caHome       string
)

var claudeAccountLogins claudeaccount.LoginStore = allin.KeychainLogins{}

var claudeAccountCmd = &cobra.Command{
	Use:   "claude-account",
	Short: "Create and remove native Claude login accounts",
	Long:  "Mutation commands for native Claude logins (each isolated by its own CLAUDE_CONFIG_DIR); the single source of truth shared by the menu and the add-login flow",
}

var claudeAccountAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Register a new Claude account and print its dir name",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := claudeaccount.Add(caList, caAccountDir, caLabel)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), dir)
		return nil
	},
}

var claudeAccountRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove a Claude account and clear the pointer if it was active",
	RunE: func(cmd *cobra.Command, args []string) error {
		return claudeaccount.Remove(caList, caAccountDir, caPointer, caDir)
	},
}

// reconcile never fails its caller: it runs in the launch pane, where output
// would land on the user's screen.
var claudeAccountReconcileCmd = &cobra.Command{
	Use:           "reconcile",
	Short:         "Move crossed Claude logins back to the slots their emails are pinned to",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		home := caHome
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		opts := claudeaccount.ReconcileOptions{
			ListFile:    caList,
			AccountsDir: caAccountDir,
			EmailsFile:  caEmails,
			UsageDir:    caUsageDir,
			HomeDir:     home,
		}
		if currentHostEffectsDecision().Allowed {
			opts.Store = claudeAccountLogins
		}
		_ = claudeaccount.Reconcile(opts)
		return nil
	},
}

func init() {
	claudeAccountAddCmd.Flags().StringVar(&caList, "list", "", "Path to accounts list (label:dir)")
	claudeAccountAddCmd.Flags().StringVar(&caAccountDir, "accounts-dir", "", "Path to accounts directory")
	claudeAccountAddCmd.Flags().StringVar(&caLabel, "label", "", "Display label for the new account")

	claudeAccountRemoveCmd.Flags().StringVar(&caList, "list", "", "Path to accounts list (label:dir)")
	claudeAccountRemoveCmd.Flags().StringVar(&caAccountDir, "accounts-dir", "", "Path to accounts directory")
	claudeAccountRemoveCmd.Flags().StringVar(&caPointer, "pointer", "", "Path to active account pointer file")
	claudeAccountRemoveCmd.Flags().StringVar(&caDir, "dir", "", "Dir name of the account to remove")

	claudeAccountReconcileCmd.Flags().StringVar(&caList, "list", "", "Path to accounts list (label:dir)")
	claudeAccountReconcileCmd.Flags().StringVar(&caAccountDir, "accounts-dir", "", "Path to accounts directory")
	claudeAccountReconcileCmd.Flags().StringVar(&caEmails, "emails", "", "Path to the dir:email pin file")
	claudeAccountReconcileCmd.Flags().StringVar(&caUsageDir, "usage-dir", "", "Directory of per-login usage caches")
	claudeAccountReconcileCmd.Flags().StringVar(&caHome, "home", "", "Home dir holding the default login's .claude.json (tests)")

	claudeAccountCmd.AddCommand(claudeAccountAddCmd, claudeAccountRemoveCmd, claudeAccountReconcileCmd)
	rootCmd.AddCommand(claudeAccountCmd)
}
