package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackuait/wisp-deck/internal/gptbridge"
)

func TestFormatGPTBridgeColdLineCarriesTheNumbers(t *testing.T) {
	line := formatGPTBridgeColdLine(time.Date(2026, 9, 28, 4, 5, 0, 0, time.UTC),
		gptbridge.ColdStartWarning{Tokens: 3_400_000, Starts: 27, Window: 10 * time.Minute})
	for _, want := range []string{"2026-09-28T04:05:00Z", "3400000", "27", "10m0s"} {
		if !strings.Contains(line, want) {
			t.Fatalf("line %q lacks %q", line, want)
		}
	}
	if !strings.HasSuffix(line, "\n") {
		t.Fatalf("line %q is not newline-terminated", line)
	}
}

func TestGPTBridgeColdWarningWritesNothingUnderTest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gptbridge-cold.log")
	warnGPTBridgeCold(path, gptbridge.ColdStartWarning{Tokens: 3_000_000, Starts: 20, Window: 10 * time.Minute})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a test binary wrote the host log: %v", err)
	}
}
