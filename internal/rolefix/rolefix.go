// Package rolefix is a loopback Anthropic pass-through that repairs the two
// things a strict endpoint mishandles about a Claude Code request. Both were
// measured against Featherless by replaying a request captured from a live
// pane, and this proxy touches nothing else.
//
// First, roles. Claude Code puts its capability listings (agent types, skills)
// into messages[] as entries with role "system". Anthropic's own API accepts
// that; an endpoint validating the published Messages schema — where a message
// role is only "user" or "assistant" — rejects the request with a 400 that
// kills the turn before the model sees it. Measured 2026-09-01: as Claude Code
// sends it, 400 `messages.1.role: Invalid enum value`; with that one role
// rewritten to "user", 200 and a normal completion.
//
// Second, thinking. Extended thinking is on by default, so every request
// carries a top-level "thinking" — and its presence switches Featherless's
// tool-call parser off. The model still emits a tool call, but the endpoint no
// longer converts it, so the raw `<tool_call>` XML arrives as assistant TEXT
// and the pane renders the markup instead of running anything. Measured
// 2026-09-02 on TurboVadim/Qwen3.8-27B-OBLITERATED and
// huihui-ai/Huihui-Qwen3.8-27B-abliterated, same prompt, both arms: without the
// field stop_reason is "tool_use" and a tool_use block arrives; with it
// stop_reason is "end_turn" and the XML sits in a text block. The endpoint
// returns a thinking block either way, so dropping the field costs no
// reasoning — only the request's ability to break its own tool calling.
package rolefix

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

// maxRewriteBytes caps the body this will parse. A conversation replaying many
// images runs to megabytes; anything past the cap is forwarded untouched rather
// than held in memory twice.
const maxRewriteBytes = 128 << 20

// Rewrite replaces every messages[].role of "system" with "user", reporting
// whether anything changed. A body it cannot parse as a Messages request comes
// back exactly as it went in: an unrecognized shape is the upstream's business,
// and guessing at it would corrupt a request that might be valid.
func Rewrite(body []byte) ([]byte, bool, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return body, false, nil
	}
	raw, ok := envelope["messages"]
	if !ok {
		return body, false, nil
	}
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		return body, false, nil
	}
	changed := false
	// Featherless turns its tool-call parser off for a request that declares
	// thinking, and hands the model's own `<tool_call>` XML back as assistant
	// text — so the pane renders the markup and never runs a tool. The model
	// keeps reasoning without the field, and the endpoint still returns a
	// thinking block for it, so dropping it costs the turn nothing.
	if _, ok := envelope["thinking"]; ok {
		delete(envelope, "thinking")
		changed = true
	}
	for _, message := range messages {
		var role string
		if err := json.Unmarshal(message["role"], &role); err != nil {
			continue
		}
		if role != "system" {
			continue
		}
		// "user" rather than folding into the top-level system prompt: the
		// listings are positional, and Claude Code places them between turns on
		// purpose. Moving them would change what the model reads and when.
		message["role"] = json.RawMessage(`"user"`)
		changed = true
	}
	if !changed {
		return body, false, nil
	}
	fixed, err := json.Marshal(messages)
	if err != nil {
		return body, false, err
	}
	envelope["messages"] = fixed
	out, err := json.Marshal(envelope)
	if err != nil {
		return body, false, err
	}
	return out, true, nil
}

// NewHandler returns a reverse proxy to upstream that repairs message roles on
// the way through. The client's own credential travels with the request, so the
// proxy never stores or logs one.
func NewHandler(upstream string) http.Handler {
	target, err := url.Parse(upstream)
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "rolefix: bad upstream: "+err.Error(), http.StatusInternalServerError)
		})
	}
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = strings.TrimSuffix(target.Path, "/") + req.URL.Path
			// Vendor gateways route on Host; sending the loopback name reaches
			// the wrong virtual host or none at all.
			req.Host = target.Host
		},
		// FlushInterval -1 forwards each write as it arrives. Buffering would
		// swallow the ": keep-alive" comments an endpoint sends while waiting on
		// its first token, and those are what keep Claude Code's byte-stall
		// watchdog from aborting and replaying a working turn.
		FlushInterval: -1,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.Body != nil {
			repairRequestBody(r)
		}
		proxy.ServeHTTP(w, r)
	})
}

func repairRequestBody(r *http.Request) {
	if r.ContentLength > maxRewriteBytes {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRewriteBytes))
	_ = r.Body.Close()
	if err != nil {
		r.Body = io.NopCloser(strings.NewReader(""))
		return
	}
	fixed, changed, err := Rewrite(body)
	if err != nil || !changed {
		fixed = body
	}
	r.Body = io.NopCloser(strings.NewReader(string(fixed)))
	r.ContentLength = int64(len(fixed))
	r.Header.Set("Content-Length", strconv.Itoa(len(fixed)))
}
