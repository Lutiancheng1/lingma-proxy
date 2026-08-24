package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"lingma-ipc-proxy/internal/service"
	"lingma-ipc-proxy/internal/toolemulation"
)

// Media-tool injection (opt-in via LINGMA_INJECT_MEDIA_TOOLS). When enabled, the
// proxy advertises the gateway-only ImageSearch/ImageGen tools to the model and
// executes them server-side in an agentic loop, so a client that has no such
// tools (e.g. Claude Code) can search/generate images. The injected tool calls
// are never surfaced to the client; only the final answer is.
//
// Trade-off: while enabled, the final answer is produced by a non-streaming
// generation loop and then emitted, so it is not token-by-token streamed. Keep
// the flag off for latency-sensitive pure-coding use.

const maxMediaToolRounds = 4

func mediaToolsEnabled() bool { return truthyEnv("LINGMA_INJECT_MEDIA_TOOLS") }

func isEmbeddedMediaTool(name string) bool {
	switch strings.TrimSpace(name) {
	case "ImageSearch", "ImageGen":
		return true
	}
	return false
}

// injectMediaTools merges the embedded media tool defs into req.Tools, skipping
// any the client already declares by name.
func injectMediaTools(req anthropicRequest) anthropicRequest {
	existing := map[string]bool{}
	var items []any
	if arr, ok := req.Tools.([]any); ok {
		items = append(items, arr...)
		for _, it := range arr {
			if m, ok := it.(map[string]any); ok {
				existing[strings.TrimSpace(stringFromAny(m["name"]))] = true
			}
		}
	}
	for _, t := range embeddedAnthropicTools() {
		if m, ok := t.(map[string]any); ok && existing[stringFromAny(m["name"])] {
			continue
		}
		items = append(items, t)
	}
	req.Tools = items
	return req
}

// stripInjectedMediaTools removes our injected tools, leaving client tools.
func stripInjectedMediaTools(raw any) any {
	arr, ok := raw.([]any)
	if !ok {
		return raw
	}
	kept := make([]any, 0, len(arr))
	for _, it := range arr {
		if m, ok := it.(map[string]any); ok && isEmbeddedMediaTool(stringFromAny(m["name"])) {
			continue
		}
		kept = append(kept, it)
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

func partitionMediaToolCalls(calls []toolemulation.ToolCall) (ours, others []toolemulation.ToolCall) {
	for _, c := range calls {
		if isEmbeddedMediaTool(c.Name) {
			ours = append(ours, c)
		} else {
			others = append(others, c)
		}
	}
	return ours, others
}

// executeMediaTool runs one injected tool call and returns the Anthropic
// tool_result content value (a plain string, or a []any of content blocks for
// image results). The value must use the same shape the JSON decoder produces
// (string / []any / map[string]any) so extractText / extractAnthropicImages
// parse it when the request is re-normalized.
func (s *Server) executeMediaTool(ctx context.Context, call toolemulation.ToolCall) any {
	switch call.Name {
	case "ImageSearch":
		query := strings.TrimSpace(stringFromAny(call.Arguments["query"]))
		count := 0
		if v, ok := call.Arguments["count"].(float64); ok {
			count = int(v)
		}
		results, err := s.svc.ImageSearch(ctx, query, count)
		if err != nil {
			return "Image search failed: " + err.Error()
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Image search results for %q:\n", query)
		for i, r := range results {
			fmt.Fprintf(&b, "[%d] %s — %s (%dx%d)\n", i+1, strings.TrimSpace(r.Title), strings.TrimSpace(r.ImageURL), r.Width, r.Height)
		}
		return b.String()
	case "ImageGen":
		prompt := strings.TrimSpace(stringFromAny(call.Arguments["prompt"]))
		size := strings.TrimSpace(stringFromAny(call.Arguments["size"]))
		dataURL, err := s.svc.GenerateImage(ctx, prompt, size, "")
		if err != nil {
			return "Image generation failed: " + err.Error()
		}
		mediaType, b64 := splitDataURL(dataURL)
		blocks := make([]any, 0, 2)
		if b64 != "" {
			blocks = append(blocks, map[string]any{
				"type":   "image",
				"source": map[string]any{"type": "base64", "media_type": mediaType, "data": b64},
			})
		}
		blocks = append(blocks, map[string]any{"type": "text", "text": "Generated an image for prompt: " + prompt})
		return blocks
	}
	return "unknown tool"
}

func splitDataURL(u string) (mediaType, b64 string) {
	mediaType = "image/png"
	if strings.HasPrefix(u, "data:") {
		if semi := strings.Index(u, ";"); semi > len("data:") {
			mediaType = u[len("data:"):semi]
		}
	}
	if i := strings.Index(u, "base64,"); i >= 0 {
		b64 = u[i+len("base64,"):]
	}
	return mediaType, b64
}

// handleAnthropicMediaTools runs the agentic loop: generate, execute any of our
// injected tool calls, feed results back, repeat, then emit the final answer.
func (s *Server) handleAnthropicMediaTools(w http.ResponseWriter, r *http.Request, req anthropicRequest) {
	ctx := r.Context()
	req = injectMediaTools(req)

	var finalResult *service.ChatResult
	var finalReq service.ChatRequest
	for round := 0; ; round++ {
		normalized, err := normalizeAnthropicRequest(req)
		if err != nil {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		s.applyDefaultModel(&normalized)
		result, err := s.svc.Generate(ctx, normalized)
		if err != nil {
			writeAnthropicError(w, http.StatusInternalServerError, "api_error", err.Error())
			return
		}
		ours, others := partitionMediaToolCalls(result.ToolCalls)
		if len(ours) == 0 || len(others) > 0 || round >= maxMediaToolRounds {
			finalResult, finalReq = result, normalized
			break
		}
		// Record the assistant tool_use turn and the tool_result turn, then loop.
		assistantContent := make([]any, 0, len(ours)+1)
		if strings.TrimSpace(result.Text) != "" {
			assistantContent = append(assistantContent, map[string]any{"type": "text", "text": result.Text})
		}
		for _, c := range ours {
			assistantContent = append(assistantContent, map[string]any{"type": "tool_use", "id": c.ID, "name": c.Name, "input": c.Arguments})
		}
		req.Messages = append(req.Messages, rawMessage{Role: "assistant", Content: assistantContent})

		userContent := make([]any, 0, len(ours))
		for _, c := range ours {
			userContent = append(userContent, map[string]any{
				"type":        "tool_result",
				"tool_use_id": c.ID,
				"content":     s.executeMediaTool(ctx, c),
			})
		}
		req.Messages = append(req.Messages, rawMessage{Role: "user", Content: userContent})

		// On the last permitted round, strip our tools so the model must answer.
		if round+1 >= maxMediaToolRounds {
			req.Tools = stripInjectedMediaTools(req.Tools)
		}
	}

	if req.Stream {
		s.emitAnthropicResultStream(w, finalReq, finalResult)
		return
	}
	s.emitAnthropicResultJSON(w, finalReq, finalResult)
}

func (s *Server) emitAnthropicResultJSON(w http.ResponseWriter, req service.ChatRequest, result *service.ChatResult) {
	content := make([]map[string]any, 0, 2+len(result.ToolCalls))
	if shouldEmitAnthropicThinking(req, result) {
		content = append(content, map[string]any{"type": "thinking", "thinking": result.ThoughtText})
	}
	if strings.TrimSpace(result.Text) != "" {
		content = append(content, map[string]any{"type": "text", "text": result.Text})
	}
	for _, tc := range result.ToolCalls {
		// Never surface our injected tools; only genuine client tools (Bash, etc.)
		// pass through for the client to execute.
		if isEmbeddedMediaTool(tc.Name) {
			continue
		}
		content = append(content, map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": tc.Arguments})
	}
	if len(content) == 0 {
		content = append(content, map[string]any{"type": "text", "text": ""})
	}
	stopReason, stopSequence := anthropicStopReason(result)
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = "lingma"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":            fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       content,
		"stop_reason":   stopReason,
		"stop_sequence": stopSequence,
		"usage":         anthropicFinalUsage(result),
	})
}

func (s *Server) emitAnthropicResultStream(w http.ResponseWriter, req service.ChatRequest, result *service.ChatResult) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "streaming is not supported by this server")
		return
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = "lingma"
	}
	msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	streamingHeaders(w)
	_ = writeSSEEvent(w, flusher, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": msgID, "type": "message", "role": "assistant", "content": []any{},
			"model": model, "stop_reason": nil, "stop_sequence": nil, "usage": anthropicInputUsage(result),
		},
	})
	index := 0
	if shouldEmitAnthropicThinking(req, result) {
		_ = writeSSEEvent(w, flusher, "content_block_start", map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "thinking", "thinking": ""}})
		_ = writeSSEEvent(w, flusher, "content_block_delta", map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "thinking_delta", "thinking": result.ThoughtText}})
		_ = writeSSEEvent(w, flusher, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
		index++
	}
	if strings.TrimSpace(result.Text) != "" {
		_ = writeSSEEvent(w, flusher, "content_block_start", map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "text", "text": ""}})
		_ = writeSSEEvent(w, flusher, "content_block_delta", map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "text_delta", "text": result.Text}})
		_ = writeSSEEvent(w, flusher, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
		index++
	}
	for _, tc := range result.ToolCalls {
		if isEmbeddedMediaTool(tc.Name) {
			continue
		}
		argsJSON, _ := json.Marshal(tc.Arguments)
		_ = writeSSEEvent(w, flusher, "content_block_start", map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": map[string]any{}}})
		_ = writeSSEEvent(w, flusher, "content_block_delta", map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(argsJSON)}})
		_ = writeSSEEvent(w, flusher, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
		index++
	}
	stopReason, stopSequence := anthropicStopReason(result)
	_ = writeSSEEvent(w, flusher, "message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stopReason, "stop_sequence": stopSequence}, "usage": map[string]any{"output_tokens": result.OutputTokens}})
	_ = writeSSEEvent(w, flusher, "message_stop", map[string]any{"type": "message_stop"})
}
