package allin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFastTarget_keeps_an_account_row_on_its_login(t *testing.T) {
	row := Target{Kind: KindAccount, Source: "work", Model: "claude-opus-5-5", Want1M: true}
	want := Target{Kind: KindAccount, Source: "work", Model: "claude-haiku-4-5-20251001"}
	if got := fastTarget(row, nil); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestFastTarget_uses_a_profiles_own_fast_model(t *testing.T) {
	row := Target{Kind: KindConfig, Source: "deepseek", Model: "deepseek-pro"}
	got := fastTarget(row, func(source string) string {
		if source != "deepseek" {
			t.Fatalf("asked for %q", source)
		}
		return "deepseek-flash"
	})
	want := Target{Kind: KindConfig, Source: "deepseek", Model: "deepseek-flash"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestFastTarget_keeps_a_profile_without_a_fast_model_on_its_row(t *testing.T) {
	row := Target{Kind: KindConfig, Source: "qwen", Model: "qwen-big"}
	got := fastTarget(row, func(string) string { return "" })
	want := Target{Kind: KindConfig, Source: "qwen", Model: "qwen-big"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestFastTarget_sends_a_session_row_to_haiku_on_the_session_login(t *testing.T) {
	for _, row := range []Target{{}, {Kind: KindSession, Model: "claude-opus-5-5", Want1M: true}} {
		want := Target{Kind: KindSession, Model: "claude-haiku-4-5-20251001"}
		if got := fastTarget(row, nil); got != want {
			t.Errorf("fastTarget(%+v) = %+v, want %+v", row, got, want)
		}
	}
}

func TestEnvFastModelFor_reads_the_profiles_haiku_mapping(t *testing.T) {
	env := rosterEnv(t)
	writeProfile(t, env, "DeepSeek", "deepseek.json",
		`{"env":{"ANTHROPIC_DEFAULT_HAIKU_MODEL":"deepseek-flash"}}`)
	if got := env.FastModelFor("deepseek"); got != "deepseek-flash" {
		t.Fatalf("got %q", got)
	}
	if got := env.FastModelFor("zhipu-glm"); got != "" {
		t.Fatalf("a profile with no mapping gave %q", got)
	}
}

func TestEnvFastModelFor_refuses_a_path_shaped_source(t *testing.T) {
	env := rosterEnv(t)
	outside := filepath.Join(filepath.Dir(env.ConfigsDir), "outside.json")
	if err := os.WriteFile(outside, []byte(`{"env":{"ANTHROPIC_DEFAULT_HAIKU_MODEL":"leak"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := env.FastModelFor("../outside"); got != "" {
		t.Fatalf("read a file outside the configs dir: %q", got)
	}
}

func TestStartingRow_reads_the_settings_model(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	write := func(body string) {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"model":"wisp/cfg.deepseek/deepseek-pro"}`)
	if got := StartingRow(path); got != (Target{Kind: KindConfig, Source: "deepseek", Model: "deepseek-pro"}) {
		t.Fatalf("got %+v", got)
	}
	write(`{"model":"wisp/fast"}`)
	if got := StartingRow(path); got != (Target{}) {
		t.Fatalf("a fast marker as the start row gave %+v", got)
	}
	write(`not json`)
	if got := StartingRow(path); got != (Target{}) {
		t.Fatalf("broken settings gave %+v", got)
	}
	if got := StartingRow(filepath.Join(t.TempDir(), "missing.json")); got != (Target{}) {
		t.Fatalf("a missing file gave %+v", got)
	}
}

func TestUserSettingsPath_follows_CLAUDE_CONFIG_DIR(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/acct")
	if got := UserSettingsPath(); got != "/tmp/acct/settings.json" {
		t.Fatalf("got %q", got)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home, _ := os.UserHomeDir()
	if got := UserSettingsPath(); got != filepath.Join(home, ".claude", "settings.json") {
		t.Fatalf("got %q", got)
	}
}
