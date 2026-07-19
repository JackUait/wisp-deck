package subusage

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// --- QuotaHost ---

func TestQuotaHost_strips_anthropic_path_from_zai_base_url(t *testing.T) {
	got := QuotaHost("https://api.z.ai/api/anthropic")
	if got != "https://api.z.ai" {
		t.Errorf("got %q, want %q", got, "https://api.z.ai")
	}
}

func TestQuotaHost_strips_path_from_bigmodel_base_url(t *testing.T) {
	got := QuotaHost("https://open.bigmodel.cn/api/anthropic")
	if got != "https://open.bigmodel.cn" {
		t.Errorf("got %q, want %q", got, "https://open.bigmodel.cn")
	}
}

func TestQuotaHost_empty_input_yields_empty(t *testing.T) {
	if got := QuotaHost(""); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// --- FetchZhipu ---

const zhipuBothWindows = `{"code":200,"msg":"ok","success":true,"data":{"limits":[
  {"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":42.5,"nextResetTime":1785000000000},
  {"type":"TOKENS_LIMIT","unit":6,"number":1,"percentage":81,"nextResetTime":1785400000000},
  {"type":"TIME_LIMIT","percentage":10}
]}}`

func TestFetchZhipu_maps_5h_and_weekly_token_limits(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/monitor/usage/quota/limit" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(zhipuBothWindows))
	}))
	defer srv.Close()

	rl, err := FetchZhipu(srv.Client(), srv.URL, "tok-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// z.ai authenticates the RAW token, no Bearer prefix.
	if gotAuth != "tok-123" {
		t.Errorf("Authorization = %q, want raw token", gotAuth)
	}
	if rl.FiveHour == nil || rl.FiveHour.UsedPercentage != 42.5 {
		t.Errorf("five_hour = %+v, want 42.5", rl.FiveHour)
	}
	if rl.FiveHour.ResetAt != 1785000000 {
		t.Errorf("five_hour reset_at = %d, want seconds", rl.FiveHour.ResetAt)
	}
	if rl.SevenDay == nil || rl.SevenDay.UsedPercentage != 81 {
		t.Errorf("seven_day = %+v, want 81", rl.SevenDay)
	}
}

func TestFetchZhipu_no_coding_plan_yields_empty_without_error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":500,"msg":"no coding plan","success":false}`))
	}))
	defer srv.Close()

	rl, err := FetchZhipu(srv.Client(), srv.URL, "tok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.FiveHour != nil || rl.SevenDay != nil {
		t.Errorf("want empty rate limits, got %+v", rl)
	}
}

func TestFetchZhipu_http_error_is_an_error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	if _, err := FetchZhipu(srv.Client(), srv.URL, "tok"); err == nil {
		t.Fatal("want error on HTTP 502")
	}
}

func TestFetchZhipu_ignores_unknown_window_shapes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"success":true,"data":{"limits":[
		  {"type":"TOKENS_LIMIT","unit":9,"number":9,"percentage":33}
		]}}`))
	}))
	defer srv.Close()

	rl, err := FetchZhipu(srv.Client(), srv.URL, "tok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.FiveHour != nil || rl.SevenDay != nil {
		t.Errorf("unknown unit/number must not map to a window, got %+v", rl)
	}
}

// --- FetchChatGPT ---

func writeCodexAuth(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "auth.json")
	data := `{"tokens":{"access_token":"at-1","account_id":"acct-1"}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFetchChatGPT_classifies_windows_by_duration_not_position(t *testing.T) {
	var gotAuth, gotAcct string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wham/usage" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		gotAcct = r.Header.Get("chatgpt-account-id")
		// primary is the WEEKLY window here (Pro accounts do this); secondary 5h.
		_, _ = w.Write([]byte(`{"plan_type":"pro","rate_limit":{
		  "primary_window":{"used_percent":3,"limit_window_seconds":604800,"reset_after_seconds":600391,"reset_at":1785077039},
		  "secondary_window":{"used_percent":55,"limit_window_seconds":18000,"reset_after_seconds":100,"reset_at":1785000000}
		}}`))
	}))
	defer srv.Close()

	auth := writeCodexAuth(t, t.TempDir())
	rl, err := FetchChatGPT(srv.Client(), srv.URL, auth)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer at-1" {
		t.Errorf("Authorization = %q, want Bearer token", gotAuth)
	}
	if gotAcct != "acct-1" {
		t.Errorf("chatgpt-account-id = %q, want acct-1", gotAcct)
	}
	if rl.SevenDay == nil || rl.SevenDay.UsedPercentage != 3 {
		t.Errorf("seven_day = %+v, want 3 (the 604800s window)", rl.SevenDay)
	}
	if rl.SevenDay.ResetAt != 1785077039 {
		t.Errorf("seven_day reset_at = %d", rl.SevenDay.ResetAt)
	}
	if rl.FiveHour == nil || rl.FiveHour.UsedPercentage != 55 {
		t.Errorf("five_hour = %+v, want 55 (the 18000s window)", rl.FiveHour)
	}
}

func TestFetchChatGPT_null_secondary_window_leaves_five_hour_absent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"plan_type":"pro","rate_limit":{
		  "primary_window":{"used_percent":3,"limit_window_seconds":604800,"reset_at":1},
		  "secondary_window":null
		}}`))
	}))
	defer srv.Close()

	auth := writeCodexAuth(t, t.TempDir())
	rl, err := FetchChatGPT(srv.Client(), srv.URL, auth)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.FiveHour != nil {
		t.Errorf("five_hour must be absent, got %+v", rl.FiveHour)
	}
	if rl.SevenDay == nil {
		t.Error("seven_day must be present")
	}
}

func TestFetchChatGPT_missing_auth_file_is_an_error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	if _, err := FetchChatGPT(srv.Client(), srv.URL, filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("want error when codex auth.json is missing")
	}
}

func TestFetchChatGPT_http_error_is_an_error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	auth := writeCodexAuth(t, t.TempDir())
	if _, err := FetchChatGPT(srv.Client(), srv.URL, auth); err == nil {
		t.Fatal("want error on HTTP 401")
	}
}

// --- Fetch (provider dispatch) ---

func writeConfigs(t *testing.T, root string, listLines string, files map[string]string) (configsDir, listFile string) {
	t.Helper()
	configsDir = filepath.Join(root, "claude-configs")
	if err := os.MkdirAll(configsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	listFile = filepath.Join(root, "claude-configs.list")
	if err := os.WriteFile(listFile, []byte(listLines), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(configsDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return configsDir, listFile
}

func TestFetch_zhipu_config_queries_quota_host_derived_from_base_url(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(zhipuBothWindows))
	}))
	defer srv.Close()

	root := t.TempDir()
	configsDir, listFile := writeConfigs(t, root, "Zhipu GLM:zhipu-glm.json\n", map[string]string{
		"zhipu-glm.json": `{"env":{"ANTHROPIC_BASE_URL":"` + srv.URL + `/api/anthropic","ANTHROPIC_AUTH_TOKEN":"tok"}}`,
	})

	snap, ok, err := Fetch(srv.Client(), configsDir, listFile, "zhipu-glm.json", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("zhipu must be a supported provider")
	}
	if snap.Provider != "zhipu" {
		t.Errorf("provider = %q", snap.Provider)
	}
	if snap.RateLimits.SevenDay == nil || snap.RateLimits.SevenDay.UsedPercentage != 81 {
		t.Errorf("seven_day = %+v", snap.RateLimits.SevenDay)
	}
}

func TestFetch_mimo_config_is_unsupported(t *testing.T) {
	root := t.TempDir()
	configsDir, listFile := writeConfigs(t, root, "Xiaomi MiMo:mimo.json\n", map[string]string{
		"mimo.json": `{"env":{"ANTHROPIC_BASE_URL":"https://token-plan-sgp.xiaomimimo.com/anthropic","ANTHROPIC_AUTH_TOKEN":"tp-x"}}`,
	})
	_, ok, err := Fetch(http.DefaultClient, configsDir, listFile, "mimo.json", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("mimo has no usage API; must report unsupported, never fake data")
	}
}

// --- cache ---

func TestWriteCache_then_ReadCache_roundtrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "cache.json")
	snap := Snapshot{Provider: "zhipu", FetchedAt: 123, CheckedAt: 456}
	snap.RateLimits.FiveHour = &Window{UsedPercentage: 42.5, ResetAt: 99}
	if err := WriteCache(path, snap); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadCache(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Provider != "zhipu" || got.FetchedAt != 123 || got.CheckedAt != 456 {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
	if got.RateLimits.FiveHour == nil || got.RateLimits.FiveHour.UsedPercentage != 42.5 {
		t.Errorf("five_hour mismatch: %+v", got.RateLimits.FiveHour)
	}
}

// The cache is consumed by lib/statusline.sh's gt_five_hour_used_pct /
// gt_weekly_used_pct, which sed-match `"five_hour":{..."used_percentage":N`
// inside the serialized JSON. Guard the exact key spelling.
func TestWriteCache_serializes_statusline_compatible_shape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	snap := Snapshot{Provider: "openai-chatgpt", FetchedAt: 1}
	snap.RateLimits.FiveHour = &Window{UsedPercentage: 55}
	snap.RateLimits.SevenDay = &Window{UsedPercentage: 3}
	if err := WriteCache(path, snap); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"rate_limits":{`,
		`"five_hour":{"used_percentage":55}`,
		`"seven_day":{"used_percentage":3}`,
		`"fetched_at":1`,
	} {
		if !contains(string(data), want) {
			t.Errorf("cache JSON missing %q in %s", want, data)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
