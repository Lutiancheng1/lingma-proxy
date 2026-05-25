package feishu

import (
	"reflect"
	"testing"
)

func TestBuildToolCommandNormalizesCommonShortcutMistakes(t *testing.T) {
	cases := []struct {
		name string
		argv []any
		want []string
	}{
		{
			name: "im chats list",
			argv: []any{"im", "chats", "list", "--limit", "10"},
			want: []string{"lark-cli", "im", "+chat-list", "--limit", "10", "--as", "user"},
		},
		{
			name: "im messages send",
			argv: []any{"lark-cli", "im", "messages", "send", "--chat-id", "oc_1", "--text", "hi"},
			want: []string{"lark-cli", "im", "+messages-send", "--chat-id", "oc_1", "--text", "hi", "--as", "user"},
		},
		{
			name: "calendar agenda",
			argv: []any{"calendar", "agenda"},
			want: []string{"lark-cli", "calendar", "+agenda", "--as", "user"},
		},
		{
			name: "docs fetch",
			argv: []any{"docs", "fetch", "--doc", "doc_1", "--as", "bot"},
			want: []string{"lark-cli", "docs", "+fetch", "--doc", "doc_1", "--as", "bot"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildToolCommand("lark_cli_exec", map[string]any{"argv": tc.argv})
			if err != nil {
				t.Fatalf("buildToolCommand: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("command = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestToolDefinitionsWithMCPAddsDynamicTools(t *testing.T) {
	defs := toolDefinitionsWithMCP([]mcpTool{{
		Server:      "playwright",
		Name:        "browser_navigate",
		Function:    "mcp__playwright__browser_navigate",
		Description: "Navigate",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"url": map[string]any{"type": "string"}},
			"required":   []string{"url"},
		},
	}})
	found := false
	for _, def := range defs {
		fn, _ := def["function"].(map[string]any)
		if fn == nil || fn["name"] != "mcp__playwright__browser_navigate" {
			continue
		}
		found = true
		params, _ := fn["parameters"].(map[string]any)
		if params["required"] == nil {
			t.Fatalf("dynamic MCP tool should preserve inputSchema: %#v", params)
		}
	}
	if !found {
		t.Fatalf("dynamic MCP tool definition not found in %#v", defs)
	}
}
