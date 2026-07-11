package tui

import (
	"image/color"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func previewModel(t *testing.T, path string) DiffViewModel {
	t.Helper()
	data := pngBytes(t, solidImage(8, 8, color.RGBA{9, 9, 9, 255}))
	m := NewImageView("dot.png", data, "modified")
	if path != "" {
		m = m.WithImagePath(path)
	}
	return sizeDiff(m, 100, 30)
}

// recordOpens redirects the viewer launcher and records the paths it gets.
func recordOpens(t *testing.T) *[]string {
	t.Helper()
	var got []string
	prev := openInPreview
	openInPreview = func(path string) error { got = append(got, path); return nil }
	t.Cleanup(func() { openInPreview = prev })
	return &got
}

// The header names the destination app so it's obvious where the image opens.
func TestImageView_header_shows_open_in_preview_button(t *testing.T) {
	m := previewModel(t, "/tmp/dot.png")
	if !strings.Contains(m.View(), "[ Open in Preview ]") {
		t.Error("image header must show the [ Open in Preview ] button")
	}
}

// Without a file path there is nothing to open — no button, no dead control.
func TestImageView_no_open_button_without_path(t *testing.T) {
	m := previewModel(t, "")
	if strings.Contains(m.View(), "Open in Preview") {
		t.Error("no path -> the open button must be hidden")
	}
}

func TestDiffView_no_open_button_in_diff_mode(t *testing.T) {
	m := sizeDiff(NewDiffView("a.go", "+x\n"), 100, 30)
	if strings.Contains(m.View(), "Open in Preview") {
		t.Error("diff mode must not show the open button")
	}
}

// The footer hint spells out the shortcut and its destination.
func TestImageView_footer_hints_open_in_preview(t *testing.T) {
	m := previewModel(t, "/tmp/dot.png")
	if !strings.Contains(m.View(), "o Preview") {
		t.Error("footer must hint the o shortcut opens Preview")
	}
}

func TestImageView_o_key_opens_in_preview(t *testing.T) {
	got := recordOpens(t)
	m := previewModel(t, "/tmp/dot.png")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m = updated.(DiffViewModel)
	if len(*got) != 1 || (*got)[0] != "/tmp/dot.png" {
		t.Fatalf("o must open the image path in Preview, got %v", *got)
	}
	if m.quitting {
		t.Error("opening in Preview must not close the popup")
	}
}

func TestImageView_o_key_without_path_is_noop(t *testing.T) {
	got := recordOpens(t)
	m := previewModel(t, "")
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if len(*got) != 0 {
		t.Fatalf("no path -> o must be a no-op, got %v", *got)
	}
}

// Clicking the button opens the image. The button sits on the title row,
// right-anchored just left of [ Discard ].
func TestImageView_click_open_button_opens_in_preview(t *testing.T) {
	got := recordOpens(t)
	m := previewModel(t, "/tmp/dot.png")
	mh, mv, cw, _ := m.layout()
	os, oe := openButtonSpan(cw)
	click := tea.MouseMsg{
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
		X: mh + 1 + (os+oe)/2, Y: mv + 1,
	}
	m.Update(click)
	if len(*got) != 1 || (*got)[0] != "/tmp/dot.png" {
		t.Fatalf("clicking the button must open the image in Preview, got %v", *got)
	}
}

// While the discard confirm is armed, the title row belongs to Yes/No: the
// open control hides and its shortcut is swallowed like every other key.
func TestImageView_discard_armed_hides_open_button(t *testing.T) {
	got := recordOpens(t)
	m := previewModel(t, "/tmp/dot.png")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = updated.(DiffViewModel)
	if strings.Contains(m.View(), "Open in Preview") {
		t.Error("armed discard confirm must hide the open button")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if len(*got) != 0 {
		t.Errorf("o while armed must not open anything, got %v", *got)
	}
}
