package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"lingma-ipc-proxy/internal/toolemulation"
)

func TestIsRecoverableIPCError(t *testing.T) {
	cases := []error{
		errors.New("write websocket frame: write tcp 127.0.0.1:64954->127.0.0.1:36510: use of closed network connection"),
		errors.New("broken pipe"),
		errors.New("Lingma IPC notification stream closed"),
	}
	for _, err := range cases {
		if !isRecoverableIPCError(err) {
			t.Fatalf("expected recoverable error: %v", err)
		}
	}
}

func TestIsRecoverableIPCErrorIgnoresModelErrors(t *testing.T) {
	if isRecoverableIPCError(errors.New("timed out while waiting for Lingma IPC to finish responding")) {
		t.Fatal("timeout should not be treated as an immediate reconnect retry")
	}
}

func TestNewKeepsZeroTimeoutUnlimited(t *testing.T) {
	svc := New(Config{Timeout: 0})
	if svc.cfg.Timeout != 0 {
		t.Fatalf("timeout = %v, want 0", svc.cfg.Timeout)
	}
}

func TestContextWithOptionalTimeoutZeroDoesNotSetDeadline(t *testing.T) {
	ctx, cancel := contextWithOptionalTimeout(context.Background(), 0)
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("zero timeout should not set a deadline")
	}
}

func TestContextWithOptionalTimeoutPositiveSetsDeadline(t *testing.T) {
	ctx, cancel := contextWithOptionalTimeout(context.Background(), time.Second)
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("positive timeout should set a deadline")
	}
}

func TestDescribeIPCSetupErrorClarifiesClosedLingmaBackend(t *testing.T) {
	err := describeIPCSetupError("session setup", context.DeadlineExceeded)
	if err == nil {
		t.Fatal("expected wrapped error")
	}
	text := err.Error()
	if !strings.Contains(text, "session setup timed out") || !strings.Contains(text, "重新打开 Lingma") {
		t.Fatalf("unexpected error text: %s", text)
	}
}

func TestBuildLingmaPromptOnlyInjectsToolingWhenEmulationEnabled(t *testing.T) {
	req := ChatRequest{
		Messages: []ChatMessage{{Role: "user", Text: "查看项目结构"}},
		Tools: []toolemulation.ToolDef{{
			Name: "Bash",
			InputSchema: map[string]any{
				"properties": map[string]any{
					"command": map[string]any{"type": "string"},
				},
				"required": []any{"command"},
			},
		}},
		ToolChoice: toolemulation.ToolChoice{Mode: "auto"},
	}

	remotePrompt, err := buildLingmaPrompt(req, SessionModeFresh, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(remotePrompt, "```json action") || strings.Contains(remotePrompt, "DIRECT tool access") {
		t.Fatalf("remote prompt should not include tool emulation:\n%s", remotePrompt)
	}

	ipcPrompt, err := buildLingmaPrompt(req, SessionModeFresh, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ipcPrompt, "```json action") || !strings.Contains(ipcPrompt, "DIRECT tool access") {
		t.Fatalf("ipc prompt should include tool emulation:\n%s", ipcPrompt)
	}
}

func TestShouldRetryRemoteNativeToolForContinuationText(t *testing.T) {
	req := ChatRequest{
		Tools: []toolemulation.ToolDef{{Name: "Bash"}},
		ToolChoice: toolemulation.ToolChoice{
			Mode: "auto",
		},
	}
	if !shouldRetryRemoteNativeTool(req, "让我查看一下项目的整体结构，特别是源代码目录：") {
		t.Fatal("expected continuation text to trigger native tool retry")
	}
	if shouldRetryRemoteNativeTool(req, "这是一个 uni-app 项目，核心目录是 src。") {
		t.Fatal("substantive answer should not trigger retry")
	}
	req.ToolChoice = toolemulation.ToolChoice{Mode: "none"}
	if shouldRetryRemoteNativeTool(req, "让我查看一下：") {
		t.Fatal("tool_choice none should not trigger retry")
	}
}

func TestBuildLingmaPromptKeepsToolResultsForIPC(t *testing.T) {
	req := ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Text: "查看项目"},
			{Role: "assistant", ToolCalls: []toolemulation.ToolCall{{ID: "call_1", Name: "Bash", Arguments: map[string]any{"command": "pwd"}}}},
			{Role: "tool", ToolCallID: "call_1", Text: "/tmp/project"},
		},
		Tools:      []toolemulation.ToolDef{{Name: "Bash"}},
		ToolChoice: toolemulation.ToolChoice{Mode: "auto"},
	}
	prompt, err := buildLingmaPrompt(req, SessionModeFresh, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Tool result for call_1") || !strings.Contains(prompt, "/tmp/project") {
		t.Fatalf("ipc prompt should include tool result:\n%s", prompt)
	}
	if strings.Contains(prompt, "Assistant used tool") {
		t.Fatalf("ipc prompt should not include textualized assistant tool calls:\n%s", prompt)
	}
}

func TestRemoteImagesFromRequest(t *testing.T) {
	req := ChatRequest{Messages: []ChatMessage{{Role: "user", Text: "see", Images: []Image{{MediaType: "image/png", Data: "AAAA"}}}}}
	images := remoteImagesFromRequest(req)
	if len(images) != 1 {
		t.Fatalf("images = %#v", images)
	}
	if images[0].MediaType != "image/png" || images[0].Data != "AAAA" {
		t.Fatalf("unexpected image = %#v", images[0])
	}
}

func TestRequestHasImages(t *testing.T) {
	if requestHasImages(ChatRequest{Messages: []ChatMessage{{Role: "user", Text: "plain"}}}) {
		t.Fatal("plain request should not have images")
	}
	if !requestHasImages(ChatRequest{Messages: []ChatMessage{{Role: "user", Images: []Image{{URL: "file:///tmp/a.png"}}}}}) {
		t.Fatal("image URL request should have images")
	}
}

func TestRequestForImageContextUsesLatestImageTurnOnly(t *testing.T) {
	req := ChatRequest{
		System: "old system",
		Messages: []ChatMessage{
			{Role: "user", Text: "旧问题"},
			{Role: "assistant", Text: "旧回答"},
			{Role: "user", Text: "[Image #1] 这个图片是什么?", Images: []Image{{MediaType: "image/png", Data: "AAAA"}}},
		},
		Tools: []toolemulation.ToolDef{{
			Name: "Bash",
			InputSchema: map[string]any{
				"required": []any{"command"},
			},
		}},
		ToolChoice: toolemulation.ToolChoice{Mode: "auto"},
	}

	out := requestForImageContext(req)
	if out.System != "" {
		t.Fatalf("system = %q, want empty", out.System)
	}
	if len(out.Tools) != 0 || out.ToolChoice.Mode != "none" {
		t.Fatalf("tools should be disabled: tools=%#v choice=%#v", out.Tools, out.ToolChoice)
	}
	if len(out.Messages) != 1 {
		t.Fatalf("messages = %#v, want one compact image turn", out.Messages)
	}
	message := out.Messages[0]
	if message.Role != "user" || len(message.Images) != 1 || message.Images[0].Data != "AAAA" {
		t.Fatalf("unexpected image message = %#v", message)
	}
	if strings.Contains(message.Text, "旧问题") || !strings.Contains(message.Text, "忽略更早的对话历史") {
		t.Fatalf("unexpected compact prompt = %q", message.Text)
	}
}

func TestExtractLastUserImagesFindsPreviousImageTurn(t *testing.T) {
	images := extractLastUserImages([]ChatMessage{
		{Role: "user", Text: "看这张图", Images: []Image{{URL: "file:///tmp/a.png"}}},
		{Role: "assistant", Text: "这是一张图片"},
		{Role: "user", Text: "继续基于上图分析"},
	})
	if len(images) != 1 || images[0].URL != "file:///tmp/a.png" {
		t.Fatalf("images = %#v", images)
	}
}

func TestRequestWithImageContextRemovesImagesAndAppendsContext(t *testing.T) {
	req := ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Text: "看图", Images: []Image{{URL: "file:///tmp/a.png"}}},
			{Role: "assistant", Text: "好的"},
			{Role: "user", Text: "继续分析"},
		},
	}
	out := requestWithImageContext(req, "海边礁石和海浪")
	for _, message := range out.Messages {
		if len(message.Images) > 0 {
			t.Fatalf("images should be removed: %#v", out.Messages)
		}
	}
	if !strings.Contains(out.Messages[2].Text, "[图片上下文]") || !strings.Contains(out.Messages[2].Text, "海边礁石和海浪") {
		t.Fatalf("latest user message missing image context: %#v", out.Messages[2])
	}
}
