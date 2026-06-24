package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// Per-shape preset palettes: one tuning per color for each mascot shape. The
// brand fields (Primary, UIAccent, Text) are shared so chrome/title stay the
// same hue regardless of which ghost is on screen; only the ghost-shading
// fields (Bright/Accent) diverge to fit each shape's field roles.

func TestResolveTheme_brandSharedAcrossShapes(t *testing.T) {
	for _, color := range []string{"orange", "purple", "green", "blue", "rose", "cyan"} {
		c := ResolveTheme("claude", color)
		o := ResolveTheme("opencode", color)
		if c.Primary != o.Primary {
			t.Errorf("%s: Primary differs across shapes (claude %v, opencode %v) — brand must be shared", color, c.Primary, o.Primary)
		}
		if c.UIAccent != o.UIAccent {
			t.Errorf("%s: UIAccent differs across shapes (claude %v, opencode %v) — chrome must be shared", color, c.UIAccent, o.UIAccent)
		}
		if c.Text != o.Text {
			t.Errorf("%s: Text differs across shapes (claude %v, opencode %v)", color, c.Text, o.Text)
		}
		if c.Name != color || o.Name != color {
			t.Errorf("%s: Name should equal color, got claude %q opencode %q", color, c.Name, o.Name)
		}
	}
}

func TestResolveTheme_shapeTuningDiverges(t *testing.T) {
	// The whole point of per-shape palettes: the ghost-shading fields are tuned
	// differently for the two shapes, even though the brand color matches.
	for _, color := range []string{"orange", "purple", "green", "blue", "rose", "cyan"} {
		c := ResolveTheme("claude", color)
		o := ResolveTheme("opencode", color)
		if c.Accent == o.Accent && c.Bright == o.Bright {
			t.Errorf("%s: claude and opencode tunings are identical (Accent %v, Bright %v) — expected per-shape divergence", color, c.Accent, c.Bright)
		}
	}
}

func TestResolveTheme_claudeEmblemIsGoldOnlyForOrange(t *testing.T) {
	// The Claude belly emblem is gold (220) on orange and a bright per-hue pop
	// elsewhere — never the dark body band the opencode shape uses.
	if got := ResolveTheme("claude", "orange").Accent; got != lipgloss.Color("220") {
		t.Errorf("claude orange emblem should stay gold (220), got %v", got)
	}
	// On the opencode shape, orange must NOT carry the gold emblem color — there
	// it would paint a garish band, so it uses a dark lower-body tone instead.
	if got := ResolveTheme("opencode", "orange").Accent; got == lipgloss.Color("220") {
		t.Error("opencode orange should not use gold (220) as its lower-body band")
	}
}

func TestGhost_orangeOnOpencode_hasNoGoldBand(t *testing.T) {
	joined := strings.Join(GhostForTheme("opencode", false, ResolveTheme("opencode", "orange")), "\n")
	if strings.Contains(joined, "\033[38;5;220m") {
		t.Error("opencode orange ghost should not paint the gold (220) emblem color as a body band")
	}
}

func TestGhost_claudeEmblemRendersForEveryColor(t *testing.T) {
	// Every preset must put its (bright) emblem color onto the Claude belly.
	for _, color := range []string{"orange", "purple", "green", "blue", "rose", "cyan"} {
		theme := ResolveTheme("claude", color)
		joined := strings.Join(GhostForTheme("claude", false, theme), "\n")
		emblem := AnsiFromThemeColor(theme.Accent)
		if !strings.Contains(joined, emblem) {
			t.Errorf("claude %s ghost should render the emblem color %q on the belly", color, theme.Accent)
		}
	}
}
