package claudeconfig

import "testing"

func TestWindowFitsMCP_rejects_windows_too_small_for_loaded_tool_schemas(t *testing.T) {
	tests := []struct {
		name   string
		window int
		want   bool
	}{
		{
			name:   "the 32k window nearly every Featherless model declares cannot hold the schemas",
			window: 32768,
			want:   false,
		},
		{
			name:   "a window matching the measured MCP prompt still leaves no room to reply",
			window: 49152,
			want:   false,
		},
		{
			name:   "64k holds the schemas, a reply and some conversation",
			window: 65536,
			want:   true,
		},
		{
			name:   "a 262144 flagship window is unaffected",
			window: 262144,
			want:   true,
		},
		{
			name:   "an undeclared window is not a finding: nothing is known to warn about",
			window: 0,
			want:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WindowFitsMCP(tt.window); got != tt.want {
				t.Fatalf("WindowFitsMCP(%d) = %v, want %v", tt.window, got, tt.want)
			}
		})
	}
}
