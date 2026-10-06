package gptbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingBundle is a fakeBundle that also remembers what it was asked to run
// and what private cwd it was built for.
type recordingBundle struct {
	*fakeBundle
	privateCWD string

	mu     sync.Mutex
	models []string
}

func (b *recordingBundle) Execute(
	ctx context.Context, translation Translation, emit func([]StreamEvent) error,
) (AnthropicMessage, error) {
	b.mu.Lock()
	b.models = append(b.models, translation.Model)
	b.mu.Unlock()
	if emit != nil {
		if err := emit([]StreamEvent{{
			Event: "message_start",
			Data:  map[string]any{"type": "message_start"},
		}}); err != nil {
			return AnthropicMessage{}, err
		}
	}
	return AnthropicMessage{ID: "msg_fake", Model: translation.Model}, nil
}

func (b *recordingBundle) seenModels() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.models...)
}

// countingBuilder counts bundle builds so a test can prove the bridge starts
// at most one app-server for the whole launch.
type countingBuilder struct {
	mu      sync.Mutex
	builds  int
	bundles []*recordingBundle
	delay   time.Duration
	fail    []error
}

func (c *countingBuilder) build(_ context.Context, privateCWD string) (EngineBundle, error) {
	c.mu.Lock()
	index := c.builds
	c.builds++
	delay := c.delay
	var failure error
	if index < len(c.fail) {
		failure = c.fail[index]
	}
	c.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	if failure != nil {
		return nil, failure
	}
	bundle := &recordingBundle{fakeBundle: newFakeBundle("bridge"), privateCWD: privateCWD}
	c.mu.Lock()
	c.bundles = append(c.bundles, bundle)
	c.mu.Unlock()
	return bundle, nil
}

func (c *countingBuilder) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.builds
}

func (c *countingBuilder) last() *recordingBundle {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.bundles) == 0 {
		return nil
	}
	return c.bundles[len(c.bundles)-1]
}

func newTestChatGPTBridge(t *testing.T, builder *countingBuilder) *ChatGPTBridge {
	t.Helper()
	bridge := NewChatGPTBridge(ChatGPTBridgeOptions{CodexPath: "/opt/codex"})
	bridge.buildBundle = builder.build
	t.Cleanup(bridge.Close)
	return bridge
}

func TestChatGPTBridge_starts_nothing_until_a_turn_asks_for_an_endpoint(t *testing.T) {
	builder := &countingBuilder{}
	bridge := newTestChatGPTBridge(t, builder)

	// Checked on the struct as well as through the seam: a mutation that starts
	// eagerly in the constructor runs before a test can install the seam, so a
	// build count alone would report zero while a real app-server was running.
	if bridge.server != nil || bridge.executor != nil || bridge.privateCWD != "" {
		t.Fatalf("construction already started a bridge: server=%v executor=%v cwd=%q",
			bridge.server != nil, bridge.executor != nil, bridge.privateCWD)
	}

	bridge.Close()

	if builder.count() != 0 {
		t.Fatalf("bridge built %d bundles without an Endpoint call; want 0", builder.count())
	}
}

func TestChatGPTBridge_reuses_one_app_server_across_turns(t *testing.T) {
	builder := &countingBuilder{}
	bridge := newTestChatGPTBridge(t, builder)

	firstURL, firstKey, err := bridge.Endpoint()
	if err != nil {
		t.Fatalf("first Endpoint: %v", err)
	}
	secondURL, secondKey, err := bridge.Endpoint()
	if err != nil {
		t.Fatalf("second Endpoint: %v", err)
	}

	if builder.count() != 1 {
		t.Fatalf("two turns built %d bundles; want 1", builder.count())
	}
	if firstURL != secondURL || firstKey != secondKey {
		t.Fatalf("endpoint moved between turns: %q/%q then %q/%q",
			firstURL, firstKey, secondURL, secondKey)
	}
	if !strings.HasPrefix(firstURL, "http://127.0.0.1:") {
		t.Fatalf("bridge URL %q is not an IPv4 loopback address", firstURL)
	}
	if firstKey == "" {
		t.Fatal("bridge key is empty")
	}
}

func TestChatGPTBridge_concurrent_first_turns_share_one_start(t *testing.T) {
	builder := &countingBuilder{delay: 50 * time.Millisecond}
	bridge := newTestChatGPTBridge(t, builder)

	const callers = 8
	urls := make([]string, callers)
	errs := make([]error, callers)
	var wait sync.WaitGroup
	wait.Add(callers)
	for index := range callers {
		go func() {
			defer wait.Done()
			urls[index], _, errs[index] = bridge.Endpoint()
		}()
	}
	wait.Wait()

	if builder.count() != 1 {
		t.Fatalf("%d concurrent turns built %d bundles; want 1", callers, builder.count())
	}
	for index := range callers {
		if errs[index] != nil {
			t.Fatalf("caller %d: %v", index, errs[index])
		}
		if urls[index] != urls[0] {
			t.Fatalf("caller %d got %q, caller 0 got %q", index, urls[index], urls[0])
		}
	}
}

func TestChatGPTBridge_reports_a_failed_start_and_retries_on_the_next_turn(t *testing.T) {
	builder := &countingBuilder{fail: []error{errors.New("Codex is signed out; run `codex login`")}}
	bridge := newTestChatGPTBridge(t, builder)

	if _, _, err := bridge.Endpoint(); err == nil {
		t.Fatal("a failed start reported no error")
	} else if !strings.Contains(err.Error(), "codex login") {
		t.Fatalf("start error %q does not name the reason", err)
	}

	url, _, err := bridge.Endpoint()
	if err != nil {
		t.Fatalf("second Endpoint after a fixed failure: %v", err)
	}
	if url == "" {
		t.Fatal("second Endpoint returned no URL")
	}
	if builder.count() != 2 {
		t.Fatalf("built %d bundles; want 2 (a failed start must not be cached)", builder.count())
	}
}

func TestChatGPTBridge_refuses_a_launch_with_no_codex(t *testing.T) {
	builder := &countingBuilder{}
	bridge := NewChatGPTBridge(ChatGPTBridgeOptions{})
	bridge.buildBundle = builder.build
	t.Cleanup(bridge.Close)

	_, _, err := bridge.Endpoint()
	if err == nil {
		t.Fatal("Endpoint succeeded with no Codex path")
	}
	if !strings.Contains(err.Error(), "Codex") {
		t.Fatalf("error %q does not name Codex", err)
	}
	if builder.count() != 0 {
		t.Fatalf("built %d bundles with no Codex path; want 0", builder.count())
	}
}

// A pane whose launch found no Codex (it was mid-reinstall) must not keep
// every GPT turn dead until a relaunch: the bridge looks again on each turn.
func TestChatGPTBridge_finds_a_codex_installed_after_launch(t *testing.T) {
	builder := &countingBuilder{}
	answers := []string{"", "/opt/codex"}
	lookups := 0
	bridge := NewChatGPTBridge(ChatGPTBridgeOptions{ResolveCodexPath: func() string {
		answer := answers[min(lookups, len(answers)-1)]
		lookups++
		return answer
	}})
	bridge.buildBundle = builder.build
	t.Cleanup(bridge.Close)

	if _, _, err := bridge.Endpoint(); err == nil {
		t.Fatal("Endpoint succeeded before Codex was found")
	}
	if _, _, err := bridge.Endpoint(); err != nil {
		t.Fatalf("Endpoint after Codex appeared: %v", err)
	}
	if bridge.options.CodexPath != "/opt/codex" {
		t.Fatalf("bridge starts Codex at %q; want /opt/codex", bridge.options.CodexPath)
	}
	if _, _, err := bridge.Endpoint(); err != nil {
		t.Fatalf("third Endpoint: %v", err)
	}
	if lookups != 2 {
		t.Fatalf("looked Codex up %d times; want 2 (a hit is kept, a miss is not)", lookups)
	}
	if builder.count() != 1 {
		t.Fatalf("built %d bundles; want 1", builder.count())
	}
}

func TestChatGPTBridge_close_shuts_the_app_server_down_and_removes_its_private_cwd(t *testing.T) {
	builder := &countingBuilder{}
	bridge := newTestChatGPTBridge(t, builder)

	url, _, err := bridge.Endpoint()
	if err != nil {
		t.Fatalf("Endpoint: %v", err)
	}
	bundle := builder.last()
	if bundle == nil {
		t.Fatal("no bundle was built")
	}
	if bundle.privateCWD == "" || !strings.HasPrefix(bundle.privateCWD, os.TempDir()) {
		t.Fatalf("private cwd %q is not a temporary directory", bundle.privateCWD)
	}
	info, err := os.Stat(bundle.privateCWD)
	if err != nil {
		t.Fatalf("stat private cwd: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("private cwd mode is %v; want 0700", info.Mode().Perm())
	}

	bridge.Close()

	if !bundle.isClosed() {
		t.Fatal("Close left the app-server bundle running")
	}
	if _, err := os.Stat(bundle.privateCWD); !os.IsNotExist(err) {
		t.Fatalf("Close left the private cwd on disk: %v", err)
	}
	if _, err := http.Get(url + "/health"); err == nil { //nolint:noctx // the listener must be gone
		t.Fatal("Close left the loopback listener accepting connections")
	}
}

func TestChatGPTBridge_close_is_safe_before_any_turn_and_twice(t *testing.T) {
	builder := &countingBuilder{}
	bridge := NewChatGPTBridge(ChatGPTBridgeOptions{CodexPath: "/opt/codex"})
	bridge.buildBundle = builder.build

	bridge.Close()
	bridge.Close()

	if _, _, err := bridge.Endpoint(); err == nil {
		t.Fatal("Endpoint started a bridge after Close")
	}
	if builder.count() != 0 {
		t.Fatalf("built %d bundles after Close; want 0", builder.count())
	}
}

func TestChatGPTBridge_serves_a_bare_codex_model_id_to_the_engine(t *testing.T) {
	builder := &countingBuilder{}
	bridge := newTestChatGPTBridge(t, builder)

	url, key, err := bridge.Endpoint()
	if err != nil {
		t.Fatalf("Endpoint: %v", err)
	}
	body, err := json.Marshal(map[string]any{
		"model":      "gpt-6-astra",
		"max_tokens": 16,
		"messages":   []any{map[string]any{"role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	request, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, url+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("content-type", "application/json")
	request.Header.Set("anthropic-version", "2023-06-01")
	request.Header.Set("Authorization", "Bearer "+key)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("post to bridge: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("bridge answered HTTP %d", response.StatusCode)
	}

	bundle := builder.last()
	if bundle == nil {
		t.Fatal("no bundle was built")
	}
	if seen := bundle.seenModels(); len(seen) != 1 || seen[0] != "gpt-6-astra" {
		t.Fatalf("engine saw models %v; want exactly [gpt-6-astra]", seen)
	}
}

func TestChatGPTBridge_rejects_a_request_without_its_own_key(t *testing.T) {
	builder := &countingBuilder{}
	bridge := newTestChatGPTBridge(t, builder)

	url, _, err := bridge.Endpoint()
	if err != nil {
		t.Fatalf("Endpoint: %v", err)
	}
	request, err := http.NewRequestWithContext(context.Background(),
		http.MethodGet, url+"/health", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request answered HTTP %d; want 401", response.StatusCode)
	}
}
