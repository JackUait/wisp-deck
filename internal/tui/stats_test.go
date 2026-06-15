package tui

import "testing"

func TestHumanizeTokens(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1.0K"},
		{1500, "1.5K"},
		{12345, "12.3K"},
		{1000000, "1.0M"},
		{1500000, "1.5M"},
		{2000000000, "2.0B"},
	}
	for _, tt := range tests {
		if got := humanizeTokens(tt.in); got != tt.want {
			t.Errorf("humanizeTokens(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
