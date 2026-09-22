package allin

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Listed is one model a source's live list reports.
type Listed struct {
	ID      string    `json:"id"`
	Label   string    `json:"label,omitempty"`
	Created time.Time `json:"created,omitempty"`
	Context int       `json:"context,omitempty"`
}

// A version segment is a number, optionally behind a one- or two-letter tag
// (v4, k3, k2.7). The tag stays in the family so kimi-k3 and k3 differ.
var versionSegment = regexp.MustCompile(`^([a-z]{0,2})(\d+(?:\.\d+)*)$`)

// An 8-digit segment is a snapshot date, not a version: haiku-4-5-20251001
// must share a family and a version with haiku-4-5.
var dateSegment = regexp.MustCompile(`^\d{8}$`)

func familyOf(id string) (family string, version []int, date string) {
	var parts []string
	for _, seg := range strings.Split(strings.ToLower(id), "-") {
		if dateSegment.MatchString(seg) {
			date = seg
			continue
		}
		if m := versionSegment.FindStringSubmatch(seg); m != nil {
			if m[1] != "" {
				parts = append(parts, m[1])
			}
			for _, n := range strings.Split(m[2], ".") {
				v, _ := strconv.Atoi(n)
				version = append(version, v)
			}
			continue
		}
		parts = append(parts, seg)
	}
	return strings.Join(parts, "-"), version, date
}

func compareVersions(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return len(a) - len(b)
}

// newer reports whether a should replace b in their shared family. Dates are
// compared before Created because some sources (Kimi) give every model the
// same Created.
func newer(a, b Listed) bool {
	_, av, ad := familyOf(a.ID)
	_, bv, bd := familyOf(b.ID)
	if c := compareVersions(av, bv); c != 0 {
		return c > 0
	}
	if ad != bd {
		return ad > bd
	}
	return a.Created.After(b.Created)
}

// Latest keeps the newest model of each family, in the order each family
// first appears in the listing.
func Latest(models []Listed) []Listed {
	var order []string
	best := map[string]Listed{}
	for _, m := range models {
		if m.ID == "" {
			continue
		}
		family, _, _ := familyOf(m.ID)
		current, seen := best[family]
		if !seen {
			order = append(order, family)
			best[family] = m
			continue
		}
		if newer(m, current) {
			best[family] = m
		}
	}
	out := make([]Listed, 0, len(order))
	for _, family := range order {
		out = append(out, best[family])
	}
	return out
}
