package bash_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func wrapperSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(projectRoot(t), "wrapper.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The server-wide exit-unattached option kills unattached stacked sessions.
// The orphan reaper replaced it; it must never come back to wrapper.sh.
// (lib/spare-tabs.sh's inner-server `set -g exit-unattached on` is fine.)
func TestWrapper_never_sets_server_exit_unattached(t *testing.T) {
	if strings.Contains(wrapperSource(t), "exit-unattached") {
		t.Fatal("wrapper.sh sets exit-unattached — incompatible with session stacking; the orphan reaper owns leak GC now")
	}
}

func TestWrapper_stamps_owner_pid_in_session_env(t *testing.T) {
	if !strings.Contains(wrapperSource(t), `WISP_DECK_OWNER_PID=$$`) {
		t.Fatal("wrapper.sh must stamp WISP_DECK_OWNER_PID into the session env (orphan reaper contract)")
	}
}

func TestWrapper_loads_session_stack_lib_and_starts_reaper(t *testing.T) {
	src := wrapperSource(t)
	if !strings.Contains(src, "session-stack") {
		t.Fatal("wrapper.sh must source lib/session-stack.sh")
	}
	if !strings.Contains(src, "stack_reaper_watch") {
		t.Fatal("wrapper.sh must background stack_reaper_watch")
	}
}

func TestWrapper_binds_use_session_format_not_baked_names(t *testing.T) {
	src := wrapperSource(t)
	for _, fn := range []string{"stack_cycle", "stack_close_current", "stack_request_new"} {
		re := regexp.MustCompile(fn + `[^\n]*#\{session_name\}`)
		if !re.MatchString(src) {
			t.Fatalf("wrapper.sh bind for %s must pass #{session_name} (bind-key is server-global; a baked name acts on the wrong session)", fn)
		}
	}
}
