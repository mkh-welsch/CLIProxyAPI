package executor

import (
	"context"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestXAIExecutorPreparesDesktopForwardedNote(t *testing.T) {
	exec := NewXAIExecutor(&config.Config{})
	prepared, err := exec.prepareResponsesRequest(context.Background(), cliproxyexecutor.Request{
		Model:   "grok-4.6",
		Payload: []byte(`{"model":"grok-4.6","input":[{"type":"function_call_output","name":"send_message_to_thread","namespace":"codex_app","output":"A parent task forwarded this question."},{"type":"message","role":"user","content":[{"type":"input_text","text":"Reply briefly."}]}],"tools":[{"type":"function","name":"echo","description":"Echo a string","parameters":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}}]}`),
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse}, true)
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(prepared.body, "input.0.type").String() != "message" || gjson.GetBytes(prepared.body, "input.0.role").String() != "user" {
		t.Fatal("forwarded note did not survive the full request pipeline as user context")
	}
	if gjson.GetBytes(prepared.body, "input.1.content.0.text").String() != "Reply briefly." {
		t.Fatal("user prompt was changed")
	}
	if gjson.GetBytes(prepared.body, "tools.0.name").String() != "echo" || gjson.GetBytes(prepared.body, "tools.#").Int() != 1 {
		t.Fatal("tool declaration was changed")
	}
}
