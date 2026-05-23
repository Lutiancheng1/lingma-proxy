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
	callLLMForConversation = func(ctx context.Context, proxyURL string, model string, messages []map[string]any, forceToolUse bool) (*llmResponse, error) {
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

	got := strings.TrimSpace(string(mustReadFileContainingEventually(t, replyLog, "--markdown 收到，我可以正常回复。")))
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
	if !strings.Contains(got, "--markdown 收到，我可以正常回复。") {
		t.Fatalf("reply command did not include LLM reply, got: %s", got)
	}
	state := manager.getConversationState("oc_test_chat")
	if len(state.History) == 0 {
		t.Fatalf("conversation history was not stored")
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
	callLLMForConversation = func(context.Context, string, string, []map[string]any, bool) (*llmResponse, error) {
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
	deadline := time.Now().Add(2 * time.Second)
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
	deadline := time.Now().Add(2 * time.Second)
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
