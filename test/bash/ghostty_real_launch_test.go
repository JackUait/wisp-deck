package bash_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// This file is the durable backstop for the "Ghostty fails to launch the
// wrapper" bug class. Multiple prior fixes in this repo's history each looked
// correct by string inspection or by reading Ghostty's own doc comments, but
// still left users broken (or added dead code fixing a non-problem), because
// nobody had actually launched a real Ghostty process to observe the truth:
//   - "bare paths are broken on 1.2.x" was false — only "~" paths are, because
//     the exec'd shell never expands them.
//   - "Ghostty also reads ~/Library/Application Support/…/config" was false —
//     confirmed both by Ghostty's own --docs output ("the default
//     configuration file paths are currently only the XDG config path") and by
//     launching real Ghostty with a WORKING command placed only there: it did
//     not launch.
//
// TestGhosttyRealLaunch_repairs_every_historical_broken_form closes the gap
// for good: for every command form legacy or current wisp-deck installers are
// known to have written, it runs the actual repair code path and then
// launches the REAL Ghostty binary and waits for the wrapper to prove it ran.
// No assumption about Ghostty's behavior is trusted — it is observed
// directly, the same way the real root causes were finally found.

const ghosttyBinaryPath = "/Applications/Ghostty.app/Contents/MacOS/ghostty"

// realLaunchOptedIn reports whether the developer explicitly asked for the
// real-launch tests by setting WISP_DECK_REAL_LAUNCH=1. Exactly "1" — the
// launches are a deliberate pre-release action, not something a stray
// truthy-looking value should trigger.
func realLaunchOptedIn() bool {
	return os.Getenv("WISP_DECK_REAL_LAUNCH") == "1"
}

// requireRealGhostty skips the test unless it was explicitly opted into with
// WISP_DECK_REAL_LAUNCH=1 AND Ghostty is installed. The opt-in gate exists
// because each real launch starts a separate visible Ghostty instance whose
// window flashes open and closed on the developer's screen — running that as
// a side effect of every `./run-tests.sh` invocation is the "phantom Ghostty
// opening and closing tabs" incident. Run before releases with:
//
//	WISP_DECK_REAL_LAUNCH=1 go test ./test/bash/... -run TestGhosttyRealLaunch -v
func requireRealGhostty(t *testing.T) {
	t.Helper()
	if !realLaunchOptedIn() {
		t.Skip("real-launch verification is opt-in — set WISP_DECK_REAL_LAUNCH=1 to run (opens visible Ghostty windows)")
	}
	if _, err := os.Stat(ghosttyBinaryPath); err != nil {
		t.Skip("Ghostty.app not installed — skipping real-launch verification")
	}
}

// launchRealGhosttyAndAwaitMarker starts a real, isolated Ghostty instance
// (HOME repointed at sandboxHome, XDG_CONFIG_HOME cleared so Ghostty resolves
// ~/.config against the sandbox HOME exactly as it would for a real user) and
// polls for markerPath to appear, proving the configured command actually
// launched the wrapper. Always terminates the process it started.
func launchRealGhosttyAndAwaitMarker(t *testing.T, sandboxHome, markerPath string, timeout time.Duration) bool {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout+2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, ghosttyBinaryPath, "--quit-after-last-window-closed=true")
	cmd.Env = append(os.Environ(), "HOME="+sandboxHome)
	filtered := cmd.Env[:0]
	for _, kv := range cmd.Env {
		if len(kv) >= len("XDG_CONFIG_HOME=") && kv[:len("XDG_CONFIG_HOME=")] == "XDG_CONFIG_HOME=" {
			continue
		}
		filtered = append(filtered, kv)
	}
	cmd.Env = filtered

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start real Ghostty: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(markerPath); err == nil {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// writeMarkerWrapper writes an executable script at wrapperPath that touches
// markerPath then sleeps, so a real Ghostty launch can be observed.
func writeMarkerWrapper(t *testing.T, wrapperPath, markerPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(wrapperPath), 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	script := "#!/bin/bash\necho ran > " + markerPath + "\nsleep 20\n"
	if err := os.WriteFile(wrapperPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write wrapper: %v", err)
	}
}

func TestGhosttyRealLaunch_repairs_every_historical_broken_form(t *testing.T) {
	requireRealGhostty(t)

	root := projectRoot(t)
	tuiPath := filepath.Join(root, "lib", "tui.sh")
	installPath := filepath.Join(root, "lib", "install.sh")
	adapterPath := filepath.Join(root, "lib", "terminals", "ghostty.sh")

	// Every command form a wisp-deck installer (current or historical) is
	// known to have written into a Ghostty config, per git archaeology.
	brokenForms := []string{
		"command = ~/.config/wisp-deck/wrapper.sh",
		"command = ~/.config/ghost-tab/wrapper.sh",
		"command = ~/.config/vibecode-editor/wrapper.sh",
		"command = ~/.config/ghostty/claude-wrapper.sh",
		"command = /bin/bash -l ~/.config/ghost-tab/wrapper.sh",
	}

	for _, form := range brokenForms {
		form := form
		t.Run(form, func(t *testing.T) {
			sandboxHome := t.TempDir()
			wrapperPath := filepath.Join(sandboxHome, ".config/wisp-deck/wrapper.sh")
			markerPath := filepath.Join(sandboxHome, "MARKER")
			writeMarkerWrapper(t, wrapperPath, markerPath)

			cfgPath := filepath.Join(sandboxHome, ".config/ghostty/config")
			if err := os.MkdirAll(filepath.Dir(cfgPath), 0755); err != nil {
				t.Fatalf("mkdir failed: %v", err)
			}
			if err := os.WriteFile(cfgPath, []byte(form+"\n"), 0644); err != nil {
				t.Fatalf("failed to write broken config: %v", err)
			}

			// Run the installer's actual repair path exactly as bin/wisp-deck
			// invokes it. Choice "2" (Skip) is deliberate: terminal_apply_config
			// only repairs a pre-existing command line regardless of choice
			// when detection recognizes it as ours; passing Skip means a
			// detection regression (failing to recognize a legacy form) is NOT
			// masked by an unconditional Merge — it would leave the config
			// broken and this test would correctly fail.
			script := fmt.Sprintf(`
source %q && source %q && source %q
TERMINAL_CONFIG="$(terminal_get_config_path)"
terminal_apply_config "$TERMINAL_CONFIG" %q 2
`, tuiPath, installPath, adapterPath, wrapperPath)

			env := buildEnv(t, nil, "HOME="+sandboxHome)
			out, code := runBashSnippet(t, script, env)
			if code != 0 {
				t.Fatalf("repair script failed (exit %d): %s", code, out)
			}

			if !launchRealGhosttyAndAwaitMarker(t, sandboxHome, markerPath, 6*time.Second) {
				data, _ := os.ReadFile(cfgPath)
				t.Fatalf("real Ghostty did not launch the wrapper after repair\nform: %q\nresulting config:\n%s", form, data)
			}
		})
	}
}

// TestGhosttyRealLaunch_application_support_path_is_not_read_by_default is the
// real-binary evidence behind the correction in
// TestGhosttyAdapter_config_path_is_the_only_default_location
// (terminal_ghostty_test.go): a prior fix wrongly assumed Ghostty also reads
// ~/Library/Application Support/com.mitchellh.ghostty/config and added dead
// repair logic for it. This places a WORKING wisp-deck command ONLY at that
// path (nothing at ~/.config/ghostty/config) and confirms the wrapper does
// NOT launch — proving that path is not part of Ghostty's default config
// resolution. If a future Ghostty version starts reading it, this test will
// start failing (the marker will appear), which is the correct signal to
// revisit that assumption.
func TestGhosttyRealLaunch_application_support_path_is_not_read_by_default(t *testing.T) {
	requireRealGhostty(t)

	sandboxHome := t.TempDir()
	wrapperPath := filepath.Join(sandboxHome, ".config/wisp-deck/wrapper.sh")
	markerPath := filepath.Join(sandboxHome, "MARKER")
	writeMarkerWrapper(t, wrapperPath, markerPath)

	appSupportCfg := filepath.Join(sandboxHome, "Library/Application Support/com.mitchellh.ghostty/config")
	if err := os.MkdirAll(filepath.Dir(appSupportCfg), 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(appSupportCfg, []byte("command = "+wrapperPath+"\n"), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	if launchRealGhosttyAndAwaitMarker(t, sandboxHome, markerPath, 5*time.Second) {
		t.Fatal("wrapper launched from Application Support config alone — Ghostty now reads that path; update terminal_get_config_path and its repair logic accordingly")
	}
}

// The real-launch tests visibly hijack the developer's machine: each one
// starts a real, separate Ghostty instance whose window flashes open and
// closed — the "phantom Ghostty with tabs opening and closing" during normal
// `./run-tests.sh` runs. They must therefore be explicitly opted into via
// WISP_DECK_REAL_LAUNCH=1 (set by a human before a release), never run as a
// side effect of the default suite.
func TestRealLaunchOptedIn_requires_explicit_env_flag(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"0", false},
		{"true", false},
		{"1", true},
	}
	for _, c := range cases {
		t.Setenv("WISP_DECK_REAL_LAUNCH", c.value)
		if got := realLaunchOptedIn(); got != c.want {
			t.Errorf("WISP_DECK_REAL_LAUNCH=%q: realLaunchOptedIn() = %v, want %v", c.value, got, c.want)
		}
	}
}
