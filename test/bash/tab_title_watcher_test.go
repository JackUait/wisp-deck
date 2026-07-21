package bash_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tabTitleSnippet sources tui.sh and tab-title-watcher.sh, then runs body.
func tabTitleSnippet(t *testing.T, body string) string {
	t.Helper()
	root := projectRoot(t)
	return fmt.Sprintf("source %q && source %q && %s",
		filepath.Join(root, "lib", "tui.sh"),
		filepath.Join(root, "lib", "tab-title-watcher.sh"), body)
}

func writeAttentionState(t *testing.T, root, generation, sequence, phase, reason string) string {
	t.Helper()
	dir := filepath.Join(root, generation)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "state")
	record := fmt.Sprintf("1\t%s\t%s\t%s\t%s\n", generation, sequence, phase, reason)
	if err := os.WriteFile(state, []byte(record), 0600); err != nil {
		t.Fatal(err)
	}
	return state
}

func writeAttentionDescriptor(t *testing.T, root, generation, tool, state string) string {
	t.Helper()
	descriptor := filepath.Join(root, "descriptor")
	record := fmt.Sprintf("1\t%s\t%s\t%s\n", generation, tool, state)
	if err := os.WriteFile(descriptor, []byte(record), 0600); err != nil {
		t.Fatal(err)
	}
	return descriptor
}

func TestTabTitleWatcher_play_notification_sound_uses_real_marked_guard(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTempFile(
		t,
		configDir,
		"claude-features.json",
		`{"sound":true,"sound_name":"Glass"}`,
	)
	generation := "generation.silent1"
	state := writeAttentionState(t, root, generation, "0", "ready", "-")
	descriptor := writeAttentionDescriptor(t, root, generation, "claude", state)
	recording := filepath.Join(root, "tui-argv")
	binDir := mockCommand(t, root, "wisp-deck-tui",
		fmt.Sprintf(`printf '%%s\n' "$@" > %q`, recording))

	project := projectRoot(t)
	script := fmt.Sprintf(`
source %q
source %q
source %q
apply_tab_title() { :; }
keep_awake_tick() { :; }
tmux_fixture() {
  [ "$1" = list-panes ] && printf '%%7\t1\n'
}
attention_watcher_reset
attention_watcher_tick session project full tmux_fixture %q %q
printf '1\t%s\t1\tattention\tquestion\n' > %q
attention_watcher_tick session project full tmux_fixture %q %q
wait
`,
		filepath.Join(project, "lib", "tui.sh"),
		filepath.Join(project, "lib", "notification-setup.sh"),
		filepath.Join(project, "lib", "tab-title-watcher.sh"),
		descriptor,
		configDir,
		generation,
		state,
		descriptor,
		configDir,
	)
	_, code := runBashSnippet(t, script, buildEnv(t, []string{binDir}))
	assertExitCode(t, code, 0)
	if _, err := os.Stat(recording); !os.IsNotExist(err) {
		t.Fatalf("marked watcher notification delegated to wisp-deck-tui: %v", err)
	}
}

func TestTabTitleWatcher_reads_strict_descriptor_and_state_records(t *testing.T) {
	root := t.TempDir()
	generation := "generation.Abc123"
	state := writeAttentionState(t, root, generation, "18446744073709551615", "attention", "question")
	descriptor := writeAttentionDescriptor(t, root, generation, "codex", state)

	out, code := runBashSnippet(t, tabTitleSnippet(t,
		fmt.Sprintf(`attention_watcher_read_descriptor %q && attention_watcher_read_state %q %q`,
			descriptor, state, generation)), nil)
	assertExitCode(t, code, 0)
	want := generation + "\tcodex\t" + state + "\n" +
		generation + "\t18446744073709551615\tattention\tquestion"
	if strings.TrimSpace(out) != want {
		t.Fatalf("strict records = %q, want %q", strings.TrimSpace(out), want)
	}
}

func TestTabTitleWatcher_rejects_malformed_descriptors(t *testing.T) {
	root := t.TempDir()
	generation := "generation.good1"
	state := writeAttentionState(t, root, generation, "0", "ready", "-")
	descriptor := filepath.Join(root, "descriptor")

	tests := map[string]string{
		"empty":                "",
		"missing newline":      "1\t" + generation + "\tclaude\t" + state,
		"extra newline":        "1\t" + generation + "\tclaude\t" + state + "\n\n",
		"wrong version":        "2\t" + generation + "\tclaude\t" + state + "\n",
		"invalid generation":   "1\tgeneration../escape\tclaude\t" + state + "\n",
		"invalid tool":         "1\t" + generation + "\tother\t" + state + "\n",
		"outside state":        "1\t" + generation + "\tclaude\t/tmp/state\n",
		"wrong generation dir": "1\t" + generation + "\tclaude\t" + filepath.Join(root, "generation.other", "state") + "\n",
		"extra field":          "1\t" + generation + "\tclaude\t" + state + "\textra\n",
		"oversized":            strings.Repeat("x", 4097),
	}
	for name, record := range tests {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(descriptor, []byte(record), 0600); err != nil {
				t.Fatal(err)
			}
			_, code := runBashSnippet(t, tabTitleSnippet(t,
				fmt.Sprintf(`attention_watcher_read_descriptor %q`, descriptor)), nil)
			if code == 0 {
				t.Fatalf("accepted malformed descriptor %q", record)
			}
		})
	}

	t.Run("missing", func(t *testing.T) {
		_ = os.Remove(descriptor)
		_, code := runBashSnippet(t, tabTitleSnippet(t,
			fmt.Sprintf(`attention_watcher_read_descriptor %q`, descriptor)), nil)
		if code == 0 {
			t.Fatal("accepted a missing descriptor")
		}
	})
}

func TestTabTitleWatcher_rejects_malformed_or_stale_states(t *testing.T) {
	root := t.TempDir()
	generation := "generation.good2"
	state := filepath.Join(root, generation, "state")
	if err := os.MkdirAll(filepath.Dir(state), 0700); err != nil {
		t.Fatal(err)
	}

	tests := map[string]string{
		"empty":                    "",
		"missing newline":          "1\t" + generation + "\t0\tready\t-",
		"extra newline":            "1\t" + generation + "\t0\tready\t-\n\n",
		"wrong version":            "2\t" + generation + "\t0\tready\t-\n",
		"stale generation":         "1\tgeneration.old\t1\tattention\tdone\n",
		"leading-zero sequence":    "1\t" + generation + "\t01\tready\t-\n",
		"overflow sequence":        "1\t" + generation + "\t18446744073709551616\tready\t-\n",
		"unknown phase":            "1\t" + generation + "\t1\tidle\t-\n",
		"reason on working":        "1\t" + generation + "\t1\tworking\tdone\n",
		"missing attention reason": "1\t" + generation + "\t1\tattention\t-\n",
		"unknown attention reason": "1\t" + generation + "\t1\tattention\tinput\n",
		"extra field":              "1\t" + generation + "\t1\tready\t-\textra\n",
		"oversized":                strings.Repeat("x", 4097),
	}
	for name, record := range tests {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(state, []byte(record), 0600); err != nil {
				t.Fatal(err)
			}
			_, code := runBashSnippet(t, tabTitleSnippet(t,
				fmt.Sprintf(`attention_watcher_read_state %q %q`, state, generation)), nil)
			if code == 0 {
				t.Fatalf("accepted malformed state %q", record)
			}
		})
	}
}

func TestTabTitleWatcher_rejects_snapshot_that_straddles_rotation(t *testing.T) {
	root := t.TempDir()
	gen1 := "generation.race1"
	gen2 := "generation.race2"
	state1 := writeAttentionState(t, root, gen1, "1", "attention", "question")
	state2 := writeAttentionState(t, root, gen2, "0", "ready", "-")
	descriptor := writeAttentionDescriptor(t, root, gen1, "claude", state1)

	script := tabTitleSnippet(t, fmt.Sprintf(`
eval "$(declare -f _attention_watcher_parse_descriptor | sed '1s/_attention_watcher_parse_descriptor/_attention_watcher_parse_descriptor_original/')"
descriptor_reads=0
_attention_watcher_parse_descriptor() {
  descriptor_reads=$((descriptor_reads + 1))
  if [ "$descriptor_reads" -eq 2 ]; then
    printf '1\t%s\tcodex\t%s\n' > %q.tmp
    mv %q.tmp %q
  fi
  _attention_watcher_parse_descriptor_original "$@"
}
if _attention_watcher_read_snapshot %q; then
  echo accepted-stale-snapshot
  exit 1
fi
`, gen2, state2, descriptor, descriptor, descriptor, descriptor))
	out, code := runBashSnippet(t, script, nil)
	assertExitCode(t, code, 0)
	assertNotContains(t, out, "accepted-stale-snapshot")
}

func TestTabTitleWatcher_discovers_exactly_one_tagged_pane_by_stable_id(t *testing.T) {
	tests := []struct {
		name     string
		panes    string
		want     string
		wantCode int
	}{
		{"one tagged", "%1\t0\n%42\t1\n%9\t0\n", "%42", 0},
		{"none tagged", "%1\t0\n%42\t0\n", "", 1},
		{"multiple tagged", "%3\t1\n%42\t1\n", "", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			calls := filepath.Join(dir, "calls")
			binDir := mockCommand(t, dir, "tmux", `
printf '%s\n' "$*" >> "$TMUX_CALLS"
[ "$1" = list-panes ] && printf '%b' "$TMUX_PANES"
`)
			env := buildEnv(t, []string{binDir}, "TMUX_CALLS="+calls, "TMUX_PANES="+tt.panes)
			out, code := runBashSnippet(t, tabTitleSnippet(t,
				fmt.Sprintf(`discover_ai_pane sess-a %q`, filepath.Join(binDir, "tmux"))), env)
			assertExitCode(t, code, tt.wantCode)
			if strings.TrimSpace(out) != tt.want {
				t.Fatalf("pane = %q, want %q", strings.TrimSpace(out), tt.want)
			}
			data, err := os.ReadFile(calls)
			if err != nil {
				t.Fatal(err)
			}
			// tmux does not translate a backslash+t in -F. The argument itself
			// must contain a real tab byte or production output cannot be split.
			assertContains(t, string(data), "#{pane_id}\t#{@gt_ai}")
		})
	}
}

func TestTabTitleWatcher_alerts_once_per_attention_tuple(t *testing.T) {
	root := t.TempDir()
	generation := "generation.dedupe1"
	state := writeAttentionState(t, root, generation, "0", "ready", "-")
	descriptor := writeAttentionDescriptor(t, root, generation, "claude", state)
	logFile := filepath.Join(root, "watch.log")

	binDir := mockCommand(t, root, "tmux", `
case "$1" in
  list-panes) printf '%%7\t1\n' ;;
  display-message) printf 'task title\n' ;;
esac
`)
	tmuxPath := filepath.Join(binDir, "tmux")
	script := tabTitleSnippet(t, fmt.Sprintf(`
WATCH_LOG=%q
apply_tab_title() { printf 'title:%%s:%%s:%%s:%%s\n' "$1" "$2" "$3" "$4" >> "$WATCH_LOG"; }
play_notification_sound() { printf 'sound:%%s\n' "$1" >> "$WATCH_LOG"; }
keep_awake_tick() { printf 'awake:%%s\n' "$4" >> "$WATCH_LOG"; }
publish_state() {
  printf '1\t%s\t%%s\t%%s\t%%s\n' "$1" "$2" "$3" > %q.tmp
  mv %q.tmp %q
}
attention_watcher_reset
attention_watcher_tick sess-a project full %q %q %q
publish_state 1 attention question
attention_watcher_tick sess-a project full %q %q %q
attention_watcher_tick sess-a project full %q %q %q
publish_state 2 working -
attention_watcher_tick sess-a project full %q %q %q
publish_state 3 attention question
attention_watcher_tick sess-a project full %q %q %q
`, logFile, generation, state, state, state,
		tmuxPath, descriptor, root,
		tmuxPath, descriptor, root,
		tmuxPath, descriptor, root,
		tmuxPath, descriptor, root,
		tmuxPath, descriptor, root))

	_, code := runBashSnippet(t, script, buildEnv(t, []string{binDir}))
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if count := strings.Count(got, "sound:claude\n"); count != 2 {
		t.Fatalf("sound count = %d, want 2; log:\n%s", count, got)
	}
	if count := strings.Count(got, "title:waiting:full:project:claude\n"); count != 2 {
		t.Fatalf("waiting title count = %d, want 2; log:\n%s", count, got)
	}
	assertContains(t, got, "title:active:full:project:claude")
	assertContains(t, got, "awake:active")
}

func TestTabTitleWatcher_missing_malformed_stale_and_unknown_never_alert(t *testing.T) {
	root := t.TempDir()
	generation := "generation.safe1"
	state := writeAttentionState(t, root, generation, "0", "unknown", "-")
	missingGeneration := "generation.missing1"
	missingStateDir := filepath.Join(root, missingGeneration)
	if err := os.MkdirAll(missingStateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	missingState := filepath.Join(missingStateDir, "state")
	malformedGeneration := "generation.malformed1"
	malformedState := writeAttentionState(t, root, malformedGeneration, "0", "unknown", "-")
	descriptor := filepath.Join(root, "descriptor")
	logFile := filepath.Join(root, "watch.log")
	binDir := mockCommand(t, root, "tmux", `
case "$1" in
  list-panes) printf '%%8\t1\n' ;;
  capture-pane) printf 'THIS MUST NEVER BE READ\n'; exit 99 ;;
esac
`)
	tmuxPath := filepath.Join(binDir, "tmux")

	script := tabTitleSnippet(t, fmt.Sprintf(`
WATCH_LOG=%q
apply_tab_title() { :; }
play_notification_sound() { printf 'sound:%%s\n' "$1" >> "$WATCH_LOG"; }
keep_awake_tick() { printf 'awake:%%s\n' "$4" >> "$WATCH_LOG"; }
attention_watcher_reset
# Missing descriptor.
attention_watcher_tick sess-a project project %q %q %q
# Malformed descriptor.
printf 'broken\n' > %q
attention_watcher_tick sess-a project project %q %q %q
# Valid descriptor whose adapter state file is missing.
printf '1\t%s\tclaude\t%s\n' > %q
attention_watcher_tick sess-a project project %q %q %q
# Valid descriptor whose adapter state record is malformed.
printf '1\t%s\tclaude\t%s\n' > %q
printf 'broken\n' > %q
attention_watcher_tick sess-a project project %q %q %q
# Valid descriptor with a state record from a stale generation.
printf '1\t%s\tclaude\t%s\n' > %q
printf '1\tgeneration.old\t1\tattention\tdone\n' > %q
attention_watcher_tick sess-a project project %q %q %q
# Valid unknown is active for keep-awake but never alerts.
printf '1\t%s\t0\tunknown\t-\n' > %q
attention_watcher_tick sess-a project project %q %q %q
`, logFile,
		tmuxPath, descriptor, root,
		descriptor, tmuxPath, descriptor, root,
		missingGeneration, missingState, descriptor,
		tmuxPath, descriptor, root,
		malformedGeneration, malformedState, descriptor, malformedState,
		tmuxPath, descriptor, root,
		generation, state, descriptor, state,
		tmuxPath, descriptor, root,
		generation, state, tmuxPath, descriptor, root))

	_, code := runBashSnippet(t, script, buildEnv(t, []string{binDir}))
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	assertNotContains(t, got, "sound:")
	if count := strings.Count(got, "awake:active\n"); count != 6 {
		t.Fatalf("unknown keep-awake count = %d, want 6; log:\n%s", count, got)
	}
}

func TestTabTitleWatcher_generation_rotation_rereads_tool_for_title_theme_and_sound(t *testing.T) {
	root := t.TempDir()
	gen1 := "generation.one1"
	gen2 := "generation.two2"
	state1 := writeAttentionState(t, root, gen1, "0", "ready", "-")
	state2 := writeAttentionState(t, root, gen2, "0", "ready", "-")
	descriptor := writeAttentionDescriptor(t, root, gen1, "claude", state1)
	logFile := filepath.Join(root, "watch.log")
	writeTempFile(t, root, "settings", "theme=auto\ntab_title=full\n")

	binDir := mockCommand(t, root, "tmux", `
[ "$1" = list-panes ] && printf '%%9\t1\n'
`)
	tmuxPath := filepath.Join(binDir, "tmux")
	script := tabTitleSnippet(t, fmt.Sprintf(`
WATCH_LOG=%q
apply_tab_title() { printf 'title:%%s:%%s\n' "$1" "$4" >> "$WATCH_LOG"; }
play_notification_sound() { printf 'sound:%%s\n' "$1" >> "$WATCH_LOG"; }
keep_awake_tick() { :; }
gt_resolve_theme() { printf 'theme-%%s\n' "$2"; }
get_theme_accent() { case "$1" in theme-claude) echo 10 ;; theme-codex) echo 20 ;; esac; }
apply_session_theme() { printf 'theme:%%s\n' "$3" >> "$WATCH_LOG"; }
attention_watcher_reset
attention_watcher_tick sess-a project full %q %q %q
printf '1\t%s\tcodex\t%s\n' > %q.tmp
mv %q.tmp %q
attention_watcher_tick sess-a project full %q %q %q
printf '1\t%s\t1\tattention\tpermission\n' > %q.tmp
mv %q.tmp %q
attention_watcher_tick sess-a project full %q %q %q
`, logFile,
		tmuxPath, descriptor, root,
		gen2, state2, descriptor, descriptor, descriptor,
		tmuxPath, descriptor, root,
		gen2, state2, state2, state2,
		tmuxPath, descriptor, root))

	_, code := runBashSnippet(t, script, buildEnv(t, []string{binDir}))
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	assertContains(t, got, "theme:10")
	assertContains(t, got, "theme:20")
	assertContains(t, got, "title:active:claude")
	assertContains(t, got, "title:active:codex")
	assertContains(t, got, "title:waiting:codex")
	if count := strings.Count(got, "sound:codex\n"); count != 1 {
		t.Fatalf("codex sound count = %d, want 1; log:\n%s", count, got)
	}
	assertNotContains(t, got, "sound:claude")
}

func TestTabTitleWatcher_generation_rotation_resets_dedupe_without_alerting_initial_ready(t *testing.T) {
	root := t.TempDir()
	gen1 := "generation.old1"
	gen2 := "generation.new2"
	state1 := writeAttentionState(t, root, gen1, "7", "attention", "done")
	state2 := writeAttentionState(t, root, gen2, "0", "ready", "-")
	descriptor := writeAttentionDescriptor(t, root, gen1, "claude", state1)
	logFile := filepath.Join(root, "watch.log")
	binDir := mockCommand(t, root, "tmux", `[ "$1" = list-panes ] && printf '%%4\t1\n'`)
	tmuxPath := filepath.Join(binDir, "tmux")

	script := tabTitleSnippet(t, fmt.Sprintf(`
WATCH_LOG=%q
apply_tab_title() { :; }
play_notification_sound() { printf 'sound:%%s\n' "$1" >> "$WATCH_LOG"; }
attention_watcher_reset
attention_watcher_tick s p full %q %q %q
printf '1\t%s\tcodex\t%s\n' > %q.tmp; mv %q.tmp %q
attention_watcher_tick s p full %q %q %q
printf '1\t%s\t1\tattention\tquestion\n' > %q.tmp; mv %q.tmp %q
attention_watcher_tick s p full %q %q %q
`, logFile,
		tmuxPath, descriptor, root,
		gen2, state2, descriptor, descriptor, descriptor,
		tmuxPath, descriptor, root,
		gen2, state2, state2, state2,
		tmuxPath, descriptor, root))
	_, code := runBashSnippet(t, script, buildEnv(t, []string{binDir}))
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if count := strings.Count(got, "sound:"); count != 2 {
		t.Fatalf("sound count = %d, want old attention + new attention only; log:\n%s", count, got)
	}
	assertContains(t, got, "sound:claude")
	assertContains(t, got, "sound:codex")
}

func TestTabTitleWatcher_same_generation_sequence_regression_never_alerts(t *testing.T) {
	root := t.TempDir()
	generation := "generation.monotonic1"
	state := writeAttentionState(t, root, generation, "5", "attention", "question")
	descriptor := writeAttentionDescriptor(t, root, generation, "claude", state)
	logFile := filepath.Join(root, "watch.log")
	binDir := mockCommand(t, root, "tmux", `[ "$1" = list-panes ] && printf '%%4\t1\n'`)
	tmuxPath := filepath.Join(binDir, "tmux")

	script := tabTitleSnippet(t, fmt.Sprintf(`
WATCH_LOG=%q
apply_tab_title() { :; }
play_notification_sound() { printf 'sound:%%s\n' "$1" >> "$WATCH_LOG"; }
keep_awake_tick() { printf 'awake:%%s\n' "$4" >> "$WATCH_LOG"; }
publish_state() { printf '1\t%s\t%%s\tattention\tquestion\n' "$1" > %q.tmp; mv %q.tmp %q; }
attention_watcher_reset
attention_watcher_tick s p full %q %q %q
publish_state 4
attention_watcher_tick s p full %q %q %q
publish_state 6
attention_watcher_tick s p full %q %q %q
`, logFile, generation, state, state, state,
		tmuxPath, descriptor, root,
		tmuxPath, descriptor, root,
		tmuxPath, descriptor, root))
	_, code := runBashSnippet(t, script, buildEnv(t, []string{binDir}))
	assertExitCode(t, code, 0)
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if count := strings.Count(got, "sound:claude\n"); count != 2 {
		t.Fatalf("sound count = %d, want sequences 5 and 6 only; log:\n%s", count, got)
	}
	assertContains(t, got, "awake:active")
}

func TestTabTitleWatcher_finds_tagged_pane_after_start(t *testing.T) {
	root := t.TempDir()
	generation := "generation.late1"
	state := writeAttentionState(t, root, generation, "0", "ready", "-")
	descriptor := writeAttentionDescriptor(t, root, generation, "codex", state)
	panes := filepath.Join(root, "panes")
	calls := filepath.Join(root, "calls")
	sounds := filepath.Join(root, "sounds")
	titles := filepath.Join(root, "titles")
	binDir := mockCommand(t, root, "tmux", `
printf '%s\n' "$*" >> "$TMUX_CALLS"
case "$1" in
  list-panes) [ -f "$TMUX_PANES" ] && cat "$TMUX_PANES" ;;
  display-message) printf 'Late task title\n' ;;
  capture-pane) exit 97 ;;
esac
`)
	tmuxPath := filepath.Join(binDir, "tmux")
	env := buildEnv(t, []string{binDir},
		"TMUX_CALLS="+calls, "TMUX_PANES="+panes, "WISP_DECK_WATCH_INTERVAL=0.05")
	script := tabTitleSnippet(t, fmt.Sprintf(`
set_tab_title() { printf '%%s\n' "$*" >> %q; }
set_tab_title_waiting() { set_tab_title "🔔 $1"; }
play_notification_sound() { printf '%%s\n' "$1" >> %q; }
start_tab_title_watcher sess-late project model %q %q %q
for _i in 1 2 3 4 5 6 7 8 9 10; do [ -s %q ] && break; sleep 0.05; done
printf '%%s\t1\n' '%%42' > %q.tmp; mv %q.tmp %q
printf '1\t%s\t1\tattention\tquestion\n' > %q.tmp; mv %q.tmp %q
for _i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do [ -s %q ] && [ -s %q ] && break; sleep 0.05; done
stop_tab_title_watcher
`, titles, sounds, tmuxPath, descriptor, root,
		calls, panes, panes, panes,
		generation, state, state, state,
		sounds, titles))

	_, code := runBashSnippet(t, script, env)
	assertExitCode(t, code, 0)
	soundData, err := os.ReadFile(sounds)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(soundData)) != "codex" {
		t.Fatalf("sounds = %q, want codex", strings.TrimSpace(string(soundData)))
	}
	titleData, err := os.ReadFile(titles)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, string(titleData), "🔔 Late task title")
	callData, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, string(callData), `display-message -p -t %42 #{pane_title}`)
	assertNotContains(t, string(callData), "capture-pane")
}

func TestTabTitleWatcher_has_no_heuristic_attention_paths(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "lib", "tab-title-watcher.sh"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	for _, forbidden := range []string{
		"capture-pane", "marker_age", "claude_pane_working", "check_ai_tool_state",
		"WISP_DECK_MARKER_FILE", "-cooldown", "-ask",
	} {
		if strings.Contains(src, forbidden) {
			t.Errorf("semantic watcher still contains legacy heuristic %q", forbidden)
		}
	}
}

func TestTabTitleWatcher_wrapper_uses_semantic_watcher_api(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "wrapper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	want := `start_tab_title_watcher "$SESSION_NAME" "$PROJECT_NAME" "$_tab_title_setting" "$TMUX_CMD" "$WISP_DECK_ATTENTION_DESCRIPTOR"`
	if !strings.Contains(src, want) {
		t.Errorf("wrapper does not use semantic watcher API; want call containing %q", want)
	}
	for _, forbidden := range []string{"WISP_DECK_MARKER_FILE", "add_waiting_indicator_hooks"} {
		if strings.Contains(src, forbidden) {
			t.Errorf("wrapper still uses legacy marker hook %q", forbidden)
		}
	}
}

func TestTabTitleWatcher_stop_is_idempotent_without_marker_cleanup(t *testing.T) {
	out, code := runBashSnippet(t, tabTitleSnippet(t,
		`_TAB_TITLE_WATCHER_PID=""; stop_tab_title_watcher; stop_tab_title_watcher`), nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("idempotent stop wrote output: %q", out)
	}
}

func TestTabTitleWatcher_wrapper_disables_tmux_set_titles(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "wrapper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `set-option set-titles off`) {
		t.Error("wrapper.sh must disable tmux set-titles so tmux cannot overwrite the watcher title")
	}
}

// waitForFile polls for populated background-job artifacts and is shared by
// this package. Shell redirection creates an empty file before the command
// writes its output, so existence alone is not enough synchronization.
func waitForFile(t *testing.T, path, msg string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			return string(data)
		}
		if time.Now().After(deadline) {
			if err != nil {
				t.Fatalf("%s: %s never appeared: %v", msg, path, err)
			}
			t.Fatalf("%s: %s remained empty", msg, path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitForFileGone is the mirror helper for asynchronous removal.
func waitForFileGone(t *testing.T, path, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: %s is still there", msg, path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestTabTitleWatcher_apply_tab_title_modes(t *testing.T) {
	tests := []struct {
		name, state, mode, want, unwanted string
	}{
		{"full active", "active", "full", "myproj · claude", "🔔"},
		{"full waiting", "waiting", "full", "🔔 myproj · claude", "●"},
		{"project active", "active", "project", "myproj", "claude"},
		{"project waiting", "waiting", "project", "🔔 myproj", "claude"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, code := runBashSnippet(t, tabTitleSnippet(t,
				fmt.Sprintf(`apply_tab_title %q %q myproj claude`, tt.state, tt.mode)), nil)
			assertExitCode(t, code, 0)
			assertContains(t, out, tt.want)
			assertNotContains(t, out, tt.unwanted)
		})
	}
	for _, state := range []string{"active", "waiting"} {
		out, code := runBashSnippet(t, tabTitleSnippet(t,
			fmt.Sprintf(`apply_tab_title %q model myproj claude`, state)), nil)
		assertExitCode(t, code, 0)
		if strings.TrimSpace(out) != "" {
			t.Errorf("model/%s wrote %q", state, out)
		}
	}
}

// In model mode the per-tick re-emit mirrors the AI tool's own pane title into
// the tab; during the attention phase it must carry the bell prefix, and drop
// it once the agent is working again.
func TestTabTitleWatcher_model_mode_tick_prepends_bell_on_attention(t *testing.T) {
	tests := []struct{ name, phase, reason, want string }{
		{"attention rings", "attention", "question", "title:🔔 Fixing tests"},
		{"working is plain", "working", "-", "title:Fixing tests"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			generation := "generation.modelbell1"
			state := writeAttentionState(t, root, generation, "1", tt.phase, tt.reason)
			descriptor := writeAttentionDescriptor(t, root, generation, "claude", state)
			binDir := mockCommand(t, root, "tmux", `
case "$1" in
  list-panes) printf '%%7\t1\n' ;;
  display-message) printf 'Fixing tests\n' ;;
esac
`)
			tmuxPath := filepath.Join(binDir, "tmux")
			logFile := filepath.Join(root, "titles")
			script := tabTitleSnippet(t, fmt.Sprintf(`
set_tab_title() { printf 'title:%%s\n' "$1" >> %q; }
attention_watcher_reset
attention_watcher_tick s p model %q %q %q
`, logFile, tmuxPath, descriptor, root))

			_, code := runBashSnippet(t, script, buildEnv(t, []string{binDir}))
			assertExitCode(t, code, 0)
			data, err := os.ReadFile(logFile)
			if err != nil {
				t.Fatal(err)
			}
			got := string(data)
			assertContains(t, got, tt.want+"\n")
			if tt.phase == "working" {
				assertNotContains(t, got, "🔔")
			}
		})
	}
}

func TestTabTitleWatcher_model_tab_title(t *testing.T) {
	tests := []struct{ pane, host, project, want string }{
		{"Refactoring auth", "host.local", "blok", "Refactoring auth"},
		{"host.local", "host.local", "blok", "blok"},
		{"", "host.local", "blok", "blok"},
	}
	for _, tt := range tests {
		out, code := runBashSnippet(t, tabTitleSnippet(t,
			fmt.Sprintf(`model_tab_title %q %q %q`, tt.pane, tt.host, tt.project)), nil)
		assertExitCode(t, code, 0)
		if strings.TrimSpace(out) != tt.want {
			t.Errorf("model title = %q, want %q", strings.TrimSpace(out), tt.want)
		}
	}
}

func TestTabTitleWatcher_background_loop_never_writes_to_session_terminal(t *testing.T) {
	root := t.TempDir()
	generation := "generation.quiet1"
	state := writeAttentionState(t, root, generation, "0", "working", "-")
	descriptor := writeAttentionDescriptor(t, root, generation, "claude", state)
	binDir := mockCommand(t, root, "tmux", `
echo 'tmux noise' >&2
[ "$1" = list-panes ] && printf '%%1\t1\n'
`)
	env := buildEnv(t, []string{binDir}, "WISP_DECK_WATCH_INTERVAL=0.05")
	script := tabTitleSnippet(t, fmt.Sprintf(`
keep_awake_tick() { echo 'WATCHER-NOISE' >&2; }
set_tab_title() { :; }
set_tab_title_waiting() { :; }
start_tab_title_watcher sess project project %q %q %q >/dev/null
sleep 0.2
stop_tab_title_watcher
`, filepath.Join(binDir, "tmux"), descriptor, root))
	out, _ := runBashSnippet(t, script, env)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("watcher leaked onto session terminal:\n%s", out)
	}
}
