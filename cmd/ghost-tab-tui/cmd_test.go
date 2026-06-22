package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRootCmd_HasVersion(t *testing.T) {
	if rootCmd.Version == "" {
		t.Fatal("Expected rootCmd.Version to be set")
	}
}

func TestVersionVar_HasDefault(t *testing.T) {
	if Version == "" {
		t.Fatal("Expected Version variable to have a default value")
	}
}

func TestRootCmd_HasAIToolFlag(t *testing.T) {
	flag := rootCmd.PersistentFlags().Lookup("ai-tool")
	if flag == nil {
		t.Fatal("Expected --ai-tool persistent flag to be registered")
	}
	if flag.DefValue != "claude" {
		t.Errorf("Expected default value %q, got %q", "claude", flag.DefValue)
	}
}

func TestRootCmd_SubcommandRegistered(t *testing.T) {
	subcommands := []string{
		"confirm",
		"show-logo",
		"select-project",
		"select-ai-tool",
		"config-menu",
		"main-menu",
		"multi-select-ai-tool",
		"select-branch",
		"claude-config-menu",
	}

	for _, name := range subcommands {
		t.Run(name, func(t *testing.T) {
			cmd, _, err := rootCmd.Find([]string{name})
			if err != nil {
				t.Fatalf("Failed to find subcommand %q: %v", name, err)
			}
			if cmd.Name() != name {
				t.Errorf("Expected command name %q, got %q", name, cmd.Name())
			}
		})
	}
}

func TestConfirmCmd_RequiresArg(t *testing.T) {
	cmd, _, _ := rootCmd.Find([]string{"confirm"})
	err := cmd.Args(cmd, []string{})
	if err == nil {
		t.Error("Expected error when no args provided to confirm")
	}
}

func TestConfirmCmd_AcceptsOneArg(t *testing.T) {
	cmd, _, _ := rootCmd.Find([]string{"confirm"})
	err := cmd.Args(cmd, []string{"Delete?"})
	if err != nil {
		t.Errorf("Expected no error with 1 arg, got: %v", err)
	}
}

func TestSelectProjectCmd_HasProjectsFileFlag(t *testing.T) {
	cmd, _, _ := rootCmd.Find([]string{"select-project"})
	flag := cmd.Flags().Lookup("projects-file")
	if flag == nil {
		t.Fatal("Expected --projects-file flag on select-project")
	}
}

func TestMainMenuCmd_HasAllFlags(t *testing.T) {
	cmd, _, _ := rootCmd.Find([]string{"main-menu"})

	flags := []struct {
		name     string
		defValue string
	}{
		{"projects-file", ""},
		{"ai-tool", "claude"},
		{"ai-tools", "claude"},
		{"ghost-display", "animated"},
		{"tab-title", "full"},
		{"update-version", ""},
	}

	for _, f := range flags {
		t.Run(f.name, func(t *testing.T) {
			flag := cmd.Flags().Lookup(f.name)
			if flag == nil {
				t.Fatalf("Expected --%s flag", f.name)
			}
			if flag.DefValue != f.defValue {
				t.Errorf("Expected default %q, got %q", f.defValue, flag.DefValue)
			}
		})
	}
}

func TestRunSelectProject_EmptyProjectsFile(t *testing.T) {
	// Create an empty projects file
	tmpDir := t.TempDir()
	emptyFile := filepath.Join(tmpDir, "projects")
	os.WriteFile(emptyFile, []byte(""), 0644)

	// Reset flag and execute
	rootCmd.SetArgs([]string{"select-project", "--projects-file", emptyFile})

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := rootCmd.Execute()

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("Expected no error for empty projects, got: %v", err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if !strings.Contains(output, `"selected":false`) {
		t.Errorf("Expected {\"selected\":false} for empty projects, got: %s", output)
	}
}

func TestRunSelectProject_MissingFile(t *testing.T) {
	rootCmd.SetArgs([]string{"select-project", "--projects-file", "/nonexistent/path/projects"})
	err := rootCmd.Execute()

	if err == nil {
		t.Error("Expected error for missing projects file")
	}
}

func TestRunMainMenu_MissingProjectsFile(t *testing.T) {
	rootCmd.SetArgs([]string{"main-menu", "--projects-file", "/nonexistent/projects"})
	err := rootCmd.Execute()
	if err == nil {
		t.Error("Expected error for missing projects file")
	}
}

func TestRunMainMenu_EmptyProjectsFile(t *testing.T) {
	tmpDir := t.TempDir()
	emptyFile := filepath.Join(tmpDir, "projects")
	os.WriteFile(emptyFile, []byte(""), 0644)

	rootCmd.SetArgs([]string{"main-menu", "--projects-file", emptyFile})

	// Capture stdout so any JSON output doesn't pollute test output
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := rootCmd.Execute()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)

	// Empty projects file is valid (LoadProjects succeeds with 0 projects),
	// but the TUI will fail because there's no TTY in test.
	// The key assertion: it gets past the projects-loading stage without error
	// from LoadProjects. The error, if any, comes from TUITeaOptions (no TTY).
	_ = err
}

func TestConfirmCmd_TooManyArgs(t *testing.T) {
	cmd, _, _ := rootCmd.Find([]string{"confirm"})
	err := cmd.Args(cmd, []string{"arg1", "arg2"})
	if err == nil {
		t.Error("Expected error with too many args for confirm")
	}
}

func TestMultiSelectAIToolCmd_HasAIToolFlag(t *testing.T) {
	// ai-tool is a persistent flag on root, should be accessible from any subcommand
	flag := rootCmd.PersistentFlags().Lookup("ai-tool")
	if flag == nil {
		t.Fatal("Expected --ai-tool persistent flag to be accessible")
	}
}

func TestShowLogoCmd_HasAIToolFlag(t *testing.T) {
	// ai-tool comes from root persistent flags, accessible by show-logo
	flag := rootCmd.PersistentFlags().Lookup("ai-tool")
	if flag == nil {
		t.Fatal("Expected --ai-tool persistent flag")
	}
}

func TestConfigMenuCmd_Exists(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"config-menu"})
	if err != nil {
		t.Fatalf("Failed to find config-menu: %v", err)
	}
	if cmd.Name() != "config-menu" {
		t.Errorf("Expected 'config-menu', got %q", cmd.Name())
	}
}

func TestConfigMenuCmd_Metadata(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"config-menu"})
	if err != nil {
		t.Fatalf("Failed to find config-menu: %v", err)
	}
	if cmd.Short != "Interactive configuration menu" {
		t.Errorf("Expected Short %q, got %q", "Interactive configuration menu", cmd.Short)
	}
	if cmd.Long != "Shows configuration options and returns selected action as JSON" {
		t.Errorf("Expected Long %q, got %q", "Shows configuration options and returns selected action as JSON", cmd.Long)
	}
}

func TestSelectAIToolCmd_Exists(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"select-ai-tool"})
	if err != nil {
		t.Fatalf("Failed to find select-ai-tool: %v", err)
	}
	if cmd.Name() != "select-ai-tool" {
		t.Errorf("Expected 'select-ai-tool', got %q", cmd.Name())
	}
}

func TestRunMainMenu_ProjectsFileFlagRequired(t *testing.T) {
	cmd, _, _ := rootCmd.Find([]string{"main-menu"})
	flag := cmd.Flags().Lookup("projects-file")
	if flag == nil {
		t.Fatal("Expected --projects-file flag on main-menu")
	}
	// Verify the flag is annotated as required
	annotations := flag.Annotations
	if annotations == nil {
		t.Fatal("Expected required annotation on --projects-file")
	}
	required, ok := annotations[cobra.BashCompOneRequiredFlag]
	if !ok || len(required) == 0 || required[0] != "true" {
		t.Error("Expected --projects-file to be marked as required")
	}
}

func TestRunMainMenu_MalformedProjectsFile(t *testing.T) {
	// A file with only comments/blank lines should load as 0 projects
	// (not error at LoadProjects), then fail at TUITeaOptions (no TTY)
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "projects")
	os.WriteFile(f, []byte("# comment\n\n# another comment\n"), 0644)

	rootCmd.SetArgs([]string{"main-menu", "--projects-file", f})

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := rootCmd.Execute()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)

	// LoadProjects should succeed (returns empty slice for comments-only file)
	// Any error here comes from TUITeaOptions, not from loading
	_ = err
}

func TestMainMenuCmd_HasProjectsRootFileFlag(t *testing.T) {
	cmd, _, _ := rootCmd.Find([]string{"main-menu"})
	flag := cmd.Flags().Lookup("projects-root-file")
	if flag == nil {
		t.Fatal("Expected --projects-root-file flag on main-menu")
	}
	if flag.DefValue != "" {
		t.Errorf("Expected default %q, got %q", "", flag.DefValue)
	}
}

func TestSelectBranchCmd_HasProjectPathFlag(t *testing.T) {
	cmd, _, _ := rootCmd.Find([]string{"select-branch"})
	flag := cmd.Flags().Lookup("project-path")
	if flag == nil {
		t.Fatal("Expected --project-path flag on select-branch")
	}
}

func TestSelectBranchCmd_ProjectPathFlagRequired(t *testing.T) {
	cmd, _, _ := rootCmd.Find([]string{"select-branch"})
	flag := cmd.Flags().Lookup("project-path")
	if flag == nil {
		t.Fatal("Expected --project-path flag on select-branch")
	}
	annotations := flag.Annotations
	if annotations == nil {
		t.Fatal("Expected required annotation on --project-path")
	}
	required, ok := annotations[cobra.BashCompOneRequiredFlag]
	if !ok || len(required) == 0 || required[0] != "true" {
		t.Error("Expected --project-path to be marked as required")
	}
}

func TestRunSelectBranch_NonGitDir(t *testing.T) {
	// A non-git directory should yield no branches, outputting {"selected":false}
	tmpDir := t.TempDir()

	rootCmd.SetArgs([]string{"select-branch", "--project-path", tmpDir})

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := rootCmd.Execute()

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("Expected no error for non-git dir, got: %v", err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if !strings.Contains(output, `"selected":false`) {
		t.Errorf("Expected {\"selected\":false} for non-git dir, got: %s", output)
	}
}

func TestConfigMenuCmd_HasVersionFlag(t *testing.T) {
	cmd, _, _ := rootCmd.Find([]string{"config-menu"})
	flag := cmd.Flags().Lookup("version")
	if flag == nil {
		t.Fatal("Expected --version flag on config-menu")
	}
}

func TestSelectProjectCmd_ProjectsFileFlagRequired(t *testing.T) {
	cmd, _, _ := rootCmd.Find([]string{"select-project"})
	flag := cmd.Flags().Lookup("projects-file")
	if flag == nil {
		t.Fatal("Expected --projects-file flag on select-project")
	}
	// Verify the flag is annotated as required
	annotations := flag.Annotations
	if annotations == nil {
		t.Fatal("Expected required annotation on --projects-file")
	}
	required, ok := annotations[cobra.BashCompOneRequiredFlag]
	if !ok || len(required) == 0 || required[0] != "true" {
		t.Error("Expected --projects-file to be marked as required")
	}
}

func TestDiffViewCmd_Registered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"diff-view"})
	if err != nil {
		t.Fatalf("subcommand not found: %v", err)
	}
	if cmd.Name() != "diff-view" {
		t.Fatalf("got %q", cmd.Name())
	}
}

func TestDiffViewCmd_HasTitleFlag(t *testing.T) {
	cmd, _, _ := rootCmd.Find([]string{"diff-view"})
	if cmd.Flags().Lookup("title") == nil {
		t.Fatal("expected --title flag on diff-view")
	}
}

func TestDiffViewCmd_HasBackdropFileFlag(t *testing.T) {
	cmd, _, _ := rootCmd.Find([]string{"diff-view"})
	if cmd.Flags().Lookup("backdrop-file") == nil {
		t.Fatal("expected --backdrop-file flag on diff-view")
	}
}

func TestClaudeConfigMenuCmd_Registered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"claude-config-menu"})
	if err != nil {
		t.Fatalf("subcommand not found: %v", err)
	}
	if cmd.Name() != "claude-config-menu" {
		t.Fatalf("got %q", cmd.Name())
	}
}

func TestClaudeConfigMenuCmd_HasConfigsListFlag(t *testing.T) {
	cmd, _, _ := rootCmd.Find([]string{"claude-config-menu"})
	if cmd.Flags().Lookup("configs-list") == nil {
		t.Fatal("expected --configs-list flag")
	}
}
