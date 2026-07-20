package npx_test

import (
	"strconv"
	"strings"
	"testing"
)

// versionShipsProductionBoundary reports whether a published wisp-deck version
// is expected to carry the production capability boundary (first shipped in
// 2.23.1).
func versionShipsProductionBoundary(version string) bool {
	return publishedVersionAtLeast(version, 2, 23, 1)
}

// versionShipsOpenAIGPTConfig reports whether a published wisp-deck version
// ships defaults/claude-configs/openai-gpt.json (first shipped in 2.23.1).
func versionShipsOpenAIGPTConfig(version string) bool {
	return publishedVersionAtLeast(version, 2, 23, 1)
}

// publishedVersionAtLeast compares an X.Y.Z version (pre-release suffix
// ignored) against a floor. Anything unparseable is held to the current
// contract — a malformed version fails loudly instead of skipping checks.
func publishedVersionAtLeast(version string, major, minor, patch int) bool {
	base, _, _ := strings.Cut(version, "-")
	parts := strings.Split(base, ".")
	if len(parts) != 3 {
		return true
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return true
		}
		nums[i] = n
	}
	floor := [3]int{major, minor, patch}
	for i := range nums {
		if nums[i] != floor[i] {
			return nums[i] > floor[i]
		}
	}
	return true
}

// The production capability boundary (the `capabilities --require-production`
// contract) first shipped in v2.23.1. Older published versions can never
// satisfy it, so the npm-registry e2e must not demand it of them — otherwise
// the scheduled published-package check stays red from the moment the
// assertion lands until the next release, with no regression anywhere.
func TestVersionShipsProductionBoundary(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"2.22.0", false},
		{"2.23.0", false},
		{"2.23.1", true},
		{"2.23.2", true},
		{"2.24.0", true},
		{"3.0.0", true},
		{"10.0.0", true},
		{"2.23.0-rc.1", false},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			if got := versionShipsProductionBoundary(tt.version); got != tt.want {
				t.Errorf("versionShipsProductionBoundary(%q) = %v, want %v", tt.version, got, tt.want)
			}
		})
	}
}

// The OpenAI GPT subscription config rode the same release train: first
// published in 2.23.1, absent from every earlier tarball.
func TestVersionShipsOpenAIGPTConfig(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"2.23.0", false},
		{"2.23.1", true},
		{"2.24.0", true},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			if got := versionShipsOpenAIGPTConfig(tt.version); got != tt.want {
				t.Errorf("versionShipsOpenAIGPTConfig(%q) = %v, want %v", tt.version, got, tt.want)
			}
		})
	}
}
