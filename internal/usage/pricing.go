package usage

import "strings"

type modelRate struct{ inPerMTok, outPerMTok float64 }

// modelRates holds published Anthropic prices per 1,000,000 tokens, matched by
// model-id prefix so date-suffixed ids (e.g. claude-haiku-4-5-20251001) resolve.
var modelRates = map[string]modelRate{
	"claude-opus-4-5":   {5, 25},
	"claude-opus-4-6":   {5, 25},
	"claude-opus-4-7":   {5, 25},
	"claude-opus-4-8":   {5, 25},
	"claude-sonnet-4-5": {3, 15},
	"claude-sonnet-4-6": {3, 15},
	"claude-haiku-4-5":  {1, 5},
	"claude-fable-5":    {10, 50},
	"claude-mythos-5":   {10, 50},

	// Bare aliases recorded by Claude Code in some transcripts. They carry real
	// tokens, so they must be priced at the current-generation rate rather than
	// dropped as $0. Listed after the full ids; the matched-prefix lookup never
	// confuses them (a full id like "claude-opus-4-8" does not start with "opus").
	"opus":   {5, 25},
	"sonnet": {3, 15},
	"haiku":  {1, 5},

	// Non-Anthropic models routed through Claude Code via a custom provider. Priced
	// at each provider's published standard API rates (per 1M tokens). mimo-v2.5 is
	// a prefix of mimo-v2.5-pro; rateFor's longest-prefix match keeps them distinct.
	"glm-5.2":       {1.40, 4.40},  // Z.ai
	"glm-4.7":       {0.40, 1.75},  // Z.ai
	"glm-4.5-air":   {0.13, 0.85},  // Z.ai
	"mimo-v2.5":     {0.14, 0.28},  // Xiaomi
	"mimo-v2.5-pro": {0.435, 0.87}, // Xiaomi
}

const (
	// cacheWriteMult prices 5-minute-TTL cache creation relative to the input rate.
	cacheWriteMult = 1.25
	// cacheWrite1hMult prices 1-hour-TTL cache creation relative to the input rate.
	cacheWrite1hMult = 2.0
	// cacheReadMult prices cache reads relative to the input rate.
	cacheReadMult = 0.10
)

// rateFor returns the rate whose key is the longest prefix of model. Longest-prefix
// wins so overlapping ids resolve deterministically despite random map iteration
// order (e.g. "mimo-v2.5-pro" must not match the shorter "mimo-v2.5").
func rateFor(model string) (modelRate, bool) {
	var (
		best  modelRate
		bestN = -1
		found bool
	)
	for prefix, r := range modelRates {
		if len(prefix) > bestN && strings.HasPrefix(model, prefix) {
			best, bestN, found = r, len(prefix), true
		}
	}
	return best, found
}

// ModelCostUSD returns the estimated USD cost for a model's usage and whether the
// model had a pricing entry. Input and output use the model's rates; cache-write
// is 1.25x the input rate and cache-read is 0.1x.
func ModelCostUSD(m ModelUsage) (float64, bool) {
	// A row with no tokens (e.g. Claude Code's "<synthetic>" model) costs nothing
	// regardless of rate, so treat it as priced to avoid flagging a month as an
	// approximate estimate over a $0 row.
	if m.Total() == 0 {
		return 0, true
	}
	r, ok := rateFor(m.Model)
	if !ok {
		return 0, false
	}
	inRate := r.inPerMTok / 1_000_000
	outRate := r.outPerMTok / 1_000_000
	// CacheWrite is the total; CacheWrite1h is its 1-hour-TTL subset (2x), the rest
	// is 5-minute-TTL (1.25x). Guard against a malformed subset exceeding the total.
	cw1h := m.CacheWrite1h
	if cw1h > m.CacheWrite {
		cw1h = m.CacheWrite
	}
	cw5m := m.CacheWrite - cw1h
	usd := float64(m.Input)*inRate +
		float64(m.Output)*outRate +
		float64(cw5m)*cacheWriteMult*inRate +
		float64(cw1h)*cacheWrite1hMult*inRate +
		float64(m.CacheRead)*cacheReadMult*inRate
	return usd, true
}

// CostUSD sums the cost of every priced model in the month. allPriced is false if
// any model in the month lacked a pricing entry.
func (mu MonthlyUsage) CostUSD() (float64, bool) {
	var total float64
	allPriced := true
	for _, m := range mu.Models {
		usd, priced := ModelCostUSD(m)
		if !priced {
			allPriced = false
			continue
		}
		total += usd
	}
	return total, allPriced
}
