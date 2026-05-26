package feishu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSystemPromptInjectsBotIdentity(t *testing.T) {
	prompt := buildSystemPrompt(nil, "你是研发效能助手。", "", "")
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
	prompt := buildSystemPrompt(nil, "  ", "", "")
	if strings.Contains(prompt, "用户自定义 Bot 身份描述") {
		t.Fatal("empty custom identity should not add identity section")
	}
}

func TestBuildSystemPromptAppendsMCPSection(t *testing.T) {
	prompt := buildSystemPrompt(nil, "", "已启用 MCP 工具。", "")
	if !strings.Contains(prompt, "已启用 MCP 工具") {
		t.Fatal("prompt should include MCP section")
	}
}

func TestBuildSystemPromptGuidesIdentityAndDriveQueries(t *testing.T) {
	prompt := buildSystemPrompt(nil, "", "", "")
	for _, want := range []string{
		`["auth","list"]`,
		"禁止用日历、任务、云盘等无关业务工具",
		`lark_skill_view {"name":"lark-drive"}`,
		"has_more/page_token",
		"任务路由速查",
		`lark_skill_view {"name":"lark-sheets"}`,
		"不要猜 Sheet1、0、1",
		"unknown flag: --as",
		"只能逐字复制工具结果里真实出现的 url/link 字段",
		"drive +search 不支持 --limit",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
}

func TestBuildSystemPromptUsesLarkSkillIndexOnly(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "lark-doc")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\ndescription: 飞书云文档读取和创建\nwhen_to_use: 用户需要读取或创建云文档\n---\n# lark-doc\n\n完整正文不应默认注入。\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	prompt := buildSystemPrompt([]SkillStatus{
		{Name: "lark-doc", Found: true, Path: skillDir},
		{Name: "lark-sheets", Found: false},
	}, "", "", "")
	if !strings.Contains(prompt, "官方 lark-cli Skills 索引") {
		t.Fatal("prompt should include official lark skill index")
	}
	if !strings.Contains(prompt, "lark-doc") || !strings.Contains(prompt, "lark-sheets") {
		t.Fatalf("prompt should list installed and missing skills: %s", prompt)
	}
	if !strings.Contains(prompt, "飞书云文档读取和创建") || !strings.Contains(prompt, "用户需要读取或创建云文档") {
		t.Fatalf("prompt should include skill metadata descriptions: %s", prompt)
	}
	if strings.Contains(prompt, "## lark-doc") {
		t.Fatalf("prompt should not inject full skill excerpt by default: %s", prompt)
	}
	if strings.Contains(prompt, "完整正文不应默认注入") {
		t.Fatalf("prompt should not inject full skill body by default: %s", prompt)
	}
}
