package allin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeResolver struct {
	credential Credential
	err        error
}

func (f fakeResolver) Resolve(Target) (Credential, error) { return f.credential, f.err }

func TestHandler_swaps_the_credential_and_strips_the_row_prefix(t *testing.T) {
	var gotAuth, gotBeta string
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotBeta = r.Header.Get("Anthropic-Beta")
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	handler := NewHandler(fakeResolver{credential: Credential{
		BaseURL: upstream.URL, Header: "Authorization", Value: "Bearer swapped",
	}}, "http://unused.invalid")

	request := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"wisp/acct.personal/claude-opus-5[1m]"}`))
	request.Header.Set("Authorization", "Bearer session")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if gotAuth != "Bearer swapped" {
		t.Fatalf("auth %q", gotAuth)
	}
	if !strings.Contains(gotBeta, "context-1m-2025-08-07") {
		t.Fatalf("beta %q", gotBeta)
	}
	var sent map[string]any
	_ = json.Unmarshal(gotBody, &sent)
	if sent["model"] != "claude-opus-5" {
		t.Fatalf("model %v", sent["model"])
	}
}

func TestHandler_leaves_a_session_row_untouched(t *testing.T) {
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	handler := NewHandler(fakeResolver{err: errors.New("must not be called")}, upstream.URL)
	request := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"claude-opus-5"}`))
	request.Header.Set("Authorization", "Bearer session")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if gotAuth != "Bearer session" {
		t.Fatalf("auth %q", gotAuth)
	}
}

func TestHandler_reports_a_stale_account_as_400_not_401(t *testing.T) {
	handler := NewHandler(fakeResolver{err: ErrStaleAccount}, "http://unused.invalid")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"wisp/acct.personal/claude-opus-5"}`)))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "personal") {
		t.Fatalf("body does not name the account: %s", recorder.Body.String())
	}
	var envelope struct {
		Type  string `json:"type"`
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("body is not JSON: %v (%s)", err, recorder.Body.String())
	}
	if envelope.Type != "error" || envelope.Error.Type != "invalid_request_error" {
		t.Fatalf("envelope %+v", envelope)
	}
}

// The 400 body is all the user sees of a failed login, so it must be the
// resolver's own advice, not a fixed line.
func TestHandler_shows_the_resolvers_advice_for_a_stale_login(t *testing.T) {
	stale := &StaleAccountError{Login: "Personal", PaneLogin: "Work", Err: fmt.Errorf("%w: invalid_grant", ErrSignedOut)}
	handler := NewHandler(fakeResolver{err: stale}, "http://unused.invalid")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"wisp/acct.personal/claude-opus-5"}`)))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status %d", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "invalid_grant") || !strings.Contains(body, `Do not run /login in this pane`) {
		t.Fatalf("body lacks the advice: %s", body)
	}
	if strings.Contains(body, "Open it once") {
		t.Fatalf("body still carries the old advice: %s", body)
	}
}

func TestHandler_routes_a_count_tokens_request_the_same_way(t *testing.T) {
	var gotPath, gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	handler := NewHandler(fakeResolver{credential: Credential{
		BaseURL: upstream.URL, Header: "Authorization", Value: "Bearer swapped",
	}}, "http://unused.invalid")
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost,
		"/v1/messages/count_tokens", strings.NewReader(`{"model":"wisp/acct.personal/claude-opus-5"}`)))

	if gotPath != "/v1/messages/count_tokens" || gotAuth != "Bearer swapped" {
		t.Fatalf("path %q auth %q", gotPath, gotAuth)
	}
}

func TestHandler_rejects_a_body_over_the_routing_cap(t *testing.T) {
	reached := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	handler := NewHandler(fakeResolver{err: errors.New("must not be called")}, upstream.URL)
	oversized := strings.Repeat("a", maxRouteBytes+1)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(oversized)))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status %d", recorder.Code)
	}
	if reached {
		t.Fatal("upstream must not be reached for a body over the routing cap")
	}
}

func TestHandler_routes_correctly_when_content_length_is_unknown(t *testing.T) {
	var gotAuth string
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	handler := NewHandler(fakeResolver{credential: Credential{
		BaseURL: upstream.URL, Header: "Authorization", Value: "Bearer swapped",
	}}, "http://unused.invalid")

	request := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"wisp/acct.personal/claude-opus-5"}`))
	// A chunked request declares no length up front; ContentLength reads -1.
	request.ContentLength = -1
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if gotAuth != "Bearer swapped" {
		t.Fatalf("auth %q", gotAuth)
	}
	var sent map[string]any
	_ = json.Unmarshal(gotBody, &sent)
	if sent["model"] != "claude-opus-5" {
		t.Fatalf("model %v, body %s", sent["model"], gotBody)
	}
}

func TestHandler_reports_a_malformed_upstream_as_400_not_502(t *testing.T) {
	handler := NewHandler(fakeResolver{credential: Credential{
		BaseURL: "http://exa mple.com", Header: "Authorization", Value: "Bearer x",
	}}, "http://unused.invalid")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"wisp/acct.personal/claude-opus-5"}`)))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status %d, body %s", recorder.Code, recorder.Body.String())
	}
}

// The 400-not-502/500 guarantee must hold for a NeedsRepair target too:
// validUpstream has to run BEFORE the rolefix delegation, or a malformed
// address reaches rolefix.NewHandler's own url.Parse failure path, which
// answers 500 rather than this package's deterministic 400.
func TestHandler_reports_a_malformed_upstream_as_400_not_502_for_a_repaired_target(t *testing.T) {
	handler := NewHandler(fakeResolver{credential: Credential{
		BaseURL: "http://exa mple.com", Header: "Authorization", Value: "Bearer x", NeedsRepair: true,
	}}, "http://unused.invalid")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"wisp/cfg.featherless/some-model"}`)))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status %d, body %s", recorder.Code, recorder.Body.String())
	}
}

func TestHandler_clears_both_credential_headers_before_swapping_in_one(t *testing.T) {
	// credential.Header == "Authorization" already deleted X-Api-Key on its
	// own, so the defect only shows through a credential that swaps in the
	// OTHER header: does the session's own Authorization survive and leak
	// upstream alongside it?
	var gotAuth, gotKey string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotKey = r.Header.Get("X-Api-Key")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	handler := NewHandler(fakeResolver{credential: Credential{
		BaseURL: upstream.URL, Header: "X-Api-Key", Value: "swapped-key",
	}}, "http://unused.invalid")

	request := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"wisp/acct.personal/claude-opus-5"}`))
	request.Header.Set("Authorization", "Bearer session")
	request.Header.Set("X-Api-Key", "session-key")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if gotKey != "swapped-key" {
		t.Fatalf("x-api-key %q", gotKey)
	}
	if gotAuth != "" {
		t.Fatalf("authorization leaked: %q", gotAuth)
	}
}

func TestHandler_streams_the_first_chunk_before_the_second_is_sent(t *testing.T) {
	// httputil.ReverseProxy hardcodes immediate flushing whenever
	// ContentLength == -1 or Content-Type is text/event-stream, regardless of
	// the configured FlushInterval — so only a response that declares a
	// Content-Length and isn't SSE actually exercises FlushInterval: -1.
	release := make(chan struct{})
	var closeOnce sync.Once
	closeRelease := func() { closeOnce.Do(func() { close(release) }) }
	t.Cleanup(closeRelease)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "11")
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte("first"))
		flusher.Flush()
		select {
		case <-release:
		case <-time.After(2 * time.Second):
		}
		_, _ = w.Write([]byte("second"))
	}))
	defer upstream.Close()

	handler := NewHandler(fakeResolver{credential: Credential{
		BaseURL: upstream.URL, Header: "Authorization", Value: "Bearer swapped",
	}}, "http://unused.invalid")
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := http.Post(proxy.URL+"/v1/messages", "application/json",
			strings.NewReader(`{"model":"wisp/acct.personal/claude-opus-5"}`))
		if err != nil {
			t.Error(err)
			return
		}
		defer resp.Body.Close()
		buf := make([]byte, 5)
		if _, err := io.ReadFull(resp.Body, buf); err != nil {
			t.Error(err)
			return
		}
		if string(buf) != "first" {
			t.Errorf("chunk %q", buf)
		}
	}()

	select {
	case <-done:
		closeRelease()
	case <-time.After(1 * time.Second):
		closeRelease()
		<-done // let the goroutine finish before this test function returns
		t.Fatal("first chunk was not observed before the upstream sent its second write")
	}
}

// errReader fails partway through a body, the way a client that goes away does.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

// writeRoutingError renders "wisp-deck: %v" itself, so an error that already
// carries that prefix reaches the user as "wisp-deck: wisp-deck: …". Every
// error this package raises uses the package's own "allin:" prefix instead.
func TestHandler_never_doubles_the_wisp_deck_prefix(t *testing.T) {
	cases := map[string]func() *http.Request{
		"body over the cap": func() *http.Request {
			return httptest.NewRequest(http.MethodPost, "/v1/messages",
				strings.NewReader(strings.Repeat("a", maxRouteBytes+1)))
		},
		"body that cannot be read": func() *http.Request {
			return httptest.NewRequest(http.MethodPost, "/v1/messages", errReader{})
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			handler := NewHandler(fakeResolver{err: errors.New("must not be called")},
				"http://unused.invalid")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, build())
			if body := recorder.Body.String(); strings.Contains(body, "wisp-deck: wisp-deck:") {
				t.Fatalf("doubled prefix: %s", body)
			}
		})
	}
}

func TestHandler_never_doubles_the_prefix_on_a_malformed_upstream(t *testing.T) {
	handler := NewHandler(fakeResolver{credential: Credential{
		BaseURL: "http://exa mple.com", Header: "Authorization", Value: "Bearer x",
	}}, "http://unused.invalid")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"wisp/acct.personal/claude-opus-5"}`)))
	if body := recorder.Body.String(); strings.Contains(body, "wisp-deck: wisp-deck:") {
		t.Fatalf("doubled prefix: %s", body)
	}
}

// A chunked request declares ContentLength -1, so a cap enforced on that field
// never fires and io.LimitReader(body, max) hands back a silently truncated
// body with a nil error. The read has to go one byte past the cap.
func TestHandler_rejects_an_over_cap_body_that_declares_no_length(t *testing.T) {
	reached := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	handler := NewHandler(fakeResolver{err: errors.New("must not be called")}, upstream.URL)
	request := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(strings.Repeat("a", maxRouteBytes+1)))
	request.ContentLength = -1
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status %d", recorder.Code)
	}
	if reached {
		t.Fatal("a truncated body was forwarded instead of rejected")
	}
}

// A compressed body is bytes the next hop cannot read, and this Director-level
// constraint has no other symptom: the turn simply fails at the endpoint.
func TestHandler_asks_the_upstream_for_an_undecoded_body(t *testing.T) {
	var gotEncoding string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEncoding = r.Header.Get("Accept-Encoding")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	handler := NewHandler(fakeResolver{credential: Credential{
		BaseURL: upstream.URL, Header: "Authorization", Value: "Bearer swapped",
	}}, "http://unused.invalid")
	request := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"wisp/acct.personal/claude-opus-5"}`))
	request.Header.Set("Accept-Encoding", "gzip, br")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if gotEncoding != "identity" {
		t.Fatalf("Accept-Encoding %q, want identity", gotEncoding)
	}
}

// Both of these are unreachable through the handler today, and only because two
// non-local invariants hold: Route returns KindSession for the empty id a nil
// payload produces, and a payload decoded from JSON always re-encodes. Failing
// open here means the credential is swapped while the wisp/… id is NOT, so a
// third-party endpoint receives a routing id it cannot answer — carrying
// someone's real credential.
func TestRewriteModel_refuses_a_body_it_cannot_re_address(t *testing.T) {
	if _, err := rewriteModel(nil, "claude-opus-5", nil); err == nil {
		t.Fatal("a nil payload was accepted; assigning into it panics")
	}
	// A channel has no JSON encoding, so Marshal fails on the whole object.
	if _, err := rewriteModel(map[string]any{"x": make(chan int)}, "claude-opus-5", nil); err == nil {
		t.Fatal("an unencodable payload was accepted")
	}
}

func TestRewriteModel_replaces_the_routed_id(t *testing.T) {
	got, err := rewriteModel(map[string]any{
		"model": "wisp/acct.personal/claude-opus-5", "max_tokens": float64(8),
	}, "claude-opus-5", nil)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["model"] != "claude-opus-5" || decoded["max_tokens"] != float64(8) {
		t.Fatalf("decoded %v", decoded)
	}
}

// A Credential.NeedsRepair target (Featherless) must go through
// internal/rolefix's own handler rather than the plain reverse proxy: Claude
// Code's role:"system" capability listings 400 on Featherless's strict schema,
// and a request declaring "thinking" turns its tool-call parser off. Both must
// be gone by the time the upstream sees the body. This also proves the
// NewHandler-delegation premise: the credential this handler swapped in
// (Authorization: Bearer swapped) must survive into the upstream call, because
// rolefix's own Director never touches that header.
func TestHandler_repairs_a_featherless_request_before_it_reaches_the_upstream(t *testing.T) {
	var gotAuth string
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	handler := NewHandler(fakeResolver{credential: Credential{
		BaseURL: upstream.URL, Header: "Authorization", Value: "Bearer swapped", NeedsRepair: true,
	}}, "http://unused.invalid")

	body := `{"model":"wisp/cfg.featherless/TurboVadim/Qwen3.8-27B-OBLITERATED",` +
		`"thinking":{"type":"adaptive","display":"omitted"},` +
		`"messages":[{"role":"system","content":"agent roster"},{"role":"user","content":"hi"}]}`
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(body)))

	if gotAuth != "Bearer swapped" {
		t.Fatalf("credential did not survive delegation to rolefix: auth %q", gotAuth)
	}
	var sent map[string]any
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("upstream body is not JSON: %v (%s)", err, gotBody)
	}
	if sent["model"] != "TurboVadim/Qwen3.8-27B-OBLITERATED" {
		t.Fatalf("routed id not rewritten to the real model: %v", sent["model"])
	}
	if _, ok := sent["thinking"]; ok {
		t.Fatalf("thinking field reached Featherless, which turns its tool-call parser off: %s", gotBody)
	}
	messages, _ := sent["messages"].([]any)
	if len(messages) == 0 {
		t.Fatalf("no messages in %s", gotBody)
	}
	first, _ := messages[0].(map[string]any)
	if first["role"] != "user" {
		t.Fatalf("role:\"system\" reached Featherless unrewritten: %s", gotBody)
	}
}

// A target that needs no repair (an ordinary Anthropic-speaking gateway) must
// go through the plain reverse proxy unchanged: rolefix's rewrite must never
// run on a request that has no reason to be touched.
func TestHandler_leaves_an_unrepaired_target_untouched(t *testing.T) {
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	handler := NewHandler(fakeResolver{credential: Credential{
		BaseURL: upstream.URL, Header: "Authorization", Value: "Bearer swapped", NeedsRepair: false,
	}}, "http://unused.invalid")

	body := `{"model":"wisp/cfg.zhipu-glm/glm-4.7",` +
		`"thinking":{"type":"adaptive","display":"omitted"},` +
		`"messages":[{"role":"system","content":"agent roster"}]}`
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(body)))

	var sent map[string]any
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("upstream body is not JSON: %v (%s)", err, gotBody)
	}
	if _, ok := sent["thinking"]; !ok {
		t.Fatalf("thinking field was stripped from a target that needs no repair: %s", gotBody)
	}
	messages, _ := sent["messages"].([]any)
	first, _ := messages[0].(map[string]any)
	if first["role"] != "system" {
		t.Fatalf("role rewritten for a target that needs no repair: %s", gotBody)
	}
}

// FlushInterval: -1 is what keeps Claude Code's byte-stall watchdog from
// aborting and replaying a turn while Featherless sends ": keep-alive"
// comments before its first token. rolefix.NewHandler sets this itself, but
// delegation must not wrap it in anything that buffers.
func TestHandler_streams_a_repaired_targets_first_chunk_before_the_second_is_sent(t *testing.T) {
	release := make(chan struct{})
	var closeOnce sync.Once
	closeRelease := func() { closeOnce.Do(func() { close(release) }) }
	t.Cleanup(closeRelease)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "11")
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte("first"))
		flusher.Flush()
		select {
		case <-release:
		case <-time.After(2 * time.Second):
		}
		_, _ = w.Write([]byte("second"))
	}))
	defer upstream.Close()

	handler := NewHandler(fakeResolver{credential: Credential{
		BaseURL: upstream.URL, Header: "Authorization", Value: "Bearer swapped", NeedsRepair: true,
	}}, "http://unused.invalid")
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := http.Post(proxy.URL+"/v1/messages", "application/json",
			strings.NewReader(`{"model":"wisp/cfg.featherless/some-model"}`))
		if err != nil {
			t.Error(err)
			return
		}
		defer resp.Body.Close()
		buf := make([]byte, 5)
		if _, err := io.ReadFull(resp.Body, buf); err != nil {
			t.Error(err)
			return
		}
		if string(buf) != "first" {
			t.Errorf("chunk %q", buf)
		}
	}()

	select {
	case <-done:
		closeRelease()
	case <-time.After(1 * time.Second):
		closeRelease()
		<-done
		t.Fatal("first chunk was not observed before the upstream sent its second write")
	}
}
