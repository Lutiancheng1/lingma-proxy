package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"lingma-ipc-proxy/internal/service"
	"lingma-ipc-proxy/internal/toolemulation"
	"lingma-ipc-proxy/internal/version"
)

type Server struct {
	svc     *service.Service
	http    *http.Server
	sem     chan struct{}
	recMu   sync.RWMutex
	records []debugRequestRecord
	logs    []debugLogRecord
	// OnRequest is called after each request completes with summary info.
	// method, path, statusCode, duration, requestBody, responseBody
	OnRequest func(method, path string, statusCode int, duration time.Duration, reqBody, respBody string)
}

type anthropicRequest struct {
	Model         string         `json:"model"`
	MaxTokens     int            `json:"max_tokens,omitempty"`
	System        any            `json:"system,omitempty"`
	Messages      []rawMessage   `json:"messages"`
	Stream        bool           `json:"stream,omitempty"`
	Tools         any            `json:"tools,omitempty"`
	ToolChoice    any            `json:"tool_choice,omitempty"`
	Temperature   *float64       `json:"temperature,omitempty"`
	TopP          *float64       `json:"top_p,omitempty"`
	TopK          int            `json:"top_k,omitempty"`
	StopSequences []string       `json:"stop_sequences,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	Thinking      any            `json:"thinking,omitempty"`
}

type openAIChatRequest struct {
	Model               string       `json:"model"`
	Messages            []rawMessage `json:"messages"`
	Stream              bool         `json:"stream,omitempty"`
	MaxTokens           int          `json:"max_tokens,omitempty"`
	MaxCompletionTokens int          `json:"max_completion_tokens,omitempty"`
	Tools               any          `json:"tools,omitempty"`
	ToolChoice          any          `json:"tool_choice,omitempty"`
	ParallelToolCalls   *bool        `json:"parallel_tool_calls,omitempty"`
	Temperature         *float64     `json:"temperature,omitempty"`
	TopP                *float64     `json:"top_p,omitempty"`
	Stop                any          `json:"stop,omitempty"`
	PresencePenalty     float64      `json:"presence_penalty,omitempty"`
	FrequencyPenalty    float64      `json:"frequency_penalty,omitempty"`
	Logprobs            bool         `json:"logprobs,omitempty"`
	TopLogprobs         int          `json:"top_logprobs,omitempty"`
	ResponseFormat      any          `json:"response_format,omitempty"`
	Seed                int          `json:"seed,omitempty"`
	User                string       `json:"user,omitempty"`
	ReasoningEffort     string       `json:"reasoning_effort,omitempty"`
}

type openAIResponsesRequest struct {
	Model             string   `json:"model"`
	Input             any      `json:"input"`
	Instructions      string   `json:"instructions,omitempty"`
	Stream            bool     `json:"stream,omitempty"`
	Tools             any      `json:"tools,omitempty"`
	ToolChoice        any      `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool    `json:"parallel_tool_calls,omitempty"`
	Temperature       *float64 `json:"temperature,omitempty"`
	TopP              *float64 `json:"top_p,omitempty"`
	MaxOutputTokens   int      `json:"max_output_tokens,omitempty"`
	MaxTokens         int      `json:"max_tokens,omitempty"`
	Stop              any      `json:"stop,omitempty"`
	User              string   `json:"user,omitempty"`
	Reasoning         any      `json:"reasoning,omitempty"`
	Text              any      `json:"text,omitempty"`
}

type rawMessage struct {
	Role       string `json:"role"`
	Content    any    `json:"content"`
	ToolCalls  []any  `json:"tool_calls,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

type modelResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
	Name    string `json:"name,omitempty"`
}

type debugRequestRecord struct {
	Time       string `json:"time"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	StatusCode int    `json:"statusCode"`
	DurationMS int64  `json:"durationMs"`
	Request    string `json:"request,omitempty"`
	Response   string `json:"response,omitempty"`
}

type debugLogRecord struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

func NewServer(addr string, svc *service.Service) *Server {
	s := &Server{
		svc: svc,
		sem: make(chan struct{}, maxConcurrentRequests()),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/health", s.handleRoot)
	mux.HandleFunc("/debug/requests", s.handleDebugRequests)
	mux.HandleFunc("/debug/access-logs", s.handleDebugLogs)
	mux.HandleFunc("/api/requests", s.handleDebugRequests)
	mux.HandleFunc("/api/access-logs", s.handleDebugLogs)
	mux.HandleFunc("/debug/logs", s.handleDebugLogs)
	mux.HandleFunc("/api/logs", s.handleDebugLogs)
	mux.HandleFunc("/capabilities", s.handleCapabilities)
	mux.HandleFunc("/v1/capabilities", s.handleCapabilities)
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/api/v1/models", s.handleLMStudioModels)
	mux.HandleFunc("/api/tags", s.handleOllamaTags)
	mux.HandleFunc("/v1/props", s.handleModelProps)
	mux.HandleFunc("/props", s.handleModelProps)
	mux.HandleFunc("/version", s.handleVersion)
	mux.HandleFunc("/v1/messages/count_tokens", s.handleAnthropicCountTokens)
	mux.HandleFunc("/v1/messages", s.handleAnthropicMessages)
	mux.HandleFunc("/v1/chat/completions", s.handleOpenAIChatCompletions)
	mux.HandleFunc("/api/v1/chat/completions", s.handleOpenAIChatCompletions)
	mux.HandleFunc("/v1/responses", s.handleOpenAIResponses)
	mux.HandleFunc("/api/v1/responses", s.handleOpenAIResponses)

	s.http = &http.Server{
		Addr:              addr,
		Handler:           s.withRecorder(withCORS(mux)),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

func (s *Server) ListenAndServe() error {
	return s.http.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	err := s.http.Shutdown(ctx)
	if err != nil {
		if forceErr := s.http.Close(); forceErr != nil {
			err = fmt.Errorf("%w; force close failed: %v", err, forceErr)
		} else {
			err = nil
		}
	}
	closeErr := s.svc.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func (s *Server) SetDefaultModel(model string) {
	s.svc.SetDefaultModel(model)
}

func (s *Server) applyDefaultModel(req *service.ChatRequest) {
	if strings.TrimSpace(req.Model) == "" {
		req.Model = s.svc.DefaultModel()
	}
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/health" {
		writeOpenAIError(w, http.StatusNotFound, "not_found_error", "not found")
		return
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"service": "lingma-proxy",
		"state":   s.svc.State(),
	})
}

func (s *Server) handleDebugRequests(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}

	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			switch {
			case parsed < 1:
				limit = 1
			case parsed > 200:
				limit = 200
			default:
				limit = parsed
			}
		}
	}

	records := s.debugRecords(limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"service":  "lingma-proxy",
		"count":    len(records),
		"requests": records,
		"state":    s.svc.State(),
	})
}

func (s *Server) handleDebugLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}

	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			switch {
			case parsed < 1:
				limit = 1
			case parsed > 200:
				limit = 200
			default:
				limit = parsed
			}
		}
	}

	logs := s.debugLogs(limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"service": "lingma-proxy",
		"kind":    "http_access_logs",
		"count":   len(logs),
		"logs":    logs,
		"state":   s.svc.State(),
	})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}

	models, err := s.svc.ListModels(r.Context())
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", err.Error())
		return
	}

	data := make([]modelResponse, 0, len(models))
	created := time.Now().Unix()
	for _, model := range models {
		data = append(data, modelResponse{
			ID:      model.ID,
			Object:  "model",
			Created: created,
			OwnedBy: "lingma",
			Name:    model.Name,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   data,
	})
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"service": "lingma-proxy",
		"protocols": []string{
			"openai.chat_completions",
			"openai.responses",
			"anthropic.messages",
			"lm_studio.discovery",
			"ollama.discovery",
			"llamacpp.discovery",
			"vllm.discovery",
		},
		"features": map[string]any{
			"streaming":                true,
			"tools":                    true,
			"tool_prompt_emulation":    true,
			"tool_alias_mapping":       true,
			"images":                   true,
			"local_image_paths":        true,
			"remote_image_urls":        true,
			"image_auto_resize":        true,
			"request_log_image_redact": true,
		},
		"recommended_models": map[string]any{
			"default":     "kmodel",
			"agent_tools": []string{"kmodel", "MiniMax-M2.7", "Qwen3-Coder", "Qwen3.6-Plus"},
			"vision":      []string{"Kimi-K2.6", "Qwen3-Max", "Qwen3.6-Plus", "MiniMax-M2.7", "Auto"},
			"coding":      []string{"kmodel", "Qwen3-Coder", "MiniMax-M2.7"},
		},
		"model_metadata": map[string]any{
			"Kimi-K2.6": map[string]any{
				"context_window_tokens": 256000,
				"modalities":            []string{"text", "image", "video"},
				"capabilities":          []string{"agent", "coding", "tool_use", "vision"},
				"basis":                 "official_kimi_docs",
				"source":                "https://platform.kimi.ai/docs/guide/kimi-k2-6-quickstart",
			},
			"Qwen3-Coder": map[string]any{
				"context_window_tokens": 256000,
				"context_window_note":   "native 256K; official Qwen material describes extension up to 1M with extrapolation",
				"modalities":            []string{"text"},
				"capabilities":          []string{"agentic_coding", "tool_use"},
				"basis":                 "official_qwen_docs",
				"source":                "https://qwenlm.github.io/blog/qwen3-coder/",
			},
			"MiniMax-M2.7": map[string]any{
				"context_window_tokens": 204800,
				"modalities":            []string{"text"},
				"capabilities":          []string{"agent", "coding", "tool_use", "skills"},
				"basis":                 "minimax_and_nvidia_model_cards",
				"source":                "https://developer.nvidia.com/blog/minimax-m2-7-advances-scalable-agentic-workflows-on-nvidia-platforms-for-complex-ai-applications/",
			},
			"Qwen3.6-Plus": map[string]any{
				"context_window_tokens": nil,
				"modalities":            []string{"text", "image"},
				"capabilities":          []string{"general", "vision_observed_via_lingma"},
				"basis":                 "observed_via_lingma_proxy; no official Lingma-specific context length published in this proxy",
			},
		},
		"endpoints": map[string]any{
			"openai_chat":        []string{"/v1/chat/completions", "/api/v1/chat/completions"},
			"openai_responses":   []string{"/v1/responses", "/api/v1/responses"},
			"anthropic_messages": "/v1/messages",
			"models":             []string{"/v1/models", "/api/v1/models", "/api/tags"},
			"capabilities":       []string{"/capabilities", "/v1/capabilities"},
			"props":              []string{"/props", "/v1/props"},
			"version":            "/version",
		},
	})
}

func (s *Server) handleLMStudioModels(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}

	models, err := s.svc.ListModels(r.Context())
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", err.Error())
		return
	}

	items := make([]map[string]any, 0, len(models))
	for _, model := range models {
		items = append(items, map[string]any{
			"id":                 model.ID,
			"key":                model.ID,
			"display_name":       model.Name,
			"type":               "llm",
			"publisher":          "lingma",
			"max_context_length": 128000,
			"loaded_instances": []map[string]any{
				{
					"id":    model.ID,
					"model": model.ID,
					"config": map[string]any{
						"context_length": 128000,
					},
				},
			},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": items})
}

func (s *Server) handleOllamaTags(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}

	models, err := s.svc.ListModels(r.Context())
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", err.Error())
		return
	}

	items := make([]map[string]any, 0, len(models))
	for _, model := range models {
		items = append(items, map[string]any{
			"name":        model.ID,
			"model":       model.ID,
			"modified_at": time.Now().UTC().Format(time.RFC3339),
			"size":        0,
			"digest":      "",
			"details": map[string]any{
				"family":             "lingma",
				"families":           []string{"lingma"},
				"parameter_size":     "",
				"quantization_level": "",
			},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": items})
}

func (s *Server) handleModelProps(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}

	model := strings.TrimSpace(s.svc.DefaultModel())
	if model == "" {
		model = "kmodel"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"model_alias":   model,
		"chat_template": "{{ .Messages }}",
		"default_generation_settings": map[string]any{
			"n_ctx":       128000,
			"temperature": 0.7,
			"top_p":       1,
		},
	})
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": version.Version,
		"service": "lingma-proxy",
	})
}

func (s *Server) handleAnthropicCountTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		writeAnthropicError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}

	var req anthropicRequest
	if err := decodeJSON(r, &req); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"input_tokens": estimateAnthropicInputTokens(req),
	})
}

func (s *Server) handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		writeAnthropicError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	if !s.acquire(r.Context()) {
		writeAnthropicError(w, http.StatusRequestTimeout, "timeout_error", "request was cancelled while waiting for a proxy execution slot")
		return
	}
	defer s.release()

	var req anthropicRequest
	if err := decodeJSON(r, &req); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	if reqBody, _ := json.Marshal(req); len(reqBody) > 0 {
		fmt.Printf("[ANTHROPIC REQUEST] %s\n", string(reqBody))
	}

	if call, ok := anthropicHostedWebSearchCall(req); ok {
		if req.Stream {
			s.writeAnthropicHostedToolStream(w, req.Model, call)
			return
		}
		s.writeAnthropicHostedToolResponse(w, req.Model, call)
		return
	}

	normalized, err := normalizeAnthropicRequest(req)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	s.applyDefaultModel(&normalized)

	if req.Stream {
		s.handleAnthropicStream(w, r, normalized)
		return
	}

	result, err := s.svc.Generate(r.Context(), normalized)
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", err.Error())
		return
	}

	content := make([]map[string]any, 0, 2+len(result.ToolCalls))
	if shouldEmitAnthropicThinking(normalized, result) {
		content = append(content, map[string]any{"type": "thinking", "thinking": result.ThoughtText})
	}
	content = append(content, map[string]any{"type": "text", "text": result.Text})
	stopReason := "end_turn"
	if len(result.ToolCalls) > 0 {
		for _, tc := range result.ToolCalls {
			content = append(content, map[string]any{
				"type":  "tool_use",
				"id":    tc.ID,
				"name":  tc.Name,
				"input": tc.Arguments,
			})
		}
		stopReason = "tool_use"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":            fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		"type":          "message",
		"role":          "assistant",
		"content":       content,
		"model":         result.Model,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  result.InputTokens,
			"output_tokens": result.OutputTokens,
		},
	})
}

func (s *Server) handleOpenAIChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	if !s.acquire(r.Context()) {
		writeOpenAIError(w, http.StatusRequestTimeout, "timeout_error", "request was cancelled while waiting for a proxy execution slot")
		return
	}
	defer s.release()

	var req openAIChatRequest
	if err := decodeJSON(r, &req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	normalized, err := normalizeOpenAIRequest(req)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	s.applyDefaultModel(&normalized)

	if req.Stream {
		s.handleOpenAIStream(w, r, normalized)
		return
	}

	result, err := s.svc.Generate(r.Context(), normalized)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", err.Error())
		return
	}

	writeOpenAIChatCompletion(w, result)
}

func (s *Server) handleOpenAIResponses(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	if !s.acquire(r.Context()) {
		writeOpenAIError(w, http.StatusRequestTimeout, "timeout_error", "request was cancelled while waiting for a proxy execution slot")
		return
	}
	defer s.release()

	var req openAIResponsesRequest
	if err := decodeJSON(r, &req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	chatReq, err := responsesRequestToChatRequest(req)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	normalized, err := normalizeOpenAIRequest(chatReq)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	s.applyDefaultModel(&normalized)

	if req.Stream {
		s.handleOpenAIResponsesStream(w, r, normalized)
		return
	}

	result, err := s.svc.Generate(r.Context(), normalized)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", err.Error())
		return
	}
	writeOpenAIResponse(w, result)
}

func (s *Server) handleAnthropicStream(w http.ResponseWriter, r *http.Request, req service.ChatRequest) {
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

	if shouldAggregateToolStream(req) {
		result, err := s.svc.Generate(r.Context(), req)
		if err != nil {
			writeAnthropicError(w, http.StatusInternalServerError, "api_error", err.Error())
			return
		}

		streamingHeaders(w)
		if err := writeSSEEvent(w, flusher, "message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            msgID,
				"type":          "message",
				"role":          "assistant",
				"content":       []any{},
				"model":         model,
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage": map[string]any{
					"input_tokens":  result.InputTokens,
					"output_tokens": 0,
				},
			},
		}); err != nil {
			return
		}

		index := 0
		if shouldEmitAnthropicThinking(req, result) {
			if err := writeSSEEvent(w, flusher, "content_block_start", map[string]any{
				"type":          "content_block_start",
				"index":         index,
				"content_block": map[string]any{"type": "thinking", "thinking": ""},
			}); err != nil {
				return
			}
			if err := writeSSEEvent(w, flusher, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": index,
				"delta": map[string]any{"type": "thinking_delta", "thinking": result.ThoughtText},
			}); err != nil {
				return
			}
			if err := writeSSEEvent(w, flusher, "content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": index,
			}); err != nil {
				return
			}
			index++
		}
		if strings.TrimSpace(result.Text) != "" {
			if err := writeSSEEvent(w, flusher, "content_block_start", map[string]any{
				"type":          "content_block_start",
				"index":         index,
				"content_block": map[string]any{"type": "text", "text": ""},
			}); err != nil {
				return
			}
			if err := writeSSEEvent(w, flusher, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": index,
				"delta": map[string]any{"type": "text_delta", "text": result.Text},
			}); err != nil {
				return
			}
			if err := writeSSEEvent(w, flusher, "content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": index,
			}); err != nil {
				return
			}
			index++
		}

		for _, tc := range result.ToolCalls {
			if err := writeSSEEvent(w, flusher, "content_block_start", map[string]any{
				"type":          "content_block_start",
				"index":         index,
				"content_block": map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": map[string]any{}},
			}); err != nil {
				return
			}
			argsJSON, _ := json.Marshal(tc.Arguments)
			if err := writeSSEEvent(w, flusher, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": index,
				"delta": map[string]any{"type": "input_json_delta", "partial_json": string(argsJSON)},
			}); err != nil {
				return
			}
			if err := writeSSEEvent(w, flusher, "content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": index,
			}); err != nil {
				return
			}
			index++
		}

		stopReason := "end_turn"
		if len(result.ToolCalls) > 0 {
			stopReason = "tool_use"
		}
		_ = writeSSEEvent(w, flusher, "message_delta", map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason":   stopReason,
				"stop_sequence": nil,
			},
			"usage": map[string]any{
				"output_tokens": result.OutputTokens,
			},
		})
		_ = writeSSEEvent(w, flusher, "message_stop", map[string]any{"type": "message_stop"})
		return
	}

	events, done, err := s.svc.GenerateStream(r.Context(), req)
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", err.Error())
		return
	}

	streamingHeaders(w)
	if err := writeSSEEvent(w, flusher, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            msgID,
			"type":          "message",
			"role":          "assistant",
			"content":       []any{},
			"model":         model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  0,
				"output_tokens": 0,
			},
		},
	}); err != nil {
		return
	}

	filter := newToolStreamFilter(len(req.Tools) > 0)
	eventsCh := events
	doneCh := done
	var final *service.ChatResult
	var finalErr error
	thinkingEnabled := strings.TrimSpace(req.ReasoningEffort) != ""
	thinkingOpen := false
	textOpen := false
	textIndex := 0
	if thinkingEnabled {
		textIndex = 1
	}

	for eventsCh != nil || doneCh != nil {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-eventsCh:
			if !ok {
				eventsCh = nil
				continue
			}
			switch event.Type {
			case service.StreamEventThinking:
				if !thinkingEnabled || strings.TrimSpace(event.Delta) == "" {
					continue
				}
				if !thinkingOpen {
					if err := writeSSEEvent(w, flusher, "content_block_start", map[string]any{
						"type":          "content_block_start",
						"index":         0,
						"content_block": map[string]any{"type": "thinking", "thinking": ""},
					}); err != nil {
						return
					}
					thinkingOpen = true
				}
				if err := writeSSEEvent(w, flusher, "content_block_delta", map[string]any{
					"type":  "content_block_delta",
					"index": 0,
					"delta": map[string]any{
						"type":     "thinking_delta",
						"thinking": event.Delta,
					},
				}); err != nil {
					return
				}
			default:
				for _, delta := range filter.Push(event.Delta) {
					if delta == "" {
						continue
					}
					if thinkingOpen {
						if err := writeSSEEvent(w, flusher, "content_block_stop", map[string]any{
							"type":  "content_block_stop",
							"index": 0,
						}); err != nil {
							return
						}
						thinkingOpen = false
					}
					if !textOpen {
						if err := writeSSEEvent(w, flusher, "content_block_start", map[string]any{
							"type":          "content_block_start",
							"index":         textIndex,
							"content_block": map[string]any{"type": "text", "text": ""},
						}); err != nil {
							return
						}
						textOpen = true
					}
					if err := writeSSEEvent(w, flusher, "content_block_delta", map[string]any{
						"type":  "content_block_delta",
						"index": textIndex,
						"delta": map[string]any{
							"type": "text_delta",
							"text": delta,
						},
					}); err != nil {
						return
					}
				}
			}
		case result, ok := <-doneCh:
			if !ok {
				doneCh = nil
				continue
			}
			final = result.Result
			finalErr = result.Err
			doneCh = nil
		}
	}

	if finalErr != nil {
		_ = writeSSEEvent(w, flusher, "error", map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "api_error",
				"message": finalErr.Error(),
			},
		})
		return
	}
	if final == nil {
		_ = writeSSEEvent(w, flusher, "error", map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "api_error",
				"message": "stream finished without a final result",
			},
		})
		return
	}
	if len(final.ToolCalls) == 0 {
		for _, delta := range filter.Flush() {
			if delta == "" {
				continue
			}
			if thinkingOpen {
				if err := writeSSEEvent(w, flusher, "content_block_stop", map[string]any{
					"type":  "content_block_stop",
					"index": 0,
				}); err != nil {
					return
				}
				thinkingOpen = false
			}
			if !textOpen {
				if err := writeSSEEvent(w, flusher, "content_block_start", map[string]any{
					"type":          "content_block_start",
					"index":         textIndex,
					"content_block": map[string]any{"type": "text", "text": ""},
				}); err != nil {
					return
				}
				textOpen = true
			}
			if err := writeSSEEvent(w, flusher, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": textIndex,
				"delta": map[string]any{
					"type": "text_delta",
					"text": delta,
				},
			}); err != nil {
				return
			}
		}
	}
	if thinkingOpen {
		if err := writeSSEEvent(w, flusher, "content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": 0,
		}); err != nil {
			return
		}
		thinkingOpen = false
	}
	if textOpen {
		if err := writeSSEEvent(w, flusher, "content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": textIndex,
		}); err != nil {
			return
		}
	}
	for i, tc := range final.ToolCalls {
		blockIndex := i + 1
		if textOpen {
			blockIndex = textIndex + 1 + i
		} else if thinkingEnabled {
			blockIndex = 1 + i
		}
		_ = writeSSEEvent(w, flusher, "content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         blockIndex,
			"content_block": map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": map[string]any{}},
		})
		argsJSON, _ := json.Marshal(tc.Arguments)
		_ = writeSSEEvent(w, flusher, "content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": blockIndex,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": string(argsJSON)},
		})
		_ = writeSSEEvent(w, flusher, "content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": blockIndex,
		})
	}
	stopReason := "end_turn"
	if len(final.ToolCalls) > 0 {
		stopReason = "tool_use"
	}
	if err := writeSSEEvent(w, flusher, "message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"output_tokens": final.OutputTokens,
		},
	}); err != nil {
		return
	}
	_ = writeSSEEvent(w, flusher, "message_stop", map[string]any{
		"type": "message_stop",
	})
}

func (s *Server) writeAnthropicHostedToolResponse(w http.ResponseWriter, model string, call toolemulation.ToolCall) {
	model = strings.TrimSpace(model)
	if model == "" {
		model = "lingma"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":   fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		"type": "message",
		"role": "assistant",
		"content": []map[string]any{{
			"type":  "tool_use",
			"id":    call.ID,
			"name":  call.Name,
			"input": call.Arguments,
		}},
		"model":         model,
		"stop_reason":   "tool_use",
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  0,
			"output_tokens": 0,
		},
	})
}

func (s *Server) writeAnthropicHostedToolStream(w http.ResponseWriter, model string, call toolemulation.ToolCall) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "streaming is not supported by this server")
		return
	}

	model = strings.TrimSpace(model)
	if model == "" {
		model = "lingma"
	}
	streamingHeaders(w)
	msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	if err := writeSSEEvent(w, flusher, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            msgID,
			"type":          "message",
			"role":          "assistant",
			"content":       []any{},
			"model":         model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  0,
				"output_tokens": 0,
			},
		},
	}); err != nil {
		return
	}
	if err := writeSSEEvent(w, flusher, "content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         0,
		"content_block": map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": map[string]any{}},
	}); err != nil {
		return
	}
	argsJSON, _ := json.Marshal(call.Arguments)
	if err := writeSSEEvent(w, flusher, "content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{"type": "input_json_delta", "partial_json": string(argsJSON)},
	}); err != nil {
		return
	}
	if err := writeSSEEvent(w, flusher, "content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": 0,
	}); err != nil {
		return
	}
	_ = writeSSEEvent(w, flusher, "message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   "tool_use",
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"output_tokens": 0,
		},
	})
	_ = writeSSEEvent(w, flusher, "message_stop", map[string]any{"type": "message_stop"})
}

func (s *Server) handleOpenAIStream(w http.ResponseWriter, r *http.Request, req service.ChatRequest) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", "streaming is not supported by this server")
		return
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = "lingma"
	}
	chatID := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	created := time.Now().Unix()

	if shouldAggregateToolStream(req) {
		result, err := s.svc.Generate(r.Context(), req)
		if err != nil {
			writeOpenAIError(w, http.StatusInternalServerError, "api_error", err.Error())
			return
		}
		streamingHeaders(w)
		_ = writeOpenAIChunk(w, flusher, map[string]any{
			"id": chatID, "object": "chat.completion.chunk", "created": created, "model": model,
			"choices": []map[string]any{{"index": 0, "delta": map[string]any{"role": "assistant"}, "finish_reason": nil}},
		})
		if result.Text != "" {
			_ = writeOpenAIChunk(w, flusher, map[string]any{
				"id": chatID, "object": "chat.completion.chunk", "created": created, "model": model,
				"choices": []map[string]any{{"index": 0, "delta": map[string]any{"content": result.Text}, "finish_reason": nil}},
			})
		}
		for i, tc := range result.ToolCalls {
			argsJSON, _ := json.Marshal(tc.Arguments)
			_ = writeOpenAIChunk(w, flusher, map[string]any{
				"id": chatID, "object": "chat.completion.chunk", "created": created, "model": model,
				"choices": []map[string]any{{
					"index": 0,
					"delta": map[string]any{
						"tool_calls": []map[string]any{{
							"index": i, "id": tc.ID, "type": "function",
							"function": map[string]any{"name": tc.Name, "arguments": string(argsJSON)},
						}},
					},
					"finish_reason": nil,
				}},
			})
		}
		finishReason := "stop"
		if len(result.ToolCalls) > 0 {
			finishReason = "tool_calls"
		}
		_ = writeOpenAIChunk(w, flusher, map[string]any{
			"id": chatID, "object": "chat.completion.chunk", "created": created, "model": model,
			"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": finishReason}},
		})
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}

	events, done, err := s.svc.GenerateStream(r.Context(), req)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", err.Error())
		return
	}

	streamingHeaders(w)
	if err := writeOpenAIChunk(w, flusher, map[string]any{
		"id":      chatID,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []map[string]any{
			{
				"index": 0,
				"delta": map[string]any{
					"role": "assistant",
				},
				"finish_reason": nil,
			},
		},
	}); err != nil {
		return
	}

	filter := newToolStreamFilter(len(req.Tools) > 0)
	eventsCh := events
	doneCh := done
	var final *service.ChatResult
	var finalErr error

	for eventsCh != nil || doneCh != nil {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-eventsCh:
			if !ok {
				eventsCh = nil
				continue
			}
			for _, delta := range filter.Push(event.Delta) {
				if delta == "" {
					continue
				}
				if err := writeOpenAIChunk(w, flusher, map[string]any{
					"id":      chatID,
					"object":  "chat.completion.chunk",
					"created": created,
					"model":   model,
					"choices": []map[string]any{
						{
							"index": 0,
							"delta": map[string]any{
								"content": delta,
							},
							"finish_reason": nil,
						},
					},
				}); err != nil {
					return
				}
			}
		case result, ok := <-doneCh:
			if !ok {
				doneCh = nil
				continue
			}
			final = result.Result
			finalErr = result.Err
			doneCh = nil
		}
	}

	if finalErr != nil {
		_ = writeOpenAIChunk(w, flusher, map[string]any{
			"error": map[string]any{
				"message": finalErr.Error(),
				"type":    "api_error",
				"code":    nil,
				"param":   nil,
			},
		})
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}
	if final == nil {
		_ = writeOpenAIChunk(w, flusher, map[string]any{
			"error": map[string]any{
				"message": "stream finished without a final result",
				"type":    "api_error",
				"code":    nil,
				"param":   nil,
			},
		})
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}
	if len(final.ToolCalls) == 0 {
		for _, delta := range filter.Flush() {
			if delta == "" {
				continue
			}
			if err := writeOpenAIChunk(w, flusher, map[string]any{
				"id":      chatID,
				"object":  "chat.completion.chunk",
				"created": created,
				"model":   model,
				"choices": []map[string]any{
					{
						"index": 0,
						"delta": map[string]any{
							"content": delta,
						},
						"finish_reason": nil,
					},
				},
			}); err != nil {
				return
			}
		}
	}
	for i, tc := range final.ToolCalls {
		argsJSON, _ := json.Marshal(tc.Arguments)
		_ = writeOpenAIChunk(w, flusher, map[string]any{
			"id": chatID, "object": "chat.completion.chunk", "created": created, "model": model,
			"choices": []map[string]any{{
				"index": 0,
				"delta": map[string]any{
					"tool_calls": []map[string]any{{
						"index": i, "id": tc.ID, "type": "function",
						"function": map[string]any{"name": tc.Name, "arguments": string(argsJSON)},
					}},
				},
				"finish_reason": nil,
			}},
		})
	}
	finishReason := "stop"
	if len(final.ToolCalls) > 0 {
		finishReason = "tool_calls"
	}
	if err := writeOpenAIChunk(w, flusher, map[string]any{
		"id":      chatID,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []map[string]any{
			{
				"index":         0,
				"delta":         map[string]any{},
				"finish_reason": finishReason,
			},
		},
	}); err != nil {
		return
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func (s *Server) handleOpenAIResponsesStream(w http.ResponseWriter, r *http.Request, req service.ChatRequest) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", "streaming is not supported by this server")
		return
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = "lingma"
	}
	responseID := fmt.Sprintf("resp_%d", time.Now().UnixNano())
	messageID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	created := time.Now().Unix()
	messageStarted := false
	streamingHeaders(w)
	emitter := newOpenAIResponseStreamEmitter(w, flusher, responseID)
	if err := emitter.Event("response.created", map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id":         responseID,
			"object":     "response",
			"created_at": created,
			"status":     "in_progress",
			"model":      model,
		},
	}); err != nil {
		return
	}

	if shouldAggregateToolStream(req) {
		result, err := s.svc.Generate(r.Context(), req)
		if err != nil {
			_ = emitter.Event("error", map[string]any{"type": "error", "error": map[string]any{"type": "api_error", "message": err.Error()}})
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
		writeOpenAIResponseStreamCompleted(emitter, responseID, created, model, result, messageID, false, shouldEmitResponsesReasoning(req, result))
		return
	}

	events, done, err := s.svc.GenerateStream(r.Context(), req)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", err.Error())
		return
	}

	filter := newToolStreamFilter(len(req.Tools) > 0)
	eventsCh := events
	doneCh := done
	var final *service.ChatResult
	var finalErr error
	pendingText := make([]string, 0, 16)
	pendingThought := make([]string, 0, 16)
	reasoningEnabled := strings.TrimSpace(req.ReasoningEffort) != ""
	reasoningEmitted := false

	for eventsCh != nil || doneCh != nil {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-eventsCh:
			if !ok {
				eventsCh = nil
				continue
			}
			switch event.Type {
			case service.StreamEventThinking:
				if reasoningEnabled && strings.TrimSpace(event.Delta) != "" {
					pendingThought = append(pendingThought, event.Delta)
				}
			default:
				for _, delta := range filter.Push(event.Delta) {
					if delta == "" {
						continue
					}
					pendingText = append(pendingText, delta)
				}
			}
		case result, ok := <-doneCh:
			if !ok {
				doneCh = nil
				continue
			}
			final = result.Result
			finalErr = result.Err
			doneCh = nil
		}
	}

	if finalErr != nil {
		_ = emitter.Event("error", map[string]any{"type": "error", "error": map[string]any{"type": "api_error", "message": finalErr.Error()}})
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}
	if final == nil {
		_ = emitter.Event("error", map[string]any{"type": "error", "error": map[string]any{"type": "api_error", "message": "stream finished without a final result"}})
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}
	reasoningEnabled = shouldEmitResponsesReasoning(req, final)
	if reasoningEnabled && len(pendingThought) > 0 {
		reasoningText := strings.Join(pendingThought, "")
		if err := writeOpenAIResponseReasoning(emitter, "rs_"+responseID, 0, reasoningText); err != nil {
			return
		}
		reasoningEmitted = true
	}
	if len(final.ToolCalls) == 0 {
		pendingText = append(pendingText, filter.Flush()...)
		for _, delta := range pendingText {
			if delta == "" {
				continue
			}
			outputIndex := 0
			if reasoningEmitted {
				outputIndex = 1
			}
			if !messageStarted {
				if err := writeOpenAIResponseMessageStarted(emitter, messageID, outputIndex); err != nil {
					return
				}
				messageStarted = true
			}
			if err := emitter.Event("response.output_text.delta", map[string]any{
				"type":          "response.output_text.delta",
				"item_id":       messageID,
				"output_index":  outputIndex,
				"content_index": 0,
				"delta":         delta,
			}); err != nil {
				return
			}
		}
	}
	writeOpenAIResponseStreamCompleted(emitter, responseID, created, model, final, messageID, messageStarted, reasoningEmitted)
}

func shouldAggregateToolStream(req service.ChatRequest) bool {
	return len(req.Tools) > 0 && truthyEnv("LINGMA_AGGREGATE_TOOL_STREAM")
}

type toolStreamFilter struct {
	enabled bool
	buffer  string
	blocked bool
}

func newToolStreamFilter(enabled bool) *toolStreamFilter {
	return &toolStreamFilter{enabled: enabled}
}

func (f *toolStreamFilter) Push(delta string) []string {
	if delta == "" {
		return nil
	}
	if !f.enabled {
		return []string{delta}
	}
	f.buffer += delta
	if f.blocked {
		return nil
	}
	if idx := actionBlockStartIndex(f.buffer); idx >= 0 {
		safe := f.buffer[:idx]
		f.buffer = f.buffer[idx:]
		f.blocked = true
		if safe == "" {
			return nil
		}
		return []string{safe}
	}
	if looksLikeActionPrefix(f.buffer) {
		return nil
	}
	return f.flushSafeTail(96)
}

func (f *toolStreamFilter) Flush() []string {
	if f.buffer == "" || f.blocked {
		return nil
	}
	out := f.buffer
	f.buffer = ""
	return []string{out}
}

func (f *toolStreamFilter) flushSafeTail(tailRunes int) []string {
	runes := []rune(f.buffer)
	if len(runes) <= tailRunes {
		return nil
	}
	safe := string(runes[:len(runes)-tailRunes])
	f.buffer = string(runes[len(runes)-tailRunes:])
	if safe == "" {
		return nil
	}
	return []string{safe}
}

func actionBlockStartIndex(text string) int {
	lower := strings.ToLower(text)
	markers := []string{
		"```json action",
		"``` action",
		"{\"tool\"",
		"{\"name\"",
	}
	best := -1
	for _, marker := range markers {
		if idx := strings.Index(lower, marker); idx >= 0 && (best == -1 || idx < best) {
			best = idx
		}
	}
	return best
}

func looksLikeActionPrefix(text string) bool {
	trimmed := strings.ToLower(strings.TrimLeft(text, " \t\r\n"))
	if trimmed == "" {
		return true
	}
	prefixes := []string{
		"```json action",
		"``` action",
		"{\"tool\"",
		"{\"name\"",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(prefix, trimmed) || strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

func truthyEnv(name string) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func anthropicHostedWebSearchCall(req anthropicRequest) (toolemulation.ToolCall, bool) {
	if !hasAnthropicHostedWebSearchTool(req.Tools) {
		return toolemulation.ToolCall{}, false
	}
	if hasAnthropicToolResult(req.Messages) {
		return toolemulation.ToolCall{}, false
	}
	if !anthropicHostedWebSearchRequested(req.Tools, req.ToolChoice) {
		return toolemulation.ToolCall{}, false
	}

	query := anthropicHostedWebSearchQuery(req.Messages)
	if query == "" {
		return toolemulation.ToolCall{}, false
	}
	return toolemulation.ToolCall{
		ID:        fmt.Sprintf("toolu_%d", time.Now().UnixNano()),
		Name:      "web_search",
		Arguments: map[string]any{"query": query},
	}, true
}

func hasAnthropicToolResult(messages []rawMessage) bool {
	for _, message := range messages {
		items, ok := message.Content.([]any)
		if !ok {
			continue
		}
		for _, item := range items {
			m, ok := item.(map[string]any)
			if ok && stringFromAny(m["type"]) == "tool_result" {
				return true
			}
		}
	}
	return false
}

func estimateAnthropicInputTokens(req anthropicRequest) int {
	payload := map[string]any{
		"model":       req.Model,
		"system":      req.System,
		"messages":    req.Messages,
		"tools":       req.Tools,
		"tool_choice": req.ToolChoice,
		"thinking":    req.Thinking,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return 1
	}
	runes := len([]rune(string(raw)))
	if runes == 0 {
		return 1
	}
	tokens := (runes + 2) / 3
	if tokens < 1 {
		return 1
	}
	return tokens
}

func hasAnthropicHostedWebSearchTool(raw any) bool {
	items, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(stringFromAny(m["name"])) == "web_search" &&
			toolemulation.IsAnthropicHostedToolType(stringFromAny(m["type"])) {
			return true
		}
	}
	return false
}

func anthropicHostedWebSearchRequested(tools any, choice any) bool {
	if m, ok := choice.(map[string]any); ok {
		if strings.TrimSpace(stringFromAny(m["name"])) == "web_search" {
			return true
		}
	}

	items, ok := tools.([]any)
	if !ok || len(items) != 1 {
		return false
	}
	m, ok := items[0].(map[string]any)
	if !ok {
		return false
	}
	return strings.TrimSpace(stringFromAny(m["name"])) == "web_search" &&
		toolemulation.IsAnthropicHostedToolType(stringFromAny(m["type"]))
}

func anthropicHostedWebSearchQuery(messages []rawMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.ToLower(strings.TrimSpace(messages[i].Role)) != "user" {
			continue
		}
		text := strings.TrimSpace(extractText(messages[i].Content))
		if text == "" {
			continue
		}
		return cleanHostedWebSearchQuery(text)
	}
	return ""
}

func cleanHostedWebSearchQuery(text string) string {
	text = strings.TrimSpace(text)
	prefixes := []string{
		"Perform a web search for the query:",
		"Search the web for:",
		"Web search query:",
	}
	lower := strings.ToLower(text)
	for _, prefix := range prefixes {
		idx := strings.Index(lower, strings.ToLower(prefix))
		if idx >= 0 {
			text = strings.TrimSpace(text[idx+len(prefix):])
			break
		}
	}
	text = strings.Trim(text, " \t\r\n\"'`")
	return text
}

func normalizeAnthropicRequest(req anthropicRequest) (service.ChatRequest, error) {
	messages := make([]service.ChatMessage, 0, len(req.Messages))
	for _, message := range req.Messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		switch role {
		case "user":
			text, toolResults := extractAnthropicUserContent(message.Content)
			images := extractAnthropicImages(message.Content)
			if text != "" || len(images) > 0 {
				messages = append(messages, service.ChatMessage{Role: role, Text: text, Images: images})
			}
			for _, tr := range toolResults {
				if strings.TrimSpace(tr.Content) != "" {
					messages = append(messages, service.ChatMessage{Role: "tool", Text: tr.Content, ToolCallID: tr.ToolUseID})
				}
			}
		case "assistant":
			text, calls := extractAnthropicAssistantContent(message.Content)
			if text != "" || len(calls) > 0 {
				messages = append(messages, service.ChatMessage{Role: role, Text: text, ToolCalls: calls})
			}
		}
	}
	if len(messages) == 0 {
		return service.ChatRequest{}, fmt.Errorf("no user or assistant messages found")
	}

	toolChoice := toolemulation.ToolChoice{Mode: "auto"}
	if req.ToolChoice != nil {
		toolChoice = toolemulation.ExtractAnthropicToolChoice(req.ToolChoice)
	}

	return service.ChatRequest{
		Model:           strings.TrimSpace(req.Model),
		System:          strings.TrimSpace(extractText(req.System)),
		Messages:        messages,
		Tools:           toolemulation.ExtractAnthropicTools(req.Tools),
		ToolChoice:      toolChoice,
		Temperature:     req.Temperature,
		TopP:            req.TopP,
		TopK:            req.TopK,
		Stop:            req.StopSequences,
		MaxTokens:       req.MaxTokens,
		ReasoningEffort: extractAnthropicReasoningEffort(req.Thinking),
	}, nil
}

func normalizeOpenAIRequest(req openAIChatRequest) (service.ChatRequest, error) {
	messages := make([]service.ChatMessage, 0, len(req.Messages))
	systemParts := make([]string, 0, 2)
	for _, message := range req.Messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		switch role {
		case "system", "developer":
			text := strings.TrimSpace(extractText(message.Content))
			if text != "" {
				systemParts = append(systemParts, text)
			}
		case "user":
			text := strings.TrimSpace(extractText(message.Content))
			images := extractOpenAIImages(message.Content)
			if text != "" || len(images) > 0 {
				messages = append(messages, service.ChatMessage{Role: role, Text: text, Images: images})
			}
		case "assistant":
			text := strings.TrimSpace(extractText(message.Content))
			calls := extractOpenAIToolCalls(message.ToolCalls)
			if text != "" || len(calls) > 0 {
				messages = append(messages, service.ChatMessage{Role: role, Text: text, ToolCalls: calls})
			}
		case "tool":
			output := strings.TrimSpace(extractText(message.Content))
			if output == "" || message.ToolCallID == "" {
				continue
			}
			messages = append(messages, service.ChatMessage{Role: "tool", Text: output, ToolCallID: message.ToolCallID})
		}
	}
	if len(messages) == 0 {
		return service.ChatRequest{}, fmt.Errorf("no user or assistant messages found")
	}
	return service.ChatRequest{
		Model:             strings.TrimSpace(req.Model),
		System:            strings.Join(systemParts, "\n\n"),
		Messages:          messages,
		Tools:             toolemulation.ExtractTools(req.Tools),
		ToolChoice:        toolemulation.ExtractToolChoice(req.ToolChoice),
		ParallelToolCalls: req.ParallelToolCalls,
		Temperature:       req.Temperature,
		TopP:              req.TopP,
		Stop:              extractStop(req.Stop),
		PresencePenalty:   req.PresencePenalty,
		FrequencyPenalty:  req.FrequencyPenalty,
		MaxTokens:         maxTokens(req.MaxTokens, req.MaxCompletionTokens),
		Seed:              req.Seed,
		User:              req.User,
		ReasoningEffort:   req.ReasoningEffort,
		ResponseFormat:    extractResponseFormat(req.ResponseFormat),
	}, nil
}

func extractStop(stop any) []string {
	if stop == nil {
		return nil
	}
	switch typed := stop.(type) {
	case string:
		if typed != "" {
			return []string{typed}
		}
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s := stringFromAny(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return typed
	}
	return nil
}

func extractResponseFormat(rf any) string {
	if rf == nil {
		return ""
	}
	m, ok := rf.(map[string]any)
	if !ok {
		return ""
	}
	if format, ok := m["format"].(map[string]any); ok {
		return stringFromAny(format["type"])
	}
	return stringFromAny(m["type"])
}

func responsesRequestToChatRequest(req openAIResponsesRequest) (openAIChatRequest, error) {
	messages, err := responsesInputToMessages(req.Input)
	if err != nil {
		return openAIChatRequest{}, err
	}
	if instructions := strings.TrimSpace(req.Instructions); instructions != "" {
		messages = append([]rawMessage{{Role: "system", Content: instructions}}, messages...)
	}
	return openAIChatRequest{
		Model:               strings.TrimSpace(req.Model),
		Messages:            messages,
		Stream:              req.Stream,
		MaxTokens:           req.MaxTokens,
		MaxCompletionTokens: req.MaxOutputTokens,
		Tools:               req.Tools,
		ToolChoice:          req.ToolChoice,
		ParallelToolCalls:   req.ParallelToolCalls,
		Temperature:         req.Temperature,
		TopP:                req.TopP,
		Stop:                req.Stop,
		User:                req.User,
		ReasoningEffort:     extractReasoningEffort(req.Reasoning),
		ResponseFormat:      req.Text,
	}, nil
}

func responsesInputToMessages(input any) ([]rawMessage, error) {
	switch typed := input.(type) {
	case nil:
		return nil, fmt.Errorf("input is required")
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil, fmt.Errorf("input is required")
		}
		return []rawMessage{{Role: "user", Content: typed}}, nil
	case []any:
		messages := make([]rawMessage, 0, len(typed))
		for _, item := range typed {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			switch stringFromAny(m["type"]) {
			case "function_call":
				if toolCall := responsesFunctionCallToRawMessage(m); toolCall != nil {
					messages = append(messages, *toolCall)
				}
				continue
			case "function_call_output":
				if toolResult := responsesFunctionCallOutputToRawMessage(m); toolResult != nil {
					messages = append(messages, *toolResult)
				}
				continue
			}
			role := stringFromAny(m["role"])
			if role == "" {
				role = "user"
			}
			content := normalizeResponsesContent(m["content"])
			if text := strings.TrimSpace(extractText(content)); text == "" && len(extractOpenAIImages(content)) == 0 {
				continue
			}
			messages = append(messages, rawMessage{Role: role, Content: content})
		}
		if len(messages) == 0 {
			return nil, fmt.Errorf("no usable input messages found")
		}
		return messages, nil
	case map[string]any:
		switch stringFromAny(typed["type"]) {
		case "function_call":
			if toolCall := responsesFunctionCallToRawMessage(typed); toolCall != nil {
				return []rawMessage{*toolCall}, nil
			}
		case "function_call_output":
			if toolResult := responsesFunctionCallOutputToRawMessage(typed); toolResult != nil {
				return []rawMessage{*toolResult}, nil
			}
		}
		role := stringFromAny(typed["role"])
		if role == "" {
			role = "user"
		}
		if content := normalizeResponsesContent(typed); strings.TrimSpace(extractText(content)) != "" || len(extractOpenAIImages(content)) > 0 {
			return []rawMessage{{Role: role, Content: content}}, nil
		}
	}
	return nil, fmt.Errorf("unsupported input format")
}

func responsesFunctionCallToRawMessage(item map[string]any) *rawMessage {
	name := strings.TrimSpace(stringFromAny(item["name"]))
	if name == "" {
		return nil
	}
	callID := strings.TrimSpace(stringFromAny(item["call_id"]))
	if callID == "" {
		callID = strings.TrimSpace(stringFromAny(item["id"]))
	}
	if callID == "" {
		return nil
	}
	arguments := strings.TrimSpace(stringFromAny(item["arguments"]))
	return &rawMessage{
		Role: "assistant",
		ToolCalls: []any{
			map[string]any{
				"id":   callID,
				"type": "function",
				"function": map[string]any{
					"name":      name,
					"arguments": arguments,
				},
			},
		},
	}
}

func responsesFunctionCallOutputToRawMessage(item map[string]any) *rawMessage {
	callID := strings.TrimSpace(stringFromAny(item["call_id"]))
	output := strings.TrimSpace(stringFromAny(item["output"]))
	if callID == "" || output == "" {
		return nil
	}
	return &rawMessage{
		Role:       "tool",
		Content:    output,
		ToolCallID: callID,
	}
}

func normalizeResponsesContent(content any) any {
	switch typed := content.(type) {
	case nil:
		return nil
	case string:
		return typed
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			switch stringFromAny(m["type"]) {
			case "input_text", "output_text", "text":
				text := stringFromAny(m["text"])
				if text == "" {
					text = stringFromAny(m["input_text"])
				}
				out = append(out, map[string]any{"type": "text", "text": text})
			case "input_image", "image_url":
				url := stringFromAny(m["image_url"])
				if nested, ok := m["image_url"].(map[string]any); ok {
					url = stringFromAny(nested["url"])
				}
				if url != "" {
					out = append(out, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
				}
			}
		}
		if len(out) == 0 {
			return content
		}
		return out
	case map[string]any:
		if role := stringFromAny(typed["role"]); role != "" {
			return normalizeResponsesContent(typed["content"])
		}
		if text := stringFromAny(typed["text"]); text != "" {
			return text
		}
	}
	return content
}

func extractReasoningEffort(reasoning any) string {
	m, ok := reasoning.(map[string]any)
	if !ok {
		return ""
	}
	return stringFromAny(m["effort"])
}

func extractAnthropicReasoningEffort(thinking any) string {
	m, ok := thinking.(map[string]any)
	if !ok || len(m) == 0 {
		return ""
	}
	if effort := strings.TrimSpace(stringFromAny(m["effort"])); effort != "" {
		return effort
	}
	mode := strings.ToLower(strings.TrimSpace(stringFromAny(m["type"])))
	switch mode {
	case "", "enabled", "adaptive":
		// treat adaptive as an enabled reasoning request with default effort
	default:
		return ""
	}
	budget := parseReasoningBudget(m["budget_tokens"])
	switch {
	case budget >= 4096:
		return "high"
	case budget > 0 && budget < 1024:
		return "low"
	default:
		return "medium"
	}
}

func parseReasoningBudget(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		if n, err := typed.Int64(); err == nil {
			return int(n)
		}
	case string:
		if typed == "" {
			return 0
		}
		if n, err := strconv.Atoi(typed); err == nil {
			return n
		}
	}
	return 0
}

func maxTokens(a, b int) int {
	if b > 0 {
		return b
	}
	return a
}

func extractText(content any) string {
	switch typed := content.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			text := extractText(item)
			if text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		if text := stringFromAny(typed["text"]); text != "" {
			return text
		}
		if text := stringFromAny(typed["input_text"]); text != "" {
			return text
		}
		if nested := extractText(typed["content"]); nested != "" {
			return nested
		}
		return ""
	default:
		return ""
	}
}

func stringFromAny(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

func decodeJSON(r *http.Request, out any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeAnthropicError(w http.ResponseWriter, status int, kind string, message string) {
	writeJSON(w, status, map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    kind,
			"message": message,
		},
	})
}

func writeOpenAIError(w http.ResponseWriter, status int, kind string, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    kind,
			"code":    nil,
			"param":   nil,
		},
	})
}

func writeOpenAIChatCompletion(w http.ResponseWriter, result *service.ChatResult) {
	created := time.Now().Unix()
	message := map[string]any{
		"role":    "assistant",
		"content": result.Text,
	}
	finishReason := "stop"
	if len(result.ToolCalls) > 0 {
		toolCalls := make([]map[string]any, 0, len(result.ToolCalls))
		for _, tc := range result.ToolCalls {
			argsJSON, _ := json.Marshal(tc.Arguments)
			toolCalls = append(toolCalls, map[string]any{
				"id":   tc.ID,
				"type": "function",
				"function": map[string]any{
					"name":      tc.Name,
					"arguments": string(argsJSON),
				},
			})
		}
		message["tool_calls"] = toolCalls
		finishReason = "tool_calls"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion",
		"created": created,
		"model":   result.Model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       message,
			"finish_reason": finishReason,
		}},
		"usage": map[string]any{
			"prompt_tokens":     result.InputTokens,
			"completion_tokens": result.OutputTokens,
			"total_tokens":      result.InputTokens + result.OutputTokens,
		},
	})
}

func writeOpenAIResponse(w http.ResponseWriter, result *service.ChatResult) {
	responseID := fmt.Sprintf("resp_%d", time.Now().UnixNano())
	created := time.Now().Unix()
	writeJSON(w, http.StatusOK, buildOpenAIResponseBody(responseID, created, result.Model, result, "", strings.TrimSpace(result.ThoughtText) != ""))
}

func buildOpenAIResponseMessageItem(messageID string, text string, status string) map[string]any {
	item := map[string]any{
		"id":      messageID,
		"type":    "message",
		"status":  status,
		"role":    "assistant",
		"content": []map[string]any{},
	}
	if strings.TrimSpace(text) != "" {
		item["content"] = []map[string]any{{
			"type": "output_text",
			"text": text,
		}}
	}
	return item
}

func responseTextForResponses(result *service.ChatResult) string {
	if result == nil {
		return ""
	}
	if len(result.ToolCalls) > 0 {
		return ""
	}
	return strings.TrimSpace(result.Text)
}

func buildOpenAIResponseBody(responseID string, created int64, model string, result *service.ChatResult, messageID string, includeReasoning bool) map[string]any {
	output := make([]map[string]any, 0, 1+len(result.ToolCalls))
	if includeReasoning && strings.TrimSpace(result.ThoughtText) != "" {
		output = append(output, buildOpenAIResponseReasoningItem("rs_"+responseID, result.ThoughtText, "completed"))
	}
	text := responseTextForResponses(result)
	if text != "" {
		if strings.TrimSpace(messageID) == "" {
			messageID = fmt.Sprintf("msg_%d", time.Now().UnixNano())
		}
		output = append(output, buildOpenAIResponseMessageItem(messageID, text, "completed"))
	}
	for _, tc := range result.ToolCalls {
		argsJSON, _ := json.Marshal(tc.Arguments)
		output = append(output, map[string]any{
			"type":      "function_call",
			"id":        tc.ID,
			"call_id":   tc.ID,
			"name":      tc.Name,
			"arguments": string(argsJSON),
			"status":    "completed",
		})
	}
	if strings.TrimSpace(model) == "" {
		model = result.Model
	}
	return map[string]any{
		"id":          responseID,
		"object":      "response",
		"created_at":  created,
		"status":      "completed",
		"model":       model,
		"output":      output,
		"output_text": text,
		"usage": map[string]any{
			"input_tokens":  result.InputTokens,
			"output_tokens": result.OutputTokens,
			"total_tokens":  result.InputTokens + result.OutputTokens,
		},
	}
}

type openAIResponseStreamEmitter struct {
	w          http.ResponseWriter
	flusher    http.Flusher
	responseID string
	sequence   int
}

func newOpenAIResponseStreamEmitter(w http.ResponseWriter, flusher http.Flusher, responseID string) *openAIResponseStreamEmitter {
	return &openAIResponseStreamEmitter{w: w, flusher: flusher, responseID: responseID}
}

func (e *openAIResponseStreamEmitter) Event(name string, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	if _, ok := payload["sequence_number"]; !ok {
		payload["sequence_number"] = e.sequence
		e.sequence++
	}
	if _, ok := payload["response_id"]; !ok && e.responseID != "" {
		payload["response_id"] = e.responseID
	}
	return writeSSEEvent(e.w, e.flusher, name, payload)
}

func writeOpenAIResponseMessageStarted(emitter *openAIResponseStreamEmitter, messageID string, outputIndex int) error {
	if err := emitter.Event("response.output_item.added", map[string]any{
		"type":         "response.output_item.added",
		"output_index": outputIndex,
		"item":         buildOpenAIResponseMessageItem(messageID, "", "in_progress"),
	}); err != nil {
		return err
	}
	return emitter.Event("response.content_part.added", map[string]any{
		"type":          "response.content_part.added",
		"item_id":       messageID,
		"output_index":  outputIndex,
		"content_index": 0,
		"part": map[string]any{
			"type": "output_text",
			"text": "",
		},
	})
}

func buildOpenAIResponseReasoningItem(itemID string, reasoningText string, status string) map[string]any {
	return map[string]any{
		"id":     itemID,
		"type":   "reasoning",
		"status": status,
		"summary": []map[string]any{{
			"type": "summary_text",
			"text": reasoningText,
		}},
	}
}

func writeOpenAIResponseReasoning(emitter *openAIResponseStreamEmitter, itemID string, outputIndex int, reasoningText string) error {
	if err := emitter.Event("response.output_item.added", map[string]any{
		"type":         "response.output_item.added",
		"output_index": outputIndex,
		"item":         buildOpenAIResponseReasoningItem(itemID, "", "in_progress"),
	}); err != nil {
		return err
	}
	if err := emitter.Event("response.reasoning_summary_part.added", map[string]any{
		"type":          "response.reasoning_summary_part.added",
		"item_id":       itemID,
		"output_index":  outputIndex,
		"summary_index": 0,
		"part": map[string]any{
			"type": "summary_text",
			"text": "",
		},
	}); err != nil {
		return err
	}
	if err := emitter.Event("response.reasoning_summary_text.delta", map[string]any{
		"type":          "response.reasoning_summary_text.delta",
		"item_id":       itemID,
		"output_index":  outputIndex,
		"summary_index": 0,
		"delta":         reasoningText,
	}); err != nil {
		return err
	}
	if err := emitter.Event("response.reasoning_summary_text.done", map[string]any{
		"type":          "response.reasoning_summary_text.done",
		"item_id":       itemID,
		"output_index":  outputIndex,
		"summary_index": 0,
		"text":          reasoningText,
	}); err != nil {
		return err
	}
	if err := emitter.Event("response.reasoning_summary_part.done", map[string]any{
		"type":          "response.reasoning_summary_part.done",
		"item_id":       itemID,
		"output_index":  outputIndex,
		"summary_index": 0,
		"part": map[string]any{
			"type": "summary_text",
			"text": reasoningText,
		},
	}); err != nil {
		return err
	}
	return emitter.Event("response.output_item.done", map[string]any{
		"type":         "response.output_item.done",
		"output_index": outputIndex,
		"item":         buildOpenAIResponseReasoningItem(itemID, reasoningText, "completed"),
	})
}

func writeOpenAIResponseStreamCompleted(emitter *openAIResponseStreamEmitter, responseID string, created int64, model string, result *service.ChatResult, messageID string, messageStarted bool, reasoningEmitted bool) {
	outputIndex := 0
	if reasoningEmitted {
		outputIndex++
	}
	text := responseTextForResponses(result)
	if text != "" {
		if !messageStarted {
			_ = writeOpenAIResponseMessageStarted(emitter, messageID, outputIndex)
		}
		_ = emitter.Event("response.content_part.done", map[string]any{
			"type":          "response.content_part.done",
			"item_id":       messageID,
			"output_index":  outputIndex,
			"content_index": 0,
			"part": map[string]any{
				"type": "output_text",
				"text": text,
			},
		})
		_ = emitter.Event("response.output_text.done", map[string]any{
			"type":          "response.output_text.done",
			"item_id":       messageID,
			"output_index":  outputIndex,
			"content_index": 0,
			"text":          text,
		})
		_ = emitter.Event("response.output_item.done", map[string]any{
			"type":         "response.output_item.done",
			"output_index": outputIndex,
			"item":         buildOpenAIResponseMessageItem(messageID, text, "completed"),
		})
		outputIndex++
	}
	for _, tc := range result.ToolCalls {
		argsJSON, _ := json.Marshal(tc.Arguments)
		argsText := string(argsJSON)
		_ = emitter.Event("response.output_item.added", map[string]any{
			"type":         "response.output_item.added",
			"output_index": outputIndex,
			"item": map[string]any{
				"type":      "function_call",
				"id":        tc.ID,
				"call_id":   tc.ID,
				"name":      tc.Name,
				"arguments": "",
				"status":    "in_progress",
			},
		})
		_ = emitter.Event("response.function_call_arguments.delta", map[string]any{
			"type":         "response.function_call_arguments.delta",
			"item_id":      tc.ID,
			"output_index": outputIndex,
			"delta":        argsText,
		})
		_ = emitter.Event("response.function_call_arguments.done", map[string]any{
			"type":         "response.function_call_arguments.done",
			"item_id":      tc.ID,
			"output_index": outputIndex,
			"name":         tc.Name,
			"arguments":    argsText,
		})
		_ = emitter.Event("response.output_item.done", map[string]any{
			"type":         "response.output_item.done",
			"output_index": outputIndex,
			"item": map[string]any{
				"type":      "function_call",
				"id":        tc.ID,
				"call_id":   tc.ID,
				"name":      tc.Name,
				"arguments": argsText,
				"status":    "completed",
			},
		})
		outputIndex++
	}
	_ = emitter.Event("response.completed", map[string]any{
		"type":     "response.completed",
		"response": buildOpenAIResponseBody(responseID, created, model, result, messageID, reasoningEmitted),
	})
	_, _ = fmt.Fprint(emitter.w, "data: [DONE]\n\n")
	emitter.flusher.Flush()
}

func shouldEmitAnthropicThinking(req service.ChatRequest, result *service.ChatResult) bool {
	return strings.TrimSpace(req.ReasoningEffort) != "" && result != nil && strings.TrimSpace(result.ThoughtText) != ""
}

func shouldEmitResponsesReasoning(req service.ChatRequest, result *service.ChatResult) bool {
	if strings.TrimSpace(req.ReasoningEffort) == "" {
		return false
	}
	if result == nil {
		return true
	}
	return strings.TrimSpace(result.ThoughtText) != ""
}

func streamingHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
}

func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, event string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", body); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func writeOpenAIChunk(w http.ResponseWriter, flusher http.Flusher, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", body); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

type recordingResponseWriter struct {
	http.ResponseWriter
	statusCode int
	body       []byte
	wrote      bool
}

func (rw *recordingResponseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.wrote = true
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *recordingResponseWriter) Write(b []byte) (int, error) {
	if !rw.wrote {
		rw.WriteHeader(http.StatusOK)
	}
	rw.body = append(rw.body, b...)
	return rw.ResponseWriter.Write(b)
}

func (rw *recordingResponseWriter) Flush() {
	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (s *Server) withRecorder(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isDebugInspectionPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()

		// Read request body for recording, then restore for downstream handler
		var reqBody string
		if r.Body != nil && r.Body != http.NoBody {
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			reqBody = sanitizeRecordedBody(body)
		}

		rw := &recordingResponseWriter{ResponseWriter: w, statusCode: 200}
		next.ServeHTTP(rw, r)
		duration := time.Since(start)

		respBody := sanitizeRecordedBody(rw.body)

		s.recordRequest(r.Method, r.URL.Path, rw.statusCode, duration, reqBody, respBody)
		if s.OnRequest != nil {
			go s.OnRequest(r.Method, r.URL.Path, rw.statusCode, duration, reqBody, respBody)
		}
	})
}

func isDebugInspectionPath(path string) bool {
	switch path {
	case "/debug/requests", "/debug/logs", "/api/requests", "/api/logs", "/debug/access-logs", "/api/access-logs":
		return true
	default:
		return false
	}
}

func (s *Server) recordRequest(method, path string, statusCode int, duration time.Duration, reqBody, respBody string) {
	s.recMu.Lock()
	defer s.recMu.Unlock()

	s.records = append(s.records, debugRequestRecord{
		Time:       time.Now().Format(time.RFC3339),
		Method:     method,
		Path:       path,
		StatusCode: statusCode,
		DurationMS: duration.Milliseconds(),
		Request:    reqBody,
		Response:   respBody,
	})
	if len(s.records) > 200 {
		s.records = s.records[len(s.records)-200:]
	}
	s.logs = append(s.logs, debugLogRecord{
		Time:    time.Now().Format(time.RFC3339),
		Level:   debugLogLevel(statusCode),
		Message: fmt.Sprintf("%s %s -> %d (%dms)", method, path, statusCode, duration.Milliseconds()),
	})
	if len(s.logs) > 200 {
		s.logs = s.logs[len(s.logs)-200:]
	}
}

func (s *Server) debugRecords(limit int) []debugRequestRecord {
	s.recMu.RLock()
	defer s.recMu.RUnlock()

	if limit > len(s.records) {
		limit = len(s.records)
	}
	out := make([]debugRequestRecord, 0, limit)
	for i := len(s.records) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, s.records[i])
	}
	return out
}

func (s *Server) debugLogs(limit int) []debugLogRecord {
	s.recMu.RLock()
	defer s.recMu.RUnlock()

	if limit > len(s.logs) {
		limit = len(s.logs)
	}
	out := make([]debugLogRecord, 0, limit)
	for i := len(s.logs) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, s.logs[i])
	}
	return out
}

func debugLogLevel(statusCode int) string {
	switch {
	case statusCode >= 500:
		return "error"
	case statusCode >= 400:
		return "warn"
	default:
		return "info"
	}
}

func sanitizeRecordedBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return truncateRecordedString(string(body))
	}
	return truncateRecordedString(string(mustMarshalJSON(redactRecordedValue(value))))
}

func redactRecordedValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			lower := strings.ToLower(k)
			if lower == "data" || lower == "url" {
				if s := stringFromAny(v); looksLikeImagePayload(s) {
					out[k] = imageRedaction(s)
					continue
				}
			}
			out[k] = redactRecordedValue(v)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, redactRecordedValue(item))
		}
		return out
	case string:
		if looksLikeImagePayload(typed) {
			return imageRedaction(typed)
		}
		return typed
	default:
		return typed
	}
}

func looksLikeImagePayload(value string) bool {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "data:image/") {
		return true
	}
	if len(value) > 4096 && isLikelyBase64(value) {
		return true
	}
	return false
}

func imageRedaction(value string) string {
	return fmt.Sprintf("[image payload redacted, %d chars]", len(value))
}

func isLikelyBase64(value string) bool {
	for _, r := range value {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '+' || r == '/' || r == '=' || r == '\n' || r == '\r' {
			continue
		}
		return false
	}
	return true
}

func mustMarshalJSON(value any) []byte {
	body, err := json.Marshal(value)
	if err != nil {
		return []byte("{}")
	}
	return body
}

func truncateRecordedString(value string) string {
	return value
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, x-api-key, anthropic-version")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func maxConcurrentRequests() int {
	raw := strings.TrimSpace(os.Getenv("LINGMA_PROXY_MAX_CONCURRENT"))
	if raw == "" {
		return 4
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 4
	}
	if n > 16 {
		return 16
	}
	return n
}

func (s *Server) acquire(ctx context.Context) bool {
	select {
	case s.sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Server) release() {
	select {
	case <-s.sem:
	default:
	}
}

func extractOpenAIToolCalls(raw []any) []toolemulation.ToolCall {
	if len(raw) == 0 {
		return nil
	}
	out := make([]toolemulation.ToolCall, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := stringFromAny(m["id"])
		fn, ok := m["function"].(map[string]any)
		if !ok {
			continue
		}
		name := stringFromAny(fn["name"])
		if name == "" {
			continue
		}
		argsRaw := stringFromAny(fn["arguments"])
		var args map[string]any
		if argsRaw != "" {
			_ = json.Unmarshal([]byte(argsRaw), &args)
		}
		out = append(out, toolemulation.ToolCall{
			ID:        id,
			Name:      name,
			Arguments: args,
		})
	}
	return out
}

type anthropicToolResult struct {
	ToolUseID string
	Content   string
}

func extractAnthropicUserContent(content any) (string, []anthropicToolResult) {
	items, ok := content.([]any)
	if !ok {
		return extractText(content), nil
	}
	var results []anthropicToolResult
	var textParts []string
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		switch stringFromAny(m["type"]) {
		case "text":
			if t := stringFromAny(m["text"]); t != "" {
				textParts = append(textParts, t)
			}
		case "thinking", "redacted_thinking":
			// Skip thinking blocks in user messages
			continue
		case "tool_result":
			toolUseID := stringFromAny(m["tool_use_id"])
			resultText := extractText(m["content"])
			if resultText != "" {
				results = append(results, anthropicToolResult{
					ToolUseID: toolUseID,
					Content:   resultText,
				})
			}
		}
	}
	text := ""
	if len(textParts) > 0 {
		text = strings.Join(textParts, "\n")
	}
	return text, results
}

func extractAnthropicAssistantContent(content any) (string, []toolemulation.ToolCall) {
	items, ok := content.([]any)
	if !ok {
		return extractText(content), nil
	}
	calls := make([]toolemulation.ToolCall, 0, len(items))
	var textParts []string
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		switch stringFromAny(m["type"]) {
		case "text":
			if t := stringFromAny(m["text"]); t != "" {
				textParts = append(textParts, t)
			}
		case "thinking", "redacted_thinking":
			// Skip thinking blocks — they are not part of the conversation text
			continue
		case "tool_use":
			id := stringFromAny(m["id"])
			name := stringFromAny(m["name"])
			if name == "" {
				continue
			}
			var args map[string]any
			if rawInput, ok := m["input"].(map[string]any); ok {
				args = rawInput
			} else if inputStr, ok := m["input"].(string); ok && inputStr != "" {
				if err := json.Unmarshal([]byte(inputStr), &args); err != nil {
					args = map[string]any{}
				}
			}
			calls = append(calls, toolemulation.ToolCall{
				ID:        id,
				Name:      name,
				Arguments: args,
			})
		}
	}
	text := ""
	if len(textParts) > 0 {
		text = strings.Join(textParts, "\n")
	}
	return text, calls
}

func extractOpenAIImages(content any) []service.Image {
	items, ok := content.([]any)
	if !ok {
		return nil
	}
	var images []service.Image
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if stringFromAny(m["type"]) != "image_url" {
			continue
		}
		imageURL, ok := m["image_url"].(map[string]any)
		if !ok {
			continue
		}
		url := stringFromAny(imageURL["url"])
		if url == "" {
			continue
		}
		img := parseImageURL(url)
		if img != nil {
			images = append(images, *img)
		}
	}
	return images
}

func extractAnthropicImages(content any) []service.Image {
	items, ok := content.([]any)
	if !ok {
		return nil
	}
	var images []service.Image
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if stringFromAny(m["type"]) != "image" {
			continue
		}
		source, ok := m["source"].(map[string]any)
		if !ok {
			continue
		}
		if stringFromAny(source["type"]) != "base64" {
			continue
		}
		mediaType := stringFromAny(source["media_type"])
		data := stringFromAny(source["data"])
		if data == "" {
			continue
		}
		images = append(images, service.Image{
			MediaType: mediaType,
			Data:      data,
		})
	}
	return images
}

func parseImageURL(url string) *service.Image {
	if strings.HasPrefix(url, "data:") {
		return normalizeImage(parseDataURL(url))
	}
	if img := parseLocalImagePath(url); img != nil {
		return normalizeImage(img)
	}
	img, err := fetchImageAsBase64(url)
	if err != nil {
		return nil
	}
	return normalizeImage(img)
}

func parseLocalImagePath(raw string) *service.Image {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	path := raw
	if strings.HasPrefix(raw, "file://") {
		u, err := url.Parse(raw)
		if err != nil {
			return nil
		}
		path = u.Path
	}
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		path = home + strings.TrimPrefix(path, "~")
	}
	if !strings.HasPrefix(path, "/") {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil
	}
	return &service.Image{
		MediaType: mediaTypeForImagePath(path),
		Data:      base64.StdEncoding.EncodeToString(data),
		URL:       raw,
	}
}

func mediaTypeForImagePath(path string) string {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".png"):
		return "image/png"
	case strings.HasSuffix(lower, ".gif"):
		return "image/gif"
	case strings.HasSuffix(lower, ".webp"):
		return "image/webp"
	case strings.HasSuffix(lower, ".bmp"):
		return "image/bmp"
	default:
		return "image/jpeg"
	}
}

func parseDataURL(url string) *service.Image {
	const prefix = "data:"
	if !strings.HasPrefix(url, prefix) {
		return nil
	}
	rest := url[len(prefix):]
	commaIdx := strings.Index(rest, ",")
	if commaIdx < 0 {
		return nil
	}
	meta := rest[:commaIdx]
	data := rest[commaIdx+1:]

	mediaType := ""
	if strings.HasSuffix(meta, ";base64") {
		mediaType = strings.TrimSuffix(meta, ";base64")
	} else {
		mediaType = meta
	}

	return &service.Image{
		MediaType: mediaType,
		Data:      data,
	}
}

func fetchImageAsBase64(url string) (*service.Image, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch image failed: %s", resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	mediaType := resp.Header.Get("Content-Type")
	if mediaType == "" {
		mediaType = "image/jpeg"
	} else {
		// Strip parameters like "image/png; charset=utf-8"
		if idx := strings.Index(mediaType, ";"); idx >= 0 {
			mediaType = strings.TrimSpace(mediaType[:idx])
		}
	}

	return &service.Image{
		MediaType: mediaType,
		Data:      base64.StdEncoding.EncodeToString(data),
	}, nil
}

func normalizeImage(img *service.Image) *service.Image {
	if img == nil || strings.TrimSpace(img.Data) == "" {
		return img
	}
	data, err := base64.StdEncoding.DecodeString(img.Data)
	if err != nil || len(data) == 0 {
		return img
	}
	const maxImageBytes = 2 * 1024 * 1024
	const maxImageSide = 1568
	if len(data) <= maxImageBytes {
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
			if cfg.Width <= maxImageSide && cfg.Height <= maxImageSide {
				return img
			}
		}
	}

	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return img
	}
	bounds := decoded.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	if width <= 0 || height <= 0 {
		return img
	}
	targetWidth, targetHeight := scaledDimensions(width, height, maxImageSide)
	dst := resizeNearest(decoded, targetWidth, targetHeight)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
		return img
	}
	img.MediaType = "image/jpeg"
	img.Data = base64.StdEncoding.EncodeToString(buf.Bytes())
	return img
}

func resizeNearest(src image.Image, width int, height int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	bounds := src.Bounds()
	srcWidth := bounds.Dx()
	srcHeight := bounds.Dy()
	for y := 0; y < height; y++ {
		sy := bounds.Min.Y + y*srcHeight/height
		for x := 0; x < width; x++ {
			sx := bounds.Min.X + x*srcWidth/width
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}

func scaledDimensions(width int, height int, maxSide int) (int, int) {
	if width <= maxSide && height <= maxSide {
		return width, height
	}
	if width >= height {
		scaledHeight := height * maxSide / width
		if scaledHeight < 1 {
			scaledHeight = 1
		}
		return maxSide, scaledHeight
	}
	scaledWidth := width * maxSide / height
	if scaledWidth < 1 {
		scaledWidth = 1
	}
	return scaledWidth, maxSide
}
