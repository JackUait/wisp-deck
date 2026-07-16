package bash_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// compact_view probes `wisp-deck-tui ledger --help` synchronously on every
// pane launch just to ask a yes/no capability question — a Gatekeeper-assessed
// Go binary exec on the launch critical path. A binary's capabilities are
// fixed, so the verdict must be cached keyed by the binary's path+mtime+size
// (the gt_claude_filter_prefix pattern): only the first launch after an
// install/update pays the probe.

// ledgerProbeEnv builds a PATH with a mocked wisp-deck-tui that records every
// `ledger --help` probe and reports capability per proble-exit.
func ledgerProbeEnv(t *testing.T, dir string, probeExit int) ([]string, string, string) {
	t.Helper()
	rec := filepath.Join(dir, "probe-rec")
	body := `
if [ "$1" = "ledger" ] && [ "$2" = "--help" ]; then
  echo probed >> ` + rec + `
  exit ` + map[bool]string{true: "0", false: "1"}[probeExit == 0] + `
fi
exit 0`
	bin := mockCommand(t, dir, "wisp-deck-tui", body)
	return buildEnv(t, []string{bin}), rec, filepath.Join(bin, "wisp-deck-tui")
}

func TestLedgerNativeCapable_probes_once_then_caches(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache")
	env, rec, _ := ledgerProbeEnv(t, dir, 0)

	for i := 0; i < 3; i++ {
		_, code := runBashFunc(t, "lib/compact-view.sh", "gt_ledger_native_capable",
			[]string{cache}, env)
		assertExitCode(t, code, 0)
	}
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("capability was never probed: %v", err)
	}
	if got := strings.Count(string(data), "probed"); got != 1 {
		t.Errorf("binary probed %d times across 3 launches, want exactly 1 (cached)", got)
	}
}

func TestLedgerNativeCapable_caches_negative_verdict(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache")
	env, rec, _ := ledgerProbeEnv(t, dir, 1)

	for i := 0; i < 2; i++ {
		_, code := runBashFunc(t, "lib/compact-view.sh", "gt_ledger_native_capable",
			[]string{cache}, env)
		if code == 0 {
			t.Fatal("an incapable binary must fail the capability check")
		}
	}
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("capability was never probed: %v", err)
	}
	if got := strings.Count(string(data), "probed"); got != 1 {
		t.Errorf("negative verdict probed %d times, want 1 (cached)", got)
	}
}

func TestLedgerNativeCapable_reprobes_when_binary_changes(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache")
	env, rec, binPath := ledgerProbeEnv(t, dir, 0)

	_, code := runBashFunc(t, "lib/compact-view.sh", "gt_ledger_native_capable",
		[]string{cache}, env)
	assertExitCode(t, code, 0)

	// A new install: same path, different content/size — signature changes.
	orig, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath, append(orig, []byte("\n# v2\n")...), 0755); err != nil {
		t.Fatal(err)
	}

	_, code = runBashFunc(t, "lib/compact-view.sh", "gt_ledger_native_capable",
		[]string{cache}, env)
	assertExitCode(t, code, 0)

	data, _ := os.ReadFile(rec)
	if got := strings.Count(string(data), "probed"); got != 2 {
		t.Errorf("changed binary probed %d times, want 2 (cache invalidated)", got)
	}
}

// compact_view must route its native-renderer decision through the cached
// helper — a raw inline `wisp-deck-tui ledger --help` reintroduces the
// per-launch probe spawn.
func TestCompactView_uses_cached_ledger_capability_probe(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "lib", "compact-view.sh"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	if !strings.Contains(src, "gt_ledger_native_capable") {
		t.Error("compact_view must use gt_ledger_native_capable for the native-renderer decision")
	}
	if strings.Count(src, "ledger --help") > 1 {
		t.Error("the `ledger --help` probe may only appear inside gt_ledger_native_capable")
	}
}
