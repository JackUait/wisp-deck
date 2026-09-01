package rolefix

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// claudeCodeBody is the shape Claude Code 2.1.252 actually sends: its capability
// listings (agent types, skills) ride in messages[] as role "system". Captured
// from a live pane on 2026-09-01.
const claudeCodeBody = `{
  "model": "moonshotai/Kimi-K3",
  "max_tokens": 32,
  "system": [{"type":"text","text":"You are Claude Code."}],
  "messages": [
    {"role":"user","content":[{"type":"text","text":"hello"}]},
    {"role":"system","content":[{"type":"text","text":"Available agent types for the Agent tool:"}]},
    {"role":"assistant","content":[{"type":"text","text":"hi"}]},
    {"role":"system","content":[{"type":"text","text":"The following skills are available"}]}
  ]
}`

func roles(t *testing.T, body []byte) []string {
	t.Helper()
	var parsed struct {
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("parse forwarded body: %v", err)
	}
	out := make([]string, len(parsed.Messages))
	for i, m := range parsed.Messages {
		out[i] = m.Role
	}
	return out
}

// Featherless validates the Anthropic schema strictly and rejects a "system"
// role in messages[] with a 400 that kills the turn. Anthropic's own API accepts
// it. Rewriting the role to "user" is what makes the identical request succeed —
// verified against the live endpoint: 400 as sent, 200 rewritten.
func TestRewrite_turns_a_system_message_into_a_user_message(t *testing.T) {
	out, changed, err := Rewrite([]byte(claudeCodeBody))
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("a body carrying system roles must report as changed")
	}
	got := roles(t, out)
	want := []string{"user", "user", "assistant", "user"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("roles = %v, want %v", got, want)
		}
	}
}

// The top-level system prompt is where Anthropic wants instructions, and it is
// accepted as-is. Touching it would change what the model is told.
func TestRewrite_leaves_the_top_level_system_prompt_alone(t *testing.T) {
	out, _, err := Rewrite([]byte(claudeCodeBody))
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	system, ok := parsed["system"].([]any)
	if !ok || len(system) != 1 {
		t.Fatalf("top-level system = %#v, want the original one-block list", parsed["system"])
	}
}

// Every other field must survive byte-for-byte in meaning: the proxy is a
// role-fixer, not a request rewriter.
func TestRewrite_preserves_every_other_field(t *testing.T) {
	out, _, err := Rewrite([]byte(claudeCodeBody))
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	if err := json.Unmarshal([]byte(claudeCodeBody), &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out, &after); err != nil {
		t.Fatal(err)
	}
	for key, want := range before {
		if key == "messages" {
			continue
		}
		if got := after[key]; !jsonEqual(got, want) {
			t.Errorf("%s changed: %#v, want %#v", key, got, want)
		}
	}
}

// A body with nothing to fix must come back untouched, so the proxy never
// re-serializes a request it had no reason to alter.
func TestRewrite_reports_a_clean_body_as_unchanged(t *testing.T) {
	clean := `{"model":"x","messages":[{"role":"user","content":"hi"}]}`
	out, changed, err := Rewrite([]byte(clean))
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("a body with no system role must report unchanged")
	}
	if string(out) != clean {
		t.Errorf("clean body was rewritten: %s", out)
	}
}

// Anything the proxy cannot parse is forwarded as-is: a body it does not
// understand is the upstream's business, and guessing would corrupt it.
func TestRewrite_passes_through_what_it_cannot_parse(t *testing.T) {
	for _, body := range []string{`not json at all`, `{"messages": "not a list"}`, ``} {
		out, changed, err := Rewrite([]byte(body))
		if err != nil {
			t.Errorf("Rewrite(%q) errored: %v", body, err)
		}
		if changed {
			t.Errorf("Rewrite(%q) claimed a change", body)
		}
		if string(out) != body {
			t.Errorf("Rewrite(%q) = %q, want it untouched", body, out)
		}
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func TestProxy_forwards_the_fixed_body_and_the_credential(t *testing.T) {
	var gotBody []byte
	var gotAuth, gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	proxy := httptest.NewServer(NewHandler(upstream.URL))
	defer proxy.Close()

	req, err := http.NewRequest(http.MethodPost, proxy.URL+"/v1/messages", strings.NewReader(claudeCodeBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer rc_secret")
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if gotPath != "/v1/messages" {
		t.Errorf("upstream path = %q, want /v1/messages", gotPath)
	}
	if gotAuth != "Bearer rc_secret" {
		t.Errorf("Authorization = %q, want the client's credential forwarded", gotAuth)
	}
	if got := roles(t, gotBody); got[1] != "user" || got[3] != "user" {
		t.Errorf("upstream saw roles %v, want the system entries rewritten", got)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want the upstream's 200", resp.StatusCode)
	}
}

// The stream must reach Claude Code as it arrives. Buffering it would swallow
// the ": keep-alive" comments that keep the byte-stall watchdog quiet, turning
// a working slow model into an aborted turn.
func TestProxy_streams_the_response_without_buffering_it(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(": keep-alive\n\n"))
		w.(http.Flusher).Flush()
		<-release
		_, _ = w.Write([]byte("event: message_stop\ndata: {}\n\n"))
		w.(http.Flusher).Flush()
	}))
	defer upstream.Close()

	proxy := httptest.NewServer(NewHandler(upstream.URL))
	defer proxy.Close()

	resp, err := http.Post(proxy.URL+"/v1/messages", "application/json", strings.NewReader(claudeCodeBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	first := make(chan string, 1)
	go func() {
		line, _ := reader.ReadString('\n')
		first <- line
	}()
	select {
	case line := <-first:
		if !strings.HasPrefix(line, ":") {
			t.Errorf("first line = %q, want the keep-alive comment", line)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the keep-alive never arrived: the proxy is buffering the stream")
	}
	close(release)
}

// A GET or any other path is not a Messages request; it must reach the upstream
// untouched rather than be parsed as one.
func TestProxy_passes_other_requests_straight_through(t *testing.T) {
	var gotPath, gotMethod string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer upstream.Close()

	proxy := httptest.NewServer(NewHandler(upstream.URL))
	defer proxy.Close()

	resp, err := http.Get(proxy.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if gotPath != "/v1/models" || gotMethod != http.MethodGet {
		t.Errorf("upstream saw %s %s, want GET /v1/models", gotMethod, gotPath)
	}
}

// thinkingBody is the second half of what Claude Code sends: extended thinking
// is on by default, so every request carries a top-level "thinking". Captured
// from a live 2.1.258 pane on 2026-09-02.
const thinkingBody = `{
  "model": "TurboVadim/Qwen3.8-27B-OBLITERATED",
  "max_tokens": 8192,
  "thinking": {"type":"adaptive","display":"omitted"},
  "output_config": {"effort":"xhigh"},
  "messages": [{"role":"user","content":[{"type":"text","text":"hello"}]}],
  "tools": [{"name":"Bash","input_schema":{"type":"object"}}]
}`

func hasKey(t *testing.T, body []byte, key string) bool {
	t.Helper()
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	_, ok := parsed[key]
	return ok
}

// A request carrying "thinking" turns Featherless's tool-call parser off: the
// model's own `<tool_call>` XML is then handed back as assistant TEXT, so the
// pane renders the markup and never runs a tool. Measured against the live
// endpoint on 2026-09-02, same prompt and model, both arms: without the field
// stop_reason is "tool_use" and a tool_use block arrives; with it stop_reason is
// "end_turn" and the XML sits in a text block.
func TestRewrite_drops_the_thinking_field_that_disables_tool_calls(t *testing.T) {
	out, changed, err := Rewrite([]byte(thinkingBody))
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("a body carrying thinking must report as changed")
	}
	if hasKey(t, out, "thinking") {
		t.Errorf("thinking survived the rewrite: %s", out)
	}
}

// Dropping "thinking" must cost the request nothing else — the model, the tool
// schemas and the effort setting all still have to reach the endpoint.
func TestRewrite_keeps_every_other_field_when_it_drops_thinking(t *testing.T) {
	out, _, err := Rewrite([]byte(thinkingBody))
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	if err := json.Unmarshal([]byte(thinkingBody), &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out, &after); err != nil {
		t.Fatal(err)
	}
	for key, want := range before {
		if key == "thinking" {
			continue
		}
		if got := after[key]; !jsonEqual(got, want) {
			t.Errorf("%s changed: %#v, want %#v", key, got, want)
		}
	}
}

// Both repairs are one pass. A real request carries both problems at once, and
// a rewrite that returned after the first would forward the other untouched.
func TestRewrite_repairs_roles_and_drops_thinking_together(t *testing.T) {
	both := `{
	  "model": "TurboVadim/Qwen3.8-27B-OBLITERATED",
	  "thinking": {"type":"adaptive","display":"omitted"},
	  "messages": [
	    {"role":"user","content":"hi"},
	    {"role":"system","content":"Available agent types for the Agent tool:"}
	  ]
	}`
	out, changed, err := Rewrite([]byte(both))
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("a body carrying both problems must report as changed")
	}
	if hasKey(t, out, "thinking") {
		t.Errorf("thinking survived: %s", out)
	}
	if got, want := roles(t, out), []string{"user", "user"}; !jsonEqual(got, want) {
		t.Errorf("roles = %v, want %v", got, want)
	}
}

// The repair has to reach the wire, not just Rewrite's return value: the
// forwarded body is re-framed with a new Content-Length, and an upstream that
// still saw "thinking" would still hand the pane raw XML.
func TestProxy_forwards_a_body_with_thinking_stripped(t *testing.T) {
	var gotBody []byte
	var gotLen int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotLen = r.ContentLength
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	proxy := httptest.NewServer(NewHandler(upstream.URL))
	defer proxy.Close()

	resp, err := http.Post(proxy.URL+"/v1/messages", "application/json", strings.NewReader(thinkingBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if hasKey(t, gotBody, "thinking") {
		t.Errorf("upstream saw thinking: %s", gotBody)
	}
	if !hasKey(t, gotBody, "tools") {
		t.Errorf("upstream lost the tools: %s", gotBody)
	}
	if gotLen != int64(len(gotBody)) {
		t.Errorf("Content-Length = %d, want %d — a stale length re-frames the body", gotLen, len(gotBody))
	}
}
