package rolefix

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// UpstreamFromSettings returns the endpoint a launch overlay points at, or an
// error when there is nothing to proxy. A loopback endpoint is already a bridge
// of some kind, so proxying it again would stack two hops for no reason.
func UpstreamFromSettings(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return "", fmt.Errorf("rolefix: parse settings: %w", err)
	}
	endpoint := strings.TrimSpace(settings.Env["ANTHROPIC_BASE_URL"])
	if endpoint == "" {
		return "", fmt.Errorf("rolefix: settings declare no endpoint")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", fmt.Errorf("rolefix: endpoint %q is not an http(s) URL", endpoint)
	}
	if host := parsed.Hostname(); host == "127.0.0.1" || host == "localhost" || host == "::1" {
		return "", fmt.Errorf("rolefix: endpoint %q is already local", endpoint)
	}
	return endpoint, nil
}

// PointSettingsAt rewrites the launch overlay's endpoint to the local proxy,
// leaving every other key exactly as it was — the credential the proxy forwards
// but never holds, the picked model, the declared context window, and the image
// deny rules all live in this same file.
//
// The overlay is the session's own generated copy (write_claude_launch_settings
// never modifies the stored profile), so rewriting it in place is safe. It is
// published by rename so a reader can never observe half a file, and it keeps
// owner-only permissions because it carries the API key.
func PointSettingsAt(path, proxyURL string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return fmt.Errorf("rolefix: parse settings: %w", err)
	}
	env, _ := settings["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	env["ANTHROPIC_BASE_URL"] = proxyURL
	settings["env"] = env

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), ".rolefix-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
