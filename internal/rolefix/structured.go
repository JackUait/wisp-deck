package rolefix

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// A prompt hook, a memory selection and an auto-mode proposal all ask the model
// for a JSON object and then parse the reply with a plain JSON.parse — Claude
// Code strips a markdown fence and nothing else. What makes that safe on
// Anthropic's API is output_config.format: the server constrains the decode, so
// the reply cannot be anything but the object.
//
// Featherless accepts that field and ignores it. Measured 2026-09-02 on
// TurboVadim/Qwen3.8-27B-OBLITERATED: under a schema requiring
// {capital_city, population_millions}, a system prompt asking for one sentence
// of prose answered "Paris". The same is true of every other lever an
// OpenAI-compatible server usually offers — response_format, guided_json,
// extra_body.guided_json — all accepted, none honoured.
//
// So on that endpoint the JSON contract degrades to a suggestion in the prompt,
// and the model breaks it whenever it feels like explaining itself first. A
// /goal Stop hook on 2026-09-02 came back as a paragraph followed by a perfectly
// good verdict object, and Claude Code reported "Stop hook error: JSON
// validation failed" — twice, on a session where the hook was the whole point.
//
// The proxy therefore delivers the contract the endpoint dropped: for a request
// that declared a JSON schema, and only for one, an assistant text block that
// does not parse is replaced by the JSON object it contains.

// keepAliveInterval is how often the proxy writes an SSE comment while it holds
// a text block back to inspect it. Claude Code arms a byte-stall watchdog on
// every stream it reads: 20s of silence paints a network banner and the end of
// the budget aborts and replays the turn. A comment is bytes, which is all the
// watchdog counts — the same trick Featherless itself uses to fill the wait
// before a first token. A var so the test can drive it without sleeping.
var keepAliveInterval = 5 * time.Second

// maxRepairBytes caps the text held back for repair. Past it the reply is
// delivered as it arrived: something that large is not a JSON verdict, and
// holding it would trade a hook error for memory growth. A var so the test can
// drive it without allocating the real budget.
var maxRepairBytes = 8 << 20

// responsePlan carries to a response everything repairing it needs: the JSON
// contract its request declared, if any; a meter holding the prompt cost the
// endpoint will report as zero; and the tools it supplied, which decide both
// what a tool call may be named and whether an empty reply lost one.
type responsePlan struct {
	required []string
	schema   bool
	usage    *usageMeter
	tools    map[string]string
	// declaresTools is separate from len(tools): a request can supply tools
	// whose names are all ambiguous, and it is the supplying that decides
	// whether an empty reply lost a tool call.
	declaresTools bool
}

type planKeyType struct{}

var planKey planKeyType

// JSONSchemaContract reports whether a request asked the endpoint to constrain
// its reply to a JSON schema, and which fields that schema requires. Both the
// current output_config.format and the deprecated top-level output_format count;
// output_config also carries effort on every ordinary turn, so the format is what
// is looked for, never the object holding it.
func JSONSchemaContract(body []byte) ([]string, bool) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, false
	}
	format, ok := envelope["output_format"]
	if !ok {
		var config map[string]json.RawMessage
		if err := json.Unmarshal(envelope["output_config"], &config); err != nil {
			return nil, false
		}
		if format, ok = config["format"]; !ok {
			return nil, false
		}
	}
	var declared struct {
		Type   string `json:"type"`
		Schema struct {
			Required []string `json:"required"`
		} `json:"schema"`
	}
	if err := json.Unmarshal(format, &declared); err != nil || declared.Type != "json_schema" {
		return nil, false
	}
	return declared.Schema.Required, true
}

// RepairJSONText returns the text a client relying on a JSON schema should have
// received, and whether anything moved. Text that already parses — Claude Code
// strips a markdown fence first, so text inside one parses too — is returned
// untouched: repairing what is not broken is how a proxy invents bugs. Text
// holding no complete object is also returned untouched, because the endpoint's
// own answer beats a fabricated one.
func RepairJSONText(text string, required []string) (string, bool) {
	if json.Valid([]byte(stripFence(text))) {
		return text, false
	}
	objects := topLevelObjects(text)
	if len(objects) == 0 {
		return text, false
	}
	// Later is better — a model states its verdict last — but a schema hit beats
	// position, so a trailing aside like "the schema is {...}" cannot win over
	// the object that actually answers.
	best := objects[len(objects)-1]
	for i := len(objects) - 1; i >= 0; i-- {
		if hasAll(objects[i], required) {
			best = objects[i]
			break
		}
	}
	return best, true
}

// stripFence mirrors what Claude Code does to a model's reply before parsing it:
// trim, drop a leading ```lang and a trailing ```, trim again.
func stripFence(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	if _, rest, found := strings.Cut(trimmed, "\n"); found {
		trimmed = rest
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(trimmed), "```"))
}

// topLevelObjects returns every complete JSON object in text, outermost first
// and in order. Scanning past each match is what keeps a member object out of
// the list: `{"ok":1,"detail":{"ok":2}}` decodes at its own opening brace too, so
// a scan that did not skip would answer with the wrong one.
func topLevelObjects(text string) []string {
	var found []string
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		decoder := json.NewDecoder(strings.NewReader(text[i:]))
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			continue
		}
		end := i + int(decoder.InputOffset())
		found = append(found, text[i:end])
		i = end - 1
	}
	return found
}

func hasAll(object string, required []string) bool {
	if len(required) == 0 {
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(object), &fields); err != nil {
		return false
	}
	for _, key := range required {
		if _, ok := fields[key]; !ok {
			return false
		}
	}
	return true
}

// repairResponse rewrites an assistant reply so it reports the tokens it spent
// and, when its request declared one, honours that JSON schema. A streamed body
// is repaired event by event; a body that was not streamed is repaired in place.
// Anything else is left alone.
func repairResponse(resp *http.Response, plan responsePlan) error {
	contentType := resp.Header.Get("Content-Type")
	switch {
	case strings.Contains(contentType, "text/event-stream"):
		source := resp.Body
		reader, writer := io.Pipe()
		go repairEventStream(writer, source, plan)
		resp.Body = reader
		resp.ContentLength = -1
		resp.Header.Del("Content-Length")
	case strings.Contains(contentType, "application/json"):
		return repairJSONBody(resp, plan)
	}
	return nil
}

func repairJSONBody(resp *http.Response, plan responsePlan) error {
	original := resp.Body
	// One byte past the budget is how an oversized body is recognized, and it is
	// then handed on unread: io.LimitReader alone would truncate it silently.
	body, err := io.ReadAll(io.LimitReader(original, int64(maxRepairBytes)+1))
	if err != nil || len(body) > maxRepairBytes {
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(body), original), original}
		return nil
	}
	_ = original.Close()
	if plan.schema {
		if fixed, changed := repairMessageBody(body, plan.required); changed {
			body = fixed
		}
	}
	if fixed, changed := repairToolNamesInBody(plan.tools, body); changed {
		body = fixed
	}
	if fixed, changed := noticeBody(plan.declaresTools, body); changed {
		body = fixed
	}
	if fixed, changed := plan.usage.repairBody(body); changed {
		body = fixed
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", itoa(len(body)))
	return nil
}

// repairMessageBody rewrites the text of every text block in a non-streamed
// message, leaving every other key of the envelope byte-for-byte as it arrived.
func repairMessageBody(body []byte, required []string) ([]byte, bool) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return body, false
	}
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(envelope["content"], &blocks); err != nil {
		return body, false
	}
	changed := false
	for _, block := range blocks {
		var kind, text string
		if err := json.Unmarshal(block["type"], &kind); err != nil || kind != "text" {
			continue
		}
		if err := json.Unmarshal(block["text"], &text); err != nil {
			continue
		}
		fixed, moved := RepairJSONText(text, required)
		if !moved {
			continue
		}
		encoded, err := json.Marshal(fixed)
		if err != nil {
			continue
		}
		block["text"] = encoded
		changed = true
	}
	if !changed {
		return body, false
	}
	content, err := json.Marshal(blocks)
	if err != nil {
		return body, false
	}
	envelope["content"] = content
	out, err := json.Marshal(envelope)
	if err != nil {
		return body, false
	}
	return out, true
}

// repairEventStream copies an Anthropic SSE stream through, repairing each
// event as it passes: the token counts the endpoint left at zero, the spelling
// of a tool name it did not check, and — for a request that declared a JSON
// schema — the text of a block held back long enough to check it against the
// contract. A block that already conforms is replayed exactly as it arrived;
// one that does not is replaced by a single delta carrying the object it
// contained. A reply that turns out to hold nothing at all is given the notice
// that says so.
func repairEventStream(dst *io.PipeWriter, src io.ReadCloser, plan responsePlan) {
	defer func() { _ = src.Close() }()

	var mu sync.Mutex
	holding := false
	write := func(p []byte) bool {
		mu.Lock()
		defer mu.Unlock()
		_, err := dst.Write(p)
		return err == nil
	}
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(keepAliveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				mu.Lock()
				if holding {
					_, _ = dst.Write([]byte(": keep-alive\n\n"))
				}
				mu.Unlock()
			}
		}
	}()

	stream := &textBlockBuffer{required: plan.required, schema: plan.schema}
	empty := &emptyReplyWatch{armed: plan.declaresTools}
	var event bytes.Buffer
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 0, 64*1024), maxRewriteBytes)
	failed := false
	for scanner.Scan() {
		line := scanner.Text()
		event.WriteString(line)
		event.WriteByte('\n')
		if line != "" {
			continue
		}
		plan.usage.observe(event.Bytes())
		notice := empty.precede(event.Bytes())
		repaired := repairToolName(plan.tools, plan.usage.repair(event.Bytes()))
		out, held := stream.consume(repaired)
		if len(notice) > 0 {
			out = append(notice, out...)
		}
		mu.Lock()
		holding = held
		mu.Unlock()
		sent := len(out) == 0 || write(out)
		// The reset comes after the write: a pass-through event is forwarded as
		// a slice of this very buffer.
		event.Reset()
		if !sent {
			failed = true
			break
		}
	}
	if !failed {
		if rest := stream.flush(event.Bytes()); len(rest) > 0 {
			write(rest)
		}
	}
	close(stop)
	if err := scanner.Err(); err != nil {
		_ = dst.CloseWithError(err)
		return
	}
	_ = dst.Close()
}

// textBlockBuffer decides, one SSE event at a time, what the client sees.
type textBlockBuffer struct {
	required []string
	schema   bool
	index    *int // the text block being held, if any
	text     strings.Builder
	raw      bytes.Buffer // the held events, verbatim, for a conforming block
}

// consume returns the bytes to forward for one event and whether the stream is
// now holding a block back.
func (b *textBlockBuffer) consume(event []byte) ([]byte, bool) {
	if !b.schema {
		// Only a request that declared a JSON schema has a contract to hold a
		// text block back for. A normal turn's prose is the answer.
		return event, false
	}
	kind, index, block := classify(event)
	switch {
	case kind == "content_block_start" && block == "text" && b.index == nil && startText(event) == "":
		// A start already carrying text has been forwarded by the time the
		// block ends, so its text can no longer be taken back — leave the whole
		// block alone rather than half-repair it.
		b.index = &index
		b.text.Reset()
		b.raw.Reset()
		return event, true
	case kind == "content_block_delta" && b.index != nil && index == *b.index && block == "text_delta":
		if b.text.Len()+len(event) > maxRepairBytes {
			// Too large to be a verdict: release what is held and stop holding.
			return b.releaseHeld(event), false
		}
		b.text.WriteString(deltaText(event))
		b.raw.Write(event)
		return nil, true
	case kind == "content_block_stop" && b.index != nil && index == *b.index:
		out := b.release(event)
		return out, false
	}
	return event, b.index != nil
}

// flush releases anything still held when the stream ends early, so a truncated
// response reaches the client as the endpoint sent it.
func (b *textBlockBuffer) flush(trailing []byte) []byte {
	if b.index == nil {
		return trailing
	}
	return b.releaseHeld(trailing)
}

// releaseHeld hands back everything buffered, followed by trailing, and stops
// holding. The bytes are copied out: the caller writes them after the buffer is
// reset for the next block, so a slice aliasing it would be rewritten underfoot.
func (b *textBlockBuffer) releaseHeld(trailing []byte) []byte {
	out := make([]byte, 0, b.raw.Len()+len(trailing))
	out = append(out, b.raw.Bytes()...)
	out = append(out, trailing...)
	b.reset()
	return out
}

func (b *textBlockBuffer) reset() {
	b.index = nil
	b.raw.Reset()
	b.text.Reset()
}

func (b *textBlockBuffer) release(stopEvent []byte) []byte {
	text := b.text.String()
	fixed, changed := RepairJSONText(text, b.required)
	if !changed {
		return b.releaseHeld(stopEvent)
	}
	payload, err := json.Marshal(map[string]any{
		"type":  "content_block_delta",
		"index": indexOf(stopEvent),
		"delta": map[string]any{"type": "text_delta", "text": fixed},
	})
	if err != nil {
		return b.releaseHeld(stopEvent)
	}
	var out bytes.Buffer
	out.WriteString("event: content_block_delta\ndata: ")
	out.Write(payload)
	out.WriteString("\n\n")
	out.Write(stopEvent)
	b.reset()
	return out.Bytes()
}

type sseEvent struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content_block"`
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
}

func parseEvent(event []byte) (sseEvent, bool) {
	var parsed sseEvent
	for _, line := range strings.Split(string(event), "\n") {
		data, found := strings.CutPrefix(line, "data: ")
		if !found {
			continue
		}
		if err := json.Unmarshal([]byte(data), &parsed); err != nil {
			return parsed, false
		}
		return parsed, true
	}
	return parsed, false
}

// classify names the event, its block index, and the inner type that decides
// whether the block is one the repair cares about.
func classify(event []byte) (string, int, string) {
	parsed, ok := parseEvent(event)
	if !ok {
		return "", 0, ""
	}
	switch parsed.Type {
	case "content_block_start":
		return parsed.Type, parsed.Index, parsed.ContentBlock.Type
	case "content_block_delta":
		return parsed.Type, parsed.Index, parsed.Delta.Type
	}
	return parsed.Type, parsed.Index, ""
}

func startText(event []byte) string {
	parsed, _ := parseEvent(event)
	return parsed.ContentBlock.Text
}

func deltaText(event []byte) string {
	parsed, _ := parseEvent(event)
	return parsed.Delta.Text
}

func indexOf(event []byte) int {
	parsed, _ := parseEvent(event)
	return parsed.Index
}

func itoa(n int) string {
	out, _ := json.Marshal(n)
	return string(out)
}
