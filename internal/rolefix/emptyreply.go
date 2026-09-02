package rolefix

import (
	"bytes"
	"encoding/json"
)

// An assistant turn with no content block at all is not a shape Anthropic's API
// can produce, and it is not the model declining to act. Measured 2026-09-02 on
// the real first request a Claude Code pane sends: Qwen/Qwen3.5-397B-A17B billed
// 181 completion tokens for zero bytes of reply, and
// Qwen/Qwen3-VL-30B-A3B-Instruct billed 181 for 22 tokens of prose — Featherless
// strips the model's own tool call out of the text and then emits no tool call
// in its place, so 159 generated tokens are simply destroyed. Twenty-one of the
// twenty-six models wide enough to run Claude Code call tools normally through
// the same endpoint; the two usable Qwen models are the ones this happens to.
//
// Forwarded as it arrives, that reply is a turn where nothing happens and
// nothing is said — no tool runs, no text appears, no error is raised. It is
// what a /goal Stop hook loops on forever, and it is why a pane can spin for
// forty minutes with a clean screen. The proxy cannot recover the discarded
// bytes; what it can do is refuse to pass the silence on.
const emptyReplyNotice = "[wisp-deck] The endpoint returned an empty reply — it generated output for this " +
	"turn and dropped it instead of sending a tool call, so nothing ran. Measured on Featherless's Qwen " +
	"models; GLM, Kimi, DeepSeek and MiniMax were verified to call tools normally on the same endpoint. " +
	"Switch the subscription model if this repeats."

// noticeEvents renders the notice as the content block the reply is missing.
func noticeEvents() []byte {
	start, _ := json.Marshal(map[string]any{
		"type": "content_block_start", "index": 0,
		"content_block": map[string]any{"type": "text", "text": ""},
	})
	delta, _ := json.Marshal(map[string]any{
		"type": "content_block_delta", "index": 0,
		"delta": map[string]any{"type": "text_delta", "text": emptyReplyNotice},
	})
	var out bytes.Buffer
	out.WriteString("event: content_block_start\ndata: ")
	out.Write(start)
	out.WriteString("\n\nevent: content_block_delta\ndata: ")
	out.Write(delta)
	out.WriteString("\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
	return out.Bytes()
}

// emptyReplyWatch tracks whether a streamed reply ever opened a content block,
// so the notice can be written in front of the event that ends the message.
type emptyReplyWatch struct {
	armed bool // the request declared tools, so a tool call was possible
	saw   bool
	told  bool
}

// precede returns the events to write before this one. A reply that opened no
// block by the time the message ends gets the notice; every other event gets
// nothing.
func (w *emptyReplyWatch) precede(event []byte) []byte {
	var parsed struct {
		Type string `json:"type"`
	}
	if !decodeEventData(event, &parsed) {
		return nil
	}
	if parsed.Type == "content_block_start" {
		w.saw = true
		return nil
	}
	// message_delta carries the stop reason and message_stop ends the turn; a
	// stream cut short reaches neither, and a half-delivered turn is the
	// endpoint's own failure to report, not a dropped tool call.
	if parsed.Type != "message_delta" && parsed.Type != "message_stop" {
		return nil
	}
	if !w.armed || w.saw || w.told {
		return nil
	}
	w.told = true
	return noticeEvents()
}

// noticeBody replaces the content of a reply that was not streamed and arrived
// with none.
func noticeBody(armed bool, body []byte) ([]byte, bool) {
	if !armed {
		return body, false
	}
	envelope, ok := assistantMessage(body)
	if !ok {
		return body, false
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(envelope["content"], &blocks); err != nil || len(blocks) > 0 {
		return body, false
	}
	content, err := json.Marshal([]map[string]string{{"type": "text", "text": emptyReplyNotice}})
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
