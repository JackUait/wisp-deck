package usage

import (
	"strings"
	"testing"

	"github.com/jackuait/wisp-deck/internal/claudeconfig"
)

// inheritsFamilyRate lists catalog ids that are priced by a shorter key on
// purpose. Every other catalog id must have its own entry in modelRates: a
// new tier sibling ("gpt-6-sol" under "gpt-6") silently takes its family's
// rate, which billed Sol at Astra's price, 5x too high.
var inheritsFamilyRate = map[string]string{
	"gpt-5.6-terra":       "aggregators disagree ($2/$12 vs $2.50/$15) and OpenAI's page was unreachable",
	"gpt-5.6-luna":        "aggregators disagree ($0.20/$1.20 vs $1/$6) and OpenAI's page was unreachable",
	"gpt-5.3-codex-spark": "a ChatGPT research preview that never had API pricing",
}

func TestEveryCatalogModelHasItsOwnPrice(t *testing.T) {
	for _, m := range claudeconfig.CatalogModels() {
		if _, ok := inheritsFamilyRate[m.ID]; ok {
			continue
		}
		key := ""
		for k := range modelRates {
			if strings.HasPrefix(m.ID, k) && len(k) > len(key) {
				key = k
			}
		}
		if key != m.ID {
			t.Errorf("%s is priced by %q, not its own entry: add %q to modelRates with its published rate, "+
				"or list it in inheritsFamilyRate with the reason", m.ID, key, m.ID)
		}
	}
}

func TestInheritsFamilyRateNamesOnlyCatalogModels(t *testing.T) {
	catalog := map[string]bool{}
	for _, m := range claudeconfig.CatalogModels() {
		catalog[m.ID] = true
	}
	for id := range inheritsFamilyRate {
		if !catalog[id] {
			t.Errorf("%s is no longer in the catalog: remove it from inheritsFamilyRate", id)
		}
		if _, own := modelRates[id]; own {
			t.Errorf("%s now has its own entry: remove it from inheritsFamilyRate", id)
		}
	}
}
