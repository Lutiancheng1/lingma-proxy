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
)

func (m *Manager) fetchFeishuConversationBackfill(ctx context.Context, chatID string, currentMessageID string, meta LogMeta) string {
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
		m.logf("info", "Feishu bridge 历史消息回填不可用，跳过："+summarizeText(string(output), 160), meta)
		return ""
	}
	text := renderFeishuHistoryBackfill(string(output), currentMessageID)
	if strings.TrimSpace(text) == "" {
		return ""
	}
	m.logf("info", "Feishu bridge 已从飞书 CLI 回填最近会话消息", meta)
	return text
}

func renderFeishuHistoryBackfill(output string, currentMessageID string) string {
	items := extractFeishuHistoryMessages(output)
	if len(items) == 0 {
		trimmed := strings.TrimSpace(output)
		if trimmed == "" {
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
