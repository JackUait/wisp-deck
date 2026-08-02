package gptbridge

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestLiveImageEndToEnd drives a real Codex app-server through the real bridge
// handler with a real Anthropic-shaped streaming request, and checks the two
// things the mocked tests cannot: that Codex still reports a savedPath that
// exists on disk, and that the picture at that path still travels back into
// Codex as the input image of the next turn.
//
// Both halves depend on Codex's own behavior, so run this after a codex
// upgrade — an item that stops carrying savedPath would leave every generated
// image stranded, and the unit tests would stay green.
//
//	WISP_DECK_LIVE_IMAGE_E2E=1 go test ./internal/gptbridge/ -run TestLiveImageEndToEnd -v
//
// It costs one real image generation against the signed-in ChatGPT account.
func TestLiveImageEndToEnd(t *testing.T) {
	if os.Getenv("WISP_DECK_LIVE_IMAGE_E2E") == "" {
		t.Skip("set WISP_DECK_LIVE_IMAGE_E2E=1 to run the live image end-to-end check")
	}
	codexPath, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	server, err := StartAppServer(ctx, AppServerOptions{
		CodexPath: codexPath, ClientVersion: "2.30.0", ShutdownTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer closeCancel()
		_ = server.Close(closeCtx)
	}()

	models := subscriptionModelNames(server.Models)
	if len(models) == 0 {
		t.Fatal("no subscription models")
	}
	t.Logf("models: %v", models)

	engine, err := NewEngine(server.RPC, EngineOptions{
		PrivateCWD: t.TempDir(), Models: models,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	handler, err := NewHandler(engine, "test-key", ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	bridge := httptest.NewServer(handler)
	defer bridge.Close()

	body, err := json.Marshal(map[string]any{
		"model":      models[0],
		"max_tokens": 4096,
		"stream":     true,
		"system":     "You are Claude Code.",
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{{
				"type": "text",
				"text": "Generate an image of a single yellow rubber duck on a plain white background.",
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := postStream(t, ctx, bridge.URL+"/v1/messages", body)
	t.Logf("ASSISTANT TEXT:\n%s", text)

	path := extractSavedPath(text)
	if path == "" {
		t.Fatalf("no image path in the response: %q", text)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the reported image path does not exist: %v", err)
	}
	t.Logf("image on disk: %s (%d bytes)", path, info.Size())
	if strings.Contains(text, "discarded") {
		t.Fatalf("response still claims the image was discarded: %q", text)
	}
	if strings.Contains(text, "iVBORw0KGgo") {
		t.Fatalf("base64 leaked into the response: %q", text)
	}

	// The point of the path: Claude Code reads that file and the picture goes
	// back to Codex as the input image of the next turn. Replay exactly the
	// history Claude Code would send — assistant Read tool_use, user tool_result
	// carrying the image — and ask the model what it sees.
	picture, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	followUp, err := json.Marshal(map[string]any{
		"model":      models[0],
		"max_tokens": 1024,
		"stream":     true,
		"system":     "You are Claude Code.",
		"messages": []map[string]any{
			{"role": "user", "content": []map[string]any{
				{"type": "text", "text": "Generate an image of a single yellow rubber duck on a plain white background."},
			}},
			{"role": "assistant", "content": []map[string]any{
				{"type": "text", "text": text},
				{"type": "tool_use", "id": "toolu_read_1", "name": "Read",
					"input": map[string]any{"file_path": path}},
			}},
			{"role": "user", "content": []map[string]any{
				{"type": "tool_result", "tool_use_id": "toolu_read_1", "content": []map[string]any{
					{"type": "image", "source": map[string]any{
						"type": "base64", "media_type": "image/png",
						"data": base64.StdEncoding.EncodeToString(picture),
					}},
				}},
			}},
			{"role": "user", "content": []map[string]any{
				{"type": "text", "text": "Name the single object visible in that image, in one word."},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	answer := postStream(t, ctx, bridge.URL+"/v1/messages", followUp)
	t.Logf("ROUND-TRIP ANSWER: %s", answer)
	if !strings.Contains(strings.ToLower(answer), "duck") {
		t.Fatalf("the generated image did not survive the round trip back to Codex: %q", answer)
	}
}

func postStream(t *testing.T, ctx context.Context, url string, body []byte) string {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("x-api-key", "test-key")
	request.Header.Set("content-type", "application/json")
	request.Header.Set("anthropic-version", "2023-06-01")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		raw := make([]byte, 4096)
		n, _ := response.Body.Read(raw)
		t.Fatalf("status %d: %s", response.StatusCode, raw[:n])
	}
	var text strings.Builder
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 1<<20), 64<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil {
			continue
		}
		if event.Type == "content_block_delta" && event.Delta.Type == "text_delta" {
			text.WriteString(event.Delta.Text)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("stream read: %v", err)
	}
	return text.String()
}

func extractSavedPath(text string) string {
	const marker = "saved it to "
	index := strings.Index(text, marker)
	if index < 0 {
		return ""
	}
	rest := text[index+len(marker):]
	if end := strings.Index(rest, ".png"); end >= 0 {
		return rest[:end+len(".png")]
	}
	return ""
}
