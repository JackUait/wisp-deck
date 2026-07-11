package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The --update-version flag must reach the model: it drives the header update
// notice and the U/click update action.
func TestBuildMainMenuModel_AppliesUpdateVersion(t *testing.T) {
	dir := t.TempDir()
	projectsFile := filepath.Join(dir, "projects")
	if err := os.WriteFile(projectsFile, []byte("proj:/tmp/proj\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldProjects, oldVer := mainMenuProjectsFile, mainMenuUpdateVer
	t.Cleanup(func() { mainMenuProjectsFile, mainMenuUpdateVer = oldProjects, oldVer })
	mainMenuProjectsFile = projectsFile
	mainMenuUpdateVer = "9.9.9"

	model, err := buildMainMenuModel()
	if err != nil {
		t.Fatalf("buildMainMenuModel: %v", err)
	}
	if got := model.UpdateVersion(); got != "9.9.9" {
		t.Errorf("model.UpdateVersion() = %q, want %q (flag not applied)", got, "9.9.9")
	}
}

// The binary's build-time version must reach the model so the About card can
// show it.
func TestBuildMainMenuModel_AppliesAppVersion(t *testing.T) {
	dir := t.TempDir()
	projectsFile := filepath.Join(dir, "projects")
	if err := os.WriteFile(projectsFile, []byte("proj:/tmp/proj\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldProjects, oldVersion := mainMenuProjectsFile, Version
	t.Cleanup(func() { mainMenuProjectsFile, Version = oldProjects, oldVersion })
	mainMenuProjectsFile = projectsFile
	Version = "3.1.4"

	model, err := buildMainMenuModel()
	if err != nil {
		t.Fatalf("buildMainMenuModel: %v", err)
	}
	if got := model.AppVersion(); got != "3.1.4" {
		t.Errorf("model.AppVersion() = %q, want %q (binary version not applied)", got, "3.1.4")
	}
}
