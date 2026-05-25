package feishu

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type llmResponse struct {
	Choices []struct {
		Message struct {
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens         int `json:"prompt_tokens"`
		CompletionTokens     int `json:"completion_tokens"`
		TotalTokens          int `json:"total_tokens"`
		CacheReadInputTokens int `json:"cache_read_input_tokens"`
		CacheCreationTokens  int `json:"cache_creation_input_tokens"`
		PromptTokensDetails  struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

// listProxyModels calls the proxy's /v1/models endpoint to enumerate available
// model ids. proxyURL is the chat/completions URL — the helper derives the
// /v1/models sibling endpoint from it. Returns at most `limit` ids.
func listProxyModels(ctx context.Context, proxyURL string, limit int) ([]string, error) {
	if strings.TrimSpace(proxyURL) == "" {
		return nil, fmt.Errorf("empty proxy url")
	}
	modelsURL := proxyURL
	for _, suffix := range []string{"/chat/completions", "/v1/chat/completions"} {
		if strings.HasSuffix(modelsURL, suffix) {
			modelsURL = strings.TrimSuffix(modelsURL, suffix)
			break
		}
	}
	modelsURL = strings.TrimRight(modelsURL, "/") + "/v1/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("proxy /v1/models returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(parsed.Data))
	for _, item := range parsed.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		ids = append(ids, id)
		if limit > 0 && len(ids) >= limit {
			break
		}
	}
	return ids, nil
}

func callLLM(ctx context.Context, proxyURL string, model string, messages []map[string]any, forceToolUse bool, tools []map[string]any) (*llmResponse, error) {
	if len(tools) == 0 {
		tools = toolDefinitions()
	}
	body := map[string]any{
		"model":    model,
		"messages": messages,
		"tools":    tools,
	}
	if forceToolUse {
		body["tool_choice"] = "required"
	} else {
		body["tool_choice"] = "auto"
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, proxyURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("proxy returned %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var parsed llmResponse
	if err := json.Unmarshal(responseBody, &parsed); err != nil {
		return nil, err
	}
	return &parsed, nil
}

func callLLMPlain(ctx context.Context, proxyURL string, model string, messages []map[string]any) (*llmResponse, error) {
	body := map[string]any{
		"model":    model,
		"messages": messages,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, proxyURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("proxy returned %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var parsed llmResponse
	if err := json.Unmarshal(responseBody, &parsed); err != nil {
		return nil, err
	}
	return &parsed, nil
}

// streamingDelta is the per-chunk hook surface for callLLMStream / callLLMPlainStream.
// onText receives each new text chunk (already decoded from delta.content).
// onToolCallProgress is fired whenever a tool_call accumulates more arguments
// or a new index appears — it is purely advisory (callers usually ignore it
// and rely on the final aggregated llmResponse).
type streamingDelta struct {
	onText             func(string)
	onToolCallProgress func()
}

// streamChoiceDelta mirrors the OpenAI SSE schema we receive from the upstream
// proxy. We only consume content + tool_calls; everything else is ignored.
type streamChoiceDelta struct {
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function *struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens         int `json:"prompt_tokens"`
		CompletionTokens     int `json:"completion_tokens"`
		TotalTokens          int `json:"total_tokens"`
		CacheReadInputTokens int `json:"cache_read_input_tokens"`
		CacheCreationTokens  int `json:"cache_creation_input_tokens"`
		PromptTokensDetails  struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

// callLLMStream issues a streaming chat completion. Each text delta is
// forwarded to deltas.onText so callers (the feishu card writer) can render
// a typewriter effect; tool_call argument fragments are accumulated by
// index and surfaced in the final *llmResponse exactly like the non-streaming
// callLLM result. Returns an error if the upstream connection fails before
// any data arrives — callers can then fall back to the non-streaming path.
func callLLMStream(ctx context.Context, proxyURL string, model string, messages []map[string]any, forceToolUse bool, tools []map[string]any, deltas streamingDelta) (*llmResponse, error) {
	if len(tools) == 0 {
		tools = toolDefinitions()
	}
	body := map[string]any{
		"model":    model,
		"messages": messages,
		"tools":    tools,
		"stream":   true,
		"stream_options": map[string]any{
			"include_usage": true,
		},
	}
	if forceToolUse {
		body["tool_choice"] = "required"
	} else {
		body["tool_choice"] = "auto"
	}
	return runStreamingRequest(ctx, proxyURL, body, deltas)
}

// callLLMPlainStream is the streaming counterpart of callLLMPlain — no tools,
// used by synthesizeToolFinalReply so the post-tool summary also types out.
func callLLMPlainStream(ctx context.Context, proxyURL string, model string, messages []map[string]any, deltas streamingDelta) (*llmResponse, error) {
	body := map[string]any{
		"model":    model,
		"messages": messages,
		"stream":   true,
		"stream_options": map[string]any{
			"include_usage": true,
		},
	}
	return runStreamingRequest(ctx, proxyURL, body, deltas)
}

func runStreamingRequest(ctx context.Context, proxyURL string, body map[string]any, deltas streamingDelta) (*llmResponse, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, proxyURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	// No client-level timeout: a streaming response can legitimately take
	// minutes; we rely on ctx for cancellation.
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("proxy returned %d: %s", resp.StatusCode, strings.TrimSpace(string(errBody)))
	}
	return parseSSEStream(resp.Body, deltas)
}

// parseSSEStream consumes an OpenAI-style SSE stream and folds it into a
// single llmResponse equivalent to what the non-streaming endpoint returns.
// Exposed (lower-case) for unit testing via in-package callers.
func parseSSEStream(body io.Reader, deltas streamingDelta) (*llmResponse, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var (
		contentBuf strings.Builder
		toolCalls  []ToolCall
		toolByIdx  = map[int]int{} // index in stream → position in toolCalls
		usage      llmResponse
		sawAny     bool
	)
	for scanner.Scan() {
		raw := scanner.Text()
		if raw == "" {
			continue
		}
		if !strings.HasPrefix(raw, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(raw, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}
		var chunk streamChoiceDelta
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// Skip malformed lines instead of failing the whole stream;
			// some proxies emit keep-alive pings as comments or garbage.
			continue
		}
		sawAny = true
		if chunk.Usage != nil {
			usage.Usage.PromptTokens = chunk.Usage.PromptTokens
			usage.Usage.CompletionTokens = chunk.Usage.CompletionTokens
			usage.Usage.TotalTokens = chunk.Usage.TotalTokens
			usage.Usage.CacheReadInputTokens = chunk.Usage.CacheReadInputTokens
			usage.Usage.CacheCreationTokens = chunk.Usage.CacheCreationTokens
			usage.Usage.PromptTokensDetails.CachedTokens = chunk.Usage.PromptTokensDetails.CachedTokens
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content != "" {
				contentBuf.WriteString(ch.Delta.Content)
				if deltas.onText != nil {
					deltas.onText(ch.Delta.Content)
				}
			}
			for _, tc := range ch.Delta.ToolCalls {
				pos, ok := toolByIdx[tc.Index]
				if !ok {
					pos = len(toolCalls)
					toolByIdx[tc.Index] = pos
					toolCalls = append(toolCalls, ToolCall{Type: "function"})
				}
				if tc.ID != "" {
					toolCalls[pos].ID = tc.ID
				}
				if tc.Type != "" {
					toolCalls[pos].Type = tc.Type
				}
				if tc.Function != nil {
					if tc.Function.Name != "" {
						toolCalls[pos].Function.Name = tc.Function.Name
					}
					if tc.Function.Arguments != "" {
						toolCalls[pos].Function.Arguments += tc.Function.Arguments
					}
				}
				if deltas.onToolCallProgress != nil {
					deltas.onToolCallProgress()
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read stream: %w", err)
	}
	if !sawAny {
		return nil, fmt.Errorf("empty stream")
	}
	out := &llmResponse{Usage: usage.Usage}
	out.Choices = []struct {
		Message struct {
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls"`
		} `json:"message"`
	}{{}}
	out.Choices[0].Message.Content = contentBuf.String()
	out.Choices[0].Message.ToolCalls = toolCalls
	return out, nil
}
