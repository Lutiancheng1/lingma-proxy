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

func TestRemoteFallbackModelsNormalizeAndDedupe(t *testing.T) {
	svc := New(Config{
		Backend:              BackendRemote,
		RemoteFallbackModels: []string{"Kimi-K2.6", "kmodel", "MiniMax-M2.7"},
	})
	got := svc.remoteFallbackModels()
	want := []string{"kmodel", "mmodel"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("fallback models = %v, want %v", got, want)
	}
}

func TestShouldProbeRemoteModelForListOnlyKnownMissingAliases(t *testing.T) {
	if !shouldProbeRemoteModelForList("Kimi-K2.6") {
		t.Fatal("expected Kimi alias to be probed")
	}
	if !shouldProbeRemoteModelForList("MiniMax-M2.7") {
		t.Fatal("expected MiniMax alias to be probed")
	}
	if shouldProbeRemoteModelForList("dashscope_qwen3_coder") {
		t.Fatal("unexpected probe for normal list model")
	}
}

func TestRemoteModelDisplayNameForVerifiedFallbackAliases(t *testing.T) {
	cases := map[string]string{
		"kmodel":                "Kimi-K2.6",
		"mmodel":                "MiniMax-M2.7",
		"some-enterprise-model": "some-enterprise-model",
	}
	for input, want := range cases {
		if got := remoteModelDisplayName(input); got != want {
			t.Fatalf("remoteModelDisplayName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDescribeIPCSetupErrorClarifiesClosedLingmaBackend(t *testing.T) {
	err := describeIPCSetupError("session setup", context.DeadlineExceeded)
	if err == nil {
		t.Fatal("expected wrapped error")
	}
	text := err.Error()
	if !strings.Contains(text, "session setup timed out") || !strings.Contains(text, "重新打开 Lingma App、QoderCN App") {
		t.Fatalf("unexpected error text: %s", text)
	}
}

func TestBuildLingmaPromptInjectsToolingWhenEmulationEnabled(t *testing.T) {
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

func TestShouldEmulateRemoteToolsForToolRequests(t *testing.T) {
	req := ChatRequest{
		Messages: []ChatMessage{{Role: "user", Text: "查看项目结构"}},
		Tools:    []toolemulation.ToolDef{{Name: "Bash"}},
		ToolChoice: toolemulation.ToolChoice{
			Mode: "auto",
		},
	}
	if !shouldEmulateRemoteTools(req) {
		t.Fatal("remote tool requests should enable prompt tool emulation fallback")
	}

	req.ToolChoice = toolemulation.ToolChoice{Mode: "none"}
	if shouldEmulateRemoteTools(req) {
		t.Fatal("tool_choice none should disable remote prompt tool emulation")
	}
}

func TestRemoteMessagesForChatUsesPromptWhenToolEmulationEnabled(t *testing.T) {
	req := ChatRequest{
		System:   "original system",
		Messages: []ChatMessage{{Role: "user", Text: "查看项目结构"}},
		Tools:    []toolemulation.ToolDef{{Name: "Bash"}},
		ToolChoice: toolemulation.ToolChoice{
			Mode: "auto",
		},
	}

	messages := remoteMessagesForChat(req, "User: 查看项目结构\n\nDIRECT tool access\n\nAssistant:", true)
	if len(messages) != 1 {
		t.Fatalf("messages = %#v", messages)
	}
	if messages[0].Role != "user" || !strings.Contains(messages[0].Content, "DIRECT tool access") {
		t.Fatalf("expected emulated prompt message, got %#v", messages)
	}

	plain := remoteMessagesForChat(req, "ignored", false)
	if len(plain) != 2 || plain[0].Role != "system" || plain[1].Content != "查看项目结构" {
		t.Fatalf("expected structured messages without emulation, got %#v", plain)
	}
}

func TestBuildLingmaPromptIncludesReasoningHintOnlyWhenRequested(t *testing.T) {
	req := ChatRequest{
		Messages:        []ChatMessage{{Role: "user", Text: "解释这个函数"}},
		ReasoningEffort: "high",
	}
	prompt, err := buildLingmaPrompt(req, SessionModeFresh, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Reasoning mode is enabled") {
		t.Fatalf("prompt should include reasoning hint:\n%s", prompt)
	}

	plainPrompt, err := buildLingmaPrompt(ChatRequest{
		Messages: []ChatMessage{{Role: "user", Text: "解释这个函数"}},
	}, SessionModeFresh, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plainPrompt, "Reasoning mode is enabled") {
		t.Fatalf("plain prompt should not include reasoning hint:\n%s", plainPrompt)
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

func TestBuildLingmaPromptUsesImageFallbackForImageOnlyUser(t *testing.T) {
	req := ChatRequest{
		System:   "这张图片是什么？只用两句话回答。",
		Messages: []ChatMessage{{Role: "user", Images: []Image{{URL: "file:///tmp/a.jpg"}}}},
	}

	prompt, err := buildLingmaPrompt(req, SessionModeFresh, false)
	if err != nil {
		t.Fatalf("buildLingmaPrompt returned error: %v", err)
	}
	if !strings.Contains(prompt, "这张图片是什么") {
		t.Fatalf("prompt should include image fallback question, got %q", prompt)
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

func TestRemoteMessagesFromRequestMapsReasoningText(t *testing.T) {
	req := ChatRequest{Messages: []ChatMessage{
		{Role: "assistant", Text: "answer", ReasoningText: "prior thinking"},
	}}
	out := remoteMessagesFromRequest(req)
	if len(out) != 1 {
		t.Fatalf("message count = %d", len(out))
	}
	if out[0].ReasoningText != "prior thinking" {
		t.Fatalf("ReasoningText = %q, want %q", out[0].ReasoningText, "prior thinking")
	}
}

func TestRemoteMessagesFromRequestKeepsReasoningOnlyTurn(t *testing.T) {
	req := ChatRequest{Messages: []ChatMessage{
		{Role: "assistant", ReasoningText: "only thinking"},
	}}
	out := remoteMessagesFromRequest(req)
	if len(out) != 1 || out[0].ReasoningText != "only thinking" {
		t.Fatalf("reasoning-only turn dropped or wrong: %#v", out)
	}
}

func TestRemoteToolEmulationDisabledByDefault(t *testing.T) {
	if remoteToolEmulationEnabled() {
		t.Fatal("tool emulation should be OFF by default (native tools)")
	}
	for _, v := range []string{"1", "true", "yes", "YES"} {
		t.Setenv("LINGMA_REMOTE_EMULATE_TOOLS", v)
		if !remoteToolEmulationEnabled() {
			t.Fatalf("LINGMA_REMOTE_EMULATE_TOOLS=%q should enable emulation", v)
		}
	}
	t.Setenv("LINGMA_REMOTE_EMULATE_TOOLS", "off")
	if remoteToolEmulationEnabled() {
		t.Fatal("LINGMA_REMOTE_EMULATE_TOOLS=off should keep emulation disabled")
	}
}

func TestRemoteMessagesForChatNativePreservesToolTurnsAndImages(t *testing.T) {
	req := ChatRequest{
		Tools:      []toolemulation.ToolDef{{Name: "get_image"}},
		ToolChoice: toolemulation.ToolChoice{Mode: "auto"},
		Messages: []ChatMessage{
			{Role: "user", Text: "look at this"},
			{Role: "assistant", ToolCalls: []toolemulation.ToolCall{{ID: "c1", Name: "get_image"}}},
			{Role: "tool", ToolCallID: "c1", Text: "here", Images: []Image{{MediaType: "image/png", Data: "abc"}}},
		},
	}
	// Native (emulateTools=false): full structured multi-message, tool turn + image preserved.
	native := remoteMessagesForChat(req, "FLATTENED PROMPT", false)
	if len(native) < 3 {
		t.Fatalf("native path should keep structured messages, got %d", len(native))
	}
	foundTool := false
	for _, m := range native {
		if m.Role == "tool" {
			foundTool = true
			if m.ToolCallID != "c1" || len(m.Images) == 0 {
				t.Fatalf("tool message lost id/images: %#v", m)
			}
		}
	}
	if !foundTool {
		t.Fatal("native path dropped the tool-role message")
	}
	// Emulation fallback (emulateTools=true): flattened to a single user prompt.
	flat := remoteMessagesForChat(req, "FLATTENED PROMPT", true)
	if len(flat) != 1 || flat[0].Role != "user" || flat[0].Content != "FLATTENED PROMPT" {
		t.Fatalf("emulation fallback should flatten to one user message, got %#v", flat)
	}
}
