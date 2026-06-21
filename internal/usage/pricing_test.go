package usage

import (
	"math"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestModelCostUSD_tiers(t *testing.T) {
	// 1M input + 1M output on opus = $5 + $25 = $30
	usd, priced := ModelCostUSD(ModelUsage{Model: "claude-opus-4-7", Input: 1_000_000, Output: 1_000_000})
	if !priced || !approx(usd, 30) {
		t.Errorf("opus = %v priced=%v, want 30/true", usd, priced)
	}
	// sonnet 1M in + 1M out = $3 + $15 = $18
	usd, _ = ModelCostUSD(ModelUsage{Model: "claude-sonnet-4-6", Input: 1_000_000, Output: 1_000_000})
	if !approx(usd, 18) {
		t.Errorf("sonnet = %v, want 18", usd)
	}
	// haiku 1M in = $1
	usd, _ = ModelCostUSD(ModelUsage{Model: "claude-haiku-4-5", Input: 1_000_000})
	if !approx(usd, 1) {
		t.Errorf("haiku = %v, want 1", usd)
	}
	// fable 1M in + 1M out = $10 + $50 = $60
	usd, _ = ModelCostUSD(ModelUsage{Model: "claude-fable-5", Input: 1_000_000, Output: 1_000_000})
	if !approx(usd, 60) {
		t.Errorf("fable = %v, want 60", usd)
	}
}

func TestModelCostUSD_cacheMultipliers(t *testing.T) {
	// opus input rate $5/MTok. cache-write 1.25x = $6.25/MTok; cache-read 0.1x = $0.50/MTok
	usd, _ := ModelCostUSD(ModelUsage{Model: "claude-opus-4-7", CacheWrite: 1_000_000})
	if !approx(usd, 6.25) {
		t.Errorf("cacheWrite = %v, want 6.25", usd)
	}
	usd, _ = ModelCostUSD(ModelUsage{Model: "claude-opus-4-7", CacheRead: 1_000_000})
	if !approx(usd, 0.5) {
		t.Errorf("cacheRead = %v, want 0.5", usd)
	}
}

func TestModelCostUSD_cacheTTLSplit(t *testing.T) {
	// opus input $5/MTok. 1M cache-write total, 400k of it at the 1-hour TTL:
	//   5m: 600k * 1.25 * $5/MTok = $3.75
	//   1h: 400k * 2.00 * $5/MTok = $4.00  -> total $7.75
	usd, _ := ModelCostUSD(ModelUsage{Model: "claude-opus-4-7", CacheWrite: 1_000_000, CacheWrite1h: 400_000})
	if !approx(usd, 7.75) {
		t.Errorf("ttl split = %v, want 7.75", usd)
	}
	// All-5m (CacheWrite1h=0) keeps the legacy 1.25x behavior.
	usd, _ = ModelCostUSD(ModelUsage{Model: "claude-opus-4-7", CacheWrite: 1_000_000})
	if !approx(usd, 6.25) {
		t.Errorf("all-5m = %v, want 6.25", usd)
	}
}

func TestModelCostUSD_prefixMatchAndUnknown(t *testing.T) {
	usd, priced := ModelCostUSD(ModelUsage{Model: "claude-haiku-4-5-20251001", Input: 1_000_000})
	if !priced || !approx(usd, 1) {
		t.Errorf("suffixed haiku = %v priced=%v, want 1/true", usd, priced)
	}
	if _, priced := ModelCostUSD(ModelUsage{Model: "llama-3.1-70b-instruct", Input: 1_000_000}); priced {
		t.Errorf("unknown model should be unpriced")
	}
}

func TestModelCostUSD_bareAliases(t *testing.T) {
	// Claude Code transcripts sometimes record bare aliases ("opus"/"sonnet"/
	// "haiku") instead of full ids. They carry real tokens and must be priced at
	// the current-generation rate, not dropped as $0.
	cases := []struct {
		model string
		want  float64 // 1M input + 1M output
	}{
		{"opus", 30},   // $5 + $25
		{"sonnet", 18}, // $3 + $15
		{"haiku", 6},   // $1 + $5
	}
	for _, c := range cases {
		usd, priced := ModelCostUSD(ModelUsage{Model: c.model, Input: 1_000_000, Output: 1_000_000})
		if !priced || !approx(usd, c.want) {
			t.Errorf("%s = %v priced=%v, want %v/true", c.model, usd, priced, c.want)
		}
	}
}

func TestModelCostUSD_customProviderModels(t *testing.T) {
	// Non-Anthropic models routed through Claude Code are priced at their own
	// provider's published API rates (per 1M tokens) so the Stats tab no longer
	// shows them as "—". 1M input + 1M output expected total per row.
	cases := []struct {
		model string
		want  float64
	}{
		{"glm-5.2", 1.40 + 4.40},        // Z.ai (official list price)
		{"glm-5.1", 1.40 + 4.40},        // Z.ai
		{"glm-4.7", 0.60 + 2.20},        // Z.ai
		{"glm-4.5-air", 0.20 + 1.10},    // Z.ai
		{"mimo-v2.5", 0.14 + 0.28},      // Xiaomi
		{"mimo-v2.5-pro", 0.435 + 0.87}, // Xiaomi
	}
	for _, c := range cases {
		usd, priced := ModelCostUSD(ModelUsage{Model: c.model, Input: 1_000_000, Output: 1_000_000})
		if !priced || !approx(usd, c.want) {
			t.Errorf("%s = %v priced=%v, want %v/true", c.model, usd, priced, c.want)
		}
	}
}

// mimo-v2.5 is a prefix of mimo-v2.5-pro, so a first-match lookup could price the
// pro model at the cheaper standard rate depending on map iteration order. The
// longest matching prefix must win.
func TestRateFor_longestPrefixWins(t *testing.T) {
	for i := 0; i < 50; i++ { // map order is randomized; hammer it
		usd, priced := ModelCostUSD(ModelUsage{Model: "mimo-v2.5-pro", Input: 1_000_000, Output: 1_000_000})
		if !priced || !approx(usd, 0.435+0.87) {
			t.Fatalf("mimo-v2.5-pro = %v priced=%v, want %v (pro rate, not standard)", usd, priced, 0.435+0.87)
		}
	}
}

func TestRateFor_exported(t *testing.T) {
	if in, out, ok := RateFor("glm-4.6"); !ok || in != 0.6 || out != 2.2 {
		t.Errorf("RateFor(glm-4.6) = %v, %v, %v; want 0.6, 2.2, true", in, out, ok)
	}
	// longest-prefix: pro rate, not the shorter mimo-v2.5 standard rate
	if in, out, ok := RateFor("mimo-v2.5-pro"); !ok || in != 0.435 || out != 0.87 {
		t.Errorf("RateFor(mimo-v2.5-pro) = %v, %v, %v; want 0.435, 0.87, true", in, out, ok)
	}
	if _, _, ok := RateFor("totally-unknown-model"); ok {
		t.Errorf("RateFor(unknown) ok = true, want false")
	}
}

func TestModelCostUSD_openCodeProviders(t *testing.T) {
	// Non-Anthropic models routed through OpenCode, priced at their models.dev
	// (the catalog OpenCode uses) input+output rates. Each case is 1M input +
	// 1M output, so want = input$/MTok + output$/MTok. Several cases use a
	// date/variant-suffixed id to exercise longest-prefix resolution.
	cases := []struct {
		model string
		want  float64
	}{
		// OpenAI
		{"gpt-5", 1.25 + 10},
		{"gpt-5-mini", 0.25 + 2},
		{"gpt-5-nano", 0.05 + 0.4},
		{"gpt-5-codex", 1.25 + 10},   // shares base gpt-5 rate via prefix
		{"gpt-5.1-codex", 1.25 + 10}, // gpt-5.1 family
		{"gpt-5.2", 1.75 + 14},
		{"gpt-5.3-codex", 1.75 + 14},
		{"gpt-4.1", 2 + 8},
		{"gpt-4.1-mini", 0.4 + 1.6},
		{"gpt-4o", 2.5 + 10},
		{"gpt-4o-mini", 0.15 + 0.6},
		{"o3", 2 + 8},
		{"o4-mini", 1.1 + 4.4},
		// Google Gemini
		{"gemini-2.5-pro", 1.25 + 10},
		{"gemini-2.5-flash", 0.3 + 2.5},
		{"gemini-2.5-flash-lite", 0.1 + 0.4},
		{"gemini-3-pro-preview", 2 + 12},
		{"gemini-3-flash-preview", 0.5 + 3},
		// xAI (grok-4 family all share 1.25/2.5)
		{"grok-4.3", 1.25 + 2.5},
		{"grok-4.20-0309-reasoning", 1.25 + 2.5},
		// DeepSeek
		{"deepseek-chat", 0.14 + 0.28},
		{"deepseek-reasoner", 0.14 + 0.28},
		// Alibaba Qwen
		{"qwen3-coder-plus", 1 + 5},
		{"qwen3-coder-480b-a35b-instruct", 1.5 + 7.5}, // base qwen3-coder
		{"qwen3-max", 1.2 + 6},
		// Moonshot Kimi
		{"kimi-k2-0905-preview", 0.6 + 2.5}, // base kimi-k2
		{"kimi-k2-turbo-preview", 2.4 + 10},
		{"kimi-k2.6", 0.95 + 4},
		// Z.ai GLM (from the shared model catalog)
		{"glm-4.6", 0.6 + 2.2},
		{"glm-5.1", 1.40 + 4.40},
		// Mistral
		{"codestral-latest", 0.3 + 0.9},
		{"devstral-medium-latest", 0.4 + 2},
	}
	for _, c := range cases {
		usd, priced := ModelCostUSD(ModelUsage{Model: c.model, Input: 1_000_000, Output: 1_000_000})
		if !priced || !approx(usd, c.want) {
			t.Errorf("%s = %v priced=%v, want %v/true", c.model, usd, priced, c.want)
		}
	}
}

func TestModelCostUSD_openCodeLongestPrefixWins(t *testing.T) {
	// Sibling ids that share a shorter prefix but have different prices must NOT
	// be mispriced as the cheaper/base model. Hammer the randomized map order.
	cases := []struct {
		model string
		want  float64 // 1M in + 1M out
	}{
		{"gpt-5-mini", 0.25 + 2},            // not gpt-5 (11.25)
		{"gpt-5.2", 1.75 + 14},              // not gpt-5 (11.25)
		{"gpt-5.4", 2.5 + 15},               // not gpt-5 (11.25)
		{"glm-5.2", 1.40 + 4.40},            // existing entry beats new glm-5 (4.2)
		{"qwen3-coder-plus", 1 + 5},         // not qwen3-coder (9)
		{"kimi-k2-turbo-preview", 2.4 + 10}, // not kimi-k2 (3.1)
	}
	for i := 0; i < 30; i++ {
		for _, c := range cases {
			usd, priced := ModelCostUSD(ModelUsage{Model: c.model, Input: 1_000_000, Output: 1_000_000})
			if !priced || !approx(usd, c.want) {
				t.Fatalf("%s = %v priced=%v, want %v (longest prefix must win)", c.model, usd, priced, c.want)
			}
		}
	}
}

func TestModelCostUSD_zeroTokensPricedFree(t *testing.T) {
	// Zero-token rows (e.g. "<synthetic>") cost nothing regardless of model, so
	// they must report priced=true and must not flag a month as approximate.
	usd, priced := ModelCostUSD(ModelUsage{Model: "<synthetic>"})
	if !priced || !approx(usd, 0) {
		t.Errorf("synthetic = %v priced=%v, want 0/true", usd, priced)
	}
}

func TestMonthlyCostUSD_sumAndFlag(t *testing.T) {
	mu := MonthlyUsage{Models: []ModelUsage{
		{Model: "claude-opus-4-7", Input: 1_000_000}, // $5
		{Model: "mystery", Input: 1_000_000},         // unpriced
	}}
	usd, allPriced := mu.CostUSD()
	if !approx(usd, 5) {
		t.Errorf("month cost = %v, want 5 (priced only)", usd)
	}
	if allPriced {
		t.Errorf("allPriced should be false when a model is unpriced")
	}
}
