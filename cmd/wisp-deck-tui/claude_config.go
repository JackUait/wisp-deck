package main

import (
	"fmt"
	"os"

	"github.com/jackuait/wisp-deck/internal/claudeconfig"
	"github.com/jackuait/wisp-deck/internal/opencodeconfig"
	"github.com/spf13/cobra"
)

var (
	ccList    string
	ccDir     string
	ccPointer string
	ccFile    string
	ccName    string
)

func syncOpenCode() {
	if ccList == "" || ccDir == "" {
		return
	}
	home, _ := os.UserHomeDir()
	_ = opencodeconfig.Sync(opencodeconfig.Inputs{
		ListFile:    ccList,
		ConfigsDir:  ccDir,
		PointerFile: ccPointer,
		Home:        home,
	})
}

var claudeConfigCmd = &cobra.Command{
	Use:   "claude-config",
	Short: "Create, rename, and delete Claude config files",
	Long:  "Mutation commands for Claude settings configs; the single source of truth shared by the inline TUI and the config menu",
}

var claudeConfigAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Create a new Claude config and print its filename",
	RunE: func(cmd *cobra.Command, args []string) error {
		file, err := claudeconfig.Add(ccList, ccDir, ccName)
		if err != nil {
			return err
		}
		syncOpenCode()
		fmt.Fprintln(cmd.OutOrStdout(), file)
		return nil
	},
}

var claudeConfigRenameCmd = &cobra.Command{
	Use:   "rename",
	Short: "Rename an existing Claude config",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := claudeconfig.Rename(ccList, ccFile, ccName); err != nil {
			return err
		}
		syncOpenCode()
		return nil
	},
}

var claudeConfigDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete a Claude config and clear the pointer if it was active",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := claudeconfig.Delete(ccList, ccDir, ccPointer, ccFile); err != nil {
			return err
		}
		syncOpenCode()
		return nil
	},
}

// A profile written before the window was declared is exactly the one that can
// strand a session, and the installer only copies a default when the file is
// absent — so existing profiles are reachable only by an explicit sweep.
var claudeConfigEnsureBudgetCmd = &cobra.Command{
	Use:   "ensure-budget",
	Short: "Backfill each config's real context window and print how many changed",
	RunE: func(cmd *cobra.Command, args []string) error {
		changed, err := claudeconfig.EnsureContextBudgetAll(ccDir)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), changed)
		return nil
	},
}

func init() {
	claudeConfigAddCmd.Flags().StringVar(&ccList, "list", "", "Path to configs list (name:file)")
	claudeConfigAddCmd.Flags().StringVar(&ccDir, "dir", "", "Path to configs directory")
	claudeConfigAddCmd.Flags().StringVar(&ccName, "name", "", "Display name for the new config")
	claudeConfigAddCmd.Flags().StringVar(&ccPointer, "pointer", "", "Path to active config pointer file")

	claudeConfigRenameCmd.Flags().StringVar(&ccList, "list", "", "Path to configs list (name:file)")
	claudeConfigRenameCmd.Flags().StringVar(&ccFile, "file", "", "Filename of the config to rename")
	claudeConfigRenameCmd.Flags().StringVar(&ccName, "name", "", "New display name")
	claudeConfigRenameCmd.Flags().StringVar(&ccDir, "dir", "", "Path to configs directory")
	claudeConfigRenameCmd.Flags().StringVar(&ccPointer, "pointer", "", "Path to active config pointer file")

	claudeConfigDeleteCmd.Flags().StringVar(&ccList, "list", "", "Path to configs list (name:file)")
	claudeConfigDeleteCmd.Flags().StringVar(&ccDir, "dir", "", "Path to configs directory")
	claudeConfigDeleteCmd.Flags().StringVar(&ccPointer, "pointer", "", "Path to active config pointer file")
	claudeConfigDeleteCmd.Flags().StringVar(&ccFile, "file", "", "Filename of the config to delete")

	claudeConfigEnsureBudgetCmd.Flags().StringVar(&ccDir, "dir", "", "Path to configs directory")

	claudeConfigCmd.AddCommand(claudeConfigAddCmd, claudeConfigRenameCmd, claudeConfigDeleteCmd,
		claudeConfigEnsureBudgetCmd)
	rootCmd.AddCommand(claudeConfigCmd)
}
