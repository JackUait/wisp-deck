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
	if _, priced := ModelCostUSD(ModelUsage{Model: "gpt-4o", Input: 1_000_000}); priced {
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
		{"glm-5.2", 1.40 + 4.40},        // Z.ai
		{"glm-4.7", 0.40 + 1.75},        // Z.ai
		{"glm-4.5-air", 0.13 + 0.85},    // Z.ai
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
