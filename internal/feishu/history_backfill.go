package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	feishuHistoryBackfillTimeout = 7 * time.Second
	feishuHistoryBackfillLimit   = 12

	feishuHistorySearchTimeout = 10 * time.Second
	feishuHistorySearchLimit   = 10
)

func (m *Manager) fetchFeishuConversationBackfill(ctx context.Context, chatID string, currentMessageID string, resetAt time.Time, resetMessageID string, meta LogMeta) string {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return ""
	}
	runCtx, cancel := context.WithTimeout(ctx, feishuHistoryBackfillTimeout)
	defer cancel()
	cmd := commandContextWithEnv(runCtx, "lark-cli", "im", "+chat-messages-list",
		"--as", "bot",
		"--chat-id", chatID,
		"--sort", "desc",
		"--page-size", fmt.Sprint(feishuHistoryBackfillLimit),
		"--format", "json",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			m.logf("warn", "Feishu bridge 历史消息回填超时，跳过本轮回填", meta)
			return ""
		}
		m.logf("info", "Feishu bridge 历史消息回填不可用，跳过："+decodeCommandOutput(output), meta)
		return ""
	}
	text := renderFeishuHistoryBackfill(decodeCommandOutput(output), currentMessageID, resetAt, resetMessageID)
	if strings.TrimSpace(text) == "" {
		return ""
	}
	m.logf("info", "Feishu bridge 已从飞书 CLI 回填最近会话消息", meta)
	return text
}

func renderFeishuHistoryBackfill(output string, currentMessageID string, resetAt time.Time, resetMessageID string) string {
	items := extractFeishuHistoryMessages(output)
	if len(items) == 0 {
		trimmed := strings.TrimSpace(output)
		if trimmed == "" {
			return ""
		}
		if !resetAt.IsZero() {
			return ""
		}
		return "飞书历史回填（来自 lark-cli 最近消息，解析失败，仅供参考）：\n" + summarizeText(trimmed, 2200)
	}
	lines := make([]string, 0, len(items)+2)
	lines = append(lines,
		"飞书历史回填（来自 lark-cli im +chat-messages-list 的最近消息，仅用于恢复本地上下文；如果与当前任务冲突，以用户最新消息为准）：",
	)
	for i := len(items) - 1; i >= 0; i-- {
		msg, _ := items[i].(map[string]any)
		if msg == nil {
			continue
		}
		messageID := firstMapString(msg, "message_id", "messageId", "id")
		if messageID != "" && messageID == currentMessageID {
			continue
		}
		if messageID != "" && messageID == strings.TrimSpace(resetMessageID) {
			continue
		}
		if !historyMessageAfterReset(msg, resetAt) {
			continue
		}
		sender := senderDisplayName(msg)
		if sender == "" {
			sender = "unknown"
		}
		msgType := firstMapString(msg, "msg_type", "message_type", "type")
		content := historyMessageContent(msg)
		if content == "" {
			content = "[" + fallbackText(msgType, "message") + "]"
		}
		created := firstMapString(msg, "create_time", "createTime", "created_at")
		prefix := "- "
		if created != "" {
			prefix += created + " "
		}
		lines = append(lines, prefix+sender+": "+summarizeText(content, 260))
	}
	if len(lines) <= 1 {
		return ""
	}
	return truncatePreserveLines(strings.Join(lines, "\n"), 3200)
}

func historyMessageAfterReset(msg map[string]any, resetAt time.Time) bool {
	if resetAt.IsZero() {
		return true
	}
	created := firstMapString(msg, "create_time", "createTime", "created_at", "createdAt")
	if created == "" {
		return false
	}
	t, ok := parseFeishuHistoryTime(created)
	if !ok {
		return false
	}
	// Feishu CLI history timestamps are often second-precision while ResetAt is
	// recorded with sub-second precision. Compare against the second boundary so
	// a message sent immediately after /reset in the same second is not dropped.
	return !t.Before(resetAt.Truncate(time.Second))
}

func parseFeishuHistoryTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	if n, err := parseFeishuHistoryUnix(value); err == nil {
		switch {
		case n > 1_000_000_000_000_000:
			return time.Unix(0, n), true
		case n > 1_000_000_000_000:
			return time.UnixMilli(n), true
		default:
			return time.Unix(n, 0), true
		}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
		if t, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func parseFeishuHistoryUnix(value string) (int64, error) {
	for _, r := range strings.TrimSpace(value) {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a unix timestamp")
		}
	}
	var n int64
	_, err := fmt.Sscan(value, &n)
	return n, err
}

func extractFeishuHistoryMessages(output string) []any {
	var root any
	if err := json.Unmarshal([]byte(output), &root); err != nil {
		return nil
	}
	obj, _ := root.(map[string]any)
	if obj == nil {
		return nil
	}
	if messages, ok := obj["messages"].([]any); ok {
		return messages
	}
	if data, ok := obj["data"].(map[string]any); ok {
		if messages, ok := data["messages"].([]any); ok {
			return messages
		}
		if items, ok := data["items"].([]any); ok {
			return items
		}
	}
	if result, ok := obj["result"].(map[string]any); ok {
		if messages, ok := result["messages"].([]any); ok {
			return messages
		}
	}
	return nil
}

func senderDisplayName(msg map[string]any) string {
	for _, key := range []string{"sender", "sender_info", "senderInfo"} {
		sender, _ := msg[key].(map[string]any)
		if sender == nil {
			continue
		}
		if value := firstMapString(sender, "name", "display_name", "displayName", "open_id", "user_id", "id"); value != "" {
			return value
		}
	}
	return firstMapString(msg, "sender_name", "senderName", "sender_id", "senderId")
}

func historyMessageContent(msg map[string]any) string {
	if value := firstMapString(msg, "content", "body", "text"); value != "" {
		return normalizeHistoryContent(value)
	}
	for _, key := range []string{"message", "content_json"} {
		nested, _ := msg[key].(map[string]any)
		if nested == nil {
			continue
		}
		if value := firstMapString(nested, "content", "text"); value != "" {
			return normalizeHistoryContent(value)
		}
	}
	data, err := json.Marshal(msg["content"])
	if err != nil || string(data) == "null" {
		return ""
	}
	return normalizeHistoryContent(string(data))
}

func normalizeHistoryContent(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(value), &decoded); err == nil {
		for _, key := range []string{"text", "title", "content"} {
			if text, ok := decoded[key].(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	return value
}

func firstMapString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func searchFeishuHistory(ctx context.Context, chatID string, query string, limit int) string {
	chatID = strings.TrimSpace(chatID)
	query = strings.TrimSpace(query)
	if chatID == "" || query == "" {
		return ""
	}
	if limit <= 0 {
		limit = feishuHistorySearchLimit
	}
	runCtx, cancel := context.WithTimeout(ctx, feishuHistorySearchTimeout)
	defer cancel()
	cmd := commandContextWithEnv(runCtx, "lark-cli", "im", "+messages-search",
		"--as", "bot",
		"--chat-id", chatID,
		"--query", query,
		"--page-size", fmt.Sprint(limit),
		"--format", "json",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			return ""
		}
		return ""
	}
	return renderFeishuHistorySearch(decodeCommandOutput(output), query)
}

func renderFeishuHistorySearch(output string, query string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	messages := extractFeishuHistoryMessages(output)
	if len(messages) == 0 {
		return ""
	}
	lines := []string{fmt.Sprintf("飞书历史消息搜索「%s」结果（%d 条）：", query, len(messages))}
	for _, raw := range messages {
		msg, _ := raw.(map[string]any)
		if msg == nil {
			continue
		}
		sender := extractHistorySender(msg)
		content := extractHistoryContent(msg)
		ts := extractHistoryTimestamp(msg)
		content = summarizeText(content, 200)
		line := fmt.Sprintf("- [%s] %s: %s", ts, sender, content)
		lines = append(lines, line)
	}
	result := strings.Join(lines, "\n")
	runes := []rune(result)
	if len(runes) > 3000 {
		result = string(runes[:3000]) + "\n...(截断)"
	}
	return result
}

func extractHistorySender(msg map[string]any) string {
	if sender, ok := msg["sender"].(map[string]any); ok {
		if name, ok := sender["name"].(string); ok && name != "" {
			return name
		}
		if id, ok := sender["id"].(string); ok {
			return id
		}
	}
	if name, ok := msg["sender_name"].(string); ok {
		return name
	}
	return "unknown"
}

func extractHistoryContent(msg map[string]any) string {
	if content, ok := msg["content"].(string); ok {
		return content
	}
	if body, ok := msg["body"].(map[string]any); ok {
		if content, ok := body["content"].(string); ok {
			return content
		}
	}
	return ""
}

func extractHistoryTimestamp(msg map[string]any) string {
	if ts, ok := msg["create_time"].(string); ok {
		return ts
	}
	if ts, ok := msg["timestamp"].(string); ok {
		return ts
	}
	return ""
}

func isHistoryReference(text string) bool {
	patterns := []string{
		"上次", "之前", "我们讨论过", "你提到过", "记得吗",
		"那个文档", "那个链接", "那个表格", "那个文件",
		"之前说的", "前面提到", "聊过的",
	}
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

func extractHistoryQuery(text string) string {
	text = strings.TrimSpace(text)
	prefixes := []string{
		"上次我们讨论的", "上次说的", "之前讨论的", "之前提到的",
		"我们讨论过的", "你提到过的", "记得吗",
		"那个", "帮我找一下", "帮我搜一下",
	}
	for _, p := range prefixes {
		if idx := strings.Index(text, p); idx >= 0 {
			rest := strings.TrimSpace(text[idx+len(p):])
			if rest != "" {
				return rest
			}
		}
	}
	if len([]rune(text)) > 30 {
		return string([]rune(text)[:30])
	}
	return text
}
