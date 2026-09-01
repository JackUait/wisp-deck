package gptbridge

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// Claude Code arms a byte-level stall watchdog on every text/event-stream body
// it receives. 20s of raw-byte silence paints
// "Waiting for API response · will retry in <N> · check your network" (the
// countdown is the watchdog's own remaining budget, not a retry backoff), and
// 300s aborts the stream — after which the whole turn, history and all, is
// replayed. A reasoning Codex turn forwards nothing for minutes, so the bridge
// keeps the socket warm itself. 10s leaves a full interval of slack under the
// 20s threshold.
const defaultKeepAliveInterval = 10 * time.Second

// keepAliveEvent is Anthropic's own idle frame. Claude Code's SSE reader drops
// `event: ping` before it reaches the stream loop, so it can never be mistaken
// for content — it counts only as bytes, which is exactly what the watchdog
// measures.
func keepAliveEvent() StreamEvent {
	return StreamEvent{Event: "ping", Data: map[string]any{"type": "ping"}}
}

// streamWriter serializes the turn's own events against the keepalive ticker,
// which is the bridge's only concurrent writer to a ResponseWriter.
type streamWriter struct {
	mu        sync.Mutex
	writer    http.ResponseWriter
	interval  time.Duration
	lastWrite time.Time
	started   bool
	stop      chan struct{}
	stopOnce  sync.Once
	done      sync.WaitGroup
}

func newStreamWriter(writer http.ResponseWriter, interval time.Duration) *streamWriter {
	if interval <= 0 {
		interval = defaultKeepAliveInterval
	}
	return &streamWriter{writer: writer, interval: interval, stop: make(chan struct{})}
}

// Started reports whether response headers have been written, which decides
// whether a failure can still become an HTTP status or must be an SSE error.
func (s *streamWriter) Started() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.started
}

// Emit writes one batch, opening the response on the first non-empty batch.
func (s *streamWriter) Emit(events []StreamEvent) error {
	if len(events) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		s.writer.Header().Set("content-type", "text/event-stream")
		s.writer.Header().Set("cache-control", "no-cache")
		s.writer.Header().Set("x-accel-buffering", "no")
		s.writer.WriteHeader(http.StatusOK)
		s.started = true
		s.startKeepAlive()
	}
	s.lastWrite = time.Now()
	return WriteSSE(s.writer, events)
}

// startKeepAlive must be called with s.mu held.
func (s *streamWriter) startKeepAlive() {
	s.done.Add(1)
	go func() {
		defer s.done.Done()
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				s.mu.Lock()
				// A real event within the last interval already moved the
				// watchdog; a redundant ping would only add noise.
				if time.Since(s.lastWrite) >= s.interval {
					s.lastWrite = time.Now()
					_ = WriteSSE(s.writer, []StreamEvent{keepAliveEvent()})
				}
				s.mu.Unlock()
			}
		}
	}()
}

// Close stops the ticker and waits for it, so nothing touches the
// ResponseWriter after the handler returns.
func (s *streamWriter) Close() {
	s.stopOnce.Do(func() { close(s.stop) })
	s.done.Wait()
}

// Codex reports quota exhaustion and sign-out as ordinary turn failures. Both
// are deterministic: every retry fails identically, quota until the window
// resets days later and sign-out until a human runs `codex login`. Reported as
// a 5xx they make Claude Code retry ~11 times over ~3 minutes and then tell the
// user the failure is "usually temporary", which is false. 400 is the only
// status Claude Code surfaces immediately AND without an unrelated
// "Failed to authenticate." prefix — 401, 403 and 429 were all measured against
// a live client and 401/429 are retried like a 5xx.
var deterministicCodexFailures = []string{
	"you've hit your usage limit",
	"you have hit your usage limit",
	"reached your usage limit",
	"purchase more credits",
	"out of credits",
	"quota exceeded",
	"insufficient_quota",
	"codex login",
	"is still signed out",
	"signed out after chatgpt sign-in",
	"api-key authentication",
	"no chatgpt subscription models",
	"authentication type",
}

// isDeterministicCodexFailure reports whether retrying can only reproduce the
// same failure, so Claude Code should be told once rather than made to retry.
func isDeterministicCodexFailure(message string) bool {
	lowered := strings.ToLower(message)
	for _, marker := range deterministicCodexFailures {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return isLocalModelProviderUnreachable(lowered)
}

// Codex reaches its model through openai_base_url in ~/.codex/config.toml. A
// loopback address there names a separate program on this machine (a
// ChatGPT-web bridge, an OpenAI-compatible proxy), and when that program is not
// running reqwest reports a send failure that every retry reproduces
// identically — nothing Claude Code can do starts a process that has exited.
// The same wording against the real upstream is the opposite: a transient reset
// a retry fixes. So the loopback host, not the send failure, is the
// discriminator.
var loopbackProviderHosts = []string{"//127.0.0.1", "//localhost", "//[::1]"}

func isLocalModelProviderUnreachable(lowered string) bool {
	if !strings.Contains(lowered, "error sending request for url") {
		return false
	}
	for _, host := range loopbackProviderHosts {
		if strings.Contains(lowered, host) {
			return true
		}
	}
	return false
}

// The reqwest wording names the port but not the fix, and Claude Code shows the
// message alone.
const localModelProviderRemedy = " — that address is Codex's own model provider" +
	" (openai_base_url in ~/.codex/config.toml): a program on this machine that" +
	" is not accepting connections. Start it, then retry."

// annotateCodexFailure adds the remedy a deterministic failure's raw wording
// omits, so the one surfaced attempt is actionable.
func annotateCodexFailure(message string) string {
	if isLocalModelProviderUnreachable(strings.ToLower(message)) {
		return message + localModelProviderRemedy
	}
	return message
}
