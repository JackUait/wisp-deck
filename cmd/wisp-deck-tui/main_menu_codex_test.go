package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMainMenuCodexFlagExists(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"main-menu"})
	if err != nil {
		t.Fatal(err)
	}
	if flag := cmd.Flags().Lookup("codex"); flag == nil {
		t.Fatal("main-menu is missing --codex")
	}
}

func TestBuildMainMenuModelRejectsRelativeCodexPath(t *testing.T) {
	dir := t.TempDir()
	projectsFile := filepath.Join(dir, "projects")
	if err := os.WriteFile(projectsFile, []byte("proj:/tmp/proj\n"), 0644); err != nil {
		t.Fatal(err)
	}

	oldProjects, oldCodex := mainMenuProjectsFile, mainMenuCodexPath
	t.Cleanup(func() {
		mainMenuProjectsFile = oldProjects
		mainMenuCodexPath = oldCodex
	})
	mainMenuProjectsFile = projectsFile
	mainMenuCodexPath = "relative/codex"

	_, err := buildMainMenuModel()
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("buildMainMenuModel error = %v, want absolute Codex path", err)
	}
}
