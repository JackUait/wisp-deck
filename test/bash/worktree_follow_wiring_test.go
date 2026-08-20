package bash_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The follow only happens if the launch actually hands the watcher the two
// paths it needs. Without them attention_watcher_follow_agent is a silent
// no-op by design, so the feature would ship dead and every behavioral test
// above would still pass.
func TestWrapperStartsTheWatcherWithTheInputsItNeedsToFollowAWorktree(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "wrapper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	call := regexp.MustCompile(`start_tab_title_watcher [^\n]*`).Find(data)
	if call == nil {
		t.Fatal("wrapper.sh never starts the tab title watcher")
	}
	for _, want := range []string{"$WISP_DECK_RELAUNCH_FILE", "$_WRAPPER_DIR/lib"} {
		if !regexp.MustCompile(regexp.QuoteMeta(want)).Match(call) {
			t.Fatalf("watcher launch is missing %s, so it can never follow the agent:\n%s", want, call)
		}
	}
}
