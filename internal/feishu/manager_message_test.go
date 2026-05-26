package feishu

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHandleEventCallsLLMAndRepliesToMessage(t *testing.T) {
	replyLog := installFakeLarkCLI(t)
	oldDiscover := discoverSkillsForPrompt
	oldCallLLM := callLLMForConversation
	t.Cleanup(func() {
		discoverSkillsForPrompt = oldDiscover
		callLLMForConversation = oldCallLLM
	})
	discoverSkillsForPrompt = func(context.Context) ([]SkillStatus, error) {
		return []SkillStatus{{Name: "lark-im", Found: true}}, nil
	}
	callLLMForConversation = func(ctx context.Context, proxyURL string, model string, messages []map[string]any, forceToolUse bool, tools []map[string]any) (*llmResponse, error) {
		if proxyURL != "http://127.0.0.1:8095/v1/chat/completions" {
			t.Fatalf("unexpected proxy URL: %s", proxyURL)
		}
		if model != "kmodel" {
			t.Fatalf("unexpected model: %s", model)
		}
		if forceToolUse {
			t.Fatalf("plain greeting should not force tool use")
		}
		var resp llmResponse
		resp.Choices = append(resp.Choices, struct {
			Message struct {
				Content   string     `json:"content"`
				ToolCalls []ToolCall `json:"tool_calls"`
			} `json:"message"`
		}{})
		resp.Choices[0].Message.Content = "收到，我可以正常回复。"
		return &resp, nil
	}

	manager := NewManager(ManagerOptions{
		ProxyURL: func() string { return "http://127.0.0.1:8095/v1/chat/completions" },
	})
	manager.SetConfig(Config{Model: "kmodel", MaxToolRounds: 5})
	manager.handleEvent(context.Background(), incomingEvent{
		ChatID:      "oc_test_chat",
		ChatType:    "p2p",
		Content:     "你好",
		CreateTime:  "1",
		EventID:     "evt_test_1",
		MessageID:   "om_test_message",
		SenderID:    "ou_test_user",
		MessageType: "text",
	})

	got := strings.TrimSpace(string(mustReadFileContainingEventually(t, replyLog, "收到，我可以正常回复。")))
	if !strings.Contains(got, "im reactions create") {
		t.Fatalf("typing reaction create was not called, got: %s", got)
	}
	if !strings.Contains(got, "im reactions delete") {
		t.Fatalf("typing reaction cleanup was not called, got: %s", got)
	}
	if !strings.Contains(got, "emoji_type") || !strings.Contains(got, "Typing") {
		t.Fatalf("typing reaction did not use Typing emoji, got: %s", got)
	}
	if !strings.Contains(got, "--message-id om_test_message") {
		t.Fatalf("reply command did not include message id, got: %s", got)
	}
	if !strings.Contains(got, "收到，我可以正常回复。") {
		t.Fatalf("reply did not include LLM reply text, got: %s", got)
	}
	state := manager.getConversationState("oc_test_chat")
	if len(state.History) == 0 {
		t.Fatalf("conversation history was not stored")
	}
}

func TestHandleEventPassesFeishuImageToLLM(t *testing.T) {
	replyLog := installFakeLarkCLI(t)
	oldDiscover := discoverSkillsForPrompt
	oldCallLLM := callLLMForConversation
	oldPlainStream := callLLMPlainStreamForConversation
	t.Cleanup(func() {
		discoverSkillsForPrompt = oldDiscover
		callLLMForConversation = oldCallLLM
		callLLMPlainStreamForConversation = oldPlainStream
	})
	discoverSkillsForPrompt = func(context.Context) ([]SkillStatus, error) {
		return []SkillStatus{{Name: "lark-im", Found: true}}, nil
	}
	callLLMForConversation = func(ctx context.Context, proxyURL string, model string, messages []map[string]any, forceToolUse bool, tools []map[string]any) (*llmResponse, error) {
		t.Fatal("plain image question should not include tools")
		return nil, nil
	}
	callLLMPlainStreamForConversation = func(ctx context.Context, proxyURL string, model string, messages []map[string]any, deltas streamingDelta) (*llmResponse, error) {
		parts, ok := messages[len(messages)-1]["content"].([]map[string]any)
		if !ok {
			t.Fatalf("expected multimodal content parts, got %#v", messages[len(messages)-1]["content"])
		}
		if len(parts) != 2 {
			t.Fatalf("expected text + image parts, got %#v", parts)
		}
		if parts[0]["type"] != "text" || !strings.Contains(parts[0]["text"].(string), "这个图片里有什么内容") || strings.Contains(parts[0]["text"].(string), "[Image:") {
			t.Fatalf("unexpected text part: %#v", parts[0])
		}
		imageURL, _ := parts[1]["image_url"].(map[string]any)
		url, _ := imageURL["url"].(string)
		if parts[1]["type"] != "image_url" || !strings.HasPrefix(url, "data:image/png;base64,") {
			t.Fatalf("unexpected image part: %#v", parts[1])
		}
		var resp llmResponse
		resp.Choices = append(resp.Choices, struct {
			Message struct {
				Content   string     `json:"content"`
				ToolCalls []ToolCall `json:"tool_calls"`
			} `json:"message"`
		}{})
		resp.Choices[0].Message.Content = "看到了图片。"
		return &resp, nil
	}

	manager := NewManager(ManagerOptions{
		ProxyURL: func() string { return "http://127.0.0.1:8095/v1/chat/completions" },
	})
	manager.SetConfig(Config{Model: "kmodel", MaxToolRounds: 5})
	manager.handleEvent(context.Background(), incomingEvent{
		ChatID:      "oc_image_chat",
		ChatType:    "p2p",
		Content:     "[Image: img_test_image] 这个图片里有什么内容?",
		CreateTime:  "1",
		EventID:     "evt_image_1",
		MessageID:   "om_image_message",
		SenderID:    "ou_test_user",
		MessageType: "post",
	})

	got := strings.TrimSpace(string(mustReadFileContainingEventually(t, replyLog, "看到了图片。")))
	if !strings.Contains(got, "+messages-resources-download") {
		t.Fatalf("image resource was not downloaded, got: %s", got)
	}
	if !strings.Contains(got, "--file-key img_test_image") {
		t.Fatalf("image download used wrong file key, got: %s", got)
	}
}

func TestHandleEventSynthesizesFinalReplyAfterToolRounds(t *testing.T) {
	replyLog := installFakeLarkCLI(t)
	oldDiscover := discoverSkillsForPrompt
	oldCallLLM := callLLMForConversation
	t.Cleanup(func() {
		discoverSkillsForPrompt = oldDiscover
		callLLMForConversation = oldCallLLM
	})
	discoverSkillsForPrompt = func(context.Context) ([]SkillStatus, error) {
		return []SkillStatus{{Name: "lark-drive", Found: true}}, nil
	}
	callLLMForConversation = func(context.Context, string, string, []map[string]any, bool, []map[string]any) (*llmResponse, error) {
		var resp llmResponse
		resp.Choices = append(resp.Choices, struct {
			Message struct {
				Content   string     `json:"content"`
				ToolCalls []ToolCall `json:"tool_calls"`
			} `json:"message"`
		}{})
		var tc ToolCall
		tc.ID = "call_weekly"
		tc.Type = "function"
		tc.Function.Name = "lark_cli_exec"
		tc.Function.Arguments = `{"argv":["drive","file","list","--query","周报"]}`
		resp.Choices[0].Message.ToolCalls = []ToolCall{tc}
		return &resp, nil
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"查到 1 份周报：项目周报，内容摘要为本周完成 Feishu Bridge 调试。"}}]}`))
	}))
	defer proxy.Close()

	manager := NewManager(ManagerOptions{
		ProxyURL: func() string { return proxy.URL },
	})
	manager.SetConfig(Config{Model: "kmodel", MaxToolRounds: 1})
	manager.handleEvent(context.Background(), incomingEvent{
		ChatID:      "oc_weekly_chat",
		ChatType:    "p2p",
		Content:     "查一下周报内容",
		CreateTime:  "2",
		EventID:     "evt_weekly_1",
		MessageID:   "om_weekly_message",
		SenderID:    "ou_test_user",
		MessageType: "text",
	})

	got := string(mustReadFileContainingEventually(t, replyLog, "查到 1 份周报"))
	if strings.Contains(got, "已完成处理") {
		t.Fatalf("generic completion fallback leaked into reply: %s", got)
	}
}

func TestHandleEventGuidesThenStopsOnRepeatedIdenticalToolFailure(t *testing.T) {
	replyLog := installFakeLarkCLI(t)
	oldDiscover := discoverSkillsForPrompt
	oldCallLLM := callLLMForConversation
	t.Cleanup(func() {
		discoverSkillsForPrompt = oldDiscover
		callLLMForConversation = oldCallLLM
	})
	discoverSkillsForPrompt = func(context.Context) ([]SkillStatus, error) {
		return []SkillStatus{{Name: "lark-im", Found: true}}, nil
	}
	calls := 0
	callLLMForConversation = func(context.Context, string, string, []map[string]any, bool, []map[string]any) (*llmResponse, error) {
		calls++
		var resp llmResponse
		resp.Choices = append(resp.Choices, struct {
			Message struct {
				Content   string     `json:"content"`
				ToolCalls []ToolCall `json:"tool_calls"`
			} `json:"message"`
		}{})
		var tc ToolCall
		tc.ID = "call_bad"
		tc.Type = "function"
		tc.Function.Name = "lark_cli_exec"
		tc.Function.Arguments = `{"argv":["unknown","bad"]}`
		resp.Choices[0].Message.ToolCalls = []ToolCall{tc}
		return &resp, nil
	}

	manager := NewManager(ManagerOptions{
		ProxyURL: func() string { return "http://127.0.0.1:8095/v1/chat/completions" },
	})
	manager.SetConfig(Config{Model: "kmodel", MaxToolRounds: 24})
	manager.handleEvent(context.Background(), incomingEvent{
		ChatID:      "oc_repeat_fail_chat",
		ChatType:    "p2p",
		Content:     "查一下内容",
		CreateTime:  "2",
		EventID:     "evt_repeat_fail_1",
		MessageID:   "om_repeat_fail_message",
		SenderID:    "ou_test_user",
		MessageType: "text",
	})

	got := string(mustReadFileContainingEventually(t, replyLog, "同一个工具调用连续失败 4 次"))
	if calls != 4 {
		t.Fatalf("repeated identical failure should stop after four LLM tool attempts, got calls=%d log=%s", calls, got)
	}
	state := manager.getConversationState("oc_repeat_fail_chat")
	foundGuidance := false
	for _, msg := range state.History {
		content, _ := msg["content"].(string)
		if strings.Contains(content, "lark-cli unknown --help") && strings.Contains(content, "不要原样重复") {
			foundGuidance = true
			break
		}
	}
	if !foundGuidance {
		t.Fatalf("expected tool result guidance to run help before stopping, history=%#v", state.History)
	}
}

func TestRemovedConversationPreferenceCommandsAreNotHandled(t *testing.T) {
	manager := NewManager(ManagerOptions{})
	for _, command := range []string{"/think off", "/lang en", "/at off", "/whoami"} {
		if reply, handled := manager.handleConversationCommand(context.Background(), "oc_removed_commands", "http://127.0.0.1:8095/v1/chat/completions", "kmodel", command, LogMeta{}); handled {
			t.Fatalf("%s should not be handled, got reply %q", command, reply)
		}
	}
}

func TestHandleEventMergesBurstMessagesPerConversation(t *testing.T) {
	replyLog := installFakeLarkCLI(t)
	oldDiscover := discoverSkillsForPrompt
	oldCallLLM := callLLMForConversation
	t.Cleanup(func() {
		discoverSkillsForPrompt = oldDiscover
		callLLMForConversation = oldCallLLM
	})
	discoverSkillsForPrompt = func(context.Context) ([]SkillStatus, error) {
		return []SkillStatus{{Name: "lark-im", Found: true}}, nil
	}
	var calls int
	callLLMForConversation = func(ctx context.Context, proxyURL string, model string, messages []map[string]any, forceToolUse bool, tools []map[string]any) (*llmResponse, error) {
		calls++
		last := messages[len(messages)-1]["content"].(string)
		if !strings.Contains(last, "第一句") || !strings.Contains(last, "第二句") {
			t.Fatalf("merged prompt missing burst messages: %q", last)
		}
		var resp llmResponse
		resp.Choices = append(resp.Choices, struct {
			Message struct {
				Content   string     `json:"content"`
				ToolCalls []ToolCall `json:"tool_calls"`
			} `json:"message"`
		}{})
		resp.Choices[0].Message.Content = "已合并处理。"
		return &resp, nil
	}

	manager := NewManager(ManagerOptions{
		ProxyURL: func() string { return "http://127.0.0.1:8095/v1/chat/completions" },
	})
	manager.SetConfig(Config{Model: "kmodel", MaxToolRounds: 5})
	go manager.handleEvent(context.Background(), incomingEvent{
		ChatID:      "oc_burst_chat",
		ChatType:    "p2p",
		Content:     "第一句",
		CreateTime:  "1",
		EventID:     "evt_burst_1",
		MessageID:   "om_burst_1",
		SenderID:    "ou_test_user",
		MessageType: "text",
	})
	time.Sleep(100 * time.Millisecond)
	manager.handleEvent(context.Background(), incomingEvent{
		ChatID:      "oc_burst_chat",
		ChatType:    "p2p",
		Content:     "第二句",
		CreateTime:  "2",
		EventID:     "evt_burst_2",
		MessageID:   "om_burst_2",
		SenderID:    "ou_test_user",
		MessageType: "text",
	})

	got := string(mustReadFileContainingEventually(t, replyLog, "im +messages-reply --as bot --message-id om_burst_2"))
	if calls != 1 {
		t.Fatalf("expected one merged LLM call, got %d; log=%s", calls, got)
	}
	if !strings.Contains(got, "已合并处理。") {
		t.Fatalf("merged reply body missing from log: %s", got)
	}
}

func TestHandleStopCancelsRunningConversation(t *testing.T) {
	replyLog := installFakeLarkCLI(t)
	oldDiscover := discoverSkillsForPrompt
	oldCallLLM := callLLMForConversation
	t.Cleanup(func() {
		discoverSkillsForPrompt = oldDiscover
		callLLMForConversation = oldCallLLM
	})
	discoverSkillsForPrompt = func(context.Context) ([]SkillStatus, error) {
		return []SkillStatus{{Name: "lark-im", Found: true}}, nil
	}
	callLLMForConversation = func(ctx context.Context, proxyURL string, model string, messages []map[string]any, forceToolUse bool, tools []map[string]any) (*llmResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	manager := NewManager(ManagerOptions{
		ProxyURL: func() string { return "http://127.0.0.1:8095/v1/chat/completions" },
	})
	manager.SetConfig(Config{Model: "kmodel", MaxToolRounds: 5})
	go manager.handleEvent(context.Background(), incomingEvent{
		ChatID:      "oc_stop_chat",
		ChatType:    "p2p",
		Content:     "跑一个长任务",
		CreateTime:  "1",
		EventID:     "evt_stop_1",
		MessageID:   "om_stop_1",
		SenderID:    "ou_test_user",
		MessageType: "text",
	})
	time.Sleep(conversationDebounceDelay + 150*time.Millisecond)
	manager.handleEvent(context.Background(), incomingEvent{
		ChatID:      "oc_stop_chat",
		ChatType:    "p2p",
		Content:     "/stop",
		CreateTime:  "2",
		EventID:     "evt_stop_2",
		MessageID:   "om_stop_2",
		SenderID:    "ou_test_user",
		MessageType: "text",
	})

	got := string(mustReadFileContainingEventually(t, replyLog, "已请求停止当前 Feishu Bridge 任务。"))
	if !strings.Contains(got, "--message-id om_stop_2") {
		t.Fatalf("/stop reply should target stop command message, got: %s", got)
	}
	_ = mustReadFileContainingEventually(t, replyLog, "当前任务已停止。")
}

func TestHandleEventInjectsQuotedMessage(t *testing.T) {
	replyLog := installFakeLarkCLI(t)
	oldDiscover := discoverSkillsForPrompt
	oldCallLLM := callLLMForConversation
	t.Cleanup(func() {
		discoverSkillsForPrompt = oldDiscover
		callLLMForConversation = oldCallLLM
	})
	discoverSkillsForPrompt = func(context.Context) ([]SkillStatus, error) {
		return []SkillStatus{{Name: "lark-im", Found: true}}, nil
	}
	callLLMForConversation = func(ctx context.Context, proxyURL string, model string, messages []map[string]any, forceToolUse bool, tools []map[string]any) (*llmResponse, error) {
		last := messages[len(messages)-1]["content"].(string)
		if !strings.Contains(last, "<quoted_message id=\"om_quoted\"") || !strings.Contains(last, "这是一条被引用的消息") {
			t.Fatalf("quoted message was not injected: %q", last)
		}
		var resp llmResponse
		resp.Choices = append(resp.Choices, struct {
			Message struct {
				Content   string     `json:"content"`
				ToolCalls []ToolCall `json:"tool_calls"`
			} `json:"message"`
		}{})
		resp.Choices[0].Message.Content = "已读取引用。"
		return &resp, nil
	}

	manager := NewManager(ManagerOptions{
		ProxyURL: func() string { return "http://127.0.0.1:8095/v1/chat/completions" },
	})
	manager.SetConfig(Config{Model: "kmodel", MaxToolRounds: 5})
	manager.handleEvent(context.Background(), incomingEvent{
		ChatID:           "oc_quote_chat",
		ChatType:         "p2p",
		Content:          "总结这条",
		CreateTime:       "1",
		EventID:          "evt_quote_1",
		MessageID:        "om_quote_1",
		ReplyToMessageID: "om_quoted",
		SenderID:         "ou_test_user",
		MessageType:      "text",
	})

	_ = mustReadFileContainingEventually(t, replyLog, "已读取引用。")
}

func TestCardWriterFallsBackToLegacyWhenCardKitCreateFails(t *testing.T) {
	replyLog := installFakeLarkCLI(t)
	t.Setenv("FEISHU_FAIL_CARDKIT_CREATE", "1")

	manager := NewManager(ManagerOptions{})
	card := newCardWriter(manager, "om_cardkit_root", "", "kmodel", LogMeta{MessageID: "om_cardkit_root"})
	card.SetReply("CardKit 不可用时走 legacy 卡片。")

	got := string(mustReadFileContainingEventually(t, replyLog, "--msg-type interactive"))
	if !strings.Contains(got, "--message-id om_cardkit_root") {
		t.Fatalf("legacy card reply should target root message, got: %s", got)
	}
	if strings.Contains(got, "/open-apis/cardkit/v1/cards//elements/") {
		t.Fatalf("should not stream update an empty CardKit entity id after create failure, got: %s", got)
	}
	if card.IsBroken() {
		t.Fatal("CardKit create failure should degrade to legacy card, not mark writer broken")
	}
}

func TestShouldIgnoreGroupMessageTreatsAnyMentionAsTrigger(t *testing.T) {
	manager := NewManager(ManagerOptions{})
	manager.SetConfig(Config{Model: "kmodel", GroupOnlyAtBot: true})
	manager.mu.Lock()
	manager.status.Auth.UserOpenID = "ou_logged_in_user_not_bot"
	manager.mu.Unlock()

	if manager.shouldIgnoreGroupMessage(incomingEvent{
		ChatID:         "oc_group",
		ChatType:       "group",
		MentionOpenIDs: []string{"ou_actual_bot"},
	}) {
		t.Fatal("group mention should trigger even when auth user open_id differs from mentioned bot id")
	}
	if !manager.shouldIgnoreGroupMessage(incomingEvent{
		ChatID:   "oc_group",
		ChatType: "group",
	}) {
		t.Fatal("unmentioned group message should be ignored by default")
	}
}

func TestFakeLarkCLIExecutesThroughCommandEnv(t *testing.T) {
	replyLog := installFakeLarkCLI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := commandContextWithEnv(ctx, "lark-cli", "im", "+messages-reply", "--as", "bot", "--message-id", "om_test", "--markdown", "hello")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fake lark-cli failed: %v output=%s", err, output)
	}
	got := string(mustReadFileContainingEventually(t, replyLog, "--message-id om_test"))
	if !strings.Contains(got, "--markdown hello") {
		t.Fatalf("fake lark-cli log missing markdown args: %s", got)
	}
}

func TestReplyToMessageSplitsLongMarkdown(t *testing.T) {
	replyLog := installFakeLarkCLI(t)
	manager := NewManager(ManagerOptions{})
	longReply := strings.Repeat("长内容\n", 900)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := manager.replyToMessage(ctx, "om_long_reply", longReply); err != nil {
		t.Fatal(err)
	}

	got := string(mustReadFileContainingEventually(t, replyLog, "（2/"))
	if count := strings.Count(got, "--message-id om_long_reply"); count < 2 {
		t.Fatalf("expected chunked replies, got %d commands: %s", count, got)
	}
	if !strings.Contains(got, "（1/") || !strings.Contains(got, "（2/") {
		t.Fatalf("chunk labels missing: %s", got)
	}
}

func TestAuthLoginToolRequestsBridgeLogin(t *testing.T) {
	result := executeToolContext(context.Background(), "lark_cli_exec", map[string]any{
		"argv": []any{"auth", "login"},
	})
	if !result.NeedsLogin {
		t.Fatalf("NeedsLogin = false, output=%s", result.Output)
	}
	if !result.IsError {
		t.Fatal("auth login should be reported as a controlled tool error")
	}
}

func TestPermissionRequirementDetectsNeedUserAuthorization(t *testing.T) {
	perm := parsePermissionRequirement(`{"ok":false,"error":{"message":"API call failed: need_user_authorization (user: )","hint":"current command requires user authorization"}}`)
	if !perm.NeedsLogin {
		t.Fatalf("NeedsLogin = false for need_user_authorization")
	}
}

func installFakeLarkCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	replyLog := filepath.Join(dir, "reply.log")
	if runtime.GOOS == "windows" {
		script := filepath.Join(dir, "lark-cli.cmd")
		content := `@echo off
setlocal
chcp 65001 >nul
echo %*>>%FEISHU_REPLY_LOG%
if "%1"=="im" goto im
if "%1"=="drive" goto drive
goto unexpected
:im
if "%2"=="+messages-resources-download" (
  echo %*>>%FEISHU_REPLY_LOG%
  echo fakepng>%CD%\img_test_image.dat
  exit /b 0
)
if "%2"=="messages" if "%3"=="get" (
  echo {"content":"这是一条被引用的消息"}
  exit /b 0
)
echo {"reaction_id":"re_typing"}
exit /b 0
:drive
echo weekly report: Feishu Bridge debug completed.
exit /b 0
:unexpected
echo unexpected lark-cli command: %* 1>&2
exit /b 1
`
		if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
		nextPath := dir + string(os.PathListSeparator) + os.Getenv("PATH")
		t.Setenv("PATH", nextPath)
		t.Setenv("Path", nextPath)
		t.Setenv("FEISHU_REPLY_LOG", replyLog)
		t.Setenv("FEISHU_SKIP_NPM_PREFIX_PROBE", "1")
		commandEnvOnce = sync.Once{}
		commandEnv = nil
		commandPath = ""
		return replyLog
	}
	script := filepath.Join(dir, "lark-cli")
	content := `#!/bin/sh
if [ "$1" = "im" ] && [ "$2" = "reactions" ] && [ "$3" = "create" ]; then
  printf '%s\n' "$*" >> "$FEISHU_REPLY_LOG"
  printf '{"reaction_id":"re_typing"}\n'
  exit 0
fi
if [ "$1" = "im" ] && [ "$2" = "reactions" ] && [ "$3" = "delete" ]; then
  printf '%s\n' "$*" >> "$FEISHU_REPLY_LOG"
  exit 0
fi
if [ "$1" = "im" ] && [ "$2" = "+messages-reply" ]; then
  printf '%s\n' "$*" >> "$FEISHU_REPLY_LOG"
  printf '{"message_id":"om_fake_reply_%s"}\n' "$(date +%s)"
  exit 0
fi
if [ "$1" = "im" ] && [ "$2" = "+messages-resources-download" ]; then
  printf '%s\n' "$*" >> "$FEISHU_REPLY_LOG"
  output="img_test_image.dat"
  while [ "$#" -gt 0 ]; do
    if [ "$1" = "--output" ]; then
      shift
      output="$1"
      break
    fi
    shift
  done
  printf '\211PNG\r\n\032\nfakepng\n' > "$output"
  exit 0
fi
if [ "$1" = "im" ] && [ "$2" = "messages" ] && [ "$3" = "get" ]; then
  printf '{"content":"这是一条被引用的消息"}\n'
  exit 0
fi
# CardKit API: create card entity
if [ "$1" = "api" ] && [ "$2" = "POST" ] && [ "$3" = "/open-apis/cardkit/v1/cards" ]; then
  printf '%s\n' "$*" >> "$FEISHU_REPLY_LOG"
  if [ "$FEISHU_FAIL_CARDKIT_CREATE" = "1" ]; then
    printf 'cardkit create denied\n' >&2
    exit 1
  fi
  printf '{"card_id":"card_fake_%s"}\n' "$(date +%s)"
  exit 0
fi
# CardKit API: stream update element content
if [ "$1" = "api" ] && [ "$2" = "PUT" ]; then
  printf '%s\n' "$*" >> "$FEISHU_REPLY_LOG"
  exit 0
fi
# CardKit API: PATCH im message
if [ "$1" = "api" ] && [ "$2" = "PATCH" ]; then
  printf '%s\n' "$*" >> "$FEISHU_REPLY_LOG"
  exit 0
fi
if [ "$1" = "drive" ]; then
  printf '项目周报：本周完成 Feishu Bridge 调试。\n'
  exit 0
fi
printf 'unexpected lark-cli command: %s\n' "$*" >&2
exit 1
`
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	nextPath := dir + string(os.PathListSeparator) + os.Getenv("PATH")
	t.Setenv("PATH", nextPath)
	if runtime.GOOS == "windows" {
		t.Setenv("Path", nextPath)
	}
	t.Setenv("FEISHU_REPLY_LOG", replyLog)
	t.Setenv("FEISHU_SKIP_NPM_PREFIX_PROBE", "1")
	commandEnvOnce = sync.Once{}
	commandEnv = nil
	commandPath = ""
	return replyLog
}

func mustReadFileEventually(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			return data
		}
		if time.Now().After(deadline) {
			t.Fatalf("file %s was not written: %v", path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func mustReadFileContainingEventually(t *testing.T, path string, needle string) []byte {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last []byte
	var lastErr error
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			last = data
			if strings.Contains(string(data), needle) {
				return data
			}
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			t.Fatalf("file %s did not contain %q, last=%q err=%v", path, needle, string(last), lastErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
