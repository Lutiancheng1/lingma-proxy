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

// Server-side tool injection. The proxy advertises gateway-only tools to the
// model and executes them server-side in an agentic loop, so a client that has
// no such tools can still use them. Two tools are supported:
//
//   - web_search: injected whenever the client declares a hosted web_search
//     tool (Anthropic) or when LINGMA_INJECT_MEDIA_TOOLS is set (OpenAI). The
//     model decides whether to call it; the proxy runs the gateway's oneSearch.
//   - ImageSearch: injected when LINGMA_INJECT_MEDIA_TOOLS is set. Runs the
//     gateway's imageSearch (returns metadata only, never downloads to disk).
//
// The injected tool calls are never surfaced to the client; only the model's
// answer is. Genuine client tools (Bash, etc.) are passed through untouched and
// never executed by the proxy. Streaming keeps real token-by-token streaming:
// thinking and text flow live, only our own tool calls are intercepted.

const maxMediaToolRounds = 4

func mediaToolsEnabled() bool { return truthyEnv("LINGMA_INJECT_MEDIA_TOOLS") }

// isServerTool reports whether a tool name is one the proxy executes itself.
func isServerTool(name string) bool {
	switch strings.TrimSpace(name) {
	case "web_search", "ImageSearch", "TextPolish":
		return true
	}
	return false
}

// anthropicServerToolDefs decides which server tools to advertise for an
// Anthropic request and returns their tool definitions. web_search is offered
// when the client declares a hosted web_search tool; ImageSearch when the
// injection flag is set.
func (s *Server) anthropicServerToolDefs(req anthropicRequest) ([]any, bool) {
	var defs []any
	if hasAnthropicHostedWebSearchTool(req.Tools) {
		defs = append(defs, webSearchSpec.anthropicDef())
	}
	if mediaToolsEnabled() {
		defs = append(defs, imageSearchSpec.anthropicDef(), textPolishSpec.anthropicDef())
	}
	return defs, len(defs) > 0
}

// injectAnthropicServerTools drops the client's hosted web_search def (we
// advertise our own callable one in its place) and appends the chosen server
// tool defs, skipping any the client already declares by name.
func injectAnthropicServerTools(req anthropicRequest, defs []any) anthropicRequest {
	stripped := stripAnthropicHostedWebSearchTool(req.Tools)
	existing := map[string]bool{}
	var items []any
	if arr, ok := stripped.([]any); ok {
		items = append(items, arr...)
		for _, it := range arr {
			if m, ok := it.(map[string]any); ok {
				existing[strings.TrimSpace(stringFromAny(m["name"]))] = true
			}
		}
	}
	for _, d := range defs {
		if m, ok := d.(map[string]any); ok && existing[stringFromAny(m["name"])] {
			continue
		}
		items = append(items, d)
	}
	req.Tools = items
	return req
}

// stripInjectedServerTools removes our injected server tools, leaving client tools.
func stripInjectedServerTools(raw any) any {
	arr, ok := raw.([]any)
	if !ok {
		return raw
	}
	kept := make([]any, 0, len(arr))
	for _, it := range arr {
		if m, ok := it.(map[string]any); ok && isServerTool(stringFromAny(m["name"])) {
			continue
		}
		kept = append(kept, it)
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

func partitionServerToolCalls(calls []toolemulation.ToolCall) (ours, others []toolemulation.ToolCall) {
	for _, c := range calls {
		if isServerTool(c.Name) {
			ours = append(ours, c)
		} else {
			others = append(others, c)
		}
	}
	return ours, others
}

// executeServerTool runs one injected tool call and returns its result as plain
// text. Both supported tools return text (web search hits / image metadata), so
// the tool_result content is always a string.
func (s *Server) executeServerTool(ctx context.Context, call toolemulation.ToolCall) string {
	switch call.Name {
	case "web_search":
		query := strings.TrimSpace(stringFromAny(call.Arguments["query"]))
		if query == "" {
			return "Web search failed: empty query"
		}
		results, err := s.svc.WebSearch(ctx, query)
		if err != nil {
			return "Web search failed: " + err.Error()
		}
		if len(results) == 0 {
			return fmt.Sprintf("No web search results for %q.", query)
		}
		return formatWebSearchResults(query, results)
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
	case "TextPolish":
		text := strings.TrimSpace(stringFromAny(call.Arguments["text"]))
		if text == "" {
			return "Text polish failed: empty text"
		}
		polished, err := s.svc.PolishText(ctx, text)
		if err != nil {
			return "Text polish failed: " + err.Error()
		}
		return polished
	}
	return "unknown tool"
}

// handleAnthropicServerTools advertises + executes our injected server tools in
// an agentic loop. Streaming requests keep real token-by-token streaming
// (thinking and text flow live); only our own tool calls are suppressed and the
// post-tool continuation is stitched into the same message. Tools must already
// be injected into req by the caller.
func (s *Server) handleAnthropicServerTools(w http.ResponseWriter, r *http.Request, req anthropicRequest) {
	if req.Stream {
		s.streamAnthropicServerTools(w, r, req)
		return
	}
	ctx := r.Context()
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
		ours, others := partitionServerToolCalls(result.ToolCalls)
		if len(ours) == 0 || len(others) > 0 || round >= maxMediaToolRounds {
			s.emitAnthropicResultJSON(w, normalized, result)
			return
		}
		req = s.appendServerToolTurn(ctx, req, result, ours, round+1 >= maxMediaToolRounds)
	}
}

// appendServerToolTurn records the assistant tool_use turn and the executed
// tool_result turn onto req, and strips our tools on the last round so the model
// is forced to answer.
func (s *Server) appendServerToolTurn(ctx context.Context, req anthropicRequest, result *service.ChatResult, ours []toolemulation.ToolCall, lastRound bool) anthropicRequest {
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
			"content":     s.executeServerTool(ctx, c),
		})
	}
	req.Messages = append(req.Messages, rawMessage{Role: "user", Content: userContent})
	if lastRound {
		req.Tools = stripInjectedServerTools(req.Tools)
	}
	return req
}

// streamAnthropicServerTools streams the agentic loop as a single Anthropic
// message: thinking/text deltas are forwarded live, our tool calls are executed
// server-side (never sent to the client), and the post-tool continuation is
// appended as further content blocks in the same message.
func (s *Server) streamAnthropicServerTools(w http.ResponseWriter, r *http.Request, req anthropicRequest) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "streaming is not supported by this server")
		return
	}
	ctx := r.Context()
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = "lingma"
	}
	streamingHeaders(w)
	_ = writeSSEEvent(w, flusher, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": fmt.Sprintf("msg_%d", time.Now().UnixNano()), "type": "message", "role": "assistant",
			"content": []any{}, "model": model, "stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	})

	index := 0
	totalOutput := 0
	blockStart := func(block map[string]any) {
		_ = writeSSEEvent(w, flusher, "content_block_start", map[string]any{"type": "content_block_start", "index": index, "content_block": block})
	}
	blockDelta := func(delta map[string]any) {
		_ = writeSSEEvent(w, flusher, "content_block_delta", map[string]any{"type": "content_block_delta", "index": index, "delta": delta})
	}
	blockStop := func() {
		_ = writeSSEEvent(w, flusher, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
		index++
	}

	for round := 0; ; round++ {
		normalized, err := normalizeAnthropicRequest(req)
		if err != nil {
			_ = writeSSEEvent(w, flusher, "message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": totalOutput}})
			_ = writeSSEEvent(w, flusher, "message_stop", map[string]any{"type": "message_stop"})
			return
		}
		s.applyDefaultModel(&normalized)
		emitThinking := reasoningEffortEnabled(normalized.ReasoningEffort)

		events, done, err := s.svc.GenerateStream(ctx, normalized)
		if err != nil {
			_ = writeSSEEvent(w, flusher, "message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": totalOutput}})
			_ = writeSSEEvent(w, flusher, "message_stop", map[string]any{"type": "message_stop"})
			return
		}

		// Strip emulated text action blocks from the visible text stream (native
		// tool calls surface via result.ToolCalls and are handled after the round).
		filter := newToolStreamFilter(len(normalized.Tools) > 0)
		thinkingOpen, textOpen := false, false
		emitText := func(piece string) {
			if piece == "" {
				return
			}
			if thinkingOpen {
				blockStop()
				thinkingOpen = false
			}
			if !textOpen {
				blockStart(map[string]any{"type": "text", "text": ""})
				textOpen = true
			}
			blockDelta(map[string]any{"type": "text_delta", "text": piece})
		}
		for ev := range events {
			switch ev.Type {
			case service.StreamEventThinking:
				if !emitThinking {
					continue
				}
				if textOpen {
					blockStop()
					textOpen = false
				}
				if !thinkingOpen {
					blockStart(map[string]any{"type": "thinking", "thinking": ""})
					thinkingOpen = true
				}
				blockDelta(map[string]any{"type": "thinking_delta", "thinking": ev.Delta})
			case service.StreamEventText:
				for _, piece := range filter.Push(ev.Delta) {
					emitText(piece)
				}
				// StreamEventToolCall fragments are buffered by the service and
				// surface in the final result; we decide on them there.
			}
		}
		for _, piece := range filter.Flush() {
			emitText(piece)
		}
		if thinkingOpen {
			blockStop()
		}
		if textOpen {
			blockStop()
		}

		res := <-done
		if res.Err != nil || res.Result == nil {
			_ = writeSSEEvent(w, flusher, "message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": totalOutput}})
			_ = writeSSEEvent(w, flusher, "message_stop", map[string]any{"type": "message_stop"})
			return
		}
		result := res.Result
		totalOutput += result.OutputTokens
		ours, others := partitionServerToolCalls(result.ToolCalls)

		if len(ours) == 0 || len(others) > 0 || round >= maxMediaToolRounds {
			// Finalize: forward any genuine client tool calls, then close the message.
			for _, tc := range others {
				argsJSON, _ := json.Marshal(tc.Arguments)
				blockStart(map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": map[string]any{}})
				blockDelta(map[string]any{"type": "input_json_delta", "partial_json": string(argsJSON)})
				blockStop()
			}
			stopReason, stopSequence := anthropicStopReason(result)
			_ = writeSSEEvent(w, flusher, "message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stopReason, "stop_sequence": stopSequence}, "usage": map[string]any{"output_tokens": totalOutput}})
			_ = writeSSEEvent(w, flusher, "message_stop", map[string]any{"type": "message_stop"})
			return
		}
		// Execute our tools server-side and continue the same message next round.
		req = s.appendServerToolTurn(ctx, req, result, ours, round+1 >= maxMediaToolRounds)
	}
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
		if isServerTool(tc.Name) {
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
