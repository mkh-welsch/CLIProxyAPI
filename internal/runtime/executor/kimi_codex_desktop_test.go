package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestKimiDesktopForwardedAssignmentOnWire(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			var upstream []byte
			ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", kimiRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
				var err error
				upstream, err = io.ReadAll(req.Body)
				if err != nil {
					return nil, err
				}
				contentType := "application/json"
				response := `{"id":"chatcmpl_test","object":"chat.completion","created":1,"model":"k3","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`
				if stream {
					contentType = "text/event-stream"
					response = "data: " + `{"id":"chatcmpl_test","object":"chat.completion.chunk","created":1,"model":"k3","choices":[{"index":0,"delta":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response))}, nil
			}))
			payload := []byte(`{"model":"kimi-k3","input":[
				{"type":"function_call_output","namespace":"codex_app","name":"send_message_to_thread","output":"Read the fixture and report its value."},
				{"type":"function_call","call_id":"call_read","name":"read_fixture","arguments":"{}"},
				{"type":"function_call_output","call_id":"call_read","output":"fixture-value"},
				{"type":"function_call_output","namespace":"codex_app","name":"send_message_to_thread","call_id":"","output":"Correction: report the value in uppercase."}
			],"tools":[{"type":"function","name":"read_fixture","parameters":{"type":"object","properties":{}}}]}`)
			original := bytes.Clone(payload)
			executor := NewKimiExecutor(&config.Config{})
			auth := &cliproxyauth.Auth{Attributes: map[string]string{}, Metadata: map[string]any{"access_token": "test-token"}}
			req := cliproxyexecutor.Request{Model: "kimi-k3", Payload: payload}
			opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: payload}
			if stream {
				result, err := executor.ExecuteStream(ctx, auth, req, opts)
				if err != nil {
					t.Fatal(err)
				}
				for chunk := range result.Chunks {
					if chunk.Err != nil {
						t.Fatal(chunk.Err)
					}
				}
			} else if _, err := executor.Execute(ctx, auth, req, opts); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(payload, original) {
				t.Fatal("request caller's input was mutated")
			}
			if gjson.GetBytes(upstream, "model").String() != "k3" {
				t.Fatalf("wrong upstream model: %s", upstream)
			}
			messages := gjson.GetBytes(upstream, "messages").Array()
			if len(messages) != 4 {
				t.Fatalf("want assignment, assistant call, tool result, correction: %s", upstream)
			}
			for _, i := range []int{0, 3} {
				if messages[i].Get("role").String() != "user" || !strings.Contains(messages[i].Get("content").Raw, "codex_app.send_message_to_thread") {
					t.Fatalf("forwarded message lost or emitted as a tool result: %s", upstream)
				}
			}
			if !strings.Contains(messages[0].Raw, "Read the fixture") || !strings.Contains(messages[3].Raw, "uppercase") {
				t.Fatalf("assignment or correction text lost: %s", upstream)
			}
			if messages[1].Get("tool_calls.0.id").String() != "call_read" ||
				messages[2].Get("role").String() != "tool" || messages[2].Get("tool_call_id").String() != "call_read" ||
				messages[2].Get("content").String() != "fixture-value" {
				t.Fatalf("real tool call/result corrupted: %s", upstream)
			}
			if gjson.GetBytes(upstream, "tools.0.function.name").String() != "read_fixture" {
				t.Fatalf("tool declaration lost: %s", upstream)
			}
		})
	}
}

func TestKimiDesktopNormalizationLeavesOtherInputsUntouched(t *testing.T) {
	for _, input := range []string{
		`{"type":"function_call_output","namespace":"codex_app","name":"send_message_to_thread","call_id":"real_call","output":"result"}`,
		`{"type":"function_call_output","namespace":"codex_app","name":"send_message_to_thread","call_id":null,"output":"result"}`,
		`{"type":"function_call_output","namespace":"other","name":"send_message_to_thread","output":"result"}`,
		`{"type":"function_call_output","namespace":"codex_app","name":"other","output":"result"}`,
		`{"type":"function_call_output","output":"orphan from an unknown tool"}`,
		`{"type":"function_call_output","namespace":"codex_app","name":"send_message_to_thread","output":{"text":"unsupported"}}`,
		`{"type":"message","role":"user","content":"hello"}`,
	} {
		body := []byte(`{ "input": [` + input + `] }`)
		if got := normalizeKimiCodexDesktopRequest(body); !bytes.Equal(got, body) {
			t.Fatalf("unexpected rewrite of %s: %s", body, got)
		}
	}
	for _, body := range []string{"invalid", "null", `{"input":"hello"}`, `{"messages":[{"role":"tool","content":"hello"}]}`} {
		if got := normalizeKimiCodexDesktopRequest([]byte(body)); string(got) != body {
			t.Fatalf("unexpected rewrite of %s: %s", body, got)
		}
	}
}

func TestKimiDesktopNormalizationPreservesPrecisionAndIsIdempotent(t *testing.T) {
	body := []byte(`{"large":9007199254740993,"input":[{"type":"function_call_output","namespace":"codex_app","name":"send_message_to_thread","output":"hello"}]}`)
	got := normalizeKimiCodexDesktopRequest(body)
	if gjson.GetBytes(got, "large").Raw != "9007199254740993" {
		t.Fatalf("numeric precision lost: %s", got)
	}
	if again := normalizeKimiCodexDesktopRequest(got); !bytes.Equal(again, got) {
		t.Fatal("second normalization changed the request")
	}
}
