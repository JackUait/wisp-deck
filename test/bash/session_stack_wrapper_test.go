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

// The server-wide `exit-unattached on` kills every session the moment no
// client is attached anywhere — fatal for background stack sessions. The
// orphan reaper replaced it, but merely not-setting it was not enough: a tmux
// server started by a pre-stacking wrapper keeps the fossil `on` for its whole
// lifetime. Every launch must therefore write the `off` explicitly.
// (lib/spare-tabs.sh's inner-server `set -g exit-unattached on` is fine.)
func TestWrapper_heals_exit_unattached_off(t *testing.T) {
	src := wrapperSource(t)
	if strings.Contains(src, "exit-unattached on") {
		t.Fatal("wrapper.sh sets exit-unattached on — incompatible with session stacking; the orphan reaper owns leak GC now")
	}
	if !strings.Contains(src, "exit-unattached off") {
		t.Fatal("wrapper.sh must set `exit-unattached off` in the launch chain: servers started by pre-stacking wrappers still carry the old `on` and would kill background stack sessions")
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

// SESSION_NAME derives from an unsanitized project-folder basename, so it may
// contain an apostrophe or other shell metacharacter. Interpolating the raw
// #{session_name} format INSIDE a single-quoted `bash -c '...'` body lets
// such a character terminate the quote early and corrupt the bind. The `q:`
// format modifier backslash-escapes shell-special characters for exactly this
// case, so the dynamic session name must ride outside the quoted body as
// #{q:session_name} (a positional arg), never as a bare #{session_name}
// baked into the quoted body.
func TestWrapper_binds_use_session_format_not_baked_names(t *testing.T) {
	src := wrapperSource(t)
	for _, fn := range []string{"stack_cycle", "stack_close_current", "--stack-new"} {
		re := regexp.MustCompile(fn + `[^\n]*#\{q:session_name\}`)
		if !re.MatchString(src) {
			t.Fatalf("wrapper.sh bind for %s must pass #{q:session_name} (bind-key is server-global and SESSION_NAME is unsanitized; a baked or unescaped name acts on the wrong session or breaks the quoting)", fn)
		}
	}
	if regexp.MustCompile(`#\{session_name\}`).MatchString(src) {
		t.Fatal("wrapper.sh uses bare #{session_name} (unescaped) — SESSION_NAME may contain shell metacharacters (e.g. an apostrophe from the project folder name); use #{q:session_name} instead")
	}
}

// The Cmd+T request/claim dance is gone: prefix+S builds the new session
// IN-PLACE (wrapper.sh --stack-new) inside the current tab's stack. The old
// flow detached pre-stacking wrappers' clients, whose in-memory cleanup then
// killed their own just-adopted sessions.
func TestWrapper_stack_new_is_inplace_not_request_claim(t *testing.T) {
	src := wrapperSource(t)
	for _, gone := range []string{"stack_request_new", "stack_request_claim"} {
		if strings.Contains(src, gone) {
			t.Fatalf("wrapper.sh still references %s — prefix+S must build in-place via --stack-new, not spawn a Ghostty tab", gone)
		}
	}
	if !strings.Contains(src, "--stack-new") {
		t.Fatal("wrapper.sh must implement the --stack-new in-place builder mode")
	}
}

func TestWrapper_opens_into_existing_tab_only_on_interactive_picks(t *testing.T) {
	src := wrapperSource(t)
	// Picking an already-open project builds INTO the existing tab's stack
	// (via the live-owner lookup, which filters out pre-stacking sessions
	// lacking the owner-pid stamp) instead of adopting-and-closing tabs.
	if !strings.Contains(src, "stack_live_owner_for_project") {
		t.Fatal("wrapper.sh must detect the existing tab via stack_live_owner_for_project")
	}
	// The detection must be gated on the consolidation flag so restored/arg
	// launches always get their own tab (restore chain = one tab per entry).
	idx := strings.Index(src, "stack_live_owner_for_project")
	region := src[max(0, idx-900):idx]
	if !strings.Contains(region, "_gt_consolidate") || !strings.Contains(region, "RESTORE_MODE") {
		t.Fatal("live-owner detection must be gated on _gt_consolidate=1 and RESTORE_MODE=0")
	}
}

// Opening the same project must never close the tabs the user already has:
// the adoption handoff (new tab adopts old sessions, finalizer detaches the
// old tabs' clients) is gone.
func TestWrapper_never_adopts_or_detaches_existing_tabs(t *testing.T) {
	src := wrapperSource(t)
	for _, gone := range []string{"stack_adopt_all", "stack_finalize_adoption", "detach-client"} {
		if strings.Contains(src, gone) {
			t.Fatalf("wrapper.sh still references %s — opening an already-open project must add a session to the existing tab's stack, never close existing tabs", gone)
		}
	}
}

func TestWrapper_cleanup_is_stack_aware(t *testing.T) {
	src := wrapperSource(t)
	cleanupStart := strings.Index(src, "cleanup() {")
	if cleanupStart < 0 {
		t.Fatal("cleanup() not found")
	}
	cleanupBody := src[cleanupStart : cleanupStart+strings.Index(src[cleanupStart:], "\n}")]
	for _, fn := range []string{"stack_adopted_away", "stack_owner_teardown"} {
		if !strings.Contains(cleanupBody, fn) {
			t.Fatalf("cleanup() must call %s", fn)
		}
	}
}

// The session bar is always visible (it hosts the + button), so the launch
// chain must write a session-level `status on` — the user's own ~/.tmux.conf
// may hide the status bar globally.
func TestWrapper_launch_chain_forces_status_bar_on(t *testing.T) {
	src := wrapperSource(t)
	if !strings.Contains(src, "set-option status on") {
		t.Fatal("wrapper.sh's launch chain must set the session-level `status on` (a global `status off` in the user's ~/.tmux.conf would hide the session bar and its + button)")
	}
}

// Clicking the bar's + button opens a new session in the same project via the
// --stack-new in-place builder. The bind rides MouseDown1Status and must
// gate itself on the wisp-stack-new status range (other status clicks stay
// inert) using expanded-at-press formats, never baked names.
func TestWrapper_binds_status_plus_click_to_stack_new(t *testing.T) {
	src := wrapperSource(t)
	if !strings.Contains(src, "MouseDown1Status") {
		t.Fatal("wrapper.sh must bind MouseDown1Status for the session bar's + button")
	}
	idx := strings.Index(src, "_stack_plus_bind=")
	if idx < 0 {
		t.Fatal("wrapper.sh must define _stack_plus_bind for the + button click")
	}
	line := src[idx : idx+strings.Index(src[idx:], "\n")]
	for _, want := range []string{"#{q:mouse_status_range}", "#{q:session_name}", "#{q:client_name}", "--stack-new", "wisp-stack-new"} {
		if !strings.Contains(line, want) {
			t.Fatalf("_stack_plus_bind must contain %s (range-gated, press-time-expanded --stack-new); got: %s", want, line)
		}
	}
}

func TestWrapper_registers_own_session_in_own_stack_file(t *testing.T) {
	src := wrapperSource(t)
	if !regexp.MustCompile(`stack_add "\$SHARE_DIR" "\$SESSION_NAME" "\$SESSION_NAME"`).MatchString(src) {
		t.Fatal("wrapper.sh must register its own session in its stack file")
	}
}
