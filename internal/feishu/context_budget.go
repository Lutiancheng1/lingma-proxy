package feishu

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultContextWindowTokens = 128000
	defaultMaxOutputTokens     = 8192
	defaultVisionTokenEstimate = 2000
	defaultToolResultRetention = 2
	defaultCompactWatermark    = 75
	defaultSkillHTTPTimeout    = 60
	defaultSkillHTTPMaxBytes   = 5 * 1024 * 1024
)

type ContextConfig struct {
	ContextWindowOverride int  `json:"contextWindowOverride,omitempty"`
	AutoCompact           bool `json:"autoCompact"`
	CompactWatermark      int  `json:"compactWatermark,omitempty"`
	ToolResultRetention   int  `json:"toolResultRetention,omitempty"`
	SkillHTTPTimeout      int  `json:"skillHttpTimeout,omitempty"`
	SkillHTTPMaxBytes     int  `json:"skillHttpMaxBytes,omitempty"`
}

type ModelContextProfile struct {
	Model                string `json:"model"`
	ContextWindow        int    `json:"contextWindow"`
	MaxOutputTokens      int    `json:"maxOutputTokens"`
	ReservedOutputTokens int    `json:"reservedOutputTokens"`
	ToolSchemaTokens     int    `json:"toolSchemaTokens"`
	VisionTokensEstimate int    `json:"visionTokensEstimate"`
	Source               string `json:"source"`
}

type ContextBudgetSnapshot struct {
	Model             string `json:"model"`
	ContextWindow     int    `json:"contextWindow"`
	EstimatedTokens   int    `json:"estimatedTokens"`
	UsedPercent       int    `json:"usedPercent"`
	RemainingTokens   int    `json:"remainingTokens"`
	ToolResultTokens  int    `json:"toolResultTokens"`
	SkillTokens       int    `json:"skillTokens"`
	Watermark         string `json:"watermark"`
	NextAction        string `json:"nextAction"`
	LastCompactedAt   string `json:"lastCompactedAt,omitempty"`
	SummaryRange      string `json:"summaryRange,omitempty"`
	EstimatorAdjusted bool   `json:"estimatorAdjusted,omitempty"`
}

type StructuredSummary struct {
	PrimaryGoal         string   `json:"primary_goal,omitempty"`
	UserPreferences     []string `json:"user_preferences,omitempty"`
	ConfirmedDecisions  []string `json:"confirmed_decisions,omitempty"`
	PendingActions      []string `json:"pending_actions,omitempty"`
	OpenQuestions       []string `json:"open_questions,omitempty"`
	ImportantEntities   []string `json:"important_entities,omitempty"`
	Artifacts           []string `json:"artifacts,omitempty"`
	ToolResults         []string `json:"tool_results,omitempty"`
	ErrorsAndRecoveries []string `json:"errors_and_recoveries,omitempty"`
	NextStep            string   `json:"next_step,omitempty"`
}

type toolResultHandling string

const (
	toolResultPreserve  toolResultHandling = "preserve"
	toolResultSummarize toolResultHandling = "summarize"
	toolResultStub      toolResultHandling = "stub"
	toolResultDiscard   toolResultHandling = "discard"
)

func classifyToolResult(toolName, content string, isError bool) toolResultHandling {
	if isError {
		return toolResultStub
	}
	switch toolName {
	case "lark_drive_search":
		return toolResultPreserve
	case "lark_docs_fetch", "lark_skill_view", "skill_view":
		return toolResultPreserve
	case "lark_sheets_read":
		return toolResultPreserve
	case "web_search", "web_fetch":
		return toolResultPreserve
	case "lark_im_search":
		return toolResultPreserve
	case "lark_calendar_agenda", "lark_task_list", "lark_base_records", "lark_wiki_search":
		return toolResultSummarize
	case "lark_im_send", "lark_docs_create", "lark_sheets_info", "lark_calendar_create":
		return toolResultStub
	case "safe_file_list", "authorize_local_path", "list_authorized_paths", "lark_permission_public":
		return toolResultStub
	case "mcp_call", "mcp_resource_read", "mcp_prompt_get":
		return toolResultSummarize
	case "lark_cli_exec":
		return toolResultSummarize
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return toolResultDiscard
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(content), &payload); err == nil {
		if fmt.Sprint(payload["kind"]) == "drive_search" {
			return toolResultPreserve
		}
		data, _ := payload["data"].(map[string]any)
		if reading, ok := data["agent_reading"].(map[string]any); ok {
			kind := fmt.Sprint(reading["kind"])
			if kind == "doc_content_chunk" || kind == "sheet_rows_chunk" {
				return toolResultPreserve
			}
		}
	}
	return toolResultSummarize
}

func extractToolResultSummary(toolName, content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return "[empty result]"
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return summarizeText(content, 800)
	}
	var lines []string
	switch toolName {
	case "lark_calendar_agenda":
		events, _ := payload["events"].([]any)
		lines = append(lines, fmt.Sprintf("日程数量：%d", len(events)))
		for i, ev := range events {
			if i >= 3 {
				break
			}
			if m, ok := ev.(map[string]any); ok {
				title := fmt.Sprint(m["summary"])
				start := fmt.Sprint(m["start_time"])
				lines = append(lines, fmt.Sprintf("- %s (%s)", title, start))
			}
		}
	case "lark_task_list":
		tasks, _ := payload["tasks"].([]any)
		if tasks == nil {
			tasks, _ = payload["items"].([]any)
		}
		lines = append(lines, fmt.Sprintf("任务数量：%d", len(tasks)))
		for i, t := range tasks {
			if i >= 3 {
				break
			}
			if m, ok := t.(map[string]any); ok {
				title := fmt.Sprint(m["summary"])
				if title == "" {
					title = fmt.Sprint(m["title"])
				}
				lines = append(lines, fmt.Sprintf("- %s", title))
			}
		}
	case "lark_base_records":
		records, _ := payload["records"].([]any)
		if records == nil {
			records, _ = payload["items"].([]any)
		}
		lines = append(lines, fmt.Sprintf("记录数量：%d", len(records)))
	case "web_search":
		results, _ := payload["results"].([]any)
		if results == nil {
			results, _ = payload["items"].([]any)
		}
		lines = append(lines, fmt.Sprintf("搜索结果：%d 条", len(results)))
		for i, r := range results {
			if i >= 3 {
				break
			}
			if m, ok := r.(map[string]any); ok {
				title := fmt.Sprint(m["title"])
				url := fmt.Sprint(m["url"])
				if title != "" {
					lines = append(lines, fmt.Sprintf("- %s: %s", title, url))
				}
			}
		}
	case "lark_im_search":
		messages, _ := payload["messages"].([]any)
		if messages == nil {
			messages, _ = payload["items"].([]any)
		}
		lines = append(lines, fmt.Sprintf("消息搜索结果：%d 条", len(messages)))
		for i, msg := range messages {
			if i >= 3 {
				break
			}
			if m, ok := msg.(map[string]any); ok {
				sender := fmt.Sprint(m["sender"])
				body := fmt.Sprint(m["content"])
				lines = append(lines, fmt.Sprintf("- %s: %s", sender, summarizeText(body, 80)))
			}
		}
	default:
		if data, ok := payload["data"].(map[string]any); ok {
			if br, ok := data["agent_reading"].(map[string]any); ok {
				kind := fmt.Sprint(br["kind"])
				total := fmt.Sprint(br["total_chars"])
				if total == "" {
					total = fmt.Sprint(br["total_rows"])
				}
				lines = append(lines, fmt.Sprintf("[%s chunk, total=%s]", kind, total))
			}
		}
		if len(lines) == 0 {
			return summarizeText(content, 800)
		}
	}
	if len(lines) == 0 {
		return summarizeText(content, 800)
	}
	result := strings.Join(lines, "\n")
	if len([]rune(result)) > 800 {
		result = string([]rune(result)[:800]) + "..."
	}
	return result
}

func extractToolResultStub(toolName, content string, isError bool) string {
	status := "成功"
	if isError {
		status = "失败"
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Sprintf("[%s: %s, empty output]", toolName, status)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return fmt.Sprintf("[%s: %s]", toolName, status)
	}
	var key string
	if id, ok := payload["message_id"].(string); ok && id != "" {
		key = "message_id=" + id
	} else if id, ok := payload["doc_token"].(string); ok && id != "" {
		key = "doc_token=" + id
	} else if id, ok := payload["spreadsheet_token"].(string); ok && id != "" {
		key = "spreadsheet_token=" + id
	} else if id, ok := payload["file_token"].(string); ok && id != "" {
		key = "file_token=" + id
	}
	if key != "" {
		return fmt.Sprintf("[%s: %s, %s]", toolName, status, key)
	}
	return fmt.Sprintf("[%s: %s]", toolName, status)
}

func smartToolSummary(toolName, fullResult string, isError bool) string {
	fullResult = strings.TrimSpace(fullResult)
	if fullResult == "" {
		return "[empty]"
	}
	if isError {
		lines := strings.Split(fullResult, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" && len([]rune(line)) > 10 {
				return "[ERROR] " + summarizeText(line, 200) + "; tool=" + toolName
			}
		}
		return "[ERROR] tool=" + toolName
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(fullResult), &payload); err == nil {
		var parts []string
		if ok, exists := payload["ok"].(bool); exists {
			if !ok {
				parts = append(parts, "ok=false")
			}
		}
		if data, ok := payload["data"].(map[string]any); ok {
			if br, ok := data["agent_reading"].(map[string]any); ok {
				parts = append(parts, fmt.Sprintf("chunk=%s", br["kind"]))
			}
		}
		if len(parts) > 0 {
			summary := strings.Join(parts, ", ")
			if len([]rune(summary)) < 400 {
				remaining := 500 - len([]rune(summary)) - 20
				if remaining > 100 {
					summary += "; " + summarizeText(fullResult, remaining)
				}
			}
			return summary
		}
	}
	return summarizeText(fullResult, 500)
}

func DefaultContextConfig() ContextConfig {
	return ContextConfig{
		AutoCompact:         true,
		CompactWatermark:    defaultCompactWatermark,
		ToolResultRetention: defaultToolResultRetention,
		SkillHTTPTimeout:    defaultSkillHTTPTimeout,
		SkillHTTPMaxBytes:   defaultSkillHTTPMaxBytes,
	}
}

func normalizeContextConfig(cfg ContextConfig) ContextConfig {
	if !cfg.AutoCompact && cfg.ContextWindowOverride == 0 && cfg.CompactWatermark == 0 && cfg.ToolResultRetention == 0 && cfg.SkillHTTPTimeout == 0 && cfg.SkillHTTPMaxBytes == 0 {
		cfg.AutoCompact = true
	}
	if cfg.CompactWatermark <= 0 {
		cfg.CompactWatermark = defaultCompactWatermark
	}
	if cfg.CompactWatermark < 50 {
		cfg.CompactWatermark = 50
	}
	if cfg.CompactWatermark > 92 {
		cfg.CompactWatermark = 92
	}
	if cfg.ToolResultRetention <= 0 {
		cfg.ToolResultRetention = defaultToolResultRetention
	}
	if cfg.ToolResultRetention > 12 {
		cfg.ToolResultRetention = 12
	}
	if cfg.SkillHTTPTimeout <= 0 {
		cfg.SkillHTTPTimeout = defaultSkillHTTPTimeout
	}
	if cfg.SkillHTTPTimeout < 5 {
		cfg.SkillHTTPTimeout = 5
	}
	if cfg.SkillHTTPTimeout > 300 {
		cfg.SkillHTTPTimeout = 300
	}
	if cfg.SkillHTTPMaxBytes <= 0 {
		cfg.SkillHTTPMaxBytes = defaultSkillHTTPMaxBytes
	}
	if cfg.SkillHTTPMaxBytes < 256*1024 {
		cfg.SkillHTTPMaxBytes = 256 * 1024
	}
	if cfg.SkillHTTPMaxBytes > 50*1024*1024 {
		cfg.SkillHTTPMaxBytes = 50 * 1024 * 1024
	}
	return cfg
}

func modelContextProfile(model string, cfg ContextConfig, toolDefs []map[string]any) ModelContextProfile {
	model = strings.TrimSpace(model)
	if model == "" {
		model = DefaultModel
	}
	window, source := defaultContextWindowForModel(model)
	if cfg.ContextWindowOverride > 0 {
		window = cfg.ContextWindowOverride
		source = "user_override"
	}
	maxOutput := defaultMaxOutputForModel(model)
	reserved := maxOutput
	if reserved > window/4 {
		reserved = window / 4
	}
	return ModelContextProfile{
		Model:                model,
		ContextWindow:        window,
		MaxOutputTokens:      maxOutput,
		ReservedOutputTokens: reserved,
		ToolSchemaTokens:     estimateAnyTokens(toolDefs),
		VisionTokensEstimate: defaultVisionTokenEstimate,
		Source:               source,
	}
}

func defaultContextWindowForModel(model string) (int, string) {
	lower := strings.ToLower(model)
	switch {
	case strings.Contains(lower, "1m") || strings.Contains(lower, "1000k"):
		return 1000000, "builtin"
	case strings.Contains(lower, "kimi-k2") || strings.Contains(lower, "k2.6"):
		return 256000, "builtin"
	case strings.Contains(lower, "qwen3") || strings.Contains(lower, "qwen-3"):
		return 128000, "builtin"
	case strings.Contains(lower, "gpt-5") || strings.Contains(lower, "claude") || strings.Contains(lower, "sonnet"):
		return 200000, "builtin"
	default:
		return defaultContextWindowTokens, "fallback"
	}
}

func defaultMaxOutputForModel(model string) int {
	lower := strings.ToLower(model)
	switch {
	case strings.Contains(lower, "gpt-5"), strings.Contains(lower, "sonnet"), strings.Contains(lower, "kimi"):
		return 32000
	default:
		return defaultMaxOutputTokens
	}
}

func estimateContextBudget(model string, cfg ContextConfig, messages []map[string]any, toolDefs []map[string]any, skillListing string, state conversationState) ContextBudgetSnapshot {
	profile := modelContextProfile(model, cfg, toolDefs)
	messageTokens := estimateMessagesTokens(messages)
	skillTokens := estimateTextTokens(skillListing)
	estimate := messageTokens + profile.ToolSchemaTokens + skillTokens + profile.ReservedOutputTokens
	if state.EstimatorScale > 0 {
		estimate = int(math.Ceil(float64(estimate) * state.EstimatorScale))
	}
	used := 0
	if profile.ContextWindow > 0 {
		used = int(math.Round(float64(estimate) / float64(profile.ContextWindow) * 100))
	}
	if used < 0 {
		used = 0
	}
	if used > 100 {
		used = 100
	}
	watermark, nextAction := budgetWatermark(used, cfg)
	lastCompacted := ""
	if !state.LastCompactedAt.IsZero() {
		lastCompacted = state.LastCompactedAt.Format(time.RFC3339)
	}
	return ContextBudgetSnapshot{
		Model:             profile.Model,
		ContextWindow:     profile.ContextWindow,
		EstimatedTokens:   estimate,
		UsedPercent:       used,
		RemainingTokens:   profile.ContextWindow - estimate,
		ToolResultTokens:  estimateToolResultTokens(messages),
		SkillTokens:       skillTokens,
		Watermark:         watermark,
		NextAction:        nextAction,
		LastCompactedAt:   lastCompacted,
		SummaryRange:      state.SummaryRange,
		EstimatorAdjusted: state.EstimatorScale > 0 && math.Abs(state.EstimatorScale-1) > 0.05,
	}
}

func budgetWatermark(used int, cfg ContextConfig) (string, string) {
	cfg = normalizeContextConfig(cfg)
	switch {
	case used >= 92:
		return "blocking", "上下文接近上限：先压缩上下文，或由用户确认丢弃旧上下文。"
	case used >= 85:
		return "critical", "强制使用摘要 + 最近活跃窗口 + 必要引用。"
	case used >= cfg.CompactWatermark:
		return "compact", "后台生成或刷新结构化摘要。"
	case used >= 60:
		return "microcompact", "优先清理旧工具结果，保留最近工具输出。"
	default:
		return "ok", "无需处理。"
	}
}

func estimateMessagesTokens(messages []map[string]any) int {
	total := 0
	for _, msg := range messages {
		total += estimateAnyTokens(msg)
	}
	return total
}

func estimateToolResultTokens(messages []map[string]any) int {
	total := 0
	for _, msg := range messages {
		role, _ := msg["role"].(string)
		if role != "tool" {
			continue
		}
		total += estimateAnyTokens(msg["content"])
	}
	return total
}

func estimateAnyTokens(value any) int {
	switch typed := value.(type) {
	case nil:
		return 0
	case string:
		return estimateTextTokens(typed)
	case []map[string]any:
		return estimateMessagesTokens(typed)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return estimateTextTokens(fmt.Sprint(typed))
		}
		return estimateTextTokens(string(data))
	}
}

func estimateTextTokens(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	runes := utf8.RuneCountInString(text)
	ascii := 0
	for _, r := range text {
		if r < 128 {
			ascii++
		}
	}
	nonASCII := runes - ascii
	// Conservative mixed-language heuristic: English ~4 chars/token, CJK
	// close to 1-2 chars/token, plus a small structural overhead.
	return int(math.Ceil(float64(ascii)/4.0 + float64(nonASCII)/1.6 + 8))
}

func updateEstimatorScale(current float64, estimated int, actual int) float64 {
	if estimated <= 0 || actual <= 0 {
		if current <= 0 {
			return 1
		}
		return current
	}
	ratio := float64(actual) / float64(estimated)
	if ratio < 0.5 {
		ratio = 0.5
	}
	if ratio > 2.5 {
		ratio = 2.5
	}
	if current <= 0 {
		current = 1
	}
	return current*0.7 + ratio*0.3
}
