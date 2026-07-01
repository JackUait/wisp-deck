package bash_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// The auto-switch setting turns on the account-rotation proxy. It is stored as a
// single-value flag file (on/off) and is only eligible when the account list has
// at least two accounts to rotate between.

func TestAutoSwitch_defaults_off(t *testing.T) {
	dir := t.TempDir()
	out, code := runBashFunc(t, "lib/auto-switch.sh", "get_auto_switch",
		[]string{filepath.Join(dir, "auto-switch-accounts")}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "off" {
		t.Fatalf("default = %q, want off", strings.TrimSpace(out))
	}
}

func TestAutoSwitch_set_and_get(t *testing.T) {
	dir := t.TempDir()
	flag := filepath.Join(dir, "auto-switch-accounts")
	if _, code := runBashFunc(t, "lib/auto-switch.sh", "set_auto_switch", []string{flag, "on"}, nil); code != 0 {
		t.Fatal("set on failed")
	}
	out, _ := runBashFunc(t, "lib/auto-switch.sh", "get_auto_switch", []string{flag}, nil)
	if strings.TrimSpace(out) != "on" {
		t.Fatalf("got %q, want on", strings.TrimSpace(out))
	}
	// is_auto_switch_enabled returns 0 when on.
	if _, code := runBashFunc(t, "lib/auto-switch.sh", "is_auto_switch_enabled", []string{flag}, nil); code != 0 {
		t.Fatal("is_auto_switch_enabled should exit 0 when on")
	}
}

func TestAutoSwitch_invalid_value_normalizes_to_off(t *testing.T) {
	dir := t.TempDir()
	flag := filepath.Join(dir, "auto-switch-accounts")
	if _, code := runBashFunc(t, "lib/auto-switch.sh", "set_auto_switch", []string{flag, "garbage"}, nil); code != 0 {
		t.Fatal("set failed")
	}
	out, _ := runBashFunc(t, "lib/auto-switch.sh", "get_auto_switch", []string{flag}, nil)
	if strings.TrimSpace(out) != "off" {
		t.Fatalf("invalid value should read as off, got %q", strings.TrimSpace(out))
	}
}

func TestProxyStartupParse_extracts_port_and_key(t *testing.T) {
	line := `{"port":54321,"key":"wd-abc123"}`
	out, code := runBashFunc(t, "lib/auto-switch.sh", "proxy_startup_port", []string{line}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "54321" {
		t.Errorf("port = %q, want 54321", strings.TrimSpace(out))
	}
	out, code = runBashFunc(t, "lib/auto-switch.sh", "proxy_startup_key", []string{line}, nil)
	assertExitCode(t, code, 0)
	if strings.TrimSpace(out) != "wd-abc123" {
		t.Errorf("key = %q, want wd-abc123", strings.TrimSpace(out))
	}
}

func TestProxyStartupParse_empty_on_garbage(t *testing.T) {
	out, _ := runBashFunc(t, "lib/auto-switch.sh", "proxy_startup_port", []string{"not json"}, nil)
	if strings.TrimSpace(out) != "" {
		t.Errorf("garbage should yield empty port, got %q", strings.TrimSpace(out))
	}
}

func TestAutoSwitchEligible_needs_two_accounts(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "claude-accounts.list")

	// Zero accounts → not eligible.
	writeTempFile(t, dir, "claude-accounts.list", "# just a comment\n\n")
	if _, code := runBashFunc(t, "lib/auto-switch.sh", "auto_switch_eligible", []string{list}, nil); code == 0 {
		t.Fatal("0 accounts should not be eligible")
	}

	// One account → not eligible (nothing to rotate to).
	writeTempFile(t, dir, "claude-accounts.list", "Work:work\n")
	if _, code := runBashFunc(t, "lib/auto-switch.sh", "auto_switch_eligible", []string{list}, nil); code == 0 {
		t.Fatal("1 account should not be eligible")
	}

	// Two accounts → eligible.
	writeTempFile(t, dir, "claude-accounts.list", "Work:work\nPersonal:personal\n")
	if _, code := runBashFunc(t, "lib/auto-switch.sh", "auto_switch_eligible", []string{list}, nil); code != 0 {
		t.Fatal("2 accounts should be eligible")
	}
}
