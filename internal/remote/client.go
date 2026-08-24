package remote

import (
	"bufio"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"lingma-ipc-proxy/internal/toolemulation"
)

const (
	DefaultBaseURL = "https://lingma.alibabacloud.com"
	chatPath       = "/algo/api/v2/service/pro/sse/agent_chat_generation"
	chatQuery      = "?FetchKeys=llm_model_result&AgentId=agent_common"
	modelListPath  = "/algo/api/v2/model/list"
	quotaPath      = "/api/v2/quota/usage"
)

var remoteBaseURLPattern = regexp.MustCompile(`https?://[^\s"'<>),\]}]+`)

type Config struct {
	BaseURL     string
	AuthFile    string
	ProxyURL    string
	CosyVersion string
	Timeout     time.Duration
}

type Client struct {
	cfg         Config
	client      *http.Client
	autoBaseURL bool
}

type BaseURLHint struct {
	URL    string
	Source string
}

type baseURLCacheFile struct {
	URL       string `json:"url"`
	UpdatedAt string `json:"updated_at"`
}

type Model struct {
	Key         string `json:"key"`
	DisplayName string `json:"display_name"`
	Model       string `json:"model"`
	Enable      bool   `json:"enable"`
}

type ChatRequest struct {
	Model           string
	Prompt          string
	Messages        []Message
	Images          []Image
	Stream          bool
	Temperature     *float64
	TopP            *float64
	TopK            int
	Stop            []string
	MaxTokens       int
	ReasoningEffort string
	Tools           []toolemulation.ToolDef
	ToolChoice      toolemulation.ToolChoice
}

type Image struct {
	MediaType string
	Data      string
	URL       string
}

type Message struct {
	Role          string
	Content       string
	Images        []Image
	Name          string
	ToolCallID    string
	ToolCalls     []toolemulation.ToolCall
	ReasoningText string
}

type ChatResult struct {
	Text              string
	ReasoningText     string
	InputTokens       int
	OutputTokens      int
	CachedInputTokens int
	ReasoningTokens   int
	TotalTokens       int
	Credits           float64
	OriginalCredits   float64
	FinishReason      string
	RequestID         string
	CredentialSrc     string
	ToolCalls         []toolemulation.ToolCall
}

// StreamEvent is a single streamed delta from the remote chat endpoint.
type StreamEvent struct {
	Kind     string // StreamKindText, StreamKindReasoning, or StreamKindToolCall
	Delta    string
	ToolCall *ToolCallDelta
}

// ToolCallDelta carries one incremental native tool-call fragment as it streams.
type ToolCallDelta struct {
	Index        int
	ID           string
	Name         string
	ArgsFragment string
}

const (
	StreamKindText      = "text"
	StreamKindReasoning = "reasoning"
	StreamKindToolCall  = "tool_call"
)

func New(cfg Config) *Client {
	autoBaseURL := strings.TrimSpace(cfg.BaseURL) == "" && strings.TrimSpace(os.Getenv("LINGMA_REMOTE_BASE_URL")) == ""
	if cfg.BaseURL == "" {
		cfg.BaseURL = ResolveBaseURL("")
	}
	if cfg.CosyVersion == "" {
		cfg.CosyVersion = "2.11.2"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	client := &http.Client{Timeout: cfg.Timeout}
	if transport, err := transportForProxy(cfg.ProxyURL); err == nil && transport != nil {
		client.Transport = transport
	}
	return &Client{cfg: cfg, client: client, autoBaseURL: autoBaseURL}
}

func ValidateProxyURL(value string) error {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("invalid remote proxy URL %q; expected http://, https://, or socks5:// URL", value)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "socks5":
		return nil
	default:
		return fmt.Errorf("invalid remote proxy URL scheme %q; expected http, https, or socks5", parsed.Scheme)
	}
}

func ProxySource(explicit string) (string, string) {
	if strings.TrimSpace(explicit) != "" {
		return strings.TrimSpace(explicit), "explicit config"
	}
	for _, key := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value, key
		}
	}
	return "", ""
}

func transportForProxy(proxyURL string) (*http.Transport, error) {
	raw := strings.TrimSpace(proxyURL)
	if raw == "" {
		return nil, nil
	}
	if err := ValidateProxyURL(raw); err != nil {
		return nil, err
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(parsed)
	return transport, nil
}

func ResolveBaseURL(explicit string) string {
	return ResolveBaseURLWithSource(explicit).URL
}

func ResolveBaseURLWithSource(explicit string) BaseURLHint {
	if strings.TrimSpace(explicit) != "" {
		return BaseURLHint{URL: strings.TrimRight(strings.TrimSpace(explicit), "/"), Source: "explicit config"}
	}
	if value := strings.TrimSpace(os.Getenv("LINGMA_REMOTE_BASE_URL")); value != "" {
		return BaseURLHint{URL: strings.TrimRight(value, "/"), Source: "LINGMA_REMOTE_BASE_URL"}
	}
	candidates := ResolveBaseURLCandidates()
	if len(candidates) > 0 {
		return candidates[0]
	}
	return BaseURLHint{URL: DefaultBaseURL, Source: "default"}
}

func ResolveBaseURLCandidates() []BaseURLHint {
	hints := make([]BaseURLHint, 0)
	for _, path := range candidateConfigFiles() {
		if value := readBaseURLHint(path); value != "" {
			hints = append(hints, BaseURLHint{URL: strings.TrimRight(value, "/"), Source: path})
		}
	}
	hints = sortBaseURLHints(uniqueBaseURLHints(hints))
	if cached := cachedBaseURLHint(); cached.URL != "" {
		hints = uniqueBaseURLHints(append([]BaseURLHint{cached}, hints...))
	}
	for _, hint := range hints {
		if hint.URL == DefaultBaseURL {
			return hints
		}
	}
	return append(hints, BaseURLHint{URL: DefaultBaseURL, Source: "default"})
}

func cachedBaseURLHint() BaseURLHint {
	path, err := baseURLCachePath()
	if err != nil {
		return BaseURLHint{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return BaseURLHint{}
	}
	var cache baseURLCacheFile
	if err := json.Unmarshal(data, &cache); err != nil {
		return BaseURLHint{}
	}
	url := normalizeRemoteBaseURLHint(cache.URL)
	if url == "" {
		return BaseURLHint{}
	}
	return BaseURLHint{URL: url, Source: "last successful remote domain"}
}

func cacheSuccessfulBaseURL(raw string) {
	url := normalizeRemoteBaseURLHint(raw)
	if url == "" {
		return
	}
	path, err := baseURLCachePath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return
	}
	data, err := json.MarshalIndent(baseURLCacheFile{URL: url, UpdatedAt: time.Now().Format(time.RFC3339)}, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
}

func baseURLCachePath() (string, error) {
	if dir, err := os.UserConfigDir(); err == nil && strings.TrimSpace(dir) != "" {
		return filepath.Join(dir, "lingma-ipc-proxy", "remote-base-url.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "lingma-ipc-proxy", "remote-base-url.json"), nil
}

// Quota is the account credit/usage snapshot from the openapi host.
type Quota struct {
	UserType   string  `json:"user_type,omitempty"`
	Unit       string  `json:"unit,omitempty"`
	Total      float64 `json:"total"`
	Used       float64 `json:"used"`
	Remaining  float64 `json:"remaining"`
	Percentage float64 `json:"percentage"`
	IsExceeded bool    `json:"is_exceeded"`
	ResetAtMS  int64   `json:"reset_at_ms,omitempty"`
	Source     string  `json:"source,omitempty"`
}

// FetchQuota reads account credit/usage from the QoderCN openapi host. It only
// needs the access token (Bearer); no cosy signature or machine headers.
func (c *Client) FetchQuota(ctx context.Context) (*Quota, error) {
	cred, err := LoadCredential(c.cfg.AuthFile)
	if err != nil {
		return nil, err
	}
	token := strings.TrimSpace(cred.AccessToken)
	if token == "" {
		return nil, fmt.Errorf("no access token in credentials; re-login QoderCN/Lingma to enable quota lookup")
	}
	base, err := openAPIBaseURL(c.cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+quotaPath, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("read quota response: %w", readErr)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("remote quota status %d: %s", resp.StatusCode, truncate(string(body), 300))
	}
	var parsed struct {
		UserType        string `json:"userType"`
		IsQuotaExceeded bool   `json:"isQuotaExceeded"`
		ExpiresAt       int64  `json:"expiresAt"`
		UserQuota       struct {
			Total      float64 `json:"total"`
			Used       float64 `json:"used"`
			Remaining  float64 `json:"remaining"`
			Percentage float64 `json:"percentage"`
			Unit       string  `json:"unit"`
		} `json:"userQuota"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse quota response: %w", err)
	}
	return &Quota{
		UserType:   parsed.UserType,
		Unit:       parsed.UserQuota.Unit,
		Total:      parsed.UserQuota.Total,
		Used:       parsed.UserQuota.Used,
		Remaining:  parsed.UserQuota.Remaining,
		Percentage: parsed.UserQuota.Percentage,
		IsExceeded: parsed.IsQuotaExceeded,
		ResetAtMS:  parsed.ExpiresAt,
		Source:     base + quotaPath,
	}, nil
}

// openAPIBaseURL derives the openapi host (quota / user APIs) from the chat base
// URL. Only QoderCN exposes this quota endpoint.
func openAPIBaseURL(chatBaseURL string) (string, error) {
	raw := strings.TrimSpace(chatBaseURL)
	if raw == "" {
		raw = DefaultBaseURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid remote base URL %q", chatBaseURL)
	}
	if u.Host == "qoder.com.cn" || strings.HasSuffix(u.Host, ".qoder.com.cn") {
		return "https://openapi.qoder.com.cn", nil
	}
	return "", fmt.Errorf("account quota is only available for QoderCN accounts (base host %q)", u.Host)
}

func (c *Client) Warmup(ctx context.Context) error {
	_, err := LoadCredential(c.cfg.AuthFile)
	if err != nil {
		return err
	}
	_, err = c.ListModels(ctx)
	return err
}

func (c *Client) ListModels(ctx context.Context) ([]Model, error) {
	models, err := c.listModels(ctx)
	if err == nil && c.autoBaseURL {
		cacheSuccessfulBaseURL(c.cfg.BaseURL)
	}
	if err == nil || !c.autoBaseURL || ctx.Err() != nil {
		return models, err
	}
	return c.listModelsWithAutoBaseURLFallback(ctx, err)
}

func (c *Client) listModels(ctx context.Context) ([]Model, error) {
	cred, err := LoadCredential(c.cfg.AuthFile)
	if err != nil {
		return nil, err
	}
	headers, err := c.headers(cred, modelListPath, "")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.BaseURL+modelListPath, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, c.modelListStatusError(resp.StatusCode, string(body))
	}
	var payload struct {
		Chat   []Model `json:"chat"`
		Inline []Model `json:"inline"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return append(payload.Chat, payload.Inline...), nil
}

func (c *Client) listModelsWithAutoBaseURLFallback(ctx context.Context, firstErr error) ([]Model, error) {
	candidates := ResolveBaseURLCandidates()
	tried := 1
	var lastErr error
	current := strings.TrimRight(c.cfg.BaseURL, "/")
	for _, candidate := range candidates {
		baseURL := strings.TrimRight(strings.TrimSpace(candidate.URL), "/")
		if baseURL == "" || baseURL == current {
			continue
		}
		tried++
		previous := c.cfg.BaseURL
		c.cfg.BaseURL = baseURL
		models, err := c.listModels(ctx)
		if err == nil {
			cacheSuccessfulBaseURL(c.cfg.BaseURL)
			return models, nil
		}
		lastErr = err
		c.cfg.BaseURL = previous
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	if lastErr != nil {
		return nil, fmt.Errorf("%w。自动探测已尝试 %d 个候选域名，均不可用；最后错误：%v", firstErr, tried, lastErr)
	}
	return nil, firstErr
}

func (c *Client) modelListStatusError(statusCode int, body string) error {
	message := fmt.Sprintf("remote model list status %d from %s: %s", statusCode, c.cfg.BaseURL, truncate(body, 500))
	if statusCode == http.StatusNotFound || strings.Contains(body, "NoSuchKey") {
		message += "。这通常表示远端 API 域名自动探测命中了错误地址，请到设置页手动填写 Lingma 官方或企业专属远端 API 域名；官方默认域名为 https://lingma.alibabacloud.com。"
	}
	return fmt.Errorf("%s", message)
}

func (c *Client) Chat(ctx context.Context, request ChatRequest, onDelta func(StreamEvent)) (*ChatResult, error) {
	cred, err := LoadCredential(c.cfg.AuthFile)
	if err != nil {
		return nil, err
	}
	requestID := newHexID()
	body, err := c.buildBody(requestID, request)
	if err != nil {
		return nil, err
	}
	headers, err := c.headers(cred, chatPath, body)
	if err != nil {
		return nil, err
	}
	if key := strings.TrimSpace(request.Model); key != "" {
		headers["X-Model-Key"] = key
		headers["X-Model-Source"] = "system"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+chatPath+chatQuery, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("remote chat status %d: %s", resp.StatusCode, truncate(string(respBody), 1000))
	}
	var builder strings.Builder
	var reasoningBuilder strings.Builder
	toolCallBuffer := newRemoteToolCallBuffer()
	var usage *remoteUsage
	finishReason := ""
	if err := scanSSE(resp.Body, func(event sseEvent) error {
		if event.Usage != nil {
			usage = event.Usage
		}
		if event.FinishReason != "" {
			finishReason = event.FinishReason
		}
		if event.Done {
			return nil
		}
		if len(event.ToolCalls) > 0 {
			toolCallBuffer.Add(event.ToolCalls)
			if onDelta != nil {
				for _, frag := range event.ToolCalls {
					onDelta(StreamEvent{Kind: StreamKindToolCall, ToolCall: &ToolCallDelta{
						Index:        frag.Index,
						ID:           frag.ID,
						Name:         frag.Name,
						ArgsFragment: frag.ArgumentsFragment,
					}})
				}
			}
		}
		if event.Reasoning != "" {
			reasoningBuilder.WriteString(event.Reasoning)
			if onDelta != nil {
				onDelta(StreamEvent{Kind: StreamKindReasoning, Delta: event.Reasoning})
			}
		}
		if event.Content != "" {
			builder.WriteString(event.Content)
			if onDelta != nil {
				onDelta(StreamEvent{Kind: StreamKindText, Delta: event.Content})
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	text := builder.String()
	result := &ChatResult{
		Text:          text,
		ReasoningText: reasoningBuilder.String(),
		InputTokens:   estimateTokens(request.Prompt),
		OutputTokens:  estimateTokens(text),
		RequestID:     requestID,
		CredentialSrc: cred.Source,
		ToolCalls:     toolCallBuffer.Calls(),
		FinishReason:  finishReason,
	}
	// Prefer the gateway's real usage numbers over local estimates when present.
	if usage != nil {
		if usage.PromptTokens > 0 {
			result.InputTokens = usage.PromptTokens
		}
		if usage.CompletionTokens > 0 {
			result.OutputTokens = usage.CompletionTokens
		}
		if usage.TotalTokens > 0 {
			result.TotalTokens = usage.TotalTokens
		} else {
			result.TotalTokens = result.InputTokens + result.OutputTokens
		}
		result.CachedInputTokens = usage.PromptTokensDetails.CachedTokens
		result.ReasoningTokens = usage.CompletionTokensDetails.ReasoningTokens
		result.Credits = usage.Credits
		result.OriginalCredits = usage.OriginalCredits
	}
	return result, nil
}

func (c *Client) buildBody(requestID string, request ChatRequest) (string, error) {
	temperature := 0.1
	if request.Temperature != nil {
		temperature = *request.Temperature
	}
	model := strings.TrimSpace(request.Model)
	if strings.EqualFold(model, "auto") {
		model = ""
	}
	hasImages := hasVisionImages(request.Images)
	payload := map[string]any{
		"request_id":     requestID,
		"request_set_id": newUUID(),
		"chat_record_id": requestID,
		"stream":         true,
		"chat_task":      "FREE_INPUT",
		// Images travel inline in the message content parts (OpenAI vision
		// format), matching the QoderCN CLI, which does not use image_urls.
		"image_urls":       nullableSlice([]string(nil)),
		"is_reply":         true,
		"is_retry":         false,
		"session_id":       "",
		"session_type":     "qoderclicn",
		"code_language":    "",
		"source":           1,
		"version":          "3",
		"chat_prompt":      "",
		"parameters":       buildGenerationParameters(request, temperature),
		"aliyun_user_type": "",
		"agent_id":         "agent_common",
		"task_id":          "common",
		"model_config": map[string]any{
			"key":              model,
			"display_name":     "",
			"model":            "",
			"format":           "openai",
			"is_vl":            hasImages,
			"is_reasoning":     remoteReasoningEnabled(request),
			"api_key":          "",
			"url":              "",
			"source":           "system",
			"max_input_tokens": 180000,
		},
		"messages": projectMessages(request),
		"business": map[string]any{
			"product":  "cli",
			"version":  c.cfg.CosyVersion,
			"type":     "agent",
			"id":       newUUID(),
			"begin_at": time.Now().UnixMilli(),
			"stage":    "start",
		},
	}
	if tools := projectTools(request.Tools); len(tools) > 0 {
		payload["tools"] = tools
	}
	if choice := projectToolChoice(request.ToolChoice); choice != nil {
		payload["tool_choice"] = choice
	}
	body, err := json.Marshal(payload)
	return string(body), err
}

// buildGenerationParameters assembles the upstream "parameters" object. The
// gateway honors temperature and the reasoning_effort/enable_thinking pair;
// max_tokens is sent for parity with the CLI but the gateway ignores it (the
// proxy enforces it downstream). top_p/top_k/stop are forwarded best-effort.
func buildGenerationParameters(request ChatRequest, temperature float64) map[string]any {
	params := map[string]any{"temperature": temperature}
	// Reasoning effort / thinking toggle. Unlike max_tokens (which the gateway
	// ignores), the QoderCN gateway honors these under parameters. We forward
	// reasoning_effort verbatim so callers can reach QoderCN-native levels
	// (none/low/medium/high/xhigh/max) beyond the OpenAI enum; the gateway
	// validates each value against the target model's thinking_config.
	switch effort := strings.ToLower(strings.TrimSpace(request.ReasoningEffort)); {
	case effort == "none" || effort == "off" || effort == "disabled":
		params["enable_thinking"] = false
		params["reasoning_effort"] = "none"
	case effort != "":
		params["enable_thinking"] = true
		params["reasoning_effort"] = strings.TrimSpace(request.ReasoningEffort)
	case remoteReasoningEnabled(request):
		// Model implies reasoning (e.g. a *-thinking variant) with no explicit
		// level; enable thinking and let the gateway pick its default effort.
		params["enable_thinking"] = true
	}
	if request.MaxTokens > 0 {
		params["max_tokens"] = request.MaxTokens
	}
	if request.TopP != nil {
		params["top_p"] = *request.TopP
	}
	if request.TopK > 0 {
		params["top_k"] = request.TopK
	}
	if len(request.Stop) > 0 {
		params["stop"] = request.Stop
	}
	return params
}

func remoteReasoningEnabled(request ChatRequest) bool {
	if strings.TrimSpace(request.ReasoningEffort) != "" {
		return true
	}
	model := strings.ToLower(strings.TrimSpace(request.Model))
	return strings.Contains(model, "thinking")
}

func nullableSlice[T any](items []T) any {
	if len(items) == 0 {
		return nil
	}
	return items
}

func hasVisionImages(images []Image) bool {
	for _, img := range images {
		if strings.TrimSpace(img.Data) != "" || strings.TrimSpace(img.URL) != "" {
			return true
		}
	}
	return false
}

func projectImage(img Image) string {
	if strings.TrimSpace(img.Data) == "" && strings.TrimSpace(img.URL) == "" {
		return ""
	}
	mediaType := strings.TrimSpace(img.MediaType)
	if mediaType == "" {
		mediaType = "image/jpeg"
	}
	if strings.TrimSpace(img.Data) != "" {
		return "data:" + mediaType + ";base64," + strings.TrimSpace(img.Data)
	}
	return strings.TrimSpace(img.URL)
}

func projectMessages(request ChatRequest) []map[string]any {
	source := request.Messages
	if len(source) == 0 {
		source = []Message{{Role: "user", Content: request.Prompt}}
	}
	out := make([]map[string]any, 0, len(source))
	for _, message := range source {
		role := strings.TrimSpace(message.Role)
		if role == "" {
			continue
		}
		item := map[string]any{
			"role":    role,
			"content": projectMessageContent(message),
			"response_meta": map[string]any{
				"id": "",
				"usage": map[string]int{
					"prompt_tokens":     0,
					"completion_tokens": 0,
					"total_tokens":      0,
				},
			},
			"reasoning_content_signature": "",
		}
		if message.Name != "" {
			item["name"] = message.Name
		}
		if message.ToolCallID != "" {
			item["tool_call_id"] = message.ToolCallID
		}
		// Round-trip prior extended thinking as reasoning_content (the gateway
		// accepts it in assistant history; the signature stays empty upstream).
		if strings.EqualFold(role, "assistant") && strings.TrimSpace(message.ReasoningText) != "" {
			item["reasoning_content"] = message.ReasoningText
		}
		if calls := projectMessageToolCalls(message.ToolCalls); len(calls) > 0 {
			item["tool_calls"] = calls
		}
		out = append(out, item)
	}
	if len(out) == 0 {
		return []map[string]any{{"role": "user", "content": request.Prompt}}
	}
	return out
}

func projectMessageContent(message Message) any {
	if len(message.Images) == 0 {
		return message.Content
	}
	content := make([]map[string]any, 0, len(message.Images)+1)
	if strings.TrimSpace(message.Content) != "" {
		content = append(content, map[string]any{
			"type": "text",
			"text": message.Content,
		})
	}
	for _, img := range message.Images {
		imageURL := projectImage(img)
		if imageURL == "" {
			continue
		}
		content = append(content, map[string]any{
			"type": "image_url",
			"image_url": map[string]any{
				"url": imageURL,
			},
		})
	}
	if len(content) == 0 {
		return message.Content
	}
	return content
}

func projectMessageToolCalls(calls []toolemulation.ToolCall) []map[string]any {
	if len(calls) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(calls))
	for i, call := range calls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			continue
		}
		args, _ := json.Marshal(call.Arguments)
		out = append(out, map[string]any{
			"index": i,
			"id":    strings.TrimSpace(call.ID),
			"type":  "function",
			"function": map[string]any{
				"name":      name,
				"arguments": string(args),
			},
		})
	}
	return out
}

func projectTools(tools []toolemulation.ToolDef) []map[string]any {
	if len(tools) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		params := any(tool.InputSchema)
		if len(tool.InputSchema) == 0 {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        name,
				"description": strings.TrimSpace(tool.Description),
				"parameters":  params,
			},
		})
	}
	return out
}

func projectToolChoice(choice toolemulation.ToolChoice) any {
	switch choice.Mode {
	case "none":
		return "none"
	case "any":
		return "required"
	case "tool":
		name := strings.TrimSpace(choice.Name)
		if name == "" {
			return nil
		}
		return map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": name,
			},
		}
	default:
		return nil
	}
}

func (c *Client) headers(cred Credential, path string, body string) (map[string]string, error) {
	if err := validateCredential(cred); err != nil {
		return nil, err
	}
	date := strconv.FormatInt(time.Now().Unix(), 10)
	authPayload := map[string]string{
		"cosyVersion": c.cfg.CosyVersion,
		"ideVersion":  "",
		"info":        cred.EncryptUserInfo,
		"requestId":   newUUID(),
		"version":     "v1",
	}
	authPayloadBytes, err := json.Marshal(authPayload)
	if err != nil {
		return nil, err
	}
	payloadBase64 := base64.StdEncoding.EncodeToString(authPayloadBytes)
	preimage := strings.Join([]string{
		payloadBase64,
		cred.CosyKey,
		date,
		body,
		normalizePath(path),
	}, "\n")
	signature := md5.Sum([]byte(preimage))
	return map[string]string{
		"Authorization":         fmt.Sprintf("Bearer COSY.%s.%x", payloadBase64, signature),
		"Content-Type":          "application/json",
		"Appcode":               "cosy",
		"Cosy-Date":             date,
		"Cosy-Key":              cred.CosyKey,
		"Cosy-Machineid":        cred.MachineID,
		"Cosy-User":             cred.UserID,
		"Cosy-Clientip":         "198.18.0.1",
		"Cosy-Clienttype":       "5",
		"Cosy-Machineos":        MachineOSHeader(),
		"Cosy-Machinetoken":     "",
		"Cosy-Machinetype":      "5",
		"Cosy-Version":          c.cfg.CosyVersion,
		"Cosy-Business-Product": "cli",
		"Cosy-Business-Type":    "agent",
		"Cosy-Scene":            "assistant",
		"Login-Version":         "v2",
		"User-Agent":            "lingma-proxy/remote",
		"Accept":                "text/event-stream",
		"Cache-Control":         "no-cache",
	}, nil
}

func normalizePath(path string) string {
	return strings.TrimPrefix(path, "/algo")
}

type outerSSE struct {
	Body       string `json:"body"`
	StatusCode int    `json:"statusCodeValue"`
}

type innerSSE struct {
	Choices []struct {
		Delta struct {
			Content          string                `json:"content"`
			ReasoningContent string                `json:"reasoning_content"`
			ToolCalls        []remoteToolCallDelta `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *remoteUsage `json:"usage"`
}

// remoteUsage mirrors the OpenAI-style usage object the Lingma/QoderCN gateway
// emits in the final SSE chunk, including cache/reasoning breakdowns and billing.
type remoteUsage struct {
	PromptTokens        int     `json:"prompt_tokens"`
	CompletionTokens    int     `json:"completion_tokens"`
	TotalTokens         int     `json:"total_tokens"`
	Credits             float64 `json:"credits"`
	OriginalCredits     float64 `json:"original_credits"`
	Billable            bool    `json:"billable"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

type sseEvent struct {
	Content      string
	Reasoning    string
	ToolCalls    []remoteToolCallFragment
	FinishReason string
	Usage        *remoteUsage
	Done         bool
}

type remoteToolCallFragment struct {
	Index             int
	ID                string
	Type              string
	Name              string
	ArgumentsFragment string
}

type remoteToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function,omitempty"`
}

func scanSSE(reader io.Reader, onEvent func(sseEvent) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			return onEvent(sseEvent{Done: true})
		}
		event, ok, err := parseSSEPayload(payload)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := onEvent(event); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func parseSSEPayload(payload string) (sseEvent, bool, error) {
	var outer outerSSE
	if err := json.Unmarshal([]byte(payload), &outer); err != nil {
		return sseEvent{}, false, err
	}
	if outer.StatusCode >= 400 {
		return sseEvent{}, false, fmt.Errorf("remote sse status %d", outer.StatusCode)
	}
	if outer.Body == "" {
		return sseEvent{}, false, nil
	}
	if outer.Body == "[DONE]" {
		return sseEvent{Done: true}, true, nil
	}
	var inner innerSSE
	if err := json.Unmarshal([]byte(outer.Body), &inner); err != nil {
		return sseEvent{}, false, err
	}
	var builder strings.Builder
	var reasoning strings.Builder
	var toolCalls []remoteToolCallFragment
	finishReason := ""
	for _, choice := range inner.Choices {
		builder.WriteString(choice.Delta.Content)
		reasoning.WriteString(choice.Delta.ReasoningContent)
		if choice.FinishReason != "" {
			finishReason = choice.FinishReason
		}
		for _, tc := range choice.Delta.ToolCalls {
			toolCalls = append(toolCalls, remoteToolCallFragment{
				Index:             tc.Index,
				ID:                strings.TrimSpace(tc.ID),
				Type:              strings.TrimSpace(tc.Type),
				Name:              strings.TrimSpace(tc.Function.Name),
				ArgumentsFragment: tc.Function.Arguments,
			})
		}
	}
	return sseEvent{
		Content:      builder.String(),
		Reasoning:    reasoning.String(),
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
		Usage:        inner.Usage,
	}, true, nil
}

type remoteToolCallBuffer struct {
	order  []int
	states map[int]*remoteToolCallState
}

type remoteToolCallState struct {
	id        string
	callType  string
	name      string
	arguments strings.Builder
}

func newRemoteToolCallBuffer() *remoteToolCallBuffer {
	return &remoteToolCallBuffer{states: map[int]*remoteToolCallState{}}
}

func (b *remoteToolCallBuffer) Add(fragments []remoteToolCallFragment) {
	if b == nil {
		return
	}
	for _, fragment := range fragments {
		state := b.states[fragment.Index]
		if state == nil {
			state = &remoteToolCallState{}
			b.states[fragment.Index] = state
			b.order = append(b.order, fragment.Index)
		}
		if fragment.ID != "" {
			state.id = fragment.ID
		}
		if fragment.Type != "" {
			state.callType = fragment.Type
		}
		if fragment.Name != "" {
			state.name = fragment.Name
		}
		if fragment.ArgumentsFragment != "" {
			state.arguments.WriteString(fragment.ArgumentsFragment)
		}
	}
}

func (b *remoteToolCallBuffer) Calls() []toolemulation.ToolCall {
	if b == nil || len(b.order) == 0 {
		return nil
	}
	out := make([]toolemulation.ToolCall, 0, len(b.order))
	for _, index := range b.order {
		state := b.states[index]
		if state == nil || strings.TrimSpace(state.name) == "" {
			continue
		}
		args := strings.TrimSpace(state.arguments.String())
		call := toolemulation.ToolCall{
			ID:        strings.TrimSpace(state.id),
			Name:      strings.TrimSpace(state.name),
			Arguments: map[string]any{},
		}
		if args != "" {
			var parsed map[string]any
			if err := json.Unmarshal([]byte(args), &parsed); err == nil {
				call.Arguments = parsed
			} else {
				call.Arguments = map[string]any{"raw_arguments": args}
			}
		}
		if call.ID == "" {
			call.ID = fmt.Sprintf("toolu_%d_%d", time.Now().UnixNano(), index)
		}
		out = append(out, call)
	}
	return out
}

func candidateConfigFiles() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	paths := []string{
		filepath.Join(home, ".qoder-cn", "shared_client", "extension", "server", "config.json"),
		filepath.Join(home, ".qoder-cn", "shared_client", "extension", "local", "config.json"),
		filepath.Join(home, ".qoder-cn", "shared_client", "bin", "config.json"),
		filepath.Join(home, ".qoder-cn", "shared_client", "cache", "app-config.json"),
		filepath.Join(home, ".qodercn", "extension", "server", "config.json"),
		filepath.Join(home, ".qodercn", "extension", "local", "config.json"),
		filepath.Join(home, ".qodercn", "bin", "config.json"),
		filepath.Join(home, "Library", "Application Support", "QoderCN", "SharedClientCache", "cache", "app-config.json"),
		filepath.Join(home, "Library", "Application Support", "Qoder", "SharedClientCache", "cache", "app-config.json"),
		filepath.Join(home, ".lingma", "extension", "server", "config.json"),
		filepath.Join(home, ".lingma", "extension", "local", "config.json"),
		filepath.Join(home, ".lingma", "bin", "config.json"),
		filepath.Join(home, "Library", "Application Support", "Lingma", "SharedClientCache", "cache", "app-config.json"),
		filepath.Join(home, ".config", "lingma-proxy", "config.json"),
		filepath.Join(home, ".config", "lingma-ipc-proxy", "config.json"),
		filepath.Join(home, ".qoder-cn", "shared_client", "logs", "qodercn.log"),
		filepath.Join(home, ".qoder-cn", "shared_client", "logs", "qodercn-extension.log"),
		filepath.Join(home, ".qodercn", "logs", "qodercn.log"),
		filepath.Join(home, ".qodercn", "logs", "qodercn-extension.log"),
		filepath.Join(home, ".qodercn", "vscode", "sharedClientCache", "logs", "qodercn.log"),
		filepath.Join(home, ".qodercn", "vscode", "sharedClientCache", "logs", "qodercn-extension.log"),
		filepath.Join(home, ".lingma", "logs", "lingma.log"),
		filepath.Join(home, ".lingma", "logs", "lingma-extension.log"),
		filepath.Join(home, ".lingma", "vscode", "sharedClientCache", "logs", "lingma.log"),
		filepath.Join(home, ".lingma", "vscode", "sharedClientCache", "logs", "lingma-extension.log"),
	}
	for _, root := range lingmaLogRoots(home) {
		paths = append(paths, recentLingmaAppLogs(root)...)
	}
	for _, root := range windowsAppDataRoots() {
		paths = append(paths, windowsSharedClientConfigFiles(root)...)
	}
	for _, root := range jetBrainsRoots() {
		paths = append(paths, jetBrainsOptionFiles(root)...)
	}
	return paths
}

func uniqueBaseURLHints(hints []BaseURLHint) []BaseURLHint {
	seen := make(map[string]struct{}, len(hints))
	out := make([]BaseURLHint, 0, len(hints))
	for _, hint := range hints {
		url := strings.TrimRight(strings.TrimSpace(hint.URL), "/")
		if url == "" {
			continue
		}
		if _, ok := seen[url]; ok {
			continue
		}
		seen[url] = struct{}{}
		out = append(out, BaseURLHint{URL: url, Source: hint.Source})
	}
	return out
}

func sortBaseURLHints(hints []BaseURLHint) []BaseURLHint {
	sort.SliceStable(hints, func(i, j int) bool {
		return baseURLHintScore(hints[i].URL) > baseURLHintScore(hints[j].URL)
	})
	return hints
}

func baseURLHintScore(raw string) int {
	parsed, err := url.Parse(raw)
	if err != nil {
		return 0
	}
	host := strings.ToLower(parsed.Host)
	switch {
	case strings.HasSuffix(host, ".rdc.aliyuncs.com"):
		return 100
	case isKnownEnterpriseRemoteHost(host):
		return 90
	case host == "lingma.alibabacloud.com":
		return 50
	case host == "lingma-api.tongyi.aliyun.com":
		return 10
	default:
		return 80
	}
}

func windowsAppDataRoots() []string {
	roots := make([]string, 0, 3)
	for _, envName := range []string{"APPDATA", "LOCALAPPDATA", "ProgramData"} {
		if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
			roots = append(roots, value)
		}
	}
	return uniqueStrings(roots)
}

func windowsSharedClientConfigFiles(root string) []string {
	paths := make([]string, 0)
	for _, appName := range []string{"QoderCN", "Qoder", "Lingma"} {
		paths = append(paths,
			filepath.Join(root, appName, "SharedClientCache", "cache", "app-config.json"),
			filepath.Join(root, appName, "shared_client", "cache", "app-config.json"),
			filepath.Join(root, appName, "cache", "app-config.json"),
		)
	}
	return paths
}

func readBaseURLHint(path string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return extractBaseURLFromText(string(body))
	}
	if value := findBaseURL(value); value != "" {
		return value
	}
	return extractBaseURLFromText(string(body))
}

func findBaseURL(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "base") || strings.Contains(lower, "domain") || strings.Contains(lower, "url") {
				if text, ok := item.(string); ok && strings.HasPrefix(strings.TrimSpace(text), "http") && (strings.Contains(strings.ToLower(text), "lingma") || strings.Contains(strings.ToLower(text), "qoder")) {
					return strings.TrimSpace(text)
				}
			}
			if nested := findBaseURL(item); nested != "" {
				return nested
			}
		}
	case []any:
		for _, item := range typed {
			if nested := findBaseURL(item); nested != "" {
				return nested
			}
		}
	}
	return ""
}

func lingmaLogRoots(home string) []string {
	roots := []string{
		filepath.Join(home, ".lingma", "logs"),
		filepath.Join(home, ".lingma", "vscode", "sharedClientCache", "logs"),
		filepath.Join(home, ".qoder-cn", "shared_client", "logs"),
		filepath.Join(home, ".qoder-cn", "shared_client", "cli", "logs"),
		filepath.Join(home, ".qoder-cn", "logs"),
		filepath.Join(home, ".qodercn", "logs"),
		filepath.Join(home, ".qodercn", "vscode", "sharedClientCache", "logs"),
		filepath.Join(home, "Library", "Application Support", "Lingma", "logs"),
		filepath.Join(home, "Library", "Application Support", "Lingma", "SharedClientCache", "logs"),
		filepath.Join(home, "Library", "Application Support", "Lingma", "SharedClientCache", "cli", "logs"),
		filepath.Join(home, "Library", "Application Support", "QoderCN", "logs"),
		filepath.Join(home, "Library", "Application Support", "QoderCN", "SharedClientCache", "logs"),
		filepath.Join(home, "Library", "Application Support", "QoderCN", "SharedClientCache", "cli", "logs"),
		filepath.Join(home, "Library", "Application Support", "Qoder", "logs"),
		filepath.Join(home, "Library", "Application Support", "Qoder", "SharedClientCache", "logs"),
		filepath.Join(home, "Library", "Application Support", "Qoder", "SharedClientCache", "cli", "logs"),
	}
	for _, envName := range []string{"APPDATA", "LOCALAPPDATA", "ProgramData"} {
		if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
			roots = append(roots,
				filepath.Join(value, "Lingma", "logs"),
				filepath.Join(value, "Lingma", "SharedClientCache", "logs"),
				filepath.Join(value, "Lingma", "SharedClientCache", "cli", "logs"),
				filepath.Join(value, "QoderCN", "logs"),
				filepath.Join(value, "QoderCN", "SharedClientCache", "logs"),
				filepath.Join(value, "QoderCN", "SharedClientCache", "cli", "logs"),
				filepath.Join(value, "Qoder", "logs"),
				filepath.Join(value, "Qoder", "SharedClientCache", "logs"),
				filepath.Join(value, "Qoder", "SharedClientCache", "cli", "logs"),
				filepath.Join(value, "Code", "User", "globalStorage", "alibaba-cloud.tongyi-lingma", "logs"),
			)
		}
	}
	for _, root := range jetBrainsRoots() {
		roots = append(roots, jetBrainsLogRoots(root)...)
	}
	if value := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); value != "" {
		roots = append(roots, filepath.Join(value, "Lingma", "logs"))
		roots = append(roots, filepath.Join(value, "QoderCN", "logs"))
	}
	if value := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); value != "" {
		roots = append(roots, filepath.Join(value, "Lingma", "logs"))
		roots = append(roots, filepath.Join(value, "QoderCN", "logs"))
	}
	roots = append(roots,
		filepath.Join(home, ".config", "Lingma", "logs"),
		filepath.Join(home, ".config", "QoderCN", "logs"),
		filepath.Join(home, ".local", "state", "Lingma", "logs"),
		filepath.Join(home, ".local", "state", "QoderCN", "logs"),
	)
	return uniqueStrings(roots)
}

func jetBrainsRoots() []string {
	roots := make([]string, 0, 2)
	for _, envName := range []string{"APPDATA", "LOCALAPPDATA"} {
		if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
			roots = append(roots, filepath.Join(value, "JetBrains"))
		}
	}
	return uniqueStrings(roots)
}

func jetBrainsProductDirs(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if strings.Contains(name, "idea") ||
			strings.Contains(name, "webstorm") ||
			strings.Contains(name, "pycharm") ||
			strings.Contains(name, "goland") ||
			strings.Contains(name, "clion") ||
			strings.Contains(name, "datagrip") ||
			strings.Contains(name, "phpstorm") ||
			strings.Contains(name, "rider") ||
			strings.Contains(name, "rubymine") {
			dirs = append(dirs, filepath.Join(root, entry.Name()))
		}
	}
	sort.Strings(dirs)
	return dirs
}

func jetBrainsLogRoots(root string) []string {
	dirs := jetBrainsProductDirs(root)
	roots := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		roots = append(roots, filepath.Join(dir, "log"))
	}
	return roots
}

func jetBrainsOptionFiles(root string) []string {
	dirs := jetBrainsProductDirs(root)
	paths := make([]string, 0)
	for _, dir := range dirs {
		optionsDir := filepath.Join(dir, "options")
		entries, err := os.ReadDir(optionsDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := strings.ToLower(entry.Name())
			if !(strings.Contains(name, "lingma") ||
				strings.Contains(name, "qoder") ||
				strings.Contains(name, "tongyi") ||
				strings.Contains(name, "alibaba")) {
				continue
			}
			if strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".properties") {
				paths = append(paths, filepath.Join(optionsDir, entry.Name()))
			}
		}
	}
	sort.Strings(paths)
	return paths
}

func recentLingmaAppLogs(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	type logDir struct {
		path    string
		modTime int64
	}
	dirs := make([]logDir, 0, len(entries))
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			appendCandidateLogPath(&paths, filepath.Join(root, entry.Name()), entry.Name())
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		dirs = append(dirs, logDir{path: filepath.Join(root, entry.Name()), modTime: info.ModTime().UnixNano()})
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].modTime > dirs[j].modTime })
	if len(dirs) > 5 {
		dirs = dirs[:5]
	}
	for _, dir := range dirs {
		_ = filepath.WalkDir(dir.path, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return nil
			}
			appendCandidateLogPath(&paths, path, entry.Name())
			return nil
		})
	}
	return paths
}

func appendCandidateLogPath(paths *[]string, path, name string) {
	lowerName := strings.ToLower(name)
	if lowerName == "renderer.log" ||
		lowerName == "sharedprocess.log" ||
		lowerName == "main.log" ||
		lowerName == "network.log" ||
		lowerName == "network-shared.log" ||
		lowerName == "agent.log" ||
		lowerName == "qoder.log" ||
		lowerName == "qodercn.log" ||
		lowerName == "lingma.log" ||
		lowerName == "lingma-extension.log" ||
		lowerName == "idea.log" ||
		lowerName == "webstorm.log" ||
		lowerName == "pycharm.log" ||
		lowerName == "goland.log" ||
		lowerName == "clion.log" ||
		lowerName == "datagrip.log" ||
		lowerName == "phpstorm.log" ||
		lowerName == "rider.log" ||
		lowerName == "rubymine.log" ||
		strings.HasSuffix(name, "Lingma.log") ||
		strings.Contains(lowerName, "lingma") && strings.HasSuffix(lowerName, ".log") ||
		strings.Contains(lowerName, "qoder") && strings.HasSuffix(lowerName, ".log") ||
		strings.Contains(lowerName, "tongyi") && strings.HasSuffix(lowerName, ".log") ||
		strings.Contains(lowerName, "alibaba") && strings.HasSuffix(lowerName, ".log") {
		*paths = append(*paths, path)
	}
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func extractBaseURLFromText(text string) string {
	matches := remoteBaseURLPattern.FindAllString(text, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		if value := normalizeRemoteAPIRequestURL(matches[i]); value != "" {
			return value
		}
	}
	for _, marker := range []string{
		"endpoint config:",
		"Endpoint:",
		"configGeneralDedicatedUrl",
		"dedicatedUrl",
		"Lingma endpoint configured",
		"Lingma endpoint from --api-endpoint flag",
		"Using service url:",
	} {
		if value := extractBaseURLAfterMarker(text, marker); value != "" {
			return value
		}
	}
	for i := len(matches) - 1; i >= 0; i-- {
		if value := normalizeRemoteBaseURLHint(matches[i]); value != "" {
			return value
		}
	}
	return ""
}

func extractBaseURLAfterMarker(text, marker string) string {
	lowerText := strings.ToLower(text)
	lowerMarker := strings.ToLower(marker)
	index := strings.LastIndex(lowerText, lowerMarker)
	if index < 0 {
		return ""
	}
	tail := text[index+len(marker):]
	if strings.HasPrefix(lowerMarker, "https://") {
		tail = marker + tail
	}
	for _, field := range strings.Fields(tail) {
		field = strings.Trim(field, `"'<>),]}`)
		if value := normalizeRemoteBaseURLHint(field); value != "" {
			return value
		}
	}
	return ""
}

func normalizeRemoteBaseURLHint(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "ttps://") {
		raw = "h" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	if !isRemoteAPIURL(parsed) {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func normalizeRemoteAPIRequestURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "ttps://") {
		raw = "h" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	if !isRemoteAPIRequestPath(parsed.EscapedPath()) {
		return ""
	}
	if !isRemoteAPIURL(parsed) {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func isRemoteAPIRequestPath(path string) bool {
	path = strings.ToLower(path)
	if path == "/algo" || strings.HasPrefix(path, "/algo/") {
		return true
	}
	return strings.Contains(path, "/api/v2/model/list") ||
		strings.Contains(path, "/agent_chat_generation") ||
		strings.Contains(path, "/remoteagent/") ||
		strings.Contains(path, "/lingma/login") ||
		strings.Contains(path, "/extension/config/pull")
}

func isRemoteAPIURL(parsed *url.URL) bool {
	host := strings.ToLower(parsed.Host)
	if host == "" {
		return false
	}
	if isStaticAssetHost(host) {
		return false
	}
	path := strings.ToLower(parsed.EscapedPath())
	if strings.Contains(path, "/download") ||
		strings.Contains(path, "/extension/") ||
		strings.HasSuffix(path, ".zip") ||
		strings.HasSuffix(path, ".vsix") ||
		strings.HasSuffix(path, ".dmg") ||
		strings.HasSuffix(path, ".exe") {
		return false
	}
	return true
}

func isStaticAssetHost(host string) bool {
	return strings.Contains(host, ".oss-") ||
		strings.Contains(host, "oss-rg-") ||
		strings.Contains(host, ".oss.") ||
		strings.Contains(host, "marketplace.visualstudio.com") ||
		strings.Contains(host, "downloads.marketplace.jetbrains.com")
}

func isKnownEnterpriseRemoteHost(host string) bool {
	return strings.HasSuffix(host, ".rdc.aliyuncs.com") ||
		(strings.Contains(host, "lingma") && host != "lingma.alibabacloud.com" && host != "lingma-api.tongyi.aliyun.com") ||
		strings.Contains(host, "qoder")
}

func estimateTokens(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	return len([]rune(text)) / 4
}

func truncate(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max] + "... [truncated]"
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func valueOr(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

var hexCounter uint64
