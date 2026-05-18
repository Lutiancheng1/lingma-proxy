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
)

var remoteBaseURLPattern = regexp.MustCompile(`https?://[^\s"'<>),\]}]+`)

type Config struct {
	BaseURL     string
	AuthFile    string
	CosyVersion string
	Timeout     time.Duration
}

type Client struct {
	cfg    Config
	client *http.Client
}

type BaseURLHint struct {
	URL    string
	Source string
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
	Role       string
	Content    string
	Images     []Image
	Name       string
	ToolCallID string
	ToolCalls  []toolemulation.ToolCall
}

type ChatResult struct {
	Text          string
	InputTokens   int
	OutputTokens  int
	RequestID     string
	CredentialSrc string
	ToolCalls     []toolemulation.ToolCall
}

type StreamEvent struct {
	Delta string
}

func New(cfg Config) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = ResolveBaseURL("")
	}
	if cfg.CosyVersion == "" {
		cfg.CosyVersion = "2.11.2"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Client{cfg: cfg, client: &http.Client{Timeout: cfg.Timeout}}
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
	for _, path := range candidateConfigFiles() {
		if value := readBaseURLHint(path); value != "" {
			return BaseURLHint{URL: strings.TrimRight(value, "/"), Source: path}
		}
	}
	return BaseURLHint{URL: DefaultBaseURL, Source: "default"}
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

func (c *Client) modelListStatusError(statusCode int, body string) error {
	message := fmt.Sprintf("remote model list status %d from %s: %s", statusCode, c.cfg.BaseURL, truncate(body, 500))
	if statusCode == http.StatusNotFound || strings.Contains(body, "NoSuchKey") {
		message += "。这通常表示远端 API 域名自动探测命中了错误地址，请到设置页手动填写 Lingma 官方或企业专属远端 API 域名；官方默认域名为 https://lingma.alibabacloud.com。"
	}
	return fmt.Errorf("%s", message)
}

func (c *Client) Chat(ctx context.Context, request ChatRequest, onDelta func(string)) (*ChatResult, error) {
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
	toolCallBuffer := newRemoteToolCallBuffer()
	if err := scanSSE(resp.Body, func(event sseEvent) error {
		if event.Done {
			return nil
		}
		if len(event.ToolCalls) > 0 {
			toolCallBuffer.Add(event.ToolCalls)
		}
		if event.Content == "" {
			return nil
		}
		builder.WriteString(event.Content)
		if onDelta != nil {
			onDelta(event.Content)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	text := builder.String()
	return &ChatResult{
		Text:          text,
		InputTokens:   estimateTokens(request.Prompt),
		OutputTokens:  estimateTokens(text),
		RequestID:     requestID,
		CredentialSrc: cred.Source,
		ToolCalls:     toolCallBuffer.Calls(),
	}, nil
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
	imageURLs := projectImages(request.Images)
	payload := map[string]any{
		"request_id":       requestID,
		"request_set_id":   "",
		"chat_record_id":   requestID,
		"stream":           true,
		"image_urls":       nullableSlice(imageURLs),
		"is_reply":         false,
		"is_retry":         false,
		"session_id":       "",
		"code_language":    "",
		"source":           0,
		"version":          "3",
		"chat_prompt":      "",
		"parameters":       map[string]float64{"temperature": temperature},
		"aliyun_user_type": "personal_standard",
		"agent_id":         "agent_common",
		"task_id":          "question_refine",
		"model_config": map[string]any{
			"key":          model,
			"display_name": "",
			"model":        model,
			"format":       "",
			"is_vl":        len(imageURLs) > 0,
			"is_reasoning": remoteReasoningEnabled(request),
			"api_key":      "",
			"url":          "",
			"source":       "",
			"enable":       false,
		},
		"messages": projectMessages(request),
		"business": map[string]any{
			"product":  "jb_plugin",
			"version":  c.cfg.CosyVersion,
			"type":     "memory",
			"id":       newUUID(),
			"begin_at": time.Now().UnixMilli(),
			"stage":    "start",
			"name":     "memory_intent_recognition_" + requestID,
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

func projectImages(images []Image) []string {
	if len(images) == 0 {
		return nil
	}
	out := make([]string, 0, len(images))
	for _, img := range images {
		item := projectImage(img)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
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
		"Authorization":     fmt.Sprintf("Bearer COSY.%s.%x", payloadBase64, signature),
		"Content-Type":      "application/json",
		"Appcode":           "cosy",
		"Cosy-Date":         date,
		"Cosy-Key":          cred.CosyKey,
		"Cosy-Machineid":    cred.MachineID,
		"Cosy-User":         cred.UserID,
		"Cosy-Clientip":     "198.18.0.1",
		"Cosy-Clienttype":   "2",
		"Cosy-Machineos":    MachineOSHeader(),
		"Cosy-Machinetoken": "",
		"Cosy-Machinetype":  "",
		"Cosy-Version":      c.cfg.CosyVersion,
		"Login-Version":     "v2",
		"User-Agent":        "lingma-proxy/remote",
		"Accept":            "text/event-stream",
		"Cache-Control":     "no-cache",
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
			Content   string                `json:"content"`
			ToolCalls []remoteToolCallDelta `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
}

type sseEvent struct {
	Content   string
	ToolCalls []remoteToolCallFragment
	Done      bool
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
	var toolCalls []remoteToolCallFragment
	for _, choice := range inner.Choices {
		builder.WriteString(choice.Delta.Content)
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
	return sseEvent{Content: builder.String(), ToolCalls: toolCalls}, true, nil
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
		filepath.Join(home, ".lingma", "extension", "server", "config.json"),
		filepath.Join(home, ".lingma", "extension", "local", "config.json"),
		filepath.Join(home, ".lingma", "bin", "config.json"),
		filepath.Join(home, ".config", "lingma-proxy", "config.json"),
		filepath.Join(home, ".config", "lingma-ipc-proxy", "config.json"),
		filepath.Join(home, ".lingma", "logs", "lingma.log"),
		filepath.Join(home, ".lingma", "logs", "lingma-extension.log"),
		filepath.Join(home, ".lingma", "vscode", "sharedClientCache", "logs", "lingma.log"),
		filepath.Join(home, ".lingma", "vscode", "sharedClientCache", "logs", "lingma-extension.log"),
	}
	for _, root := range lingmaLogRoots(home) {
		paths = append(paths, recentLingmaAppLogs(root)...)
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
				if text, ok := item.(string); ok && strings.HasPrefix(strings.TrimSpace(text), "http") && strings.Contains(text, "lingma") {
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
		filepath.Join(home, "Library", "Application Support", "Lingma", "logs"),
	}
	for _, envName := range []string{"APPDATA", "LOCALAPPDATA", "ProgramData"} {
		if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
			roots = append(roots,
				filepath.Join(value, "Lingma", "logs"),
				filepath.Join(value, "Code", "User", "globalStorage", "alibaba-cloud.tongyi-lingma", "logs"),
			)
		}
	}
	if value := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); value != "" {
		roots = append(roots, filepath.Join(value, "Lingma", "logs"))
	}
	if value := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); value != "" {
		roots = append(roots, filepath.Join(value, "Lingma", "logs"))
	}
	roots = append(roots,
		filepath.Join(home, ".config", "Lingma", "logs"),
		filepath.Join(home, ".local", "state", "Lingma", "logs"),
	)
	return uniqueStrings(roots)
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
	for _, entry := range entries {
		if !entry.IsDir() {
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
	paths := make([]string, 0, len(dirs)*4)
	for _, dir := range dirs {
		_ = filepath.WalkDir(dir.path, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return nil
			}
			name := entry.Name()
			lowerName := strings.ToLower(name)
			if lowerName == "renderer.log" ||
				lowerName == "sharedprocess.log" ||
				lowerName == "main.log" ||
				strings.HasSuffix(name, "Lingma.log") ||
				strings.Contains(lowerName, "lingma") && strings.HasSuffix(lowerName, ".log") {
				paths = append(paths, path)
			}
			return nil
		})
	}
	return paths
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
		if value := normalizeRemoteBaseURLHint(matches[i]); value != "" {
			return value
		}
	}
	for _, marker := range []string{
		"endpoint config:",
		"Using service url:",
		"Download asset from:",
	} {
		if value := extractBaseURLAfterMarker(text, marker); value != "" {
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
	host := strings.ToLower(parsed.Host)
	if !isRemoteAPIHost(host) {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func isRemoteAPIHost(host string) bool {
	if host == "" {
		return false
	}
	if strings.Contains(host, ".oss-") || strings.Contains(host, "oss-rg-") || strings.Contains(host, ".oss.") {
		return false
	}
	switch host {
	case "lingma.alibabacloud.com", "lingma-api.tongyi.aliyun.com":
		return true
	}
	if strings.HasSuffix(host, ".rdc.aliyuncs.com") {
		return true
	}
	return false
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
