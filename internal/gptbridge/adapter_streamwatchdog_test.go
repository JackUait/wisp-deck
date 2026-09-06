package gptbridge

import (
	"strings"
	"testing"
)

// Claude Code re-arms its event-tier idle watchdog for at most 30 CONSECUTIVE
// keepalive events, then lets the timer run out and aborts the stream, and the
// whole turn is replayed. The bridge answers a working turn with nothing but
// `event: ping`, so the ceiling on model silence is 30 x 10s + 300s.
//
// Measured against a live 2.1.263 client on 2026-09-06: a mock endpoint sending
// only pings was aborted and re-POSTed at 610s, while the same stream with this
// key set completed 701s of silence in one attempt.
func TestBuildClaudeEnvironmentDisarmsTheEventStreamWatchdog(t *testing.T) {
	got := BuildClaudeEnvironment(nil, "http://127.0.0.1:4321", "bridge-secret")
	joined := "\n" + strings.Join(got, "\n") + "\n"
	const want = "\nCLAUDE_ENABLE_STREAM_WATCHDOG=0\n"
	if !strings.Contains(joined, want) {
		t.Fatalf("Claude environment missing %q:\n%s", want, joined)
	}
	if countEnv(got, "CLAUDE_ENABLE_STREAM_WATCHDOG") != 1 {
		t.Fatalf("stream watchdog override occurs more than once: %q", got)
	}
}

// An inherited value would otherwise re-arm the abort inside the pane.
func TestBuildClaudeEnvironmentReplacesAnInheritedStreamWatchdogValue(t *testing.T) {
	got := BuildClaudeEnvironment(
		[]string{"CLAUDE_ENABLE_STREAM_WATCHDOG=1"},
		"http://127.0.0.1:4321",
		"bridge-secret",
	)
	if countEnv(got, "CLAUDE_ENABLE_STREAM_WATCHDOG") != 1 {
		t.Fatalf("stream watchdog override occurs more than once: %q", got)
	}
	joined := "\n" + strings.Join(got, "\n") + "\n"
	if !strings.Contains(joined, "\nCLAUDE_ENABLE_STREAM_WATCHDOG=0\n") {
		t.Fatalf("inherited value survived:\n%s", joined)
	}
}

// The byte tier is the bridge's honest liveness signal — it pings every 10s
// while it lives — so it must stay armed. Disarming it too would leave a wedged
// bridge with no abort at all.
func TestBuildClaudeEnvironmentLeavesTheByteWatchdogAlone(t *testing.T) {
	got := BuildClaudeEnvironment(nil, "http://127.0.0.1:4321", "bridge-secret")
	if countEnv(got, "CLAUDE_ENABLE_BYTE_WATCHDOG") != 0 {
		t.Fatalf("bridge disarmed the byte watchdog: %q", got)
	}
}
