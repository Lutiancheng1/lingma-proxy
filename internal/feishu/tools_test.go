package feishu

import (
	"context"
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

func TestAppendLarkCLICorrectionForKnownFailures(t *testing.T) {
	got := appendLarkCLICorrection([]string{"lark-cli", "drive", "+search", "--limit", "100"}, "Usage: lark-cli drive +search")
	if !strings.Contains(got, "不支持 --limit") || !strings.Contains(got, "page_token") {
		t.Fatalf("missing drive correction: %s", got)
	}
	got = appendLarkCLICorrection([]string{"lark-cli", "auth", "status", "--as", "user"}, "unknown flag: --as")
	if !strings.Contains(got, "auth/help/version/config") || !strings.Contains(got, "auth list") {
		t.Fatalf("missing auth correction: %s", got)
	}
	got = appendLarkCLICorrection([]string{"lark-cli", "drive", "+apply-permission", "--perm", "public"}, `invalid value "public" for --perm, allowed: view, edit`)
	if !strings.Contains(got, "不能设置“互联网所有人可见”") || !strings.Contains(got, "permission.public patch") || !strings.Contains(got, "anyone_readable") || !strings.Contains(got, "下一步要求") {
		t.Fatalf("missing public permission correction: %s", got)
	}
	got = appendLarkCLICorrection([]string{"lark-cli", "drive", "permission", "set"}, "Usage: lark-cli drive [flags]\nAvailable Commands:")
	if !strings.Contains(got, "permission.public get/patch") {
		t.Fatalf("missing permission command correction: %s", got)
	}
	got = appendLarkCLICorrection([]string{"lark-cli", "drive", "permission.public", "patch"}, `{"code":91011,"msg":"blocked"}`)
	if !strings.Contains(got, "密级策略") || !strings.Contains(got, "目标文档 URL") {
		t.Fatalf("missing permission policy correction: %s", got)
	}
}

func TestDecodeCommandOutputGBK(t *testing.T) {
	gbk := []byte{0xc3, 0xfc, 0xc1, 0xee, 0xcc, 0xab, 0xb3, 0xa4}
	if got := decodeCommandOutput(gbk); got != "命令太长" {
		t.Fatalf("decoded = %q", got)
	}
}

func TestLarkAPICommandWithJSONDataUsesStdinForLargeBody(t *testing.T) {
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
	if found != "-" {
		t.Fatalf("expected stdin data arg, got args=%#v", cmd.Args)
	}
	if cmd.Stdin == nil {
		t.Fatal("expected command stdin to contain large body")
	}
}

func TestNormalizeToolOutputSummarizesDriveSearchWithRealURLs(t *testing.T) {
	raw := `{
  "ok": true,
  "identity": "user",
  "data": {
    "has_more": true,
    "page_token": "next-token",
    "total": 31,
    "results": [
      {
        "entity_type": "DOC",
        "title_highlighted": "AI 日报",
        "result_meta": {
          "doc_types": "DOCX",
          "owner_name": "卢天成",
          "token": "doc-token",
          "update_time_iso": "2026-05-26T10:06:49+08:00",
          "url": "https://my.feishu.cn/docx/doc-token"
        }
      }
    ]
  }
}`
	got := normalizeToolOutput(raw)
	for _, want := range []string{
		`"kind": "drive_search"`,
		`"total": 31`,
		`"page_token": "next-token"`,
		`"title": "AI 日报"`,
		`"url": "https://my.feishu.cn/docx/doc-token"`,
		"禁止自造链接",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("normalized drive search missing %q:\n%s", want, got)
		}
	}
}

func TestDriveSearchSummaryBypassesGenericTruncation(t *testing.T) {
	items := make([]string, 0, 25)
	for i := 0; i < 25; i++ {
		items = append(items, `{
        "entity_type": "DOC",
        "title_highlighted": "文档 `+string(rune('A'+i))+`",
        "result_meta": {
          "doc_types": "DOCX",
          "owner_name": "卢天成",
          "token": "doc-token-`+string(rune('A'+i))+`",
          "url": "https://my.feishu.cn/docx/doc-token-`+string(rune('A'+i))+`"
        }
      }`)
	}
	raw := `{"ok":true,"identity":"user","data":{"has_more":false,"total":25,"results":[` + strings.Join(items, ",") + `]}}`
	normalized := normalizeToolOutput(raw)
	if len(normalized) <= 4000 {
		t.Fatalf("test fixture should produce a long normalized summary, got %d", len(normalized))
	}
	if !isDriveSearchSummary(normalized) {
		t.Fatal("normalized drive search should be recognized for truncation bypass")
	}
	if strings.Contains(normalized, "truncated") {
		t.Fatalf("drive search summaries must not be generically truncated:\n%s", normalized)
	}
	if !strings.Contains(normalized, "doc-token-Y") {
		t.Fatalf("last real URL should remain visible to the model:\n%s", normalized)
	}
}

func TestShouldPreserveDriveSearchToolResultForModel(t *testing.T) {
	if !shouldPreserveToolResultForModel(`{"kind":"drive_search","results":[]}`) {
		t.Fatal("drive_search summaries should stay inline for the model")
	}
	if shouldPreserveToolResultForModel(`{"kind":"other"}`) {
		t.Fatal("unrelated large results should still be compactable")
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
