package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The child is launched against a live local router: proving the rewritten
// URL is already accepting connections is what rules out a first-turn race
// against a router that has not started serving yet.
//
// Two things pin the shape of the probe. The overlay must name a NON-local
// endpoint — UpstreamFromSettings refuses 127.0.0.1/localhost/::1 as "already
// local", so pointing the fixture at an httptest server makes the wrapper fall
// through and start no router at all (verified: the child then still sees the
// fixture URL). And the probe must be one the router answers ITSELF: a request
// with no routable model is KindSession, which the router forwards to the
// overlay's own upstream — the earlier GET /healthz really did reach
// api.anthropic.com, which answered 404 through cloudflare. A cfg. row naming a
// profile that does not exist is refused locally, so nothing leaves the process
// and the 5s deadline covers loopback only. An acct. row would not do: an empty
// accounts dir sends keychainToken at the developer's real Keychain.
func TestClaudeAllIn_points_the_overlay_at_the_local_router(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(settings,
		[]byte(`{"env":{"ANTHROPIC_BASE_URL":"https://api.anthropic.com"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var seen string
	command := newClaudeAllInCommand(func([]string) error {
		data, _ := os.ReadFile(settings)
		var parsed struct {
			Env map[string]string `json:"env"`
		}
		_ = json.Unmarshal(data, &parsed)
		seen = parsed.Env["ANTHROPIC_BASE_URL"]
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Post(seen+"/v1/messages", "application/json",
			strings.NewReader(`{"model":"wisp/cfg.absent/x"}`))
		if err != nil {
			t.Errorf("router is not listening when the child starts: %v", err)
			return nil
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "absent") {
			t.Errorf("the router did not answer the probe itself: status %d, body %s",
				resp.StatusCode, body)
		}
		return nil
	})
	command.SetArgs([]string{"--settings", settings,
		"--configs-dir", filepath.Join(dir, "configs"), "--", "true"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !hasLoopbackPrefix(seen) {
		t.Fatalf("child saw %q, not a loopback router", seen)
	}
}

func TestClaudeAllIn_runs_the_child_when_the_overlay_cannot_be_read(t *testing.T) {
	ran := false
	command := newClaudeAllInCommand(func([]string) error { ran = true; return nil })
	command.SetArgs([]string{"--settings", filepath.Join(t.TempDir(), "absent.json"), "--", "true"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("child never ran")
	}
}

// Resolve never reads DefaultLabelFile — the login's tag is display-only, and
// this command only addresses credentials — but the flag is still wired here
// so this Env is shaped the same as every other construction site
// (ensure-allin, the TUI's own mutations). This just proves the flag exists
// and does not break the launch.
func TestClaudeAllIn_accepts_a_default_label_file_flag(t *testing.T) {
	ran := false
	command := newClaudeAllInCommand(func([]string) error { ran = true; return nil })
	command.SetArgs([]string{"--settings", filepath.Join(t.TempDir(), "absent.json"),
		"--default-label-file", filepath.Join(t.TempDir(), "claude-account-default-label"),
		"--", "true"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("child never ran")
	}
}

func hasLoopbackPrefix(url string) bool {
	return len(url) > 17 && url[:17] == "http://127.0.0.1:"
}

// Opening a pane reaches no usage endpoint. The round this replaces fanned out
// to every login and every enabled profile, so a deck spent a request on
// subscriptions the session never routes to — and read every login's Keychain
// entry, refreshing (and rotating) the OAuth token of logins nobody had opened
// a pane on. Quota numbers are refreshed by account-usage and
// subscription-usage instead, which the user runs when they want them.
//
// The two account caches are pre-written fresh so the throttle, not the
// network, is what keeps a RED run off api.anthropic.com: only the Zhipu stub
// can be reached, and reaching it is the failure.
func TestClaudeAllIn_fetches_no_usage_at_launch(t *testing.T) {
	hits := make(chan string, 8)
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits <- r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer stub.Close()

	root := t.TempDir()
	configsDir := filepath.Join(root, "claude-configs")
	for _, dir := range []string{configsDir, filepath.Join(root, "account-usage")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "claude-accounts.list"), "Personal:personal\n")
	write(filepath.Join(root, "claude-configs.list"), "Zhipu GLM:zhipu-glm.json\n")
	write(filepath.Join(configsDir, "zhipu-glm.json"), fmt.Sprintf(
		`{"env":{"ANTHROPIC_BASE_URL":%q,"ANTHROPIC_AUTH_TOKEN":"k"}}`, stub.URL+"/api/anthropic"))
	fresh := fmt.Sprintf(`{"provider":"claude","fetched_at":%d,"checked_at":%d}`,
		time.Now().Unix(), time.Now().Unix())
	for _, login := range []string{"default", "personal"} {
		write(filepath.Join(root, "account-usage", login+".json"), fresh)
	}

	command := newClaudeAllInCommand(func([]string) error { return nil })
	command.SetArgs([]string{
		"--settings", filepath.Join(root, "absent.json"),
		"--accounts-list", filepath.Join(root, "claude-accounts.list"),
		"--accounts-dir", filepath.Join(root, "claude-accounts"),
		"--configs-list", filepath.Join(root, "claude-configs.list"),
		"--configs-dir", configsDir,
		"--", "true"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}

	select {
	case path := <-hits:
		t.Fatalf("the launch fetched usage from a subscription the session never picked: %s", path)
	case <-time.After(2 * time.Second):
	}
}
