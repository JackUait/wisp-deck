package allin

import "testing"

func TestRoute_leaves_a_plain_model_on_the_session(t *testing.T) {
	got := Route("claude-opus-5")
	if got.Kind != KindSession || got.Model != "claude-opus-5" || got.Want1M {
		t.Fatalf("got %+v", got)
	}
}

func TestRoute_splits_an_account_row(t *testing.T) {
	got := Route("wisp/acct.personal/claude-opus-5")
	if got.Kind != KindAccount || got.Source != "personal" || got.Model != "claude-opus-5" {
		t.Fatalf("got %+v", got)
	}
}

func TestRoute_keeps_slashes_inside_the_model_id(t *testing.T) {
	got := Route("wisp/cfg.featherless/TurboVadim/Qwen3.8-27B-OBLITERATED")
	if got.Kind != KindConfig || got.Source != "featherless" {
		t.Fatalf("got %+v", got)
	}
	if got.Model != "TurboVadim/Qwen3.8-27B-OBLITERATED" {
		t.Fatalf("model %q", got.Model)
	}
}

func TestRoute_strips_the_1m_marker_and_reports_it(t *testing.T) {
	got := Route("wisp/acct.default/claude-opus-5[1m]")
	if got.Model != "claude-opus-5" || !got.Want1M {
		t.Fatalf("got %+v", got)
	}
}

func TestRoute_treats_an_unparseable_prefix_as_the_session(t *testing.T) {
	for _, id := range []string{"wisp/", "wisp/onlysource", "wisp/bogus.x/m"} {
		if got := Route(id); got.Kind != KindSession {
			t.Fatalf("%q routed to %+v", id, got)
		}
	}
}

func TestRoute_reads_the_fast_marker(t *testing.T) {
	for _, model := range []string{FastModel, FastModel + OneMillionMarker} {
		if got := Route(model); got != (Target{Kind: KindFast}) {
			t.Errorf("Route(%q) = %+v, want the bare KindFast target", model, got)
		}
	}
}
