package allin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/jackuait/wisp-deck/internal/rolefix"
)

// maxRouteBytes caps how large a request body this handler will re-address.
// A body past the cap is rejected with 400, never silently truncated: reading
// exactly maxRouteBytes via io.LimitReader can return a truncated body with a
// nil error, so the read goes one byte past the cap to detect that case. 64MiB
// clears a real conversation's measured 16,766,904 bytes of inline image
// base64 — this is a loopback listener with one client, not a public server.
const maxRouteBytes = 64 << 20

// discardLog swallows httputil.ReverseProxy's own error logging. Its default
// ErrorLog is nil, which falls back to package log — writing straight to
// stderr, which is the terminal Claude Code paints on.
var discardLog = log.New(io.Discard, "", 0)

// NewHandler routes each request by the model its body names. A row this build
// cannot place goes to sessionUpstream on the session's own credential, so an
// unrecognised id costs a turn nothing.
func NewHandler(resolver Resolver, sessionUpstream string) http.Handler {
	return NewObservingHandler(resolver, sessionUpstream, nil)
}

// NewObservingHandler is NewHandler plus a hook that sees the session's own
// credential on every request. It gets nothing unless the session upstream
// is Anthropic, so a provider key never reaches api.anthropic.com.
func NewObservingHandler(resolver Resolver, sessionUpstream string, observe func(http.Header)) http.Handler {
	sessionIsAnthropic := false
	if parsed, err := url.Parse(sessionUpstream); err == nil && parsed.Hostname() == "api.anthropic.com" {
		sessionIsAnthropic = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if observe != nil {
			auth := http.Header{}
			if sessionIsAnthropic {
				for _, key := range []string{"Authorization", "X-Api-Key"} {
					if v := r.Header.Get(key); v != "" {
						auth.Set(key, v)
					}
				}
			}
			observe(auth)
		}
		base, body := sessionUpstream, []byte(nil)
		needsRepair := false
		if r.Method == http.MethodPost && r.Body != nil {
			// Read one byte past the cap: ContentLength is unreliable (-1 for
			// a chunked request), so the only way to know a body exceeded the
			// cap is to see the extra byte arrive.
			raw, err := io.ReadAll(io.LimitReader(r.Body, maxRouteBytes+1))
			_ = r.Body.Close()
			if err != nil {
				writeRoutingError(w, Target{}, fmt.Errorf("allin: reading request body: %w", err))
				return
			}
			if len(raw) > maxRouteBytes {
				writeRoutingError(w, Target{}, fmt.Errorf("allin: request body exceeds the %d byte routing cap", maxRouteBytes))
				return
			}
			body = raw
		}

		var payload map[string]any
		if body != nil {
			_ = json.Unmarshal(body, &payload)
		}
		model, _ := payload["model"].(string)
		target := Route(model)

		// KindSession never calls Resolve: Resolve's source guard rejects an
		// empty Source, and a session row always has one.
		if target.Kind != KindSession {
			credential, err := resolver.Resolve(target)
			if err != nil {
				writeRoutingError(w, target, err)
				return
			}
			base = credential.BaseURL
			needsRepair = credential.NeedsRepair
			rewritten, err := rewriteModel(payload, target.Model, credential.DropTools)
			if err != nil {
				writeRoutingError(w, target, err)
				return
			}
			body = rewritten
			// Clear both credential headers unconditionally: Credential.Header
			// names which one to set, but the session's own value on the OTHER
			// header would otherwise survive and reach the swapped-to endpoint
			// alongside the new credential.
			r.Header.Del("Authorization")
			r.Header.Del("X-Api-Key")
			r.Header.Set(credential.Header, credential.Value)
			if target.Want1M {
				addBeta(r.Header, "context-1m-2025-08-07")
			}
		}

		upstreamURL, err := validUpstream(base)
		if err != nil {
			writeRoutingError(w, target, err)
			return
		}

		if body != nil {
			// ContentLength alone: Transport writes the header from it, and a
			// header set here would only be a second copy to keep in step.
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))
		}
		if needsRepair {
			// A RemoteCatalog target (Featherless): compose rolefix's own
			// handler rather than re-implementing its repairs here. It reads
			// the request body itself and rewrites it (role:"system" ->
			// "user", drops "thinking"), and its ModifyResponse repairs the
			// reply (structured-output extraction, synthesized usage,
			// mis-spelled tool names). Its Director only sets Scheme/Host/
			// Path/Host, so the credential header this handler just swapped
			// in survives untouched into the upstream call. validUpstream
			// above already parsed base and proved it has a scheme and host,
			// so rolefix.NewHandler(base) cannot fail its own url.Parse — the
			// malformed-upstream 400 is still decided here, never by rolefix's
			// own 500 fallback (see the NeedsRepair case of
			// TestHandler_reports_a_malformed_upstream_as_400_not_502).
			// Built fresh per request like newReverseProxy below: measured at
			// 288ns/4 allocs, dwarfed by one real HTTP round trip, so caching
			// would buy nothing but a staleness hazard.
			rolefix.NewHandler(base).ServeHTTP(w, r)
			return
		}
		newReverseProxy(upstreamURL).ServeHTTP(w, r)
	})
}

// rewriteModel replaces the routed id in an already-parsed body and re-encodes
// it. Both failures here must be fatal to the turn, and both are unreachable
// today only because a non-local invariant holds — Route answers KindSession
// for the empty id a nil payload yields, and a payload decoded from JSON always
// re-encodes. Failing open would swap the credential in while leaving the
// wisp/… id in the body, so a third-party endpoint would receive a routing id
// it cannot answer, carrying someone else's real credential.
func rewriteModel(payload map[string]any, model string, drop []string) ([]byte, error) {
	if payload == nil {
		return nil, errors.New("allin: request body is not a JSON object")
	}
	payload["model"] = model
	dropTools(payload, drop)
	rewritten, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("allin: re-encoding the routed request: %w", err)
	}
	return rewritten, nil
}

// validUpstream rejects a base URL httputil.ReverseProxy could never have
// reached anyway, before it tries: url.Parse's own error, or a URL with no
// scheme or host, would otherwise become a transport failure that surfaces as
// a retryable 502 — and this is deterministic, so it must be 400 instead.
func validUpstream(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("allin: invalid upstream address %q: %w", raw, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("allin: invalid upstream address %q", raw)
	}
	return parsed, nil
}

func newReverseProxy(target *url.URL) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme, req.URL.Host = target.Scheme, target.Host
			req.URL.Path = strings.TrimSuffix(target.Path, "/") + req.URL.Path
			// Gateways route on Host; the loopback name reaches no virtual host.
			req.Host = target.Host
			// A compressed body is bytes the next hop cannot read.
			req.Header.Set("Accept-Encoding", "identity")
		},
		// Each write forwarded as it arrives: buffering swallows the keep-alive
		// bytes that keep Claude Code's stall watchdog from replaying a turn.
		FlushInterval: -1,
		ErrorLog:      discardLog,
	}
}

func addBeta(header http.Header, value string) {
	current := header.Get("Anthropic-Beta")
	if strings.Contains(current, value) {
		return
	}
	if current == "" {
		header.Set("Anthropic-Beta", value)
		return
	}
	header.Set("Anthropic-Beta", current+","+value)
}

// writeRoutingError answers in Anthropic's own envelope, always with 400.
// Claude Code retries 401 and 5xx about eleven times, and every one of these is
// deterministic — the same request would fail again the same way.
func writeRoutingError(w http.ResponseWriter, target Target, err error) {
	message := fmt.Sprintf("wisp-deck: %v", err)
	if errors.Is(err, ErrStaleAccount) {
		message = fmt.Sprintf(
			"wisp-deck: the login %q has no usable credential. Open it once "+
				"(a wisp-deck tab on that account) so Claude refreshes its token, then retry.",
			target.Source)
	}
	body, _ := json.Marshal(map[string]any{
		"type":  "error",
		"error": map[string]string{"type": "invalid_request_error", "message": message},
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write(body)
}

// dropTools removes the named tools from an already-parsed body, so an endpoint
// that rejects one tool's schema is never sent it. Claude Code advertises the
// whole tool set on every turn, and a schema the endpoint refuses 400s the turn
// before the model reads a word.
//
// A turn that carries no `tools` key at all (the title generator) keeps none:
// inventing an empty array there would change what the endpoint is asked for.
func dropTools(payload map[string]any, drop []string) {
	if len(drop) == 0 {
		return
	}
	tools, ok := payload["tools"].([]any)
	if !ok {
		return
	}
	unwanted := make(map[string]bool, len(drop))
	for _, name := range drop {
		unwanted[name] = true
	}
	kept := make([]any, 0, len(tools))
	for _, tool := range tools {
		entry, _ := tool.(map[string]any)
		if name, _ := entry["name"].(string); unwanted[name] {
			continue
		}
		kept = append(kept, tool)
	}
	payload["tools"] = kept
}
