package bash_test

import (
	"os"
	"strings"
	"testing"
)

// A self-hosted profile written before the byte watchdog was disarmed is never
// re-copied from defaults, so the installer's sweep is the only thing that can
// repair it. Without this line the fix reaches new profiles only.
func TestInstaller_sweeps_the_byte_watchdog_on_existing_profiles(t *testing.T) {
	data, err := os.ReadFile("../../bin/wisp-deck")
	if err != nil {
		t.Fatalf("read installer: %v", err)
	}
	script := string(data)
	if !strings.Contains(script, "claude-config ensure-watchdog --dir") {
		t.Error("bin/wisp-deck never runs `claude-config ensure-watchdog`")
	}
	if !strings.Contains(script, "claude-config ensure-budget --dir") {
		t.Error("bin/wisp-deck stopped running `claude-config ensure-budget`")
	}
}
