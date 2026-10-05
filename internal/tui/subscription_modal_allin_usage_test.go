package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/jackuait/wisp-deck/internal/allin"
	"github.com/jackuait/wisp-deck/internal/subusage"
)

// writeAllInUsage puts a fresh reading in the cache the picker annotates from.
func writeAllInUsage(t *testing.T, path string, usedPercent float64) {
	t.Helper()
	now := time.Now().Unix()
	if err := subusage.WriteCache(path, subusage.Snapshot{
		RateLimits: subusage.RateLimits{FiveHour: &subusage.Window{UsedPercentage: usedPercent}},
		FetchedAt:  now,
		CheckedAt:  now,
	}); err != nil {
		t.Fatal(err)
	}
}

// allInDefaultRow is the roster row spending the implicit login, which is the
// one login this fixture can write a usage cache for by name.
func allInDefaultRow(t *testing.T, rows []allin.Row) allin.Row {
	t.Helper()
	for _, row := range rows {
		if allin.SourceKey(row.Model) == "acct.default" {
			return row
		}
	}
	t.Fatalf("no roster row spends the default login: %+v", rows)
	return allin.Row{}
}

// The pane has to show the same numbers the picker was annotated with.
func TestSubscriptionModal_allInRowsShowWhatEachSubscriptionHasLeft(t *testing.T) {
	m, rows := allInSubscriptionMenu(t)
	row := allInDefaultRow(t, rows)
	writeAllInUsage(t, allin.AccountUsageFile(m.claudeConfigsList, "default"), 77)

	m.loadAllInChecklist()

	if detail := allInDetail(t, m); !strings.Contains(detail, row.Label+" · 23% left") {
		t.Fatalf("row %q does not carry what its login has left:\n%s", row.Label, detail)
	}
}

// A spent subscription is labelled, never hidden, so the pane offers no switch
// to hide it.
func TestSubscriptionModal_allInOffersNoSpentFilter(t *testing.T) {
	m, _ := allInSubscriptionMenu(t)

	if detail := allInDetail(t, m); strings.Contains(detail, "no limits left") {
		t.Fatalf("the pane still offers a spent-row filter:\n%s", detail)
	}
	got := m.subscriptionDetailRows()
	if len(got) == 0 || got[0] != subscriptionDetailAllInBase {
		t.Fatalf("detail rows = %v, want the checklist first", got)
	}
	if m.subscriptionModal.detailCursor != subscriptionDetailAllInBase {
		t.Fatalf("cursor opened on %d, want the first checklist row %d",
			m.subscriptionModal.detailCursor, subscriptionDetailAllInBase)
	}
}
