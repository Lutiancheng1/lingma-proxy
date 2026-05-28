package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestStructuredDriveSearchCommandUsesSupportedFlags(t *testing.T) {
	got, err := buildToolCommand("lark_drive_search", map[string]any{
		"query":      "个人周报",
		"doc_types":  "docx",
		"mine":       true,
		"page_token": "next",
	})
	if err != nil {
		t.Fatalf("build drive search: %v", err)
	}
	want := []string{"lark-cli", "drive", "+search", "--as", "user", "--format", "json", "--query", "个人周报", "--doc-types", "docx", "--mine", "--page-token", "next"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("command = %#v, want %#v", got, want)
	}
	for _, arg := range got {
		if arg == "--limit" || arg == "--type" {
			t.Fatalf("drive search command used unsupported flag: %#v", got)
		}
	}
}

func TestStructuredPublicPermissionPatchCommand(t *testing.T) {
	got, err := buildToolCommand("lark_permission_public", map[string]any{
		"action":            "patch",
		"token":             "doc_token",
		"type":              "docx",
		"external_access":   true,
		"link_share_entity": "anyone_readable",
	})
	if err != nil {
		t.Fatalf("build permission patch: %v", err)
	}
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "drive permission.public patch") || !strings.Contains(joined, "--params") || !strings.Contains(joined, "--data") || !strings.Contains(joined, "--yes") {
		t.Fatalf("unexpected permission command: %#v", got)
	}
	if strings.Contains(joined, "+apply-permission") || strings.Contains(joined, " --perm ") {
		t.Fatalf("permission command used wrong public-apply path: %#v", got)
	}
}

func TestDocsCreateRequiresMarkdown(t *testing.T) {
	_, err := buildToolCommand("lark_docs_create", map[string]any{"title": "empty"})
	if err == nil {
		t.Fatal("expected missing markdown to fail")
	}
}

func TestDocsFetchChunksLongDocumentContent(t *testing.T) {
	raw := `{
  "ok": true,
  "identity": "user",
  "data": {
    "document": {
      "title": "长文档",
      "content": "` + strings.Repeat("一", 2500) + `"
    }
  }
}`
	got, ok := chunkLarkDocsFetchResult(raw, map[string]any{
		"doc_token":   "doc_1",
		"offset":      1000,
		"chunk_size":  1000,
		"require_all": true,
	})
	if !ok {
		t.Fatal("expected docs fetch result to be chunked")
	}
	if !strings.Contains(got, `"has_more": true`) {
		t.Fatalf("expected has_more true, got %s", got)
	}
	if !strings.Contains(got, `"next_offset": 2000`) {
		t.Fatalf("expected next offset, got %s", got)
	}
	if !strings.Contains(got, `"total_chars": 2500`) {
		t.Fatalf("expected total chars, got %s", got)
	}
	if !strings.Contains(got, `"required_next_call"`) {
		t.Fatalf("expected required next call guidance, got %s", got)
	}
	if strings.Contains(got, "truncated") {
		t.Fatalf("docs chunk should not use generic truncation marker: %s", got)
	}
}

func TestDocsFetchChunkMarksCompleteAtEnd(t *testing.T) {
	raw := `{"ok":true,"data":{"document":{"content":"` + strings.Repeat("二", 1200) + `"}}}`
	got, ok := chunkLarkDocsFetchResult(raw, map[string]any{"offset": 1000, "chunk_size": 1000})
	if !ok {
		t.Fatal("expected docs fetch result to be chunked")
	}
	if !strings.Contains(got, `"has_more": false`) || !strings.Contains(got, `"complete": true`) {
		t.Fatalf("expected complete final chunk, got %s", got)
	}
}

func TestSheetsReadChunksLargeRange(t *testing.T) {
	rows := make([]string, 0, 85)
	for i := 0; i < 85; i++ {
		rows = append(rows, fmt.Sprintf(`["row-%d"]`, i+1))
	}
	raw := `{"ok":true,"data":{"valueRange":{"range":"sh1!A1:B85","values":[` + strings.Join(rows, ",") + `]}}}`
	got, ok := chunkLarkSheetsReadResult(raw, map[string]any{
		"spreadsheet_token": "tok",
		"sheet_id":          "sh1",
		"range":             "A1:B85",
	})
	if !ok {
		t.Fatal("expected sheets read result to be chunked")
	}
	if !strings.Contains(got, `"kind": "sheet_rows_chunk"`) || !strings.Contains(got, `"next_range": "A81:B85"`) {
		t.Fatalf("expected next range guidance, got %s", got)
	}
	if strings.Contains(got, "row-85") {
		t.Fatalf("chunk should not include rows outside first chunk: %s", got)
	}
}

func TestNextA1RangePreservesSheetPrefix(t *testing.T) {
	if got := nextA1Range("IdMLmJ!A1:G203", 80); got != "IdMLmJ!A81:G160" {
		t.Fatalf("next range = %q", got)
	}
	if got := nextA1Range("A81:G203", 80); got != "A161:G203" {
		t.Fatalf("next final range = %q", got)
	}
}

func TestBuiltinWebFetchAllowsHTTPAndStripsHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body><h1>标题</h1><p>内容&nbsp;&amp;&nbsp;更多</p></body></html>`))
	}))
	defer server.Close()

	result := executeBuiltinWebTool(context.Background(), DefaultConfig(), "web_fetch", map[string]any{"url": server.URL})
	if result.IsError {
		t.Fatalf("web_fetch should succeed: %s", result.Output)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Output), &payload); err != nil {
		t.Fatalf("web_fetch output is not json: %v\n%s", err, result.Output)
	}
	if payload["kind"] != "web_fetch" || payload["content"] != "标题 内容 & 更多" {
		t.Fatalf("unexpected web fetch output: %s", result.Output)
	}
}

func TestBuiltinWebFetchRejectsUnsupportedScheme(t *testing.T) {
	result := executeBuiltinWebTool(context.Background(), DefaultConfig(), "web_fetch", map[string]any{"url": "file:///etc/passwd"})
	if !result.IsError || !strings.Contains(result.Output, "only http/https") {
		t.Fatalf("expected scheme rejection, got %#v", result)
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
	if !shouldPreserveToolResultForModel(`{"ok":true,"data":{"bridge_reading":{"kind":"doc_content_chunk","has_more":true},"document":{"content":"正文"}}}`) {
		t.Fatal("document chunks should stay inline for the model")
	}
	if shouldPreserveToolResultForModel(`{"kind":"other"}`) {
		t.Fatal("unrelated large results should still be compactable")
	}
}

func TestWindowsCmdQuoteKeepsArgumentAtomic(t *testing.T) {
	got := windowsCmdQuote(`{"as":"bot","text":"命令太长"}`)
	if !strings.HasPrefix(got, `"`) || !strings.HasSuffix(got, `"`) || !strings.Contains(got, `""bot""`) {
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

func TestSafeFileToolsAndPathAuthorization(t *testing.T) {
	tmp := t.TempDir()
	allowedPathsFilePathOverride = filepath.Join(tmp, "allowed_paths.json")
	defer func() {
		allowedPathsFilePathOverride = ""
	}()

	ctx := context.Background()
	workspace := filepath.Join(tmp, "workspace")
	readOnlyDir := filepath.Join(tmp, "read-only")
	writeDir := filepath.Join(tmp, "write")
	outsideDir := filepath.Join(tmp, "outside")
	for _, dir := range []string{workspace, readOnlyDir, writeDir, outsideDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	workspaceFile := filepath.Join(workspace, "note.txt")
	readOnlyFile := filepath.Join(readOnlyDir, "note.txt")
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	for path, content := range map[string]string{
		workspaceFile: "workspace",
		readOnlyFile:  "read-only",
		outsideFile:   "secret",
	} {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := NormalizeConfig(Config{
		SafeFiles: SafeFilesConfig{
			Configured:   true,
			Enabled:      true,
			WorkspaceDir: workspace,
			ExtraPaths: []SafeFilePathConfig{
				{Path: readOnlyDir, Mode: "read"},
				{Path: writeDir, Mode: "write"},
			},
		},
	})

	disabled := cfg
	disabled.SafeFiles.Enabled = false
	disabledRead := executeSafeFileTool(ctx, disabled, "safe_file_read", map[string]any{"path": workspaceFile})
	if !disabledRead.IsError || !strings.Contains(disabledRead.Output, "本机文件工具已") {
		t.Fatalf("expected disabled safe file tools to reject reads: %#v", disabledRead)
	}

	readResult := executeSafeFileTool(ctx, cfg, "safe_file_read", map[string]any{"path": workspaceFile})
	if readResult.IsError || readResult.Output != "workspace" {
		t.Fatalf("workspace read failed: %#v", readResult)
	}

	writeResult := executeSafeFileTool(ctx, cfg, "safe_file_write", map[string]any{
		"path":    filepath.Join(workspace, "created.txt"),
		"content": "created",
	})
	if writeResult.IsError {
		t.Fatalf("workspace write failed: %#v", writeResult)
	}

	deleteCtx := context.WithValue(ctx, userMessageKey, "确认删除 created.txt")
	deleteResult := executeSafeFileTool(deleteCtx, cfg, "safe_file_delete", map[string]any{
		"path":      filepath.Join(workspace, "created.txt"),
		"confirmed": true,
	})
	if deleteResult.IsError {
		t.Fatalf("workspace delete failed: %#v", deleteResult)
	}

	readOnlyWrite := executeSafeFileTool(ctx, cfg, "safe_file_write", map[string]any{
		"path":    filepath.Join(readOnlyDir, "new.txt"),
		"content": "blocked",
	})
	if !readOnlyWrite.IsError || !strings.Contains(readOnlyWrite.Output, "未获得足够权限") {
		t.Fatalf("expected read-only path write to be blocked, got %#v", readOnlyWrite)
	}

	writeOnlyFile := filepath.Join(writeDir, "new.txt")
	writeOK := executeSafeFileTool(ctx, cfg, "safe_file_write", map[string]any{
		"path":    writeOnlyFile,
		"content": "write-only",
	})
	if writeOK.IsError {
		t.Fatalf("expected write path creation to succeed: %#v", writeOK)
	}
	writeOnlyDelete := executeSafeFileTool(context.WithValue(ctx, userMessageKey, "确认删除 new.txt"), cfg, "safe_file_delete", map[string]any{
		"path":      writeOnlyFile,
		"confirmed": true,
	})
	if !writeOnlyDelete.IsError || !strings.Contains(writeOnlyDelete.Output, "未获得足够权限") {
		t.Fatalf("expected write-only path delete to be blocked, got %#v", writeOnlyDelete)
	}

	unauthorizedRead := executeSafeFileTool(ctx, cfg, "safe_file_read", map[string]any{"path": outsideFile})
	if !unauthorizedRead.IsError {
		t.Fatalf("expected outside path to be denied: %#v", unauthorizedRead)
	}

	authWithoutUserMessage := executeSafeFileTool(ctx, cfg, "authorize_local_path", map[string]any{"path": outsideDir})
	if !authWithoutUserMessage.IsError || !strings.Contains(authWithoutUserMessage.Output, "授权被拒绝") {
		t.Fatalf("expected model-only authorization to be blocked: %#v", authWithoutUserMessage)
	}

	authCtx := context.WithValue(ctx, userMessageKey, "授权目录 "+outsideDir)
	authResult := executeSafeFileTool(authCtx, cfg, "authorize_local_path", map[string]any{"path": outsideDir})
	if authResult.IsError {
		t.Fatalf("expected explicit read authorization to succeed, got %#v", authResult)
	}
	readResult = executeSafeFileTool(ctx, cfg, "safe_file_read", map[string]any{"path": outsideFile})
	if readResult.IsError || readResult.Output != "secret" {
		t.Fatalf("authorized read failed: %#v", readResult)
	}
	readAuthorizedWrite := executeSafeFileTool(ctx, cfg, "safe_file_write", map[string]any{
		"path":    filepath.Join(outsideDir, "new.txt"),
		"content": "blocked",
	})
	if !readAuthorizedWrite.IsError || !strings.Contains(readAuthorizedWrite.Output, "未获得足够权限") {
		t.Fatalf("expected oral authorization to be read-only, got %#v", readAuthorizedWrite)
	}

	sibling := filepath.Join(tmp, "outside-other", "secret.txt")
	if err := os.MkdirAll(filepath.Dir(sibling), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sibling, []byte("nope"), 0644); err != nil {
		t.Fatal(err)
	}
	siblingRead := executeSafeFileTool(ctx, cfg, "safe_file_read", map[string]any{"path": sibling})
	if !siblingRead.IsError {
		t.Fatalf("expected sibling prefix path to be denied: %#v", siblingRead)
	}

	escapeDir := filepath.Join(tmp, "escape")
	escapeFile := filepath.Join(escapeDir, "secret.txt")
	if err := os.MkdirAll(escapeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(escapeFile, []byte("escape"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(escapeFile, filepath.Join(workspace, "secret-link")); err == nil {
		linkRead := executeSafeFileTool(ctx, cfg, "safe_file_read", map[string]any{"path": filepath.Join(workspace, "secret-link")})
		if !linkRead.IsError {
			t.Fatalf("expected symlink escape to be denied: %#v", linkRead)
		}
	}

	largeFile := filepath.Join(workspace, "large.txt")
	if err := os.WriteFile(largeFile, []byte(strings.Repeat("a", safeFileReadMaxBytes+1)), 0644); err != nil {
		t.Fatal(err)
	}
	largeRead := executeSafeFileTool(ctx, cfg, "safe_file_read", map[string]any{"path": largeFile})
	if !largeRead.IsError || !strings.Contains(largeRead.Output, "文件过大") {
		t.Fatalf("expected large file to be blocked: %#v", largeRead)
	}

	overwriteTarget := filepath.Join(workspace, "overwrite.txt")
	if err := os.WriteFile(overwriteTarget, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	overwriteBlocked := executeSafeFileTool(ctx, cfg, "safe_file_write", map[string]any{"path": overwriteTarget, "content": "new"})
	if !overwriteBlocked.IsError || !strings.Contains(overwriteBlocked.Output, "目标文件已存在") {
		t.Fatalf("expected overwrite without flag to be blocked: %#v", overwriteBlocked)
	}
	overwriteNoConfirm := executeSafeFileTool(ctx, cfg, "safe_file_write", map[string]any{"path": overwriteTarget, "content": "new", "overwrite": true})
	if !overwriteNoConfirm.IsError || !strings.Contains(overwriteNoConfirm.Output, "确认覆盖") {
		t.Fatalf("expected overwrite without user confirmation to be blocked: %#v", overwriteNoConfirm)
	}
	overwriteCtx := context.WithValue(ctx, userMessageKey, "确认覆盖 overwrite.txt")
	overwriteOK := executeSafeFileTool(overwriteCtx, cfg, "safe_file_write", map[string]any{"path": overwriteTarget, "content": "new", "overwrite": true})
	if overwriteOK.IsError {
		t.Fatalf("expected confirmed overwrite to succeed: %#v", overwriteOK)
	}

	deleteDirResult := executeSafeFileTool(deleteCtx, cfg, "safe_file_delete", map[string]any{"path": workspace, "confirmed": true})
	if !deleteDirResult.IsError || !strings.Contains(deleteDirResult.Output, "目标是目录，只能删除文件") {
		t.Fatalf("expected delete directory to be blocked, got %#v", deleteDirResult)
	}
	deleteNoConfirm := executeSafeFileTool(ctx, cfg, "safe_file_delete", map[string]any{"path": overwriteTarget, "confirmed": true})
	if !deleteNoConfirm.IsError || !strings.Contains(deleteNoConfirm.Output, "确认删除") {
		t.Fatalf("expected delete without exact confirmation to be blocked: %#v", deleteNoConfirm)
	}
	deleteNegated := executeSafeFileTool(context.WithValue(ctx, userMessageKey, "不要删除 overwrite.txt"), cfg, "safe_file_delete", map[string]any{"path": overwriteTarget, "confirmed": true})
	if !deleteNegated.IsError {
		t.Fatalf("expected negated delete wording to be blocked: %#v", deleteNegated)
	}
	deleteOK := executeSafeFileTool(context.WithValue(ctx, userMessageKey, "确认删除 overwrite.txt"), cfg, "safe_file_delete", map[string]any{"path": overwriteTarget, "confirmed": true})
	if deleteOK.IsError {
		t.Fatalf("expected confirmed delete to succeed: %#v", deleteOK)
	}

	listResult := executeSafeFileTool(ctx, cfg, "list_authorized_paths", map[string]any{})
	if listResult.IsError || !strings.Contains(listResult.Output, "workspace 可读写删") || !strings.Contains(listResult.Output, "口述授权只读") {
		t.Fatalf("list_authorized_paths unexpected result: %#v", listResult)
	}
}
