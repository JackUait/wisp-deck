package allin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type recordingResolver struct {
	mu       sync.Mutex
	targets  []Target
	upstream string
}

func (r *recordingResolver) Resolve(target Target) (Credential, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.targets = append(r.targets, target)
	return Credential{BaseURL: r.upstream, Header: "Authorization", Value: "Bearer routed"}, nil
}

func (r *recordingResolver) last() Target {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.targets) == 0 {
		return Target{}
	}
	return r.targets[len(r.targets)-1]
}

type seenRequest struct {
	model, auth, beta string
}

func fastFixture(t *testing.T, fast FastRoute) (http.Handler, *recordingResolver, func() seenRequest) {
	t.Helper()
	var mu sync.Mutex
	var seen seenRequest
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var sent map[string]any
		_ = json.Unmarshal(body, &sent)
		model, _ := sent["model"].(string)
		mu.Lock()
		seen = seenRequest{model: model, auth: r.Header.Get("Authorization"), beta: r.Header.Get("Anthropic-Beta")}
		mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(upstream.Close)
	resolver := &recordingResolver{upstream: upstream.URL}
	handler := NewRoutingHandler(resolver, upstream.URL, nil, fast)
	return handler, resolver, func() seenRequest { mu.Lock(); defer mu.Unlock(); return seen }
}

func postModel(t *testing.T, handler http.Handler, model string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"`+model+`"}`))
	request.Header.Set("Authorization", "Bearer session")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s: status %d: %s", model, recorder.Code, recorder.Body.String())
	}
}

func deepseekFast(source string) string {
	if source == "deepseek" {
		return "deepseek-flash"
	}
	return ""
}

func TestFastCall_goes_to_haiku_on_the_last_account_row(t *testing.T) {
	handler, resolver, seen := fastFixture(t, FastRoute{})
	postModel(t, handler, "wisp/acct.work/claude-opus-5-5[1m]")
	postModel(t, handler, FastModel)
	want := Target{Kind: KindAccount, Source: "work", Model: "claude-haiku-4-5-20251001"}
	if got := resolver.last(); got != want {
		t.Fatalf("resolved %+v, want %+v", got, want)
	}
	if got := seen(); got.model != "claude-haiku-4-5-20251001" || strings.Contains(got.beta, "context-1m") {
		t.Fatalf("upstream saw %+v", got)
	}
}

func TestFastCall_follows_a_switch_to_another_row(t *testing.T) {
	handler, resolver, seen := fastFixture(t, FastRoute{ConfigFast: deepseekFast})
	postModel(t, handler, "wisp/acct.work/claude-opus-5-5[1m]")
	postModel(t, handler, "wisp/cfg.deepseek/deepseek-pro")
	postModel(t, handler, FastModel)
	want := Target{Kind: KindConfig, Source: "deepseek", Model: "deepseek-flash"}
	if got := resolver.last(); got != want {
		t.Fatalf("resolved %+v, want %+v", got, want)
	}
	if got := seen().model; got != "deepseek-flash" {
		t.Fatalf("upstream model %q", got)
	}
}

func TestFastCall_before_any_turn_uses_the_starting_row(t *testing.T) {
	start := Target{Kind: KindConfig, Source: "deepseek", Model: "deepseek-pro"}
	handler, resolver, _ := fastFixture(t, FastRoute{Start: start, ConfigFast: deepseekFast})
	postModel(t, handler, FastModel)
	want := Target{Kind: KindConfig, Source: "deepseek", Model: "deepseek-flash"}
	if got := resolver.last(); got != want {
		t.Fatalf("resolved %+v, want %+v", got, want)
	}
}

func TestFastCall_does_not_replace_the_remembered_row(t *testing.T) {
	handler, resolver, _ := fastFixture(t, FastRoute{})
	postModel(t, handler, "wisp/acct.work/claude-opus-5-5")
	postModel(t, handler, FastModel)
	postModel(t, handler, FastModel)
	want := Target{Kind: KindAccount, Source: "work", Model: "claude-haiku-4-5-20251001"}
	if got := resolver.last(); got != want {
		t.Fatalf("second fast call resolved %+v, want %+v", got, want)
	}
}

func TestFastCall_on_the_session_login_never_sends_the_marker_upstream(t *testing.T) {
	handler, resolver, seen := fastFixture(t, FastRoute{})
	postModel(t, handler, FastModel)
	got := seen()
	if got.model != "claude-haiku-4-5-20251001" {
		t.Fatalf("upstream model %q, want haiku", got.model)
	}
	if got.auth != "Bearer session" {
		t.Fatalf("auth %q, want the session's own", got.auth)
	}
	if len(resolver.targets) != 0 {
		t.Fatalf("a session fast call resolved %+v", resolver.targets)
	}
}

func postSessionModel(t *testing.T, handler http.Handler, session, model string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"`+model+`"}`))
	request.Header.Set("Authorization", "Bearer session")
	request.Header.Set(SessionHeader, session)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s: status %d: %s", model, recorder.Code, recorder.Body.String())
	}
}

type rememberedRow struct{ session, model string }

func TestRouter_remembers_each_session_row_once_per_change(t *testing.T) {
	var got []rememberedRow
	remember := func(session, model string) { got = append(got, rememberedRow{session, model}) }
	handler, _, _ := fastFixture(t, FastRoute{Remember: remember})
	postSessionModel(t, handler, "s1", "wisp/acct.personal/claude-opus-5-5[1m]")
	postSessionModel(t, handler, "s1", "wisp/acct.personal/claude-opus-5-5[1m]")
	postSessionModel(t, handler, "s1", FastModel)
	postSessionModel(t, handler, "s2", "wisp/cfg.deepseek/deepseek-flash")
	postSessionModel(t, handler, "s1", "wisp/acct.default/claude-opus-5-5[1m]")
	postModel(t, handler, "wisp/acct.default/claude-fable-5-1")
	want := []rememberedRow{
		{"s1", "wisp/acct.personal/claude-opus-5-5[1m]"},
		{"s2", "wisp/cfg.deepseek/deepseek-flash"},
		{"s1", "wisp/acct.default/claude-opus-5-5[1m]"},
	}
	if len(got) != len(want) {
		t.Fatalf("remembered %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("remembered %+v, want %+v", got, want)
		}
	}
}
