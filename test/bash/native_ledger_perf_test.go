package bash_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/creack/pty"
)

func nativeLedgerBottomLatency(t *testing.T, total int) time.Duration {
	t.Helper()
	project := initNativeLedgerRepo(t)
	snapshot := writeNativeLedgerSnapshot(t, total, "main")
	session := startNativeLedgerPTY(t, project, snapshot, &pty.Winsize{Rows: 18, Cols: 90}, nil)
	if _, raw, ok := session.waitFor("file_00000.go", 2*time.Second); !ok {
		t.Fatalf("%d-row first frame missing: %q", total, raw)
	}
	offset := session.write(t, "G")
	want := fmt.Sprintf("file_%05d.go", total-1)
	elapsed, raw, ok := session.waitAfter(offset, want, 100*time.Millisecond)
	if !ok {
		t.Fatalf("%d-row bottom frame exceeded 100ms; wanted %q in %q", total, want, raw)
	}
	session.stop(t)
	return elapsed
}

func TestNativeLedgerLatencyScale1kAnd10k(t *testing.T) {
	oneK := nativeLedgerBottomLatency(t, 1_000)
	tenK := nativeLedgerBottomLatency(t, 10_000)
	const ceiling = 100 * time.Millisecond
	if oneK > ceiling || tenK > ceiling {
		t.Fatalf("input-to-frame latency exceeds %v: 1k=%v 10k=%v", ceiling, oneK, tenK)
	}
	faster, slower := oneK, tenK
	if faster > slower {
		faster, slower = slower, faster
	}
	if slower > 10*(faster+time.Millisecond) {
		t.Fatalf("1k and 10k latencies differ by over one order of magnitude: 1k=%v 10k=%v", oneK, tenK)
	}
	t.Logf("native ledger input-to-bottom-frame latency: 1k=%v 10k=%v", oneK, tenK)
}
