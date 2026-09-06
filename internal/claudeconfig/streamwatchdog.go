package claudeconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// StreamWatchdogKey disarms Claude Code's event-tier stream watchdog, the one
// that measures parsed SSE events rather than raw bytes.
//
// Decoded from 2.1.263 and then measured against a live client. The client
// synthesizes a `{type:"ping"}` event every 10s while raw bytes keep arriving,
// and re-arms the idle timer for at most 30 CONSECUTIVE such events
// (`Bis=30`); after that the timer runs to `CLAUDE_STREAM_IDLE_TIMEOUT_MS`
// (floor 300s), logs "Streaming idle timeout: no chunks received", aborts the
// stream, and the whole turn is replayed. So a turn that produces no real
// stream event for 30*10s + 300s is killed — and the replay repeats the same
// work, so a turn slow enough to exceed it every time can never complete.
//
// The premise is that a healthy stream emits a real event within that window.
// It holds for Anthropic's own API, which sends content and thinking deltas
// throughout. It holds for nothing wisp-deck points Claude Code at: a bridged
// Codex turn forwards only keepalives while the model reasons, a gateway routes
// to models that think for minutes, and a self-hosted endpoint sends nothing at
// all until its first output token. There the watchdog reports a working model
// as a dead stream, then destroys the turn it was working on.
//
// Measured 2026-09-06 against 2.1.263 with a mock Anthropic endpoint: a stream
// carrying only `event: ping` was aborted and re-POSTed at 610s, one carrying
// pings for 400s completed in a single attempt, and the same 700s stream with
// this key set completed in a single attempt.
//
// The byte tier is untouched wherever it is armed. That one measures raw bytes,
// which is what a keepalive honestly reports, so it still catches an endpoint
// that has actually gone away.
const StreamWatchdogKey = "CLAUDE_ENABLE_STREAM_WATCHDOG"

// streamWatchdogDisarmed is the value Claude Code reads as off.
const streamWatchdogDisarmed = "0"

// stampStreamWatchdog disarms the event watchdog on a profile's decoded env.
//
// Unlike the byte watchdog this covers EVERY provider, not just the
// user-configured ones: a vendor gateway forwards Anthropic's `event: ping`,
// which the client drops before its stream loop, so a gateway keepalive feeds
// the byte tier and the 30-ping cap exactly like a self-hosted silence does.
//
// A profile that already declares the key keeps its value: the user may have
// armed it deliberately, and every launch path may run the sweep, so a second
// pass must change nothing.
func stampStreamWatchdog(env map[string]any) {
	if _, declared := env[StreamWatchdogKey]; declared {
		return
	}
	env[StreamWatchdogKey] = streamWatchdogDisarmed
}

// EnsureStreamWatchdog disarms the event watchdog on a config already on disk,
// reporting whether the file changed. The installer copies a default profile
// only when the file is absent, so a profile written before this key was
// declared is reachable only by this sweep.
func EnsureStreamWatchdog(configsDir, file string) (bool, error) {
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
	before, had := env[StreamWatchdogKey].(string)
	stampStreamWatchdog(env)
	after, has := env[StreamWatchdogKey].(string)
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

// EnsureStreamWatchdogAll sweeps every profile in configsDir and reports how
// many changed. A profile it cannot read or parse is skipped rather than
// failing the sweep: one hand-edited file must not leave every other profile
// unrepaired.
func EnsureStreamWatchdogAll(configsDir string) (int, error) {
	entries, err := os.ReadDir(configsDir)
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		did, err := EnsureStreamWatchdog(configsDir, entry.Name())
		if err == nil && did {
			changed++
		}
	}
	return changed, nil
}
