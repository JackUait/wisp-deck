package claudeconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// ByteWatchdogKey disarms Claude Code's raw-byte stall watchdog, which it arms
// on every text/event-stream body. 20s of byte silence paints
// "Waiting for API response · will retry in <N> · check your network", and at
// the budget's end (180s) it aborts the stream and replays the whole turn.
//
// The watchdog's premise is that a healthy stream is never silent, which holds
// for a vendor gateway: it forwards Anthropic's own `event: ping` frames. An
// endpoint the user supplies promises nothing of the sort — a self-hosted model
// prefilling a large prompt sends no bytes at all until its first output token
// — so on a user-configured profile the watchdog reports a working model as a
// broken network, then kills the turn it was working on.
//
// Measured against a live pane on 2.1.247: a mock Anthropic endpoint silent for
// 35s after message_start painted the banner at the 20s tick, and the same
// profile carrying this key ran the same turn to completion with no banner.
// There is no gentler lever — the 20s trigger is a hardcoded interval, and the
// abort budget env vars move the deadline without touching the banner.
const ByteWatchdogKey = "CLAUDE_ENABLE_BYTE_WATCHDOG"

// byteWatchdogDisarmed is the value Claude Code reads as off.
const byteWatchdogDisarmed = "0"

// stampByteWatchdog disarms the byte watchdog on a user-configured profile. A
// profile that already declares the key keeps its value: the user may have
// armed it deliberately on an endpoint that does heartbeat, and every launch
// path may run the sweep, so a second pass must change nothing.
//
// The provider comes from the explicit marker, never from the filename: an
// unmatched name resolves to Providers[0], so "qwen.json" would read as Zhipu.
func stampByteWatchdog(env map[string]any) {
	if _, declared := env[ByteWatchdogKey]; declared {
		return
	}
	marker, _ := env["WISP_DECK_SUBSCRIPTION_PROVIDER"].(string)
	provider, ok := providerByKey(marker)
	if !ok || !provider.UserConfigured {
		return
	}
	env[ByteWatchdogKey] = byteWatchdogDisarmed
}

// EnsureByteWatchdog disarms the byte watchdog on a config already on disk,
// reporting whether the file changed. The installer copies a default profile
// only when the file is absent, so a self-hosted profile written before this
// was declared is reachable only by this sweep.
func EnsureByteWatchdog(configsDir, file string) (bool, error) {
	path := filepath.Join(configsDir, file)
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return false, err
	}
	env, _ := settings["env"].(map[string]any)
	if env == nil {
		return false, nil
	}
	before, had := env[ByteWatchdogKey].(string)
	stampByteWatchdog(env)
	after, has := env[ByteWatchdogKey].(string)
	if had == has && before == after {
		return false, nil
	}
	settings["env"] = env
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return false, err
	}
	if err := writeSecure(path, append(out, '\n')); err != nil {
		return false, err
	}
	return true, nil
}

// EnsureByteWatchdogAll sweeps every profile in configsDir and reports how many
// changed. A profile it cannot read or parse is skipped rather than failing the
// sweep: one hand-edited file must not leave every other profile unrepaired.
func EnsureByteWatchdogAll(configsDir string) (int, error) {
	entries, err := os.ReadDir(configsDir)
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		did, err := EnsureByteWatchdog(configsDir, entry.Name())
		if err == nil && did {
			changed++
		}
	}
	return changed, nil
}
