package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lingma-ipc-proxy/internal/service"
	"lingma-ipc-proxy/internal/toolemulation"
)

func TestNormalizeOpenAIRequestCollectsSystemMessages(t *testing.T) {
	req := openAIChatRequest{
		Model: "test-model",
		Messages: []rawMessage{
			{Role: "system", Content: "You are concise."},
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi"},
			{Role: "system", Content: "Answer in Chinese."},
			{Role: "tool", Content: "ignored"},
			{Role: "user", Content: []any{
				map[string]any{"type": "text", "text": "Follow up"},
			}},
		},
	}

	normalized, err := normalizeOpenAIRequest(req)
	if err != nil {
		t.Fatalf("normalizeOpenAIRequest() error = %v", err)
	}
	if normalized.Model != "test-model" {
		t.Fatalf("model = %q", normalized.Model)
	}
	if normalized.System != "You are concise.\n\nAnswer in Chinese." {
		t.Fatalf("system = %q", normalized.System)
	}
	if len(normalized.Messages) != 3 {
		t.Fatalf("message count = %d", len(normalized.Messages))
	}
	if normalized.Messages[0].Role != "user" || normalized.Messages[0].Text != "Hello" {
		t.Fatalf("first message = %+v", normalized.Messages[0])
	}
	if normalized.Messages[1].Role != "assistant" || normalized.Messages[1].Text != "Hi" {
		t.Fatalf("second message = %+v", normalized.Messages[1])
	}
	if normalized.Messages[2].Role != "user" || normalized.Messages[2].Text != "Follow up" {
		t.Fatalf("third message = %+v", normalized.Messages[2])
	}
}

func TestCapabilitiesAdvertiseAgentCompatibility(t *testing.T) {
	server := NewServer("", service.New(service.Config{
		Model:   "Qwen3-Coder",
		Timeout: time.Second,
	}))

	req := httptest.NewRequest(http.MethodGet, "/capabilities", nil)
	rec := httptest.NewRecorder()
	server.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	features, ok := body["features"].(map[string]any)
	if !ok {
		t.Fatalf("missing features: %#v", body)
	}
	for _, key := range []string{"tools", "tool_alias_mapping", "images", "local_image_paths", "image_auto_resize"} {
		if features[key] != true {
			t.Fatalf("feature %s = %#v", key, features[key])
		}
	}
	protocols, ok := body["protocols"].([]any)
	if !ok {
		t.Fatalf("missing protocols: %#v", body)
	}
	foundResponses := false
	for _, item := range protocols {
		if item == "openai.responses" {
			foundResponses = true
			break
		}
	}
	if !foundResponses {
		t.Fatalf("protocols = %#v", protocols)
	}
}

func TestDebugAppLogsUsesProviderAndSkipsRecorder(t *testing.T) {
	server := NewServer("", service.New(service.Config{
		Model:   "Qwen3-Coder",
		Timeout: time.Second,
	}))
	server.AppLogs = func(limit int, source string) []DebugAppLogRecord {
		if limit != 7 {
			t.Fatalf("limit = %d", limit)
		}
		if source != "app" {
			t.Fatalf("source = %q", source)
		}
		return []DebugAppLogRecord{{
			Time:    "12:00:00",
			Source:  "app",
			Level:   "info",
			Message: "hello",
		}}
	}

	req := httptest.NewRequest(http.MethodGet, "/debug/app-logs?limit=7&source=app", nil)
	rec := httptest.NewRecorder()
	server.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["kind"] != "desktop_app_logs" {
		t.Fatalf("kind = %#v", body["kind"])
	}
	if body["count"].(float64) != 1 {
		t.Fatalf("count = %#v", body["count"])
	}
	if len(server.debugRecords(10)) != 0 {
		t.Fatalf("debug app log inspection should not be recorded as a request")
	}
}

func TestResponsesRequestToChatRequest(t *testing.T) {
	req := openAIResponsesRequest{
		Model:        "resp-model",
		Instructions: "You are concise.",
		Input: []any{map[string]any{
			"role":    "user",
			"content": []any{map[string]any{"type": "input_text", "text": "hello"}},
		}},
		MaxOutputTokens: 64,
		Reasoning:       map[string]any{"effort": "medium"},
		Text:            map[string]any{"format": map[string]any{"type": "json_object"}},
	}

	chatReq, err := responsesRequestToChatRequest(req)
	if err != nil {
		t.Fatalf("responsesRequestToChatRequest() error = %v", err)
	}
	if chatReq.Model != "resp-model" {
		t.Fatalf("model = %q", chatReq.Model)
	}
	if chatReq.MaxCompletionTokens != 64 {
		t.Fatalf("max completion tokens = %d", chatReq.MaxCompletionTokens)
	}
	if chatReq.ReasoningEffort != "medium" {
		t.Fatalf("reasoning effort = %q", chatReq.ReasoningEffort)
	}
	if len(chatReq.Messages) != 2 {
		t.Fatalf("message count = %d", len(chatReq.Messages))
	}
	if chatReq.Messages[0].Role != "system" || extractText(chatReq.Messages[0].Content) != "You are concise." {
		t.Fatalf("first message = %+v", chatReq.Messages[0])
	}
	if chatReq.Messages[1].Role != "user" || extractText(chatReq.Messages[1].Content) != "hello" {
		t.Fatalf("second message = %+v", chatReq.Messages[1])
	}
	if extractResponseFormat(chatReq.ResponseFormat) != "json_object" {
		t.Fatalf("response format = %#v", chatReq.ResponseFormat)
	}
}

func TestResponsesRequestStringInput(t *testing.T) {
	chatReq, err := responsesRequestToChatRequest(openAIResponsesRequest{Input: "hello"})
	if err != nil {
		t.Fatalf("responsesRequestToChatRequest() error = %v", err)
	}
	if len(chatReq.Messages) != 1 {
		t.Fatalf("message count = %d", len(chatReq.Messages))
	}
	if chatReq.Messages[0].Role != "user" || extractText(chatReq.Messages[0].Content) != "hello" {
		t.Fatalf("message = %+v", chatReq.Messages[0])
	}
}

func TestResponsesRequestSingleObjectPreservesRole(t *testing.T) {
	chatReq, err := responsesRequestToChatRequest(openAIResponsesRequest{Input: map[string]any{
		"role":    "assistant",
		"content": []any{map[string]any{"type": "output_text", "text": "hello"}},
	}})
	if err != nil {
		t.Fatalf("responsesRequestToChatRequest() error = %v", err)
	}
	if len(chatReq.Messages) != 1 {
		t.Fatalf("message count = %d", len(chatReq.Messages))
	}
	if chatReq.Messages[0].Role != "assistant" || extractText(chatReq.Messages[0].Content) != "hello" {
		t.Fatalf("message = %+v", chatReq.Messages[0])
	}
}

func TestNormalizeAnthropicRequestMapsThinkingToReasoningEffort(t *testing.T) {
	req := anthropicRequest{
		Model:     "Qwen3.6-Plus",
		MaxTokens: 256,
		Thinking: map[string]any{
			"type":          "enabled",
			"budget_tokens": 2048,
		},
		Messages: []rawMessage{
			{Role: "user", Content: "请先思考再回答"},
		},
	}

	normalized, err := normalizeAnthropicRequest(req)
	if err != nil {
		t.Fatalf("normalizeAnthropicRequest() error = %v", err)
	}
	if normalized.ReasoningEffort != "medium" {
		t.Fatalf("reasoning effort = %q", normalized.ReasoningEffort)
	}
}

func TestNormalizeAnthropicRequestAdaptiveThinkingEnablesReasoning(t *testing.T) {
	req := anthropicRequest{
		Model:     "Qwen3-Thinking",
		MaxTokens: 256,
		Thinking: map[string]any{
			"type": "adaptive",
		},
		Messages: []rawMessage{
			{Role: "user", Content: "请先思考再回答"},
		},
	}

	normalized, err := normalizeAnthropicRequest(req)
	if err != nil {
		t.Fatalf("normalizeAnthropicRequest() error = %v", err)
	}
	if normalized.ReasoningEffort != "medium" {
		t.Fatalf("reasoning effort = %q", normalized.ReasoningEffort)
	}
}

func TestOpenAIResponsesMethodNotAllowed(t *testing.T) {
	server := NewServer("", service.New(service.Config{
		Model:   "Qwen3-Coder",
		Timeout: time.Second,
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	rec := httptest.NewRecorder()
	server.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestWriteOpenAIResponseStreamCompletedToolOnlyEmitsFunctionCallLifecycle(t *testing.T) {
	rec := httptest.NewRecorder()
	emitter := newOpenAIResponseStreamEmitter(rec, rec, "resp_1")
	result := &service.ChatResult{
		Model: "kmodel",
		ToolCalls: []toolemulation.ToolCall{{
			ID:        "call_1",
			Name:      "read_file",
			Arguments: map[string]any{"file_path": "go.mod"},
		}},
	}

	writeOpenAIResponseStreamCompleted(emitter, "resp_1", 123, "kmodel", result, "msg_1", false, false)

	body := rec.Body.String()
	if strings.Contains(body, "\"type\":\"message\"") {
		t.Fatalf("unexpected empty message lifecycle in tool-only response: %s", body)
	}
	if !strings.Contains(body, "\"type\":\"response.output_item.added\"") {
		t.Fatalf("missing function call added event: %s", body)
	}
	if !strings.Contains(body, "\"type\":\"response.function_call_arguments.delta\"") {
		t.Fatalf("missing function call delta event: %s", body)
	}
	if !strings.Contains(body, "\"type\":\"response.function_call_arguments.done\"") {
		t.Fatalf("missing function call done event: %s", body)
	}
	if !strings.Contains(body, "\"type\":\"response.output_item.done\"") {
		t.Fatalf("missing function call done event: %s", body)
	}
	if !strings.Contains(body, "\"call_id\":\"call_1\"") {
		t.Fatalf("missing function call payload: %s", body)
	}
	for _, want := range []string{
		"\"sequence_number\":0",
		"\"response_id\":\"resp_1\"",
		"\"output_index\":0",
		"\"item_id\":\"call_1\"",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %s in body: %s", want, body)
		}
	}
}

func TestBuildOpenAIResponseBodyIncludesReasoningItem(t *testing.T) {
	result := &service.ChatResult{
		Model:       "kmodel",
		Text:        "final answer",
		ThoughtText: "reasoning summary",
	}
	body := buildOpenAIResponseBody("resp_1", 123, "kmodel", result, "msg_1", true)
	output, ok := body["output"].([]map[string]any)
	if !ok {
		t.Fatalf("output type = %T", body["output"])
	}
	if len(output) < 2 {
		t.Fatalf("output len = %d", len(output))
	}
	if output[0]["type"] != "reasoning" {
		t.Fatalf("first output item = %#v", output[0])
	}
	summary, ok := output[0]["summary"].([]map[string]any)
	if !ok || len(summary) != 1 || summary[0]["text"] != "reasoning summary" {
		t.Fatalf("reasoning summary = %#v", output[0]["summary"])
	}
}

func TestWriteOpenAIResponseReasoningEmitsLifecycle(t *testing.T) {
	rec := httptest.NewRecorder()
	emitter := newOpenAIResponseStreamEmitter(rec, rec, "resp_1")
	if err := writeOpenAIResponseReasoning(emitter, "rs_resp_1", 0, "reasoning summary"); err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"\"type\":\"response.output_item.added\"",
		"\"type\":\"response.reasoning_summary_part.added\"",
		"\"type\":\"response.reasoning_summary_text.delta\"",
		"\"type\":\"response.reasoning_summary_text.done\"",
		"\"type\":\"response.reasoning_summary_part.done\"",
		"\"type\":\"response.output_item.done\"",
		"\"type\":\"reasoning\"",
		"reasoning summary",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %s in body: %s", want, body)
		}
	}
}

func TestShouldEmitThinkingHelpers(t *testing.T) {
	req := service.ChatRequest{ReasoningEffort: "medium"}
	result := &service.ChatResult{ThoughtText: "thought"}
	if !shouldEmitAnthropicThinking(req, result) {
		t.Fatal("expected anthropic thinking to emit")
	}
	if !shouldEmitResponsesReasoning(req, result) {
		t.Fatal("expected responses reasoning to emit")
	}
	if shouldEmitAnthropicThinking(service.ChatRequest{}, result) {
		t.Fatal("unexpected anthropic thinking without reasoning effort")
	}
	if shouldEmitResponsesReasoning(req, &service.ChatResult{}) {
		t.Fatal("unexpected responses reasoning without thought text")
	}
}

func TestNormalizeOpenAIRequestRejectsMissingUserAndAssistantMessages(t *testing.T) {
	req := openAIChatRequest{
		Model: "test-model",
		Messages: []rawMessage{
			{Role: "system", Content: "Only system"},
			{Role: "tool", Content: "ignored"},
		},
	}

	_, err := normalizeOpenAIRequest(req)
	if err == nil {
		t.Fatal("expected error for request without user or assistant messages")
	}
}

func TestNormalizeAnthropicRequestExtractsStructuredText(t *testing.T) {
	req := anthropicRequest{
		Model:  "test-model",
		System: []any{map[string]any{"type": "text", "text": "System prompt"}},
		Messages: []rawMessage{
			{
				Role: "user",
				Content: []any{
					map[string]any{"type": "text", "text": "Hello"},
				},
			},
			{
				Role: "assistant",
				Content: []any{
					map[string]any{"type": "text", "text": "Hi"},
				},
			},
			{
				Role: "metadata",
				Content: []any{
					map[string]any{"type": "text", "text": "ignored"},
				},
			},
		},
	}

	normalized, err := normalizeAnthropicRequest(req)
	if err != nil {
		t.Fatalf("normalizeAnthropicRequest() error = %v", err)
	}
	if normalized.Model != "test-model" {
		t.Fatalf("model = %q", normalized.Model)
	}
	if normalized.System != "System prompt" {
		t.Fatalf("system = %q", normalized.System)
	}
	if len(normalized.Messages) != 2 {
		t.Fatalf("message count = %d", len(normalized.Messages))
	}
	if normalized.Messages[0].Role != "user" || normalized.Messages[0].Text != "Hello" {
		t.Fatalf("first message = %+v", normalized.Messages[0])
	}
	if normalized.Messages[1].Role != "assistant" || normalized.Messages[1].Text != "Hi" {
		t.Fatalf("second message = %+v", normalized.Messages[1])
	}
}

func TestNormalizeAnthropicRequestRejectsEmptyMessages(t *testing.T) {
	req := anthropicRequest{
		Model: "test-model",
		Messages: []rawMessage{
			{Role: "user", Content: ""},
			{Role: "assistant", Content: nil},
		},
	}

	_, err := normalizeAnthropicRequest(req)
	if err == nil {
		t.Fatal("expected error for request without usable messages")
	}
}

func TestAnthropicHostedWebSearchCall(t *testing.T) {
	req := anthropicRequest{
		Model: "Kimi-K2.6",
		Tools: []any{
			map[string]any{
				"name": "web_search",
				"type": "web_search_20250305",
			},
		},
		ToolChoice: map[string]any{
			"type": "tool",
			"name": "web_search",
		},
		Messages: []rawMessage{{
			Role: "user",
			Content: []any{
				map[string]any{
					"type": "text",
					"text": "Perform a web search for the query: Hermes agent web UI documentation",
				},
			},
		}},
	}

	call, ok := anthropicHostedWebSearchCall(req)
	if !ok {
		t.Fatal("expected hosted web_search tool call")
	}
	if call.Name != "web_search" {
		t.Fatalf("tool name = %q", call.Name)
	}
	if call.Arguments["query"] != "Hermes agent web UI documentation" {
		t.Fatalf("query = %#v", call.Arguments["query"])
	}
	if !strings.HasPrefix(call.ID, "toolu_") {
		t.Fatalf("id = %q", call.ID)
	}
}

func TestAnthropicHostedWebSearchCallIgnoresRegularClientWebSearch(t *testing.T) {
	req := anthropicRequest{
		Tools: []any{
			map[string]any{
				"name": "web_search",
				"input_schema": map[string]any{
					"type": "object",
				},
			},
		},
		Messages: []rawMessage{{
			Role:    "user",
			Content: "Perform a web search for the query: Lingma",
		}},
	}

	if _, ok := anthropicHostedWebSearchCall(req); ok {
		t.Fatal("regular client web_search should stay in prompt tool emulation")
	}
}

func TestAnthropicHostedWebSearchCallIgnoresToolResultFollowup(t *testing.T) {
	req := anthropicRequest{
		Tools: []any{
			map[string]any{
				"name": "web_search",
				"type": "web_search_20250305",
			},
		},
		ToolChoice: map[string]any{
			"type": "tool",
			"name": "web_search",
		},
		Messages: []rawMessage{{
			Role: "user",
			Content: []any{
				map[string]any{
					"type":        "tool_result",
					"tool_use_id": "toolu_123",
					"content":     "result",
				},
			},
		}},
	}

	if _, ok := anthropicHostedWebSearchCall(req); ok {
		t.Fatal("hosted web_search should not short-circuit after a tool_result")
	}
}

func TestAnthropicCountTokensEndpoint(t *testing.T) {
	server := NewServer("", service.New(service.Config{
		Model:   "Qwen3-Coder",
		Timeout: time.Second,
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(`{
		"model":"kmodel",
		"max_tokens":128,
		"system":"You are concise.",
		"messages":[{"role":"user","content":"hello"}],
		"tools":[{"name":"read_file","input_schema":{"type":"object","properties":{"file_path":{"type":"string"}},"required":["file_path"]}}]
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.http.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["input_tokens"].(float64) <= 0 {
		t.Fatalf("input_tokens = %#v", body["input_tokens"])
	}
}

func TestDiscoveryCompatibilityEndpoints(t *testing.T) {
	server := NewServer("", service.New(service.Config{
		Model:   "Qwen3-Coder",
		Timeout: time.Second,
	}))

	cases := []string{
		"/version",
		"/props",
		"/v1/props",
	}
	for _, path := range cases {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		server.http.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d body = %s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestToolStreamFilterStreamsNormalTextWithTools(t *testing.T) {
	filter := newToolStreamFilter(true)
	var chunks []string
	chunks = append(chunks, filter.Push(strings.Repeat("你", 120))...)
	chunks = append(chunks, filter.Push("后续内容")...)
	chunks = append(chunks, filter.Flush()...)
	out := strings.Join(chunks, "")
	if !strings.Contains(out, "后续内容") {
		t.Fatalf("streamed text = %q", out)
	}
}

func TestShouldAggregateToolStreamRequiresOptIn(t *testing.T) {
	t.Setenv("LINGMA_AGGREGATE_TOOL_STREAM", "")
	req := service.ChatRequest{Tools: []toolemulation.ToolDef{{Name: "Bash"}}}
	if shouldAggregateToolStream(req) {
		t.Fatal("tool streams should remain incremental by default")
	}

	t.Setenv("LINGMA_AGGREGATE_TOOL_STREAM", "1")
	if !shouldAggregateToolStream(req) {
		t.Fatal("explicit aggregate env should enable aggregate tool streams")
	}
}

func TestToolStreamFilterBuffersActionBlock(t *testing.T) {
	filter := newToolStreamFilter(true)
	var chunks []string
	chunks = append(chunks, filter.Push("```json ")...)
	chunks = append(chunks, filter.Push("action\n{\"tool\":\"Bash\",\"parameters\":{\"command\":\"pwd\"}}\n```")...)
	chunks = append(chunks, filter.Flush()...)
	if len(chunks) != 0 {
		t.Fatalf("unexpected leaked action chunks: %#v", chunks)
	}
}

func TestParseImageURLReadsLocalFileURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.jpg")
	data := []byte{0xff, 0xd8, 0xff, 0xd9}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	img := parseImageURL("file://" + path)
	if img == nil {
		t.Fatal("expected image")
	}
	if img.MediaType != "image/jpeg" {
		t.Fatalf("media type = %q", img.MediaType)
	}
	if img.Data != base64.StdEncoding.EncodeToString(data) {
		t.Fatalf("data = %q", img.Data)
	}
}

func TestParseImageURLReadsAbsoluteLocalPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.png")
	data := []byte{0x89, 0x50, 0x4e, 0x47}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	img := parseImageURL(path)
	if img == nil {
		t.Fatal("expected image")
	}
	if img.MediaType != "image/png" {
		t.Fatalf("media type = %q", img.MediaType)
	}
	if img.Data != base64.StdEncoding.EncodeToString(data) {
		t.Fatalf("data = %q", img.Data)
	}
}

func TestSanitizeRecordedBodyRedactsImagePayloads(t *testing.T) {
	raw := []byte(`{"messages":[{"content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,` + strings.Repeat("a", 8192) + `"}}]}]}`)
	got := sanitizeRecordedBody(raw)
	if strings.Contains(got, "data:image/png;base64") {
		t.Fatalf("image payload was not redacted: %s", got)
	}
	if !strings.Contains(got, "[image payload redacted") {
		t.Fatalf("missing redaction marker: %s", got)
	}
}

func TestOpenAIFinishReason(t *testing.T) {
	tool := []toolemulation.ToolCall{{}}
	cases := []struct {
		name   string
		result service.ChatResult
		want   string
	}{
		{"tool calls win", service.ChatResult{ToolCalls: tool, FinishReason: "length"}, "tool_calls"},
		{"length", service.ChatResult{FinishReason: "length"}, "length"},
		{"content filter", service.ChatResult{FinishReason: "content_filter"}, "content_filter"},
		{"stop passthrough", service.ChatResult{FinishReason: "stop"}, "stop"},
		{"empty defaults to stop", service.ChatResult{}, "stop"},
		{"unknown backend reason coerced to stop", service.ChatResult{FinishReason: "eos_token"}, "stop"},
		{"whitespace normalized", service.ChatResult{FinishReason: " length "}, "length"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := openAIFinishReason(&tc.result); got != tc.want {
				t.Fatalf("openAIFinishReason = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAnthropicStopReason(t *testing.T) {
	tool := []toolemulation.ToolCall{{}}
	cases := []struct {
		name    string
		result  service.ChatResult
		reason  string
		seqWant any
	}{
		{"tool use", service.ChatResult{ToolCalls: tool, FinishReason: "stop"}, "tool_use", nil},
		{"length -> max_tokens", service.ChatResult{FinishReason: "length"}, "max_tokens", nil},
		{"stop with sequence", service.ChatResult{FinishReason: "stop", StopSequence: "\n\n"}, "stop_sequence", "\n\n"},
		{"stop without sequence", service.ChatResult{FinishReason: "stop"}, "end_turn", nil},
		{"content_filter -> refusal", service.ChatResult{FinishReason: "content_filter"}, "refusal", nil},
		{"unknown -> end_turn", service.ChatResult{FinishReason: "eos_token"}, "end_turn", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, seq := anthropicStopReason(&tc.result)
			if reason != tc.reason {
				t.Fatalf("stop_reason = %q, want %q", reason, tc.reason)
			}
			if seq != tc.seqWant {
				t.Fatalf("stop_sequence = %v, want %v", seq, tc.seqWant)
			}
		})
	}
}

func TestResolveAnthropicEffort(t *testing.T) {
	cases := []struct {
		name string
		req  anthropicRequest
		want string
	}{
		{"thinking.effort verbatim", anthropicRequest{Thinking: map[string]any{"type": "adaptive", "effort": "xhigh"}}, "xhigh"},
		{"output_config.effort", anthropicRequest{OutputConfig: map[string]any{"effort": "max"}}, "max"},
		{"top-level effort", anthropicRequest{Effort: "high"}, "high"},
		{"explicit output_config beats budget bucket", anthropicRequest{Thinking: map[string]any{"type": "enabled", "budget_tokens": float64(30000)}, OutputConfig: map[string]any{"effort": "medium"}}, "medium"},
		{"budget fallback bucket", anthropicRequest{Thinking: map[string]any{"type": "enabled", "budget_tokens": float64(500)}}, "low"},
		{"none passes through", anthropicRequest{Effort: "none"}, "none"},
		{"thinking disabled -> none", anthropicRequest{Thinking: map[string]any{"type": "disabled"}}, "none"},
		{"empty", anthropicRequest{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveAnthropicEffort(tc.req); got != tc.want {
				t.Fatalf("resolveAnthropicEffort = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReasoningEffortEnabled(t *testing.T) {
	for _, e := range []string{"", "none", "off", "disabled", "NONE", " off "} {
		if reasoningEffortEnabled(e) {
			t.Fatalf("reasoningEffortEnabled(%q) = true, want false", e)
		}
	}
	for _, e := range []string{"low", "medium", "high", "xhigh", "max"} {
		if !reasoningEffortEnabled(e) {
			t.Fatalf("reasoningEffortEnabled(%q) = false, want true", e)
		}
	}
}

func TestExtractAnthropicAssistantContentCapturesThinking(t *testing.T) {
	content := []any{
		map[string]any{"type": "thinking", "thinking": "step 1"},
		map[string]any{"type": "text", "text": "the answer"},
	}
	text, reasoning, _ := extractAnthropicAssistantContent(content)
	if text != "the answer" {
		t.Fatalf("text = %q, want %q", text, "the answer")
	}
	if reasoning != "step 1" {
		t.Fatalf("reasoning = %q, want %q", reasoning, "step 1")
	}
}

func TestNormalizeAnthropicRequestThinkingRoundTrip(t *testing.T) {
	req := anthropicRequest{
		Model: "m",
		Messages: []rawMessage{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: []any{
				map[string]any{"type": "thinking", "thinking": "pondering"},
				map[string]any{"type": "text", "text": "hello"},
			}},
		},
	}
	cr, err := normalizeAnthropicRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range cr.Messages {
		if m.Role == "assistant" {
			found = true
			if m.ReasoningText != "pondering" {
				t.Fatalf("ReasoningText = %q, want %q", m.ReasoningText, "pondering")
			}
			if m.Text != "hello" {
				t.Fatalf("Text = %q, want %q", m.Text, "hello")
			}
		}
	}
	if !found {
		t.Fatal("no assistant message produced")
	}
}

type sseRec struct {
	typ         string
	index       int
	blockType   string
	id, name    string
	partialJSON string
	text        string
	thinking    string
}

func parseAnthropicSSE(t *testing.T, body string) []sseRec {
	t.Helper()
	str := func(v any) string {
		if v == nil {
			return ""
		}
		return fmt.Sprintf("%v", v)
	}
	var out []sseRec
	for _, block := range strings.Split(body, "\n\n") {
		var data string
		for _, line := range strings.Split(block, "\n") {
			if strings.HasPrefix(line, "data: ") {
				data = strings.TrimPrefix(line, "data: ")
			}
		}
		if data == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(data), &m); err != nil {
			continue
		}
		rec := sseRec{typ: str(m["type"])}
		if idx, ok := m["index"].(float64); ok {
			rec.index = int(idx)
		}
		if cb, ok := m["content_block"].(map[string]any); ok {
			rec.blockType = str(cb["type"])
			rec.id = str(cb["id"])
			rec.name = str(cb["name"])
		}
		if d, ok := m["delta"].(map[string]any); ok {
			rec.partialJSON = str(d["partial_json"])
			rec.text = str(d["text"])
			rec.thinking = str(d["thinking"])
		}
		out = append(out, rec)
	}
	return out
}

func runAnthropicStream(req service.ChatRequest, events []service.StreamEvent, final *service.ChatResult) string {
	evCh := make(chan service.StreamEvent, len(events)+1)
	for _, e := range events {
		evCh <- e
	}
	close(evCh)
	doneCh := make(chan service.StreamResult, 1)
	doneCh <- service.StreamResult{Result: final}
	close(doneCh)
	rec := httptest.NewRecorder()
	writeAnthropicStreamBody(context.Background(), rec, rec, req, evCh, doneCh)
	return rec.Body.String()
}

func TestWriteAnthropicStreamBodyIncrementalToolUse(t *testing.T) {
	req := service.ChatRequest{Tools: []toolemulation.ToolDef{{Name: "read_file"}}}
	events := []service.StreamEvent{
		{Type: service.StreamEventToolCall, ToolCall: &service.StreamToolCall{Index: 0, ID: "call_1", Name: "read_file"}},
		{Type: service.StreamEventToolCall, ToolCall: &service.StreamToolCall{Index: 0, ArgsFragment: `{"path":`}},
		{Type: service.StreamEventToolCall, ToolCall: &service.StreamToolCall{Index: 0, ArgsFragment: `"/a.txt"}`}},
	}
	final := &service.ChatResult{ToolCalls: []toolemulation.ToolCall{{ID: "call_1", Name: "read_file", Arguments: map[string]any{"path": "/a.txt"}}}, FinishReason: "tool_calls"}
	recs := parseAnthropicSSE(t, runAnthropicStream(req, events, final))

	starts, stops := 0, 0
	startIdx := -1
	var gotID, gotName, args string
	for _, r := range recs {
		if r.typ == "content_block_start" && r.blockType == "tool_use" {
			starts++
			startIdx = r.index
			gotID = r.id
			gotName = r.name
		}
		if r.typ == "content_block_delta" && r.partialJSON != "" {
			args += r.partialJSON
		}
	}
	for _, r := range recs {
		if r.typ == "content_block_stop" && r.index == startIdx {
			stops++
		}
	}
	if starts != 1 {
		t.Fatalf("tool_use content_block_start count = %d, want 1 (no duplicate aggregated block)", starts)
	}
	if gotID != "call_1" || gotName != "read_file" {
		t.Fatalf("tool id/name = %q/%q, want call_1/read_file", gotID, gotName)
	}
	if args != `{"path":"/a.txt"}` {
		t.Fatalf("assembled args = %q, want %q", args, `{"path":"/a.txt"}`)
	}
	if stops != 1 {
		t.Fatalf("content_block_stop for tool block = %d, want 1", stops)
	}
}

func TestWriteAnthropicStreamBodyToolLateStart(t *testing.T) {
	req := service.ChatRequest{Tools: []toolemulation.ToolDef{{Name: "x"}}}
	events := []service.StreamEvent{
		{Type: service.StreamEventToolCall, ToolCall: &service.StreamToolCall{Index: 0, ArgsFragment: `{"a":1}`}},
	}
	final := &service.ChatResult{ToolCalls: []toolemulation.ToolCall{{}}, FinishReason: "tool_calls"}
	recs := parseAnthropicSSE(t, runAnthropicStream(req, events, final))
	startIdx := -1
	var gotID, gotName, args string
	for _, r := range recs {
		if r.typ == "content_block_start" && r.blockType == "tool_use" {
			startIdx = r.index
			gotID = r.id
			gotName = r.name
		}
		if r.typ == "content_block_delta" && r.partialJSON != "" {
			args += r.partialJSON
		}
	}
	if startIdx < 0 {
		t.Fatal("expected a late-started tool block")
	}
	if gotID != "tool_call_0" || gotName != "unknown_tool" {
		t.Fatalf("fallback id/name = %q/%q, want tool_call_0/unknown_tool", gotID, gotName)
	}
	if args != `{"a":1}` {
		t.Fatalf("late args = %q, want %q", args, `{"a":1}`)
	}
}

func TestWriteAnthropicStreamBodyEmulatedToolAggregated(t *testing.T) {
	req := service.ChatRequest{Tools: []toolemulation.ToolDef{{Name: "read_file"}}}
	final := &service.ChatResult{ToolCalls: []toolemulation.ToolCall{{ID: "call_9", Name: "read_file", Arguments: map[string]any{"path": "/b"}}}, FinishReason: "tool_calls"}
	recs := parseAnthropicSSE(t, runAnthropicStream(req, nil, final))
	starts := 0
	var gotID string
	for _, r := range recs {
		if r.typ == "content_block_start" && r.blockType == "tool_use" {
			starts++
			gotID = r.id
		}
	}
	if starts != 1 || gotID != "call_9" {
		t.Fatalf("aggregated tool_use starts=%d id=%q, want 1/call_9", starts, gotID)
	}
}

func TestWriteAnthropicStreamBodyThinkingTextToolCoexist(t *testing.T) {
	req := service.ChatRequest{ReasoningEffort: "high", Tools: []toolemulation.ToolDef{{Name: "read_file"}}}
	events := []service.StreamEvent{
		{Type: service.StreamEventThinking, Delta: "let me think about this"},
		{Type: service.StreamEventText, Delta: "checking now for you"},
		{Type: service.StreamEventToolCall, ToolCall: &service.StreamToolCall{Index: 0, ID: "call_1", Name: "read_file", ArgsFragment: `{}`}},
	}
	final := &service.ChatResult{ToolCalls: []toolemulation.ToolCall{{ID: "call_1", Name: "read_file"}}, FinishReason: "tool_calls"}
	recs := parseAnthropicSSE(t, runAnthropicStream(req, events, final))
	thinkingIdx, textIdx, toolIdx := -1, -1, -1
	for _, r := range recs {
		if r.typ == "content_block_start" {
			switch r.blockType {
			case "thinking":
				thinkingIdx = r.index
			case "text":
				textIdx = r.index
			case "tool_use":
				toolIdx = r.index
			}
		}
	}
	if thinkingIdx != 0 {
		t.Fatalf("thinking block index = %d, want 0", thinkingIdx)
	}
	if textIdx != 1 {
		t.Fatalf("text block index = %d, want 1", textIdx)
	}
	if toolIdx != 2 {
		t.Fatalf("tool block index = %d, want 2 (after thinking+text)", toolIdx)
	}
}

func TestNormalizeAnthropicRequestToolResultError(t *testing.T) {
	req := anthropicRequest{
		Model: "m",
		Messages: []rawMessage{
			{Role: "user", Content: []any{
				map[string]any{"type": "tool_result", "tool_use_id": "call_1", "is_error": true, "content": "boom"},
			}},
		},
	}
	cr, err := normalizeAnthropicRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var toolMsg *service.ChatMessage
	for i := range cr.Messages {
		if cr.Messages[i].Role == "tool" {
			toolMsg = &cr.Messages[i]
		}
	}
	if toolMsg == nil {
		t.Fatal("no tool message produced")
	}
	if !strings.HasPrefix(toolMsg.Text, "[tool_error]") {
		t.Fatalf("error not marked inline: %q", toolMsg.Text)
	}
	if toolMsg.ToolCallID != "call_1" {
		t.Fatalf("tool_call_id = %q, want call_1", toolMsg.ToolCallID)
	}
}

func TestEstimateAnthropicInputTokensExcludesImageData(t *testing.T) {
	bigB64 := strings.Repeat("A", 200000) // ~200k base64 chars; must NOT be counted rune-by-rune
	req := anthropicRequest{
		Model: "m",
		Messages: []rawMessage{{Role: "user", Content: []any{
			map[string]any{"type": "text", "text": "hi"},
			map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": bigB64}},
		}}},
	}
	got := estimateAnthropicInputTokens(req)
	if got > 5000 {
		t.Fatalf("estimate = %d; image base64 was counted (should be excluded, ~1600/image + text)", got)
	}
}

func TestNormalizeOpenAIRequestKeepsEmptyToolResultAndReasoning(t *testing.T) {
	req := openAIChatRequest{
		Model: "m",
		Messages: []rawMessage{
			{Role: "user", Content: "go"},
			{Role: "assistant", ReasoningContent: "thinking...", ToolCalls: []any{
				map[string]any{"id": "c1", "type": "function", "function": map[string]any{"name": "f", "arguments": "{}"}},
			}},
			{Role: "tool", ToolCallID: "c1", Content: ""},
		},
	}
	cr, err := normalizeOpenAIRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var asst, tool *service.ChatMessage
	for i := range cr.Messages {
		switch cr.Messages[i].Role {
		case "assistant":
			asst = &cr.Messages[i]
		case "tool":
			tool = &cr.Messages[i]
		}
	}
	if asst == nil || asst.ReasoningText != "thinking..." {
		t.Fatalf("assistant reasoning not preserved: %#v", asst)
	}
	if tool == nil || tool.ToolCallID != "c1" || strings.TrimSpace(tool.Text) == "" {
		t.Fatalf("empty tool result dropped or unpaired: %#v", tool)
	}
}

func TestParseImageURLPassesRemoteURLThrough(t *testing.T) {
	img := parseImageURL("https://qoder-cn-vl.oss-cn-beijing.aliyuncs.com/x.png")
	if img == nil || img.URL != "https://qoder-cn-vl.oss-cn-beijing.aliyuncs.com/x.png" {
		t.Fatalf("remote URL not passed through: %#v", img)
	}
	if img.Data != "" {
		t.Fatalf("remote URL should not be downloaded/inlined: %#v", img)
	}
}

func TestExtractAnthropicImagesHandlesURLAndBase64(t *testing.T) {
	content := []any{
		map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": "https://example.com/pic.png"}},
		map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "aGVsbG8="}},
	}
	imgs := extractAnthropicImages(content)
	if len(imgs) != 2 {
		t.Fatalf("want 2 images, got %d: %#v", len(imgs), imgs)
	}
	if imgs[0].URL != "https://example.com/pic.png" {
		t.Fatalf("URL-source image not passed through: %#v", imgs[0])
	}
	if imgs[1].Data == "" {
		t.Fatalf("base64 image lost its data: %#v", imgs[1])
	}
}

func TestCreditUsageExposedWhenCharged(t *testing.T) {
	result := &service.ChatResult{
		InputTokens:     10,
		OutputTokens:    20,
		Credits:         3.5,
		OriginalCredits: 7,
		Billable:        true,
	}
	for name, usage := range map[string]map[string]any{
		"openai":    openAIUsageMap(result),
		"responses": openAIResponsesUsageMap(result),
		"anthropic": anthropicFinalUsage(result),
	} {
		if got := usage["credits"]; got != 3.5 {
			t.Errorf("%s: credits = %v, want 3.5", name, got)
		}
		if got := usage["original_credits"]; got != float64(7) {
			t.Errorf("%s: original_credits = %v, want 7", name, got)
		}
		if got := usage["billable"]; got != true {
			t.Errorf("%s: billable = %v, want true", name, got)
		}
	}
}

func TestCreditUsageOmittedWhenFree(t *testing.T) {
	result := &service.ChatResult{InputTokens: 10, OutputTokens: 20}
	for name, usage := range map[string]map[string]any{
		"openai":    openAIUsageMap(result),
		"responses": openAIResponsesUsageMap(result),
		"anthropic": anthropicFinalUsage(result),
	} {
		if _, ok := usage["credits"]; ok {
			t.Errorf("%s: credits should be absent for a free turn", name)
		}
		if _, ok := usage["billable"]; ok {
			t.Errorf("%s: billable should be absent for a free turn", name)
		}
	}
}
