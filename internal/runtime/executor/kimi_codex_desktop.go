package executor

import (
	"fmt"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// normalizeKimiCodexDesktopRequest preserves forwarded task messages as user
// messages. Desktop encodes these as function_call_output without a call_id,
// even on a task's first turn; they are not replies to an assistant tool call.
// Match the Desktop envelope before translation discards its provenance.
func normalizeKimiCodexDesktopRequest(body []byte) []byte {
	if !gjson.ValidBytes(body) {
		return body
	}
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body
	}
	out := body
	for i, item := range input.Array() {
		if item.Get("type").String() != "function_call_output" ||
			item.Get("namespace").String() != "codex_app" ||
			item.Get("name").String() != "send_message_to_thread" {
			continue
		}
		callID := item.Get("call_id")
		if callID.Exists() && (callID.Type != gjson.String || callID.String() != "") {
			continue
		}
		output := item.Get("output")
		if output.Type != gjson.String {
			continue
		}
		message, err := sjson.SetBytes([]byte(`{"type":"message","role":"user","content":[{"type":"input_text","text":""}]}`),
			"content.0.text", "Message forwarded from another Codex task via codex_app.send_message_to_thread:\n"+output.String())
		if err != nil {
			return body
		}
		out, err = sjson.SetRawBytes(out, fmt.Sprintf("input.%d", i), message)
		if err != nil {
			return body
		}
	}
	return out
}
