package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newAddProjMenu builds a menu in add-project input mode with a temp projects
// file; HOME points at the returned dir so the browser and clone destination
// land there.
func newAddProjMenu(t *testing.T) (*MainMenuModel, string) {
	t.Helper()
	m := newTestMenu()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	m.projectsFile = filepath.Join(dir, "projects")
	m.enterInputMode("add-project")
	return m, dir
}

func TestAddProjectBox_PathFieldNeverCarriesFocusMarker(t *testing.T) {
	// The path is picked in the folder browser, so the form's path row is
	// static and never focused.
	m, _ := newAddProjMenu(t)
	raw := stripAnsi(m.renderInputBox())

	if strings.Contains(raw, "▸ Path:") {
		t.Errorf("static path row must not carry the ▸ marker, got:\n%s", raw)
	}
	if strings.Contains(raw, "▸ Name:") {
		t.Errorf("unfocused name field must not carry the ▸ marker, got:\n%s", raw)
	}
}

func TestAddProjectBox_FocusMarkerMovesToName(t *testing.T) {
	m, dir := newAddProjMenu(t)
	m.pathInput.SetValue(dir)
	m.advanceToNameField()
	raw := stripAnsi(m.renderInputBox())

	if !strings.Contains(raw, "▸ Name:") {
		t.Errorf("focused name field should carry the ▸ marker, got:\n%s", raw)
	}
	if strings.Contains(raw, "▸ Path:") {
		t.Errorf("unfocused path field must not carry the ▸ marker, got:\n%s", raw)
	}
}

func TestAddProjectBox_GitHubURLShowsDestinationPreview(t *testing.T) {
	m, _ := newAddProjMenu(t)
	m.pathInput.SetValue("https://github.com/owner/my-repo")
	m.maybeAutoDeriveName()
	raw := stripAnsi(m.renderInputBox())

	if !strings.Contains(raw, "owner/my-repo → ") {
		t.Errorf("GitHub URL should show a repo → destination preview, got:\n%s", raw)
	}
	if !strings.Contains(raw, "~/my-repo") {
		t.Errorf("preview should name the clone destination ~/my-repo, got:\n%s", raw)
	}
	if strings.Contains(raw, "Tip:") {
		t.Errorf("tip should be replaced by the destination preview, got:\n%s", raw)
	}
}

func TestAddProjectBox_GitHubDestAbbreviatesHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := newTestMenu()
	dir := t.TempDir()
	m.projectsFile = filepath.Join(dir, "projects")
	m.enterInputMode("add-project")
	m.pathInput.SetValue("https://github.com/owner/my-repo")
	m.maybeAutoDeriveName()
	raw := stripAnsi(m.renderInputBox())

	if !strings.Contains(raw, "→ ~/my-repo") {
		t.Errorf("home-based destination should render with ~, got:\n%s", raw)
	}
}

func TestAddProjectBox_GitHubDestCollisionShowsError(t *testing.T) {
	m, dir := newAddProjMenu(t)
	if err := os.MkdirAll(filepath.Join(dir, "my-repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	m.pathInput.SetValue("https://github.com/owner/my-repo")
	m.maybeAutoDeriveName()
	raw := stripAnsi(m.renderInputBox())

	if !strings.Contains(raw, "✗ already exists: ") {
		t.Errorf("existing destination should surface a live collision warning, got:\n%s", raw)
	}
}

func TestAddProjectBox_TipShownWithoutURL(t *testing.T) {
	m, _ := newAddProjMenu(t)
	raw := stripAnsi(m.renderInputBox())

	if !strings.Contains(raw, "Tip: paste a GitHub repo URL to clone it") {
		t.Errorf("path focus without a URL should show the GitHub tip, got:\n%s", raw)
	}
}

func TestAddProjectBox_StableHeightAcrossStatusStates(t *testing.T) {
	m, _ := newAddProjMenu(t)
	withTip := len(strings.Split(m.renderInputBox(), "\n"))

	m.advanceToNameField() // tip hidden now: slot must stay as a blank row
	withBlank := len(strings.Split(m.renderInputBox(), "\n"))

	if withTip != withBlank {
		t.Errorf("box height must not jump when the tip hides: with tip %d lines, without %d", withTip, withBlank)
	}
}

// startCloneMenu drives the form to a live in-flight clone via the real
// submit path with an injected git clone.
func startCloneMenu(t *testing.T) *MainMenuModel {
	t.Helper()
	m, _ := newAddProjMenu(t)
	m.pathInput.SetValue("https://github.com/owner/my-repo")
	m.maybeAutoDeriveName()
	m.advanceToNameField()
	m.gitClone = func(url, dest string) error { return os.MkdirAll(dest, 0o755) }
	_, cmd := m.submitInputMode()
	if cmd == nil {
		t.Fatal("submit with a GitHub URL should dispatch a clone cmd")
	}
	if !m.cloning {
		t.Fatal("model should be cloning after submit")
	}
	return m
}

func TestAddProjectBox_CloningShowsSpinnerWithDestination(t *testing.T) {
	m := startCloneMenu(t)
	raw := stripAnsi(m.renderInputBox())

	if !strings.Contains(raw, "⠋ Cloning owner/my-repo → ") {
		t.Errorf("cloning state should show a spinner with repo and destination, got:\n%s", raw)
	}
	if strings.Contains(raw, "Cloning repository…") {
		t.Errorf("the old static cloning row should be gone, got:\n%s", raw)
	}
	if !strings.Contains(raw, "Ctrl+C quit") {
		t.Errorf("footer while cloning should advertise only Ctrl+C, got:\n%s", raw)
	}
}

func TestCloneTick_AdvancesSpinnerAndStopsWhenDone(t *testing.T) {
	m := startCloneMenu(t)

	_, cmd := m.Update(cloneTickMsg{})
	if cmd == nil {
		t.Error("tick while cloning should re-arm the ticker")
	}
	raw := stripAnsi(m.renderInputBox())
	if !strings.Contains(raw, "⠙ Cloning") {
		t.Errorf("tick should advance the spinner frame, got:\n%s", raw)
	}

	m.cloning = false
	if _, cmd := m.Update(cloneTickMsg{}); cmd != nil {
		t.Error("ticker must stop once the clone is finished")
	}
}

func TestAddProjectBox_ErrorsCarryCrossPrefix(t *testing.T) {
	m, _ := newAddProjMenu(t)
	m.inputErr = fmt.Errorf("path boom")
	raw := stripAnsi(m.renderInputBox())
	if !strings.Contains(raw, "✗ path boom") {
		t.Errorf("path error should carry ✗ prefix, got:\n%s", raw)
	}

	m.inputErr = nil
	m.nameErr = fmt.Errorf("name boom")
	raw = stripAnsi(m.renderInputBox())
	if !strings.Contains(raw, "✗ name boom") {
		t.Errorf("name error should carry ✗ prefix, got:\n%s", raw)
	}
}
