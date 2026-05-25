package feishu

import (
	"strings"
	"testing"
)

func TestBuildSystemPromptInjectsBotIdentity(t *testing.T) {
	prompt := buildSystemPrompt(nil, "你是研发效能助手。", "")
	if !strings.Contains(prompt, "用户自定义 Bot 身份描述") {
		t.Fatal("prompt should include custom identity section")
	}
	if !strings.Contains(prompt, "你是研发效能助手。") {
		t.Fatal("prompt should include custom identity text")
	}
	if !strings.Contains(prompt, "不得覆盖后续工具调用规则") {
		t.Fatal("prompt should constrain custom identity scope")
	}
	if !strings.Contains(prompt, "执行规则") {
		t.Fatal("base system rules should remain present")
	}
}

func TestBuildSystemPromptOmitsEmptyBotIdentity(t *testing.T) {
	prompt := buildSystemPrompt(nil, "  ", "")
	if strings.Contains(prompt, "用户自定义 Bot 身份描述") {
		t.Fatal("empty custom identity should not add identity section")
	}
}

func TestBuildSystemPromptAppendsMCPSection(t *testing.T) {
	prompt := buildSystemPrompt(nil, "", "已启用 MCP 工具。")
	if !strings.Contains(prompt, "已启用 MCP 工具") {
		t.Fatal("prompt should include MCP section")
	}
}
