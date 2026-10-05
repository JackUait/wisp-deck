package allin

import (
	"os"
	"path/filepath"
	"testing"
)

const testSession = "d2b840fb-2662-4480-ad4b-f732a12f6f3f"

func TestSessionRows_returns_the_row_a_session_last_ran_on(t *testing.T) {
	rows := SessionRows{Dir: filepath.Join(t.TempDir(), "rows")}
	if err := rows.Record(testSession, "wisp/acct.personal/claude-opus-5-5[1m]"); err != nil {
		t.Fatal(err)
	}
	if err := rows.Record(testSession, "wisp/cfg.deepseek/deepseek-flash"); err != nil {
		t.Fatal(err)
	}
	if got := rows.Lookup(testSession); got != "wisp/cfg.deepseek/deepseek-flash" {
		t.Fatalf("Lookup = %q", got)
	}
}

func TestSessionRows_knows_nothing_about_an_unseen_session(t *testing.T) {
	rows := SessionRows{Dir: t.TempDir()}
	if got := rows.Lookup(testSession); got != "" {
		t.Fatalf("Lookup = %q, want empty", got)
	}
}

func TestSessionRows_refuses_a_session_id_that_names_another_path(t *testing.T) {
	root := t.TempDir()
	rows := SessionRows{Dir: filepath.Join(root, "rows")}
	if err := rows.Record("../escape", "wisp/acct.default/claude-opus-5-5"); err == nil {
		t.Fatal("Record accepted a path-shaped session id")
	}
	if _, err := os.Stat(filepath.Join(root, "escape")); !os.IsNotExist(err) {
		t.Fatalf("a file was written outside the rows dir: %v", err)
	}
	if got := rows.Lookup("../escape"); got != "" {
		t.Fatalf("Lookup = %q, want empty", got)
	}
}

// The stored row becomes ANTHROPIC_MODEL for the next launch, so a value that
// could not be a picker id is never stored or served.
func TestSessionRows_refuses_a_row_that_is_not_a_model_id(t *testing.T) {
	rows := SessionRows{Dir: t.TempDir()}
	for _, model := range []string{"", "a b", "x\ny", "$(rm -rf ~)", "wisp/fast"} {
		if err := rows.Record(testSession, model); err == nil {
			t.Errorf("Record accepted %q", model)
		}
	}
	if err := os.WriteFile(filepath.Join(rows.Dir, testSession), []byte("bad value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := rows.Lookup(testSession); got != "" {
		t.Fatalf("Lookup served %q", got)
	}
}

// Background calls sent before the first turn go to the starting row, so a
// resumed session must start from its own row, not the shared settings one.
func TestStartingRowFor_prefers_the_session_row_over_shared_settings(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settings, []byte(`{"model":"wisp/acct.default/claude-opus-5-5[1m]"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rows := SessionRows{Dir: filepath.Join(dir, "rows")}
	if err := rows.Record(testSession, "wisp/cfg.deepseek/deepseek-flash"); err != nil {
		t.Fatal(err)
	}
	want := Target{Kind: KindConfig, Source: "deepseek", Model: "deepseek-flash"}
	if got := StartingRowFor(rows, testSession, settings); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	shared := Target{Kind: KindAccount, Source: "default", Model: "claude-opus-5-5", Want1M: true}
	if got := StartingRowFor(rows, "", settings); got != shared {
		t.Fatalf("no session: got %+v, want %+v", got, shared)
	}
}

func TestSessionRows_with_no_dir_stores_nothing(t *testing.T) {
	t.Chdir(t.TempDir())
	rows := SessionRows{Dir: SessionRowsDir("")}
	if err := rows.Record(testSession, "wisp/acct.default/claude-opus-5-5"); err == nil {
		t.Fatal("Record wrote with no dir")
	}
	if entries, _ := os.ReadDir("."); len(entries) != 0 {
		t.Fatalf("wrote into the cwd: %v", entries)
	}
}
