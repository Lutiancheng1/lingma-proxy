package feishu

import (
	"context"
	"os"
	"reflect"
	"strings"
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
		{
			name: "sheets cell read",
			argv: []any{"sheets", "cell", "read", "--spreadsheet-token", "tok", "--range", "A1:D10"},
			want: []string{"lark-cli", "sheets", "+read", "--spreadsheet-token", "tok", "--range", "A1:D10", "--as", "user"},
		},
		{
			name: "sheets spreadsheets get",
			argv: []any{"sheets", "spreadsheets", "get", "--spreadsheet-token", "tok"},
			want: []string{"lark-cli", "sheets", "+info", "--spreadsheet-token", "tok", "--as", "user"},
		},
		{
			name: "auth status keeps native flags",
			argv: []any{"auth", "status"},
			want: []string{"lark-cli", "auth", "status"},
		},
		{
			name: "auth help keeps native flags",
			argv: []any{"auth", "--help"},
			want: []string{"lark-cli", "auth", "--help"},
		},
		{
			name: "global help keeps native flags",
			argv: []any{"--help"},
			want: []string{"lark-cli", "--help"},
		},
		{
			name: "drive business command gets user identity",
			argv: []any{"drive", "file", "list"},
			want: []string{"lark-cli", "drive", "file", "list", "--as", "user"},
		},
		{
			name: "unknown root command keeps native flags",
			argv: []any{"version"},
			want: []string{"lark-cli", "version"},
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

func TestStructuredSheetsCommandsUseShortcutSkills(t *testing.T) {
	info, err := buildToolCommand("lark_sheets_info", map[string]any{"spreadsheet_token": "tok"})
	if err != nil {
		t.Fatalf("build sheets info: %v", err)
	}
	wantInfo := []string{"lark-cli", "sheets", "+info", "--as", "user", "--spreadsheet-token", "tok"}
	if !reflect.DeepEqual(info, wantInfo) {
		t.Fatalf("info command = %#v, want %#v", info, wantInfo)
	}

	read, err := buildToolCommand("lark_sheets_read", map[string]any{
		"spreadsheet_token": "tok",
		"sheet_id":          "sh1",
		"range":             "A1:D10",
	})
	if err != nil {
		t.Fatalf("build sheets read: %v", err)
	}
	wantRead := []string{"lark-cli", "sheets", "+read", "--as", "user", "--spreadsheet-token", "tok", "--range", "A1:D10", "--sheet-id", "sh1"}
	if !reflect.DeepEqual(read, wantRead) {
		t.Fatalf("read command = %#v, want %#v", read, wantRead)
	}
}

func TestDocsCreateRequiresMarkdown(t *testing.T) {
	_, err := buildToolCommand("lark_docs_create", map[string]any{"title": "empty"})
	if err == nil {
		t.Fatal("expected missing markdown to fail")
	}
}

func TestDecodeCommandOutputGBK(t *testing.T) {
	gbk := []byte{0xc3, 0xfc, 0xc1, 0xee, 0xcc, 0xab, 0xb3, 0xa4}
	if got := decodeCommandOutput(gbk); got != "命令太长" {
		t.Fatalf("decoded = %q", got)
	}
}

func TestLarkAPICommandWithJSONDataUsesTempFileForLargeBody(t *testing.T) {
	body := []byte(`{"data":"` + strings.Repeat("x", 7000) + `"}`)
	cmd, cleanup, err := larkAPICommandWithJSONData(context.Background(), "PUT", "/open-apis/test", "bot", body)
	if err != nil {
		t.Fatalf("larkAPICommandWithJSONData: %v", err)
	}
	defer cleanup()
	found := ""
	for i, arg := range cmd.Args {
		if arg == "--data" && i+1 < len(cmd.Args) {
			found = cmd.Args[i+1]
			break
		}
	}
	if !strings.HasPrefix(found, "@") {
		t.Fatalf("expected @file data arg, got args=%#v", cmd.Args)
	}
	if _, err := os.Stat(strings.TrimPrefix(found, "@")); err != nil {
		t.Fatalf("temp body not found: %v", err)
	}
}

func TestWindowsCmdQuoteKeepsArgumentAtomic(t *testing.T) {
	got := windowsCmdQuote(`{"as":"bot","text":"命令太长"}`)
	if !strings.HasPrefix(got, `"`) || !strings.HasSuffix(got, `"`) || !strings.Contains(got, `\"bot\"`) {
		t.Fatalf("unexpected windows quote: %s", got)
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
