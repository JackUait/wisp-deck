package allin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackuait/wisp-deck/internal/subusage"
)

func writeUsage(t *testing.T, path string, snap subusage.Snapshot) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestUsageFiles_sit_beside_the_configs_list(t *testing.T) {
	list := "/cfg/wisp-deck/claude-configs.list"
	if got, want := AccountUsageFile(list, "personal"), "/cfg/wisp-deck/account-usage/personal.json"; got != want {
		t.Fatalf("AccountUsageFile = %q, want %q", got, want)
	}
	// The subscription cache is the one the statusline already maintains, so
	// the path has to match templates/statusline-wrapper.sh exactly.
	if got, want := ConfigUsageFile(list, "zhipu-glm"), "/cfg/wisp-deck/subscription-usage/zhipu-glm.json"; got != want {
		t.Fatalf("ConfigUsageFile = %q, want %q", got, want)
	}
	if AccountUsageFile("", "personal") != "" || ConfigUsageFile("", "zhipu-glm") != "" {
		t.Fatal("an unknown list must yield no path")
	}
}

func TestUsage_reports_what_the_tightest_window_has_left(t *testing.T) {
	env := rosterEnv(t)
	now := time.Now()
	writeUsage(t, AccountUsageFile(env.ConfigsList, "personal"), subusage.Snapshot{
		RateLimits: subusage.RateLimits{
			FiveHour: &subusage.Window{UsedPercentage: 40, ResetAt: now.Add(time.Hour).Unix()},
			SevenDay: &subusage.Window{UsedPercentage: 89, ResetAt: now.Add(48 * time.Hour).Unix()},
		},
		FetchedAt: now.Add(-time.Minute).Unix(),
	})

	quota := Usage(env, now)["acct.personal"]
	if !quota.Known {
		t.Fatal("a fresh snapshot must read as known")
	}
	if quota.LeftPercent != 11 {
		t.Fatalf("left = %d%%, want 11%% (the 7-day window is the tighter one)", quota.LeftPercent)
	}
}

// A cached window whose reset has already passed says nothing about the window
// running now, so it must not hold a row back that has since been refilled.
func TestUsage_a_window_past_its_reset_counts_as_unused(t *testing.T) {
	env := rosterEnv(t)
	now := time.Now()
	writeUsage(t, AccountUsageFile(env.ConfigsList, "default"), subusage.Snapshot{
		RateLimits: subusage.RateLimits{
			FiveHour: &subusage.Window{UsedPercentage: 100, ResetAt: now.Add(-time.Minute).Unix()},
			SevenDay: &subusage.Window{UsedPercentage: 30, ResetAt: now.Add(72 * time.Hour).Unix()},
		},
		FetchedAt: now.Add(-time.Minute).Unix(),
	})

	quota := Usage(env, now)["acct.default"]
	if !quota.Known || quota.LeftPercent != 70 {
		t.Fatalf("quota = %+v, want 70%% left", quota)
	}
}

func TestUsage_ignores_a_snapshot_too_old_to_trust(t *testing.T) {
	env := rosterEnv(t)
	now := time.Now()
	writeUsage(t, ConfigUsageFile(env.ConfigsList, "zhipu-glm"), subusage.Snapshot{
		RateLimits: subusage.RateLimits{FiveHour: &subusage.Window{UsedPercentage: 100}},
		FetchedAt:  now.Add(-24 * time.Hour).Unix(),
	})

	if quota := Usage(env, now)["cfg.zhipu-glm"]; quota.Known {
		t.Fatalf("a day-old snapshot must read as unknown, got %+v", quota)
	}
}

func TestUsage_reports_nothing_for_a_source_with_no_cache(t *testing.T) {
	env := rosterEnv(t)
	if quota := Usage(env, time.Now())["acct.personal"]; quota.Known {
		t.Fatalf("a missing cache must read as unknown, got %+v", quota)
	}
}

func TestAnnotateUsage_puts_what_is_left_in_the_description(t *testing.T) {
	rows := []Row{
		{Model: "wisp/acct.personal/claude-opus-5", Label: "Personal · Opus 5", Description: "Claude subscription: Personal"},
		{Model: "wisp/cfg.zhipu-glm/glm-4.6", Label: "Zhipu GLM · glm-4.6", Description: "Zhipu / GLM"},
	}
	usage := map[string]Quota{"acct.personal": {LeftPercent: 11, Known: true}}

	annotated := AnnotateUsage(rows, usage)
	if want := "Claude subscription: Personal · 11% left"; annotated[0].Description != want {
		t.Fatalf("description = %q, want %q", annotated[0].Description, want)
	}
	if annotated[1].Description != "Zhipu / GLM" {
		t.Fatalf("an unknown source must not be annotated, got %q", annotated[1].Description)
	}
	if rows[0].Description != "Claude subscription: Personal" {
		t.Fatal("AnnotateUsage must not write through its input")
	}
}

func pickerOptions(t *testing.T, path string) []Row {
	t.Helper()
	picker := readPicker(t, path)
	encoded, err := json.Marshal(picker["options"])
	if err != nil {
		t.Fatal(err)
	}
	var rows []Row
	if err := json.Unmarshal(encoded, &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

// Claude Code reads the picker once per launch, so a row dropped for a spent
// quota stays gone after the quota resets. A spent row is labelled, never hidden.
func TestEnsureProfile_annotates_and_keeps_an_exhausted_source(t *testing.T) {
	env := rosterEnv(t)
	now := time.Now()
	writeUsage(t, AccountUsageFile(env.ConfigsList, "personal"), subusage.Snapshot{
		RateLimits: subusage.RateLimits{FiveHour: &subusage.Window{UsedPercentage: 100, ResetAt: now.Add(time.Hour).Unix()}},
		FetchedAt:  now.Unix(),
	})
	writeUsage(t, AccountUsageFile(env.ConfigsList, "default"), subusage.Snapshot{
		RateLimits: subusage.RateLimits{FiveHour: &subusage.Window{UsedPercentage: 25, ResetAt: now.Add(time.Hour).Unix()}},
		FetchedAt:  now.Unix(),
	})

	file, err := EnsureProfile(env, env.ConfigsList, env.ConfigsDir)
	if err != nil {
		t.Fatal(err)
	}
	kept := false
	for _, row := range pickerOptions(t, filepath.Join(env.ConfigsDir, file)) {
		if strings.HasPrefix(row.Model, "wisp/acct.personal/") {
			kept = true
			if !strings.HasSuffix(row.Description, "0% left") {
				t.Fatalf("an exhausted row must say so: %+v", row)
			}
		}
		if strings.HasPrefix(row.Model, "wisp/acct.default/") &&
			!strings.HasSuffix(row.Description, "75% left") {
			t.Fatalf("a live login was not annotated: %+v", row)
		}
	}
	if !kept {
		t.Fatal("an exhausted login was dropped from the picker")
	}
}
