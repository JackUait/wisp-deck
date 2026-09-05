package usage

import "testing"

// The Stats tab used to print raw model ids — `opus-4-8`, `gpt-5.6-sol`,
// `haiku-4-5-20251…` — which read as internal identifiers and truncated badly.
// DisplayModelName turns each into the name the vendor actually writes.
//
// Every id below was taken from a real usage-cache.json, so this table is the
// full shape of the input space rather than invented examples.
func TestDisplayModelName(t *testing.T) {
	for _, tc := range []struct{ id, want string }{
		// Anthropic — dashed and dotted version forms both fold to one shape,
		// and the release-date suffix is a build stamp, not part of the name.
		{"claude-fable-5", "Fable 5"},
		{"claude-haiku-4-5-20251001", "Haiku 4.5"},
		{"claude-opus-4-7", "Opus 4.7"},
		{"claude-opus-4-8", "Opus 4.8"},
		{"claude-opus-4.5", "Opus 4.5"},
		{"claude-opus-5", "Opus 5"},
		{"claude-sonnet-4-6", "Sonnet 4.6"},
		{"claude-sonnet-5", "Sonnet 5"},
		// The legacy id order (version before family) still resolves.
		{"claude-3-5-sonnet-20241022", "Sonnet 3.5"},
		// OpenAI — the vendor writes the family as an acronym joined to its
		// version by a hyphen, with any variant following as words.
		{"gpt-5", "GPT-5"},
		{"gpt-5-codex", "GPT-5 Codex"},
		{"gpt-5.1-codex", "GPT-5.1 Codex"},
		{"gpt-5.1-codex-max", "GPT-5.1 Codex Max"},
		{"gpt-5.1-codex-mini", "GPT-5.1 Codex Mini"},
		{"gpt-5.2", "GPT-5.2"},
		{"gpt-5.2-codex", "GPT-5.2 Codex"},
		{"gpt-5.3-codex", "GPT-5.3 Codex"},
		{"gpt-5.4", "GPT-5.4"},
		{"gpt-5.4-mini", "GPT-5.4 Mini"},
		{"gpt-5.5", "GPT-5.5"},
		{"gpt-5.6-luna", "GPT-5.6 Luna"},
		{"gpt-5.6-sol", "GPT-5.6 Sol"},
		{"gpt-5.6-terra", "GPT-5.6 Terra"},
		{"gpt-6-astra", "GPT-6 Astra"},
		// Zhipu and Moonshot.
		{"glm-4.7-free", "GLM-4.7 Free"},
		{"k3", "K3"},
		{"k3-256k", "K3 256K"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			if got := DisplayModelName(tc.id); got != tc.want {
				t.Errorf("DisplayModelName(%q) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}

// Anything that is not a recognisable model id must pass through untouched
// rather than be mangled into a plausible-looking name. Claude Code's
// "<synthetic>" row is the one that actually occurs.
func TestDisplayModelName_passesThroughNonModelIds(t *testing.T) {
	for _, id := range []string{"<synthetic>", "", "unknown"} {
		want := id
		if id == "unknown" {
			want = "Unknown" // a bare word is still a name, so it is capitalised
		}
		if got := DisplayModelName(id); got != want {
			t.Errorf("DisplayModelName(%q) = %q, want %q", id, got, want)
		}
	}
}

// The Stats column is fixed-width, so a name that grows past it would truncate.
// The longest real id must still fit the budget the renderer allots.
func TestDisplayModelName_fitsStatsColumn(t *testing.T) {
	const statsModelColWidth = 20
	for _, id := range []string{"gpt-5.1-codex-mini", "claude-sonnet-4-6", "glm-4.7-free"} {
		if n := len([]rune(DisplayModelName(id))); n > statsModelColWidth {
			t.Errorf("DisplayModelName(%q) is %d wide, exceeds the %d-cell column: %q",
				id, n, statsModelColWidth, DisplayModelName(id))
		}
	}
}
