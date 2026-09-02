package rolefix

import (
	"bytes"
	"encoding/json"
)

// Featherless answers /v1/messages with usage {input_tokens: 0, output_tokens:
// 0} — every turn, streamed and not. Measured 2026-09-02 against the live
// endpoint: the request recorded in testdata, posted to /v1/chat/completions,
// reported 19,838 prompt tokens. The count exists; only the Anthropic adapter
// drops it.
//
// Claude Code sizes auto-compaction from that figure. A permanent zero is not a
// cosmetic statusline bug: the transcript never compacts, /context reads empty,
// and the conversation grows until the endpoint starts rejecting every turn —
// at which point there is no way back, because /compact must itself send the
// oversized transcript and is larger than the turn that already failed.
//
// So the proxy supplies what the endpoint dropped, and only that: a figure the
// endpoint did report is always kept, because a gateway that counts knows
// better than an estimate.

const (
	// bytesPerTokenNumerator and bytesPerTokenDenominator convert prompt bytes
	// to tokens as bytes * 10 / 38. Calibrated, not guessed: the captured first
	// request in testdata measured 3.98 prompt bytes per token on Featherless's
	// own tokenizer. 3.8 is used rather than that measurement so the count
	// leans high — reading low lets a conversation grow past a window the
	// endpoint then rejects, and /compact cannot escape it because the
	// compaction request must itself carry the oversized transcript. Reading
	// high only compacts a little early.
	bytesPerTokenNumerator   = 10
	bytesPerTokenDenominator = 38

	// imageTokens is what one image costs regardless of its base64 size. The
	// payload is orders of magnitude larger than the token cost, so counting
	// its bytes would report a conversation holding one screenshot as larger
	// than the window and compact on every turn.
	imageTokens = 1600
)

// EstimateInputTokens approximates what a Messages request costs the model.
//
// It counts the JSON of system, messages and tools whole rather than only the
// strings inside it. A tool schema reaches the model as the schema — braces,
// keys and all — and counting only its string values reported 17,023 tokens for
// a request the endpoint priced at 19,838: 14% low, which is the direction that
// kills a session rather than merely compacting it early.
//
// A body it cannot parse still costs something, so the byte count stands in
// rather than a zero — reporting zero is the failure this repair removes.
func EstimateInputTokens(body []byte) int {
	var envelope struct {
		System   json.RawMessage `json:"system"`
		Messages json.RawMessage `json:"messages"`
		Tools    json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return atLeastOne(tokensIn(len(body)))
	}
	promptBytes, imageBytes, images := 0, 0, 0
	for _, part := range []json.RawMessage{envelope.System, envelope.Messages, envelope.Tools} {
		promptBytes += len(part)
		var value any
		if len(part) == 0 || json.Unmarshal(part, &value) != nil {
			continue
		}
		b, i := walkImages(value)
		imageBytes += b
		images += i
	}
	return atLeastOne(tokensIn(promptBytes-imageBytes) + images*imageTokens)
}

// walkImages finds the image blocks in a request and reports how many there are
// and how many bytes their encoded payloads take up, so those bytes can be
// taken back out of the count and replaced by the flat per-image price.
func walkImages(value any) (imageBytes, images int) {
	switch node := value.(type) {
	case []any:
		for _, item := range node {
			b, i := walkImages(item)
			imageBytes += b
			images += i
		}
	case map[string]any:
		if kind, _ := node["type"].(string); kind == "image" {
			images++
			if source, ok := node["source"].(map[string]any); ok {
				for _, field := range []string{"data", "url"} {
					if payload, ok := source[field].(string); ok {
						imageBytes += len(payload)
					}
				}
			}
			return imageBytes, images
		}
		for _, item := range node {
			b, i := walkImages(item)
			imageBytes += b
			images += i
		}
	}
	return imageBytes, images
}

// tokensIn converts a byte count to the token count it stands for.
func tokensIn(bytes int) int {
	if bytes < 0 {
		return 0
	}
	return bytes * bytesPerTokenNumerator / bytesPerTokenDenominator
}

func atLeastOne(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

// usageMeter fills in the token counts an endpoint left at zero. It carries the
// request's own estimate and accumulates the reply's bytes as they stream, so
// the output figure is known by the time message_delta — the event that reports
// it — comes past.
type usageMeter struct {
	input       int
	outputBytes int
}

// observe records the bytes of a reply event that count against the output
// budget: assistant text, the model's reasoning, and a tool call's arguments.
func (m *usageMeter) observe(event []byte) {
	var parsed struct {
		Type  string `json:"type"`
		Delta struct {
			Text        string `json:"text"`
			Thinking    string `json:"thinking"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
	}
	if !decodeEventData(event, &parsed) || parsed.Type != "content_block_delta" {
		return
	}
	m.outputBytes += len(parsed.Delta.Text) + len(parsed.Delta.Thinking) + len(parsed.Delta.PartialJSON)
}

func (m *usageMeter) output() int {
	return atLeastOne(tokensIn(m.outputBytes))
}

// repair rewrites the usage an event carries, returning the event unchanged
// when the endpoint already reported its own counts. message_start carries the
// prompt's cost inside the message envelope; message_delta carries the reply's
// at the top level.
func (m *usageMeter) repair(event []byte) []byte {
	var parsed struct {
		Type string `json:"type"`
	}
	if !decodeEventData(event, &parsed) {
		return event
	}
	switch parsed.Type {
	case "message_start":
		return rewriteEvent(event, func(payload map[string]json.RawMessage) bool {
			var message map[string]json.RawMessage
			if json.Unmarshal(payload["message"], &message) != nil {
				return false
			}
			if !fillUsage(message, m.input, 0) {
				return false
			}
			encoded, err := json.Marshal(message)
			if err != nil {
				return false
			}
			payload["message"] = encoded
			return true
		})
	case "message_delta":
		return rewriteEvent(event, func(payload map[string]json.RawMessage) bool {
			return fillUsage(payload, 0, m.output())
		})
	}
	return event
}

// fillUsage writes the counts a container's usage object is missing, reporting
// whether anything moved. A zero argument means "nothing to offer for this
// field", which is how message_start (prompt only) and message_delta (reply
// only) share one function.
func fillUsage(container map[string]json.RawMessage, input, output int) bool {
	usage := map[string]json.RawMessage{}
	if raw, ok := container["usage"]; ok {
		if err := json.Unmarshal(raw, &usage); err != nil {
			return false
		}
	}
	changed := false
	for field, value := range map[string]int{"input_tokens": input, "output_tokens": output} {
		if value <= 0 || positiveNumber(usage[field]) {
			continue
		}
		usage[field] = json.RawMessage(itoa(value))
		changed = true
	}
	if !changed {
		return false
	}
	encoded, err := json.Marshal(usage)
	if err != nil {
		return false
	}
	container["usage"] = encoded
	return true
}

// assistantMessage decodes a response body that is an assistant message.
// Everything else an endpoint answers a POST with — an error envelope above
// all — is none of these repairs' business, and stamping a token count or a
// content block onto one would be the proxy inventing data.
func assistantMessage(body []byte) (map[string]json.RawMessage, bool) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, false
	}
	var kind string
	if json.Unmarshal(envelope["type"], &kind) != nil || kind != "message" {
		return nil, false
	}
	return envelope, true
}

func positiveNumber(raw json.RawMessage) bool {
	var value float64
	return json.Unmarshal(raw, &value) == nil && value > 0
}

// repairBody fills in the usage of a reply that was not streamed, whose whole
// cost — prompt and reply — sits in one usage object.
func (m *usageMeter) repairBody(body []byte) ([]byte, bool) {
	envelope, ok := assistantMessage(body)
	if !ok {
		return body, false
	}
	m.outputBytes = len(envelope["content"])
	if !fillUsage(envelope, m.input, m.output()) {
		return body, false
	}
	out, err := json.Marshal(envelope)
	if err != nil {
		return body, false
	}
	return out, true
}

// rewriteEvent re-renders one SSE event with its data payload edited in place,
// keeping the event's own name line. The edit reports whether it changed
// anything; nothing moved means the event is forwarded as the bytes that
// arrived, so an untouched stream stays byte-identical.
func rewriteEvent(event []byte, edit func(map[string]json.RawMessage) bool) []byte {
	lines := bytes.Split(event, []byte("\n"))
	for i, line := range lines {
		data, found := bytes.CutPrefix(line, []byte("data: "))
		if !found {
			continue
		}
		var payload map[string]json.RawMessage
		if json.Unmarshal(data, &payload) != nil || !edit(payload) {
			return event
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return event
		}
		out := make([][]byte, len(lines))
		copy(out, lines)
		out[i] = append([]byte("data: "), encoded...)
		return bytes.Join(out, []byte("\n"))
	}
	return event
}

// decodeEventData unmarshals an SSE event's data line into target.
func decodeEventData(event []byte, target any) bool {
	for _, line := range bytes.Split(event, []byte("\n")) {
		data, found := bytes.CutPrefix(line, []byte("data: "))
		if !found {
			continue
		}
		return json.Unmarshal(data, target) == nil
	}
	return false
}
