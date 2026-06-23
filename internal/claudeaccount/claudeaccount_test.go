package claudeaccount

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_parses_label_dir_skips_junk(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")
	if err := os.WriteFile(list, []byte("# header\n\nWork Max:work\nnocolon\nPersonal:personal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Load(list)
	if len(got) != 2 {
		t.Fatalf("want 2 accounts, got %d: %+v", len(got), got)
	}
	if got[0].Label != "Work Max" || got[0].Dir != "work" {
		t.Errorf("got[0]=%+v", got[0])
	}
	if got[1].Label != "Personal" || got[1].Dir != "personal" {
		t.Errorf("got[1]=%+v", got[1])
	}
}

func TestLoad_missing_file_is_nil(t *testing.T) {
	if got := Load(filepath.Join(t.TempDir(), "absent")); got != nil {
		t.Fatalf("want nil, got %+v", got)
	}
}

func TestGetActive_default_when_absent_or_default(t *testing.T) {
	dir := t.TempDir()
	ptr := filepath.Join(dir, "claude-account")
	if got := GetActive(ptr); got != "" {
		t.Fatalf("absent pointer should be Default (empty), got %q", got)
	}
	if err := os.WriteFile(ptr, []byte("default\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := GetActive(ptr); got != "" {
		t.Fatalf(`"default" should be Default (empty), got %q`, got)
	}
}

func TestSetActive_writes_and_default_removes(t *testing.T) {
	dir := t.TempDir()
	ptr := filepath.Join(dir, "sub", "claude-account") // exercise MkdirAll
	if err := SetActive(ptr, "work"); err != nil {
		t.Fatal(err)
	}
	if got := GetActive(ptr); got != "work" {
		t.Fatalf("got %q", got)
	}
	if err := SetActive(ptr, "default"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ptr); !os.IsNotExist(err) {
		t.Fatalf("default should remove pointer")
	}
}

func TestAdd_creates_dir_appends_list_and_dedups_slug(t *testing.T) {
	dir := t.TempDir()
	accountsDir := filepath.Join(dir, "claude-accounts")
	list := filepath.Join(dir, "claude-accounts.list")

	d1, err := Add(list, accountsDir, "Work Max")
	if err != nil {
		t.Fatal(err)
	}
	if d1 != "work-max" {
		t.Fatalf("slug: got %q", d1)
	}
	if info, err := os.Stat(filepath.Join(accountsDir, d1)); err != nil || !info.IsDir() {
		t.Fatalf("account dir not created: %v", err)
	}
	// A second account whose label slugifies the same gets a -2 suffix.
	d2, err := Add(list, accountsDir, "work max")
	if err != nil {
		t.Fatal(err)
	}
	if d2 != "work-max-2" {
		t.Fatalf("collision: got %q", d2)
	}
	got := Load(list)
	if len(got) != 2 || got[0].Label != "Work Max" || got[0].Dir != "work-max" {
		t.Fatalf("list: %+v", got)
	}
}

func TestAdd_empty_slug_falls_back(t *testing.T) {
	dir := t.TempDir()
	d, err := Add(filepath.Join(dir, "list"), filepath.Join(dir, "acc"), "!!!")
	if err != nil {
		t.Fatal(err)
	}
	if d != "account" {
		t.Fatalf("got %q", d)
	}
}

func TestRemove_drops_line_dir_and_clears_active(t *testing.T) {
	dir := t.TempDir()
	accountsDir := filepath.Join(dir, "claude-accounts")
	list := filepath.Join(dir, "claude-accounts.list")
	ptr := filepath.Join(dir, "claude-account")

	work, _ := Add(list, accountsDir, "Work")
	personal, _ := Add(list, accountsDir, "Personal")
	if err := SetActive(ptr, work); err != nil {
		t.Fatal(err)
	}

	if err := Remove(list, accountsDir, ptr, work); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(accountsDir, work)); !os.IsNotExist(err) {
		t.Fatalf("work dir should be gone")
	}
	if got := GetActive(ptr); got != "" {
		t.Fatalf("active should revert to Default, got %q", got)
	}
	got := Load(list)
	if len(got) != 1 || got[0].Dir != personal {
		t.Fatalf("list after remove: %+v", got)
	}
}

func TestRename_changes_label_keeps_dir_and_login(t *testing.T) {
	dir := t.TempDir()
	accountsDir := filepath.Join(dir, "claude-accounts")
	list := filepath.Join(dir, "claude-accounts.list")

	work, _ := Add(list, accountsDir, "Work")
	personal, _ := Add(list, accountsDir, "Personal")

	if err := Rename(list, work, "Day Job"); err != nil {
		t.Fatal(err)
	}

	got := Load(list)
	if len(got) != 2 {
		t.Fatalf("expected 2 accounts, got %+v", got)
	}
	// The renamed entry keeps its dir but shows the new label.
	if got[0].Dir != work || got[0].Label != "Day Job" {
		t.Errorf("work entry: got %+v, want label 'Day Job' dir %q", got[0], work)
	}
	// The other entry is untouched.
	if got[1].Dir != personal || got[1].Label != "Personal" {
		t.Errorf("personal entry should be untouched, got %+v", got[1])
	}
	// The config dir (its login) is still there.
	if _, err := os.Stat(filepath.Join(accountsDir, work)); err != nil {
		t.Errorf("renamed account's config dir should be intact: %v", err)
	}
}

func TestRename_unknown_dir_is_noop(t *testing.T) {
	dir := t.TempDir()
	accountsDir := filepath.Join(dir, "claude-accounts")
	list := filepath.Join(dir, "claude-accounts.list")
	work, _ := Add(list, accountsDir, "Work")

	if err := Rename(list, "ghost", "Nope"); err != nil {
		t.Fatal(err)
	}
	got := Load(list)
	if len(got) != 1 || got[0].Dir != work || got[0].Label != "Work" {
		t.Errorf("unknown dir should leave the list unchanged, got %+v", got)
	}
}

func TestResolveDir_existing_vs_missing_vs_default(t *testing.T) {
	dir := t.TempDir()
	accountsDir := filepath.Join(dir, "claude-accounts")
	if err := os.MkdirAll(filepath.Join(accountsDir, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	ptr := filepath.Join(dir, "claude-account")

	// Default → empty.
	if got := ResolveDir(accountsDir, ptr); got != "" {
		t.Fatalf("default should resolve empty, got %q", got)
	}
	// Existing dir → abs path.
	if err := os.WriteFile(ptr, []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ResolveDir(accountsDir, ptr); got != filepath.Join(accountsDir, "work") {
		t.Fatalf("got %q", got)
	}
	// Missing dir → empty.
	if err := os.WriteFile(ptr, []byte("ghost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ResolveDir(accountsDir, ptr); got != "" {
		t.Fatalf("missing dir should resolve empty, got %q", got)
	}
}
