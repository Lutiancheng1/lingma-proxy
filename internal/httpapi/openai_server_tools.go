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

// OpenAI-side server-tool injection. Mirrors the Anthropic path (media_tools.go)
// but operates on the normalized service.ChatRequest, since normalizeOpenAIRequest
// already preserves assistant tool_calls and tool-role results. When the
// injection flag is set, web_search + ImageSearch are advertised to the model;
// the model decides whether to call them, the proxy executes them server-side,
// and our tool calls never reach the client.

// injectOpenAIServerTools appends the proxy's server tools to a normalized
// request, skipping any the client already declares by name.
func injectOpenAIServerTools(req service.ChatRequest) service.ChatRequest {
	existing := map[string]bool{}
	for _, t := range req.Tools {
		existing[strings.TrimSpace(t.Name)] = true
	}
	for _, spec := range []serverToolSpec{webSearchSpec, imageSearchSpec, textPolishSpec} {
		if existing[spec.name] {
			continue
		}
		req.Tools = append(req.Tools, spec.toolDef())
	}
	return req
}

// stripServerToolDefs removes our injected server tools, leaving client tools.
func stripServerToolDefs(tools []toolemulation.ToolDef) []toolemulation.ToolDef {
	kept := make([]toolemulation.ToolDef, 0, len(tools))
	for _, t := range tools {
		if isServerTool(t.Name) {
			continue
		}
		kept = append(kept, t)
	}
	return kept
}

// resultWithoutServerToolCalls returns a shallow copy of result whose ToolCalls
// exclude our injected server tools (so finish_reason/usage and the surfaced
// tool_calls only reflect genuine client tools).
func resultWithoutServerToolCalls(result *service.ChatResult) *service.ChatResult {
	_, others := partitionServerToolCalls(result.ToolCalls)
	clone := *result
	clone.ToolCalls = others
	return &clone
}

// handleOpenAIServerTools runs the agentic loop for injected server tools.
func (s *Server) handleOpenAIServerTools(w http.ResponseWriter, r *http.Request, req service.ChatRequest, stream, emitUsage bool) {
	if stream {
		s.streamOpenAIServerTools(w, r, req, emitUsage)
		return
	}
	ctx := r.Context()
	var usageAcc serverToolUsage
	for round := 0; ; round++ {
		if ctx.Err() != nil {
			return // client disconnected; stop issuing more rounds / gateway calls
		}
		result, err := s.svc.Generate(ctx, req)
		if err != nil {
			writeOpenAIError(w, http.StatusInternalServerError, "api_error", err.Error())
			return
		}
		usageAcc.add(result)
		ours, others := partitionServerToolCalls(result.ToolCalls)
		if len(ours) == 0 || len(others) > 0 || round >= maxMediaToolRounds {
			if len(ours) > 0 {
				// Fold pending server-tool results into the text; surface only client tools.
				result.Text = appendText(result.Text, s.foldServerToolResults(ctx, ours))
			}
			writeOpenAIChatCompletion(w, usageAcc.applyTo(resultWithoutServerToolCalls(result)))
			return
		}
		req = s.appendOpenAIToolTurn(ctx, req, result, ours, round+1 >= maxMediaToolRounds)
	}
}

// appendOpenAIToolTurn records the assistant tool_use turn and the executed
// tool-role results onto req, stripping our tools on the last round so the model
// is forced to answer.
func (s *Server) appendOpenAIToolTurn(ctx context.Context, req service.ChatRequest, result *service.ChatResult, ours []toolemulation.ToolCall, lastRound bool) service.ChatRequest {
	req.Messages = append(req.Messages, service.ChatMessage{
		Role:      "assistant",
		Text:      result.Text,
		ToolCalls: ours,
	})
	for _, c := range ours {
		req.Messages = append(req.Messages, service.ChatMessage{
			Role:       "tool",
			ToolCallID: c.ID,
			Text:       s.executeServerTool(ctx, c),
		})
	}
	if lastRound {
		req.Tools = stripServerToolDefs(req.Tools)
	}
	return req
}

// streamOpenAIServerTools streams the agentic loop as a single chat.completion
// stream: reasoning/content deltas flow live across rounds, our tool calls are
// executed server-side (never sent to the client), and only genuine client tool
// calls are surfaced at the end.
func (s *Server) streamOpenAIServerTools(w http.ResponseWriter, r *http.Request, req service.ChatRequest, emitUsage bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", "streaming is not supported by this server")
		return
	}
	ctx := r.Context()
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = "lingma"
	}
	chatID := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	created := time.Now().Unix()

	streamingHeaders(w)
	_ = writeOpenAIChunk(w, flusher, map[string]any{
		"id": chatID, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{"role": "assistant"}, "finish_reason": nil}},
	})

	var usageAcc serverToolUsage
	emitError := func(msg string) {
		_ = writeOpenAIChunk(w, flusher, map[string]any{"error": map[string]any{"message": msg, "type": "api_error", "code": nil, "param": nil}})
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}

	for round := 0; ; round++ {
		if ctx.Err() != nil {
			return // client disconnected; stop issuing more rounds / gateway calls
		}
		events, done, err := s.svc.GenerateStream(ctx, req)
		if err != nil {
			emitError(err.Error())
			return
		}
		// Strip emulated text action blocks from the visible stream (native tool
		// calls surface via result.ToolCalls and are handled after the round).
		filter := newToolStreamFilter(len(req.Tools) > 0)
		emitReasoning := reasoningEffortEnabled(req.ReasoningEffort)
		emitContent := func(piece string) {
			if piece == "" {
				return
			}
			_ = writeOpenAIChunk(w, flusher, map[string]any{
				"id": chatID, "object": "chat.completion.chunk", "created": created, "model": model,
				"choices": []map[string]any{{"index": 0, "delta": map[string]any{"content": piece}, "finish_reason": nil}},
			})
		}
		for ev := range events {
			switch ev.Type {
			case service.StreamEventThinking:
				if !emitReasoning || ev.Delta == "" {
					continue
				}
				_ = writeOpenAIChunk(w, flusher, map[string]any{
					"id": chatID, "object": "chat.completion.chunk", "created": created, "model": model,
					"choices": []map[string]any{{"index": 0, "delta": map[string]any{"reasoning_content": ev.Delta}, "finish_reason": nil}},
				})
			case service.StreamEventText:
				for _, piece := range filter.Push(ev.Delta) {
					emitContent(piece)
				}
				// StreamEventToolCall fragments are buffered by the service and
				// surface in the final result; we decide on them there.
			}
		}
		for _, piece := range filter.Flush() {
			emitContent(piece)
		}

		res := <-done
		if res.Err != nil || res.Result == nil {
			// Close the stream gracefully rather than erroring mid-message.
			_ = writeOpenAIChunk(w, flusher, map[string]any{
				"id": chatID, "object": "chat.completion.chunk", "created": created, "model": model,
				"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
			})
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
		result := res.Result
		usageAcc.add(result)
		ours, others := partitionServerToolCalls(result.ToolCalls)

		if len(ours) == 0 || len(others) > 0 || round >= maxMediaToolRounds {
			// Fold pending server-tool results into the content; surface only client tools.
			if len(ours) > 0 {
				if folded := s.foldServerToolResults(ctx, ours); folded != "" {
					emitContent(folded)
				}
			}
			for i, tc := range others {
				argsJSON, _ := json.Marshal(tc.Arguments)
				_ = writeOpenAIChunk(w, flusher, map[string]any{
					"id": chatID, "object": "chat.completion.chunk", "created": created, "model": model,
					"choices": []map[string]any{{
						"index": 0,
						"delta": map[string]any{"tool_calls": []map[string]any{{
							"index": i, "id": tc.ID, "type": "function",
							"function": map[string]any{"name": tc.Name, "arguments": string(argsJSON)},
						}}},
						"finish_reason": nil,
					}},
				})
			}
			final := resultWithoutServerToolCalls(result)
			_ = writeOpenAIChunk(w, flusher, map[string]any{
				"id": chatID, "object": "chat.completion.chunk", "created": created, "model": model,
				"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": openAIFinishReason(final)}},
			})
			if emitUsage {
				_ = writeOpenAIChunk(w, flusher, map[string]any{
					"id": chatID, "object": "chat.completion.chunk", "created": created, "model": model,
					"choices": []map[string]any{},
					"usage":   openAIUsageMap(usageAcc.result()),
				})
			}
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
		req = s.appendOpenAIToolTurn(ctx, req, result, ours, round+1 >= maxMediaToolRounds)
	}
}
