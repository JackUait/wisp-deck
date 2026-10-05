package allin

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackuait/wisp-deck/internal/subusage"
)

// Remaining limits per picker row. Every roster source already has a usage
// cache maintained for the statusline's 5h/7d bars, so the picker reads those
// same files rather than reaching for a network on a path that writes a
// settings file.
//
// Claude Code snapshots modelPicker ONCE, when the session launches — verified
// live: editing a profile under a running pane left /model showing the old rows
// and never showed a row added after launch. So an annotation is only ever as
// fresh as the last EnsureProfile call, and a row can never be re-annotated
// mid-session. That is why no reading ever hides a row: a hidden row stays
// hidden until the next launch, even after its quota resets.

// usageFreshFor bounds how old a cached snapshot may be and still be believed.
// It matches gt_sub_usage_fresh's display gate in lib/statusline.sh, so the
// picker and the statusline never disagree about whether a number is real.
const usageFreshFor = 2 * time.Hour

// Quota is what one roster source has left of its rate limits. Known is false
// for a source with no cache, an unreadable one, a stale one, or a provider
// that reports no windows at all.
type Quota struct {
	LeftPercent int
	Known       bool
}

// Text is how a quota reads beside a row's label.
func (q Quota) Text() string {
	if !q.Known {
		return ""
	}
	return fmt.Sprintf("%d%% left", q.LeftPercent)
}

// AccountUsageFile is where a Claude login's cached usage lives.
func AccountUsageFile(listFile, dir string) string {
	if listFile == "" || dir == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(listFile), "account-usage", dir+".json")
}

// ConfigUsageFile is where a provider profile's cached usage lives. The path is
// the one templates/statusline-wrapper.sh already writes, so both surfaces
// share one cache and one refresh.
func ConfigUsageFile(listFile, source string) string {
	if listFile == "" || source == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(listFile), "subscription-usage", source+".json")
}

// SourceKey reduces a picker row to the credential it spends, in the form the
// usage map is keyed by ("acct.default", "cfg.zhipu-glm"). A row this build
// does not route returns "".
func SourceKey(model string) string {
	switch target := Route(model); target.Kind {
	case KindAccount:
		return accountPrefix + target.Source
	case KindConfig:
		return configPrefix + target.Source
	}
	return ""
}

func usageFile(listFile, key string) string {
	switch {
	case strings.HasPrefix(key, accountPrefix):
		return AccountUsageFile(listFile, key[len(accountPrefix):])
	case strings.HasPrefix(key, configPrefix):
		return ConfigUsageFile(listFile, key[len(configPrefix):])
	}
	return ""
}

// Usage reads every roster source's cached quota, keyed by SourceKey. One read
// per source, not per row: a login contributes four rows that all spend it.
func Usage(env Env, now time.Time) map[string]Quota {
	quotas := map[string]Quota{}
	for _, row := range Roster(env) {
		key := SourceKey(row.Model)
		if key == "" {
			continue
		}
		if _, seen := quotas[key]; seen {
			continue
		}
		quotas[key] = readQuota(usageFile(env.ConfigsList, key), now)
	}
	return quotas
}

func readQuota(path string, now time.Time) Quota {
	if path == "" {
		return Quota{}
	}
	snap, err := subusage.ReadCache(path)
	if err != nil {
		return Quota{}
	}
	return quotaFromSnapshot(snap, now)
}

func quotaFromSnapshot(snap subusage.Snapshot, now time.Time) Quota {
	if snap.FetchedAt <= 0 || now.Sub(time.Unix(snap.FetchedAt, 0)) > usageFreshFor {
		return Quota{}
	}
	used, seen := 0.0, false
	for _, window := range []*subusage.Window{snap.RateLimits.FiveHour, snap.RateLimits.SevenDay} {
		if window == nil {
			continue
		}
		seen = true
		// A window whose reset has already passed describes a window that no
		// longer exists. Counting its usage would hold a row back for hours
		// after the limit it names refilled.
		if window.ResetAt > 0 && !time.Unix(window.ResetAt, 0).After(now) {
			continue
		}
		if window.UsedPercentage > used {
			used = window.UsedPercentage
		}
	}
	if !seen {
		return Quota{}
	}
	// The tightest window is what the session actually runs out of, and the
	// figure is rounded DOWN so a row never claims more room than it has.
	left := int(math.Floor(100 - used))
	if left < 0 {
		left = 0
	}
	if left > 100 {
		left = 100
	}
	return Quota{LeftPercent: left, Known: true}
}

// AnnotateUsage returns the rows with each known quota appended to the row's
// description, which is the second line Claude Code's /model picker renders.
// The input is never written through: the caller's roster is shared.
func AnnotateUsage(rows []Row, usage map[string]Quota) []Row {
	out := make([]Row, len(rows))
	copy(out, rows)
	for i, row := range out {
		text := usage[SourceKey(row.Model)].Text()
		if text == "" {
			continue
		}
		if description := strings.TrimSpace(row.Description); description != "" {
			text = description + " · " + text
		}
		out[i].Description = text
	}
	return out
}
