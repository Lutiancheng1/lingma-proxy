package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lingma-ipc-proxy/internal/service"
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

func TestResponsesRequestToChatRequest(t *testing.T) {
	req := openAIResponsesRequest{
		Model:           "resp-model",
		Instructions:    "You are concise.",
		Input: []any{map[string]any{
			"role": "user",
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
