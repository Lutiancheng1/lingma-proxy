package feishu

import (
	"strings"
	"testing"
)

func TestParseSSEStreamAccumulatesTextAndToolCalls(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"content":"你好"}}]}`,
		``,
		`data: {"choices":[{"index":0,"delta":{"content":"，世界"}}]}`,
		``,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"feishu.search","arguments":"{\"q\""}}]}}]}`,
		``,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"测试\"}"}}]}}]}`,
		``,
		`data: {"usage":{"prompt_tokens":12,"completion_tokens":7,"total_tokens":19}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n"))

	var chunks []string
	resp, err := parseSSEStream(stream, streamingDelta{
		onText: func(s string) { chunks = append(chunks, s) },
	})
	if err != nil {
		t.Fatalf("parseSSEStream: %v", err)
	}
	if got, want := strings.Join(chunks, ""), "你好，世界"; got != want {
		t.Fatalf("text chunks=%q want %q", got, want)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	msg := resp.Choices[0].Message
	if msg.Content != "你好，世界" {
		t.Fatalf("content=%q", msg.Content)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("tool_calls len=%d", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.ID != "call_1" || tc.Function.Name != "feishu.search" {
		t.Fatalf("tool call id/name = %q/%q", tc.ID, tc.Function.Name)
	}
	if tc.Function.Arguments != `{"q":"测试"}` {
		t.Fatalf("arguments accumulated wrong: %q", tc.Function.Arguments)
	}
	if resp.Usage.PromptTokens != 12 || resp.Usage.CompletionTokens != 7 || resp.Usage.TotalTokens != 19 {
		t.Fatalf("usage missing: %+v", resp.Usage)
	}
}

func TestParseSSEStreamSkipsMalformedLines(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`: keep-alive`,
		``,
		`data: not-json`,
		``,
		`data: {"choices":[{"delta":{"content":"hello"}}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n"))
	resp, err := parseSSEStream(stream, streamingDelta{})
	if err != nil {
		t.Fatalf("parseSSEStream: %v", err)
	}
	if resp.Choices[0].Message.Content != "hello" {
		t.Fatalf("content=%q", resp.Choices[0].Message.Content)
	}
}

func TestParseSSEStreamErrorsOnEmpty(t *testing.T) {
	if _, err := parseSSEStream(strings.NewReader(""), streamingDelta{}); err == nil {
		t.Fatal("expected error on empty stream")
	}
}

func TestParseSSEStreamMultipleToolCallsByIndex(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"foo","arguments":"{\"x\":1}"}}]}}]}`,
		``,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"b","function":{"name":"bar","arguments":"{\"y\":2}"}}]}}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n"))
	resp, err := parseSSEStream(stream, streamingDelta{})
	if err != nil {
		t.Fatalf("parseSSEStream: %v", err)
	}
	calls := resp.Choices[0].Message.ToolCalls
	if len(calls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(calls))
	}
	if calls[0].ID != "a" || calls[1].ID != "b" {
		t.Fatalf("tool call ids: %+v", calls)
	}
	if calls[0].Function.Arguments != `{"x":1}` || calls[1].Function.Arguments != `{"y":2}` {
		t.Fatalf("arguments: %q %q", calls[0].Function.Arguments, calls[1].Function.Arguments)
	}
}
