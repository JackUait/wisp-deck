package models_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jackuait/wisp-deck/internal/models"
)

// A fresh install has no projects file at all — the installer deliberately
// leaves creation to "add a project from the main menu". That menu can only
// open if a missing file reads as an empty project list, exactly like
// load_projects in lib/projects.sh. Erroring here killed every first launch
// silently (the picker never started and the window closed as if quit).
func TestLoadProjects_MissingFile_IsEmptyNotError(t *testing.T) {
	dir := t.TempDir()

	projects, err := models.LoadProjects(filepath.Join(dir, "projects"))
	if err != nil {
		t.Fatalf("a missing projects file must read as empty, got error: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("expected 0 projects, got %d", len(projects))
	}
}

func TestLoadProjects_StaleField_ExistingPath(t *testing.T) {
	dir := t.TempDir()
	realDir := filepath.Join(dir, "myproject")
	os.MkdirAll(realDir, 0755)
	file := filepath.Join(dir, "projects")
	os.WriteFile(file, []byte("myproject:"+realDir+"\n"), 0644)

	projects, err := models.LoadProjects(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
	if projects[0].Stale {
		t.Error("expected Stale=false for existing path")
	}
}

func TestLoadProjects_StaleField_MissingPath(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "projects")
	os.WriteFile(file, []byte("ghost:/nonexistent/path/xyz\n"), 0644)

	projects, err := models.LoadProjects(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
	if !projects[0].Stale {
		t.Error("expected Stale=true for missing path")
	}
}
