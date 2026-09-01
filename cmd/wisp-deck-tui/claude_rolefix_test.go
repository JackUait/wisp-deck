package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func rolefixSettings(t *testing.T, endpoint string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "overlay.json")
	body := `{"env":{"ANTHROPIC_BASE_URL":"` + endpoint + `","ANTHROPIC_AUTH_TOKEN":"rc_secret"}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func endpointIn(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	return settings.Env["ANTHROPIC_BASE_URL"]
}

// The child is launched against a live local proxy, and the proxy reaches the
// endpoint the profile named.
func TestClaudeRolefix_points_the_overlay_at_a_live_proxy(t *testing.T) {
	settings := rolefixSettings(t, "https://api.featherless.ai")

	var sawEndpoint string
	var ran bool
	command := newClaudeRolefixCommand(func(argv []string) error {
		ran = true
		sawEndpoint = endpointIn(t, settings)
		// The proxy must already be serving by the time the child starts, or
		// the first turn races it and fails with a connection refused.
		resp, err := http.Get(sawEndpoint + "/healthz")
		if err != nil {
			t.Errorf("proxy is not listening when the child starts: %v", err)
			return nil
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	})
	command.SetArgs([]string{"--settings", settings, "--", "claude", "--dangerously-skip-permissions"})
	if err := command.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !ran {
		t.Fatal("the child never ran")
	}
	if !strings.HasPrefix(sawEndpoint, "http://127.0.0.1:") {
		t.Errorf("child saw endpoint %q, want a loopback proxy", sawEndpoint)
	}
}

// A launch must never be broken by this. With nothing to proxy the child still
// runs, against exactly the endpoint it was already given.
func TestClaudeRolefix_runs_the_child_when_there_is_nothing_to_proxy(t *testing.T) {
	settings := rolefixSettings(t, "")

	ran := false
	command := newClaudeRolefixCommand(func(argv []string) error {
		ran = true
		if got := endpointIn(t, settings); got != "" {
			t.Errorf("endpoint = %q, want it untouched", got)
		}
		return nil
	})
	command.SetArgs([]string{"--settings", settings, "--", "claude"})
	if err := command.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !ran {
		t.Error("the child must run even with nothing to proxy")
	}
}

// A missing settings file is not a reason to lose the session either.
func TestClaudeRolefix_runs_the_child_when_the_overlay_is_absent(t *testing.T) {
	ran := false
	command := newClaudeRolefixCommand(func(argv []string) error {
		ran = true
		return nil
	})
	command.SetArgs([]string{"--settings", filepath.Join(t.TempDir(), "absent.json"), "--", "claude"})
	if err := command.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !ran {
		t.Error("the child must run when the overlay cannot be read")
	}
}

// The child's argv reaches it verbatim: the launch chain built that command and
// this adapter has no business editing it.
func TestClaudeRolefix_passes_the_child_argv_through(t *testing.T) {
	settings := rolefixSettings(t, "https://api.featherless.ai")

	var got []string
	command := newClaudeRolefixCommand(func(argv []string) error {
		got = append([]string(nil), argv...)
		return nil
	})
	command.SetArgs([]string{"--settings", settings, "--", "bash", "-c", "claude --settings x"})
	if err := command.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := []string{"bash", "-c", "claude --settings x"}
	if len(got) != len(want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv = %v, want %v", got, want)
		}
	}
}
