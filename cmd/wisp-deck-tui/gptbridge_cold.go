package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/jackuait/wisp-deck/internal/gptbridge"
)

const gptBridgeColdNotifyTimeout = 5 * time.Second

// gptBridgeColdLog lives beside the rest of wisp-deck's config.
func gptBridgeColdLog() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "wisp-deck", "gptbridge-cold.log")
}

func newGPTBridgeColdStartFuse(logPath string) *gptbridge.ColdStartFuse {
	return gptbridge.NewColdStartFuse(func(w gptbridge.ColdStartWarning) {
		warnGPTBridgeCold(logPath, w)
	})
}

// warnGPTBridgeCold writes one log line and shows the fixed notification.
// Both are host effects, so a test binary does neither.
func warnGPTBridgeCold(logPath string, w gptbridge.ColdStartWarning) {
	if !currentHostEffectsDecision().Allowed {
		return
	}
	if file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		_, _ = file.WriteString(formatGPTBridgeColdLine(time.Now().UTC(), w))
		_ = file.Close()
	}
	if runtime.GOOS != "darwin" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), gptBridgeColdNotifyTimeout)
	defer cancel()
	_ = runHostEffect(ctx, newGPTBridgeColdWarningHostEffect())
}

func formatGPTBridgeColdLine(at time.Time, w gptbridge.ColdStartWarning) string {
	return fmt.Sprintf("%s uncached_tokens=%d cold_starts=%d window=%s\n",
		at.Format(time.RFC3339), w.Tokens, w.Starts, w.Window)
}
