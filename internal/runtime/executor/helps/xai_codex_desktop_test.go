package helps

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

const note = `{"input":[{"type":"function_call_output","namespace":"codex_app","name":"send_message_to_thread","output":"external task says hello"}],"tools":[]}`
const nestedSchema = `{"type":"object","properties":{},"oneOf":[{"$ref":"#/$defs/view"},{"$ref":"#/$defs/create"}],"$defs":{"id":{"type":"string"},"view":{"type":"object","properties":{"mode":{"enum":["view"],"type":"string"},"id":{"$ref":"#/$defs/id"}},"required":["mode","id"],"additionalProperties":false},"create":{"oneOf":[{"type":"object","properties":{"mode":{"enum":["create"],"type":"string"},"kind":{"enum":["cron"],"type":"string"}},"required":["mode","kind"],"additionalProperties":false},{"type":"object","properties":{"mode":{"enum":["create"],"type":"string"},"kind":{"enum":["heartbeat"],"type":"string"}},"required":["mode","kind"],"additionalProperties":false}]}}}`

func TestForwardedNoteAndValidCall(t *testing.T) {
	var payload map[string]any
	json.Unmarshal([]byte(note), &payload)
	input := payload["input"].([]any)
	valid := map[string]any{"type": "function_call_output", "namespace": "codex_app", "name": "send_message_to_thread", "call_id": "call_real", "output": "result"}
	unknown := map[string]any{"type": "function_call_output", "name": "other_tool", "output": "opaque"}
	payload["input"] = append(input, valid, unknown)
	raw, _ := json.Marshal(payload)
	out := NormalizeXAICodexDesktopRequest("grok-4.6", raw)
	var got map[string]any
	json.Unmarshal(out, &got)
	items := got["input"].([]any)
	converted := items[0].(map[string]any)
	if converted["type"] != "message" || converted["role"] != "user" {
		t.Fatalf("note role: %v", converted)
	}
	if !bytes.Contains(out, []byte("external task says hello")) || !bytes.Contains(out, []byte("Untrusted tool output")) {
		t.Fatal("note text/provenance lost")
	}
	if !reflect.DeepEqual(items[1], valid) || !reflect.DeepEqual(items[2], unknown) {
		t.Fatal("legitimate or unrelated output changed")
	}
	if !bytes.Equal(NormalizeXAICodexDesktopRequest("grok-4.6", out), out) {
		t.Fatal("not idempotent")
	}
}

func TestOtherModelsUnchanged(t *testing.T) {
	for _, model := range []string{"gpt-6-astra", "grok-4.5"} {
		if !bytes.Equal(NormalizeXAICodexDesktopRequest(model, []byte(note)), []byte(note)) {
			t.Fatal(model)
		}
	}
	for _, raw := range []string{`not json`, `{"input":[],"tools":[],"n":9007199254740993}`} {
		if !bytes.Equal(NormalizeXAICodexDesktopRequest("grok-4.6", []byte(raw)), []byte(raw)) {
			t.Fatal("unexpected passthrough change")
		}
	}
}

func TestAutomationSchemaFlattenedWithConstraints(t *testing.T) {
	var schema map[string]any
	json.Unmarshal([]byte(nestedSchema), &schema)
	fixed, ok := normalizeAutomationSchema(schema)
	if !ok {
		t.Fatal("normalization declined")
	}
	leaves := fixed["oneOf"].([]any)
	if len(leaves) != 3 {
		t.Fatal("variant lost")
	}
	view := leaves[0].(map[string]any)
	if view["additionalProperties"] != false || !reflect.DeepEqual(view["required"], []any{"mode", "id"}) {
		t.Fatal("constraints lost")
	}
	id := view["properties"].(map[string]any)["id"].(map[string]any)
	if id["type"] != "string" {
		t.Fatal("reference not preserved")
	}
	tool := map[string]any{"type": "function", "name": "automation_update", "parameters": schema}
	payload := map[string]any{"tools": []any{map[string]any{"type": "namespace", "name": "mcp__codex_app", "tools": []any{tool}}, map[string]any{"type": "web_search"}}}
	raw, _ := json.Marshal(payload)
	out := NormalizeXAICodexDesktopRequest("grok-4.6-exact", raw)
	if bytes.Contains(out, []byte(`"$ref"`)) || !bytes.Contains(out, []byte(`"web_search"`)) {
		t.Fatal("normalization or preservation failed")
	}
	if !bytes.Equal(NormalizeXAICodexDesktopRequest("grok-4.6-exact", out), out) {
		t.Fatal("schema not idempotent")
	}
}

func TestUnsupportedSchemasPreserved(t *testing.T) {
	for name, raw := range map[string]string{
		"recursive":         `{"oneOf":[{"$ref":"#/$defs/x"}],"$defs":{"x":{"$ref":"#/$defs/x"}}}`,
		"missing":           `{"oneOf":[{"$ref":"#/$defs/missing"}]}`,
		"overlapping":       `{"oneOf":[{"type":"object"},{"type":"object"}]}`,
		"constrained_union": `{"additionalProperties":false,"oneOf":[{"type":"object"},{"type":"object"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			var schema map[string]any
			json.Unmarshal([]byte(raw), &schema)
			if _, ok := normalizeAutomationSchema(schema); ok {
				t.Fatal("unsafe schema accepted")
			}
		})
	}
}
