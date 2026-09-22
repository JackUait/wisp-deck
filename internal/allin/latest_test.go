package allin

import (
	"reflect"
	"testing"
	"time"
)

func ids(models []Listed) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		out = append(out, m.ID)
	}
	return out
}

func listed(idList ...string) []Listed {
	out := make([]Listed, 0, len(idList))
	for _, id := range idList {
		out = append(out, Listed{ID: id})
	}
	return out
}

func TestLatest_picks_the_newest_per_family(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"anthropic 2026-09-22", []string{
			"claude-opus-5-5", "claude-fable-5-1", "claude-opus-5", "claude-sonnet-5",
			"claude-fable-5", "claude-opus-4-8", "claude-opus-4-7", "claude-sonnet-4-6",
			"claude-opus-4-6", "claude-opus-4-5-20251101", "claude-haiku-4-5-20251001",
			"claude-sonnet-4-5-20250929",
		}, []string{"claude-opus-5-5", "claude-fable-5-1", "claude-sonnet-5", "claude-haiku-4-5-20251001"}},
		{"zhipu 2026-09-22", []string{
			"glm-4.5", "glm-4.5-air", "glm-4.6", "glm-4.7", "glm-5", "glm-5-turbo",
			"glm-5.1", "glm-5.2", "glm-5.3", "glm-5.3-flash", "glm-5.3-flashx",
		}, []string{"glm-5.3", "glm-4.5-air", "glm-5-turbo", "glm-5.3-flash", "glm-5.3-flashx"}},
		{"deepseek", []string{"deepseek-flash", "deepseek-v4-pro"},
			[]string{"deepseek-flash", "deepseek-v4-pro"}},
		{"kimi coding, equal dates", []string{"kimi-for-coding", "kimi-for-coding-highspeed", "k3", "k3-256k"},
			[]string{"kimi-for-coding", "kimi-for-coding-highspeed", "k3", "k3-256k"}},
		{"codex", []string{"gpt-6-astra", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.5"},
			[]string{"gpt-6-astra", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.5"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ids(Latest(listed(c.in...))); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("Latest = %v, want %v", got, c.want)
			}
		})
	}
}

func TestLatest_breaks_a_version_tie_on_the_date_suffix_then_created(t *testing.T) {
	got := ids(Latest(listed("claude-haiku-4-5-20250101", "claude-haiku-4-5-20251001")))
	if !reflect.DeepEqual(got, []string{"claude-haiku-4-5-20251001"}) {
		t.Fatalf("date tiebreak: %v", got)
	}
	older := Listed{ID: "kimi-for-coding", Created: time.Unix(100, 0)}
	newer := Listed{ID: "kimi-for-coding", Label: "newer", Created: time.Unix(200, 0)}
	if got := Latest([]Listed{older, newer}); len(got) != 1 || got[0].Label != "newer" {
		t.Fatalf("created tiebreak: %+v", got)
	}
}

func TestLatest_keeps_unfamiliar_ids(t *testing.T) {
	in := []string{"gpt-4o", "o3", "claude-3-7-sonnet-latest", "", "weird--id"}
	got := ids(Latest(listed(in...)))
	want := []string{"gpt-4o", "o3", "claude-3-7-sonnet-latest", "weird--id"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Latest = %v, want %v", got, want)
	}
}
