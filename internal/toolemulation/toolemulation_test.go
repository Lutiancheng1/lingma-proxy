package toolemulation

import (
	"strings"
	"testing"
)

func TestLooksLikeMissedToolUseDetectsLocalToolAvoidance(t *testing.T) {
	cases := []string{
		"我需要使用终端工具来查看内存。",
		"由于当前环境限制，请手动运行 top。",
		"当前环境限制，我无法直接执行系统命令查看你的内存占用。",
		"你可以在终端中运行 top -l 1 | grep PhysMem。",
		"I need to read the file first.",
		"Let me use the web search tool.",
		"You can run the following command in your terminal.",
		"现在我需要切换到计划模式。",
		"现在我将编辑文件，在末尾追加一行 beta，然后生成 unified diff。",
	}
	for _, tc := range cases {
		if !LooksLikeMissedToolUse(tc) {
			t.Fatalf("LooksLikeMissedToolUse(%q) = false", tc)
		}
	}
}

func TestLooksLikeRefusalDetectsLocalAccessRefusals(t *testing.T) {
	cases := []string{
		"当前环境限制，我无法直接执行系统命令查看你的内存占用。",
		"我无法访问你的电脑或本机文件。",
		"I cannot execute commands in your local machine.",
		"I can't access your computer directly.",
	}
	for _, tc := range cases {
		if !LooksLikeRefusal(tc) {
			t.Fatalf("LooksLikeRefusal(%q) = false", tc)
		}
	}
}

func TestInferToolCallsFromTextConvertsMemoryRefusalToBash(t *testing.T) {
	calls := InferToolCallsFromText("当前无法执行系统命令。你可以运行 vm_stat 查看内存占用。", []ToolDef{{
		Name: "Bash",
		InputSchema: map[string]any{
			"properties": map[string]any{
				"command": map[string]any{"type": "string"},
			},
			"required": []any{"command"},
		},
	}})
	if len(calls) != 1 {
		t.Fatalf("call count = %d", len(calls))
	}
	if calls[0].Name != "Bash" {
		t.Fatalf("tool name = %q", calls[0].Name)
	}
	command, _ := calls[0].Arguments["command"].(string)
	if !strings.Contains(command, "vm_stat") || !strings.Contains(command, "memory_pressure") {
		t.Fatalf("unexpected command = %q", command)
	}
}

func TestLooksLikeMissedToolUseIgnoresFinalAnswers(t *testing.T) {
	text := "这个文件负责 HTTP API 路由和 OpenAI 兼容响应。"
	if LooksLikeMissedToolUse(text) {
		t.Fatalf("LooksLikeMissedToolUse(%q) = true", text)
	}
}

func TestInjectToolingIncludesAutoToolGuidance(t *testing.T) {
	prompt := InjectTooling("", []ToolDef{{
		Name:        "read_file",
		Description: "Read a text file.",
		InputSchema: map[string]any{
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
			"required": []any{"path"},
		},
	}}, ToolChoice{Mode: "auto"}, nil)
	if prompt == "" {
		t.Fatal("empty prompt")
	}
	for _, want := range []string{
		"tool_choice=auto means you must decide",
		"inspect a local file path",
		"Core tool syntax examples",
		"conceptual question",
		"NEVER ask the user to run a command",
		"Emit at most 5 independent tool actions",
		"exclude node_modules",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestCoreToolExamplesSupportsCodexStyleToolNames(t *testing.T) {
	prompt := InjectTooling("", []ToolDef{
		{
			Name:        "exec_command",
			Description: "Run a shell command",
			InputSchema: map[string]any{
				"properties": map[string]any{
					"cmd": map[string]any{"type": "string"},
				},
				"required": []any{"cmd"},
			},
		},
		{
			Name:        "apply_patch",
			Description: "Edit files",
			InputSchema: map[string]any{
				"properties": map[string]any{
					"patch": map[string]any{"type": "string"},
				},
				"required": []any{"patch"},
			},
		},
	}, ToolChoice{Mode: "auto"}, nil)

	for _, want := range []string{
		"Run shell commands, inspect memory/CPU/processes/ports, build or test code: use exec_command.",
		"Edit files: use apply_patch.",
		"\"tool\":\"exec_command\"",
		"\"cmd\":\"pwd\"",
		"\"tool\":\"apply_patch\"",
		"\"patch\":\"value\"",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestInjectToolingEditRuleFallsBackToExecCommandWhenNoPatchToolExists(t *testing.T) {
	prompt := InjectTooling("", []ToolDef{
		{
			Name:        "exec_command",
			Description: "Run a shell command",
			InputSchema: map[string]any{
				"properties": map[string]any{
					"cmd": map[string]any{"type": "string"},
				},
				"required": []any{"cmd"},
			},
		},
	}, ToolChoice{Mode: "auto"}, nil)

	if !strings.Contains(prompt, "use exec_command with targeted shell commands to modify the file") {
		t.Fatalf("prompt missing exec_command edit rule:\n%s", prompt)
	}
	if strings.Contains(prompt, "call patch or write_file") {
		t.Fatalf("prompt should not mention unavailable patch/write_file tools:\n%s", prompt)
	}
}

func TestExtractToolsSupportsResponsesFunctionShape(t *testing.T) {
	tools := ExtractTools([]any{
		map[string]any{
			"type":        "function",
			"name":        "exec_command",
			"description": "Runs a command",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"cmd": map[string]any{"type": "string"},
				},
				"required": []any{"cmd"},
			},
		},
	})
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "exec_command" {
		t.Fatalf("unexpected tool name %q", tools[0].Name)
	}
	props, _ := tools[0].InputSchema["properties"].(map[string]any)
	if _, ok := props["cmd"]; !ok {
		t.Fatalf("expected responses schema properties to be preserved")
	}
}

func TestExtractAnthropicToolsSkipsHostedWebSearch(t *testing.T) {
	tools := ExtractAnthropicTools([]any{
		map[string]any{
			"name": "web_search",
			"type": "web_search_20250305",
		},
		map[string]any{
			"name": "read_file",
			"input_schema": map[string]any{
				"type": "object",
			},
		},
	})
	if len(tools) != 1 {
		t.Fatalf("tool count = %d", len(tools))
	}
	if tools[0].Name != "read_file" {
		t.Fatalf("tool = %+v", tools[0])
	}
}

func TestParseActionBlocksMapsCommonToolAliases(t *testing.T) {
	text := "```json action\n{\"tool\":\"Bash\",\"parameters\":{\"command\":\"pwd\",\"extra\":true}}\n```"
	calls, clean, err := ParseActionBlocks(text, []ToolDef{{
		Name: "terminal",
		InputSchema: map[string]any{
			"properties": map[string]any{
				"command": map[string]any{"type": "string"},
			},
		},
	}}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if clean != "" {
		t.Fatalf("clean = %q", clean)
	}
	if len(calls) != 1 {
		t.Fatalf("call count = %d", len(calls))
	}
	if calls[0].Name != "terminal" {
		t.Fatalf("tool name = %q", calls[0].Name)
	}
	if _, ok := calls[0].Arguments["command"]; !ok {
		t.Fatalf("missing command arg: %+v", calls[0].Arguments)
	}
	if _, ok := calls[0].Arguments["extra"]; ok {
		t.Fatalf("unexpected extra arg: %+v", calls[0].Arguments)
	}
}

func TestParseActionBlocksMapsReadAlias(t *testing.T) {
	text := "```json action\n{\"name\":\"Read\",\"arguments\":{\"path\":\"/tmp/a.txt\"}}\n```"
	calls, _, err := ParseActionBlocks(text, []ToolDef{{Name: "read_file"}}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].Name != "read_file" {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestParseActionBlocksDropsCallsMissingRequiredArgs(t *testing.T) {
	text := "```json action\n{\"tool\":\"Read\",\"parameters\":{\"path\":\"/tmp/a.txt\"}}\n```"
	calls, clean, err := ParseActionBlocks(text, []ToolDef{{
		Name: "Read",
		InputSchema: map[string]any{
			"properties": map[string]any{
				"file_path": map[string]any{"type": "string"},
			},
			"required": []any{"file_path"},
		},
	}}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatalf("expected no calls, got %+v", calls)
	}
	if !strings.Contains(clean, "\"path\"") {
		t.Fatalf("clean should preserve unparseable action block, got %q", clean)
	}
}

func TestParseActionBlocksDropsUnknownToolNames(t *testing.T) {
	text := "```json action\n{\"tool\":\"apply_patch\",\"parameters\":{\"patch\":\"*** Begin Patch\"}}\n```"
	calls, clean, err := ParseActionBlocks(text, []ToolDef{{
		Name: "exec_command",
		InputSchema: map[string]any{
			"properties": map[string]any{
				"cmd": map[string]any{"type": "string"},
			},
		},
	}}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatalf("expected no calls, got %+v", calls)
	}
	if !strings.Contains(clean, "\"apply_patch\"") {
		t.Fatalf("clean should preserve unknown tool action block, got %q", clean)
	}
}

func TestParseActionBlocksDeduplicatesAndLimitsCalls(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 12; i++ {
		command := "pwd"
		if i%2 == 1 {
			command = "ls " + string(rune('a'+i))
		}
		b.WriteString("```json action\n")
		b.WriteString(`{"tool":"Bash","parameters":{"command":"` + command + `"}}`)
		b.WriteString("\n```\n")
	}

	calls, clean, err := ParseActionBlocks(b.String(), []ToolDef{{
		Name: "Bash",
		InputSchema: map[string]any{
			"properties": map[string]any{
				"command": map[string]any{"type": "string"},
			},
			"required": []any{"command"},
		},
	}}, Config{MaxToolCalls: 3})
	if err != nil {
		t.Fatal(err)
	}
	if clean != "" {
		t.Fatalf("clean = %q", clean)
	}
	if len(calls) != 3 {
		t.Fatalf("call count = %d, calls = %+v", len(calls), calls)
	}
	if calls[0].Arguments["command"] != "pwd" {
		t.Fatalf("first command = %+v", calls[0].Arguments)
	}
}
