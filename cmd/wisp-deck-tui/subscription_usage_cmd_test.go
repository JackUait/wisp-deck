package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackuait/wisp-deck/internal/subusage"
)

func writeSubUsageFixture(t *testing.T, root, payload string) (configsDir, list string) {
	t.Helper()
	configsDir = filepath.Join(root, "claude-configs")
	if err := os.MkdirAll(configsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	list = filepath.Join(root, "claude-configs.list")
	if err := os.WriteFile(list, []byte("Zhipu GLM:zhipu-glm.json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configsDir, "zhipu-glm.json"), []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	return configsDir, list
}

func TestSubscriptionUsageCmd_Registered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"subscription-usage"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if cmd.Name() != "subscription-usage" {
		t.Errorf("resolved to %q", cmd.Name())
	}
}

func TestSubscriptionUsageCmd_fetches_and_writes_cache(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"success":true,"data":{"limits":[
		  {"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":40,"nextResetTime":1785000000000},
		  {"type":"TOKENS_LIMIT","unit":6,"number":1,"percentage":70,"nextResetTime":1785400000000}
		]}}`))
	}))
	defer srv.Close()

	root := t.TempDir()
	configsDir, list := writeSubUsageFixture(t, root,
		`{"env":{"ANTHROPIC_BASE_URL":"`+srv.URL+`/api/anthropic","ANTHROPIC_AUTH_TOKEN":"tok"}}`)
	cache := filepath.Join(root, "subscription-usage", "zhipu-glm.json")

	execRoot(t, "subscription-usage",
		"--configs-dir", configsDir, "--list", list,
		"--config", "zhipu-glm.json", "--cache", cache)

	snap, err := subusage.ReadCache(cache)
	if err != nil {
		t.Fatalf("cache not written: %v", err)
	}
	if snap.RateLimits.FiveHour == nil || snap.RateLimits.FiveHour.UsedPercentage != 40 {
		t.Errorf("five_hour = %+v", snap.RateLimits.FiveHour)
	}
	if snap.RateLimits.SevenDay == nil || snap.RateLimits.SevenDay.UsedPercentage != 70 {
		t.Errorf("seven_day = %+v", snap.RateLimits.SevenDay)
	}
	if snap.FetchedAt == 0 || snap.CheckedAt == 0 {
		t.Errorf("timestamps not stamped: %+v", snap)
	}
}

func TestSubscriptionUsageCmd_recent_check_throttles_refetch(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"code":200,"success":true,"data":{"limits":[]}}`))
	}))
	defer srv.Close()

	root := t.TempDir()
	configsDir, list := writeSubUsageFixture(t, root,
		`{"env":{"ANTHROPIC_BASE_URL":"`+srv.URL+`/api/anthropic","ANTHROPIC_AUTH_TOKEN":"tok"}}`)
	cache := filepath.Join(root, "cache.json")

	// A cache checked seconds ago must short-circuit without a network call.
	fresh := subusage.Snapshot{Provider: "zhipu", FetchedAt: time.Now().Unix(), CheckedAt: time.Now().Unix()}
	if err := subusage.WriteCache(cache, fresh); err != nil {
		t.Fatal(err)
	}
	execRoot(t, "subscription-usage",
		"--configs-dir", configsDir, "--list", list,
		"--config", "zhipu-glm.json", "--cache", cache)
	if hits != 0 {
		t.Errorf("throttled run hit the network %d times", hits)
	}

	// A stale checked_at must refetch.
	stale := subusage.Snapshot{Provider: "zhipu", CheckedAt: time.Now().Unix() - 3600}
	if err := subusage.WriteCache(cache, stale); err != nil {
		t.Fatal(err)
	}
	execRoot(t, "subscription-usage",
		"--configs-dir", configsDir, "--list", list,
		"--config", "zhipu-glm.json", "--cache", cache)
	if hits != 1 {
		t.Errorf("stale run hit the network %d times, want 1", hits)
	}
}

func TestSubscriptionUsageCmd_fetch_failure_preserves_last_good_data(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	root := t.TempDir()
	configsDir, list := writeSubUsageFixture(t, root,
		`{"env":{"ANTHROPIC_BASE_URL":"`+srv.URL+`/api/anthropic","ANTHROPIC_AUTH_TOKEN":"tok"}}`)
	cache := filepath.Join(root, "cache.json")

	old := subusage.Snapshot{Provider: "zhipu", FetchedAt: 100, CheckedAt: 100}
	old.RateLimits.SevenDay = &subusage.Window{UsedPercentage: 61}
	if err := subusage.WriteCache(cache, old); err != nil {
		t.Fatal(err)
	}

	execRoot(t, "subscription-usage",
		"--configs-dir", configsDir, "--list", list,
		"--config", "zhipu-glm.json", "--cache", cache)

	snap, err := subusage.ReadCache(cache)
	if err != nil {
		t.Fatal(err)
	}
	if snap.RateLimits.SevenDay == nil || snap.RateLimits.SevenDay.UsedPercentage != 61 {
		t.Errorf("last good data lost on fetch failure: %+v", snap.RateLimits)
	}
	if snap.FetchedAt != 100 {
		t.Errorf("fetched_at must keep the last SUCCESS time, got %d", snap.FetchedAt)
	}
	if snap.CheckedAt == 100 {
		t.Error("checked_at must advance on a failed attempt (throttles retries)")
	}
}

func TestSubscriptionUsageCmd_unsupported_provider_writes_empty_snapshot(t *testing.T) {
	root := t.TempDir()
	configsDir := filepath.Join(root, "claude-configs")
	if err := os.MkdirAll(configsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	list := filepath.Join(root, "claude-configs.list")
	if err := os.WriteFile(list, []byte("Xiaomi MiMo:mimo.json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configsDir, "mimo.json"),
		[]byte(`{"env":{"ANTHROPIC_BASE_URL":"https://token-plan-sgp.xiaomimimo.com/anthropic","ANTHROPIC_AUTH_TOKEN":"tp-x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(root, "cache.json")

	execRoot(t, "subscription-usage",
		"--configs-dir", configsDir, "--list", list,
		"--config", "mimo.json", "--cache", cache)

	snap, err := subusage.ReadCache(cache)
	if err != nil {
		t.Fatalf("unsupported provider must still write a (empty) snapshot to throttle retries: %v", err)
	}
	if snap.RateLimits.FiveHour != nil || snap.RateLimits.SevenDay != nil {
		t.Errorf("unsupported provider must never carry data: %+v", snap.RateLimits)
	}
}
