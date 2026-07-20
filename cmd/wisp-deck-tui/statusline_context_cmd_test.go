package main

import (
	"bytes"
	"strings"
	"testing"
)

// statusline-context: stdin is Claude Code's statusline JSON, stdout is the
// same JSON with the context_window figures corrected to the active
// subscription model's real window. The statusline wrapper pipes through it
// on subscription panes before ccstatusline renders the context percentage.

func execRootWithStdin(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	var buf bytes.Buffer
	rootCmd.SetIn(strings.NewReader(stdin))
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs(args)
	t.Cleanup(func() { rootCmd.SetIn(nil) })
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(%v): %v\noutput: %s", args, err, buf.String())
	}
	return buf.String()
}

func TestStatuslineContextCmd_Registered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"statusline-context"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if cmd.Name() != "statusline-context" {
		t.Errorf("resolved to %q", cmd.Name())
	}
}

func TestStatuslineContextCmd_rewrites_catalog_model(t *testing.T) {
	in := `{"model":{"id":"glm-5.2"},"context_window":{"context_window_size":200000,"used_percentage":50}}`
	out := execRootWithStdin(t, in, "statusline-context")
	if !strings.Contains(out, `"context_window_size":1000000`) {
		t.Errorf("real window not applied: %s", out)
	}
	if !strings.Contains(out, `"used_percentage":10`) {
		t.Errorf("used percentage not recomputed: %s", out)
	}
}

func TestStatuslineContextCmd_passes_through_native_model(t *testing.T) {
	in := `{"model":{"id":"claude-fable-5"},"context_window":{"context_window_size":200000,"used_percentage":50}}`
	out := execRootWithStdin(t, in, "statusline-context")
	if strings.TrimSpace(out) != in {
		t.Errorf("native model must pass through verbatim, got %s", out)
	}
}

// A statusline is no place for error output: malformed stdin still exits 0
// and echoes the input so the wrapper's fallback keeps rendering.
func TestStatuslineContextCmd_malformed_input_echoes_and_succeeds(t *testing.T) {
	out := execRootWithStdin(t, `not json`, "statusline-context")
	if strings.TrimSpace(out) != "not json" {
		t.Errorf("malformed input should echo through, got %q", out)
	}
}
