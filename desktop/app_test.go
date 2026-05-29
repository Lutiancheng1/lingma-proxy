package main

import (
	"errors"
	"lingma-ipc-proxy/internal/feishu"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExtractUsageFromJSONUsageWrapper(t *testing.T) {
	input, output := extractUsageFromJSON(`{"usage":{"prompt_tokens":161,"completion_tokens":3,"total_tokens":164}}`)
	if input != 161 || output != 3 {
		t.Fatalf("extractUsageFromJSON usage wrapper = (%d, %d), want (161, 3)", input, output)
	}
}

func TestExtractUsageFromJSONFlatTokens(t *testing.T) {
	input, output := extractUsageFromJSON(`{"prompt_tokens":161,"completion_tokens":3,"total_tokens":164}`)
	if input != 161 || output != 3 {
		t.Fatalf("extractUsageFromJSON flat tokens = (%d, %d), want (161, 3)", input, output)
	}
}

func TestExtractTokenUsageStreamingFlatTokens(t *testing.T) {
	resp := "event: message\n" +
		`data: {"type":"message_start","prompt_tokens":161}` + "\n\n" +
		`data: {"type":"message_delta","completion_tokens":3,"total_tokens":164}` + "\n\n"
	input, output := extractTokenUsage(resp)
	if input != 161 || output != 3 {
		t.Fatalf("extractTokenUsage stream flat tokens = (%d, %d), want (161, 3)", input, output)
	}
}

func TestRedactAndLimitPayloadJSON(t *testing.T) {
	raw := `{"authorization":"Bearer abc123","access_token":"secret","image":"data:image/png;base64,AAAA","normal":"ok"}`
	got := redactAndLimitPayload(raw)
	if strings.Contains(got, "abc123") || strings.Contains(got, "secret") || strings.Contains(got, "data:image/png") {
		t.Fatalf("redactAndLimitPayload should redact secrets, got %s", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("redactAndLimitPayload should include redaction markers, got %s", got)
	}
}

func TestResolveFeedbackRangeCustom(t *testing.T) {
	startAt, endAt, err := resolveFeedbackRange(FeedbackExportOptions{
		RangePreset: "custom",
		StartAt:     "2026-05-13T10:00",
		EndAt:       "2026-05-13T11:30",
	})
	if err != nil {
		t.Fatalf("resolveFeedbackRange custom returned error: %v", err)
	}
	if startAt.After(endAt) {
		t.Fatalf("resolveFeedbackRange returned invalid range: %s > %s", startAt, endAt)
	}
}

func TestFilterRequestsByRangeUsesCreatedAt(t *testing.T) {
	now := time.Now()
	requests := []RequestRecord{
		{CreatedAt: now.Add(-15 * time.Minute).Format(time.RFC3339), Path: "/v1/messages"},
		{CreatedAt: now.Add(-3 * time.Hour).Format(time.RFC3339), Path: "/v1/models"},
		{Path: "/unknown"},
	}
	filtered := filterRequestsByRange(requests, now.Add(-30*time.Minute), now)
	if len(filtered) != 1 {
		t.Fatalf("filterRequestsByRange len = %d, want 1", len(filtered))
	}
	if filtered[0].Path != "/v1/messages" {
		t.Fatalf("filterRequestsByRange path = %s, want /v1/messages", filtered[0].Path)
	}
}

func TestBackfillRequestCreatedAtAcrossDays(t *testing.T) {
	anchor := time.Date(2026, 5, 13, 10, 0, 0, 0, time.Local)
	requests := []RequestRecord{
		{Time: "23:40:00", Path: "/oldest"},
		{Time: "00:20:00", Path: "/middle"},
		{Time: "09:15:00", Path: "/newest"},
	}

	if !backfillRequestCreatedAt(requests, anchor) {
		t.Fatalf("backfillRequestCreatedAt should report mutation")
	}

	if requests[2].CreatedAt == "" || requests[1].CreatedAt == "" || requests[0].CreatedAt == "" {
		t.Fatalf("backfillRequestCreatedAt should populate all createdAt values: %#v", requests)
	}

	if got, want := requests[2].CreatedAt[:10], "2026-05-13"; got != want {
		t.Fatalf("newest request date = %s, want %s", got, want)
	}
	if got, want := requests[1].CreatedAt[:10], "2026-05-13"; got != want {
		t.Fatalf("middle request date = %s, want %s", got, want)
	}
	if got, want := requests[0].CreatedAt[:10], "2026-05-12"; got != want {
		t.Fatalf("oldest request date = %s, want %s", got, want)
	}
}

func TestParseDesktopConfigMigratesMissingGroupOnlyAtBotDefault(t *testing.T) {
	baseProxy := defaultConfig()
	baseAgent := feishu.DefaultConfig()
	data := []byte(`{
		"proxyConfig": {
			"host": "127.0.0.1",
			"port": "8095"
		},
		"feishuAgent": {
			"enabled": true,
			"brand": "feishu",
			"model": "kmodel",
			"maxToolRounds": 5
		}
	}`)

	_, agentCfg, ok := parseDesktopConfig(data, baseProxy, baseAgent)
	if !ok {
		t.Fatal("parseDesktopConfig returned ok=false")
	}
	if !agentCfg.GroupOnlyAtBot {
		t.Fatal("missing groupOnlyAtBot in old config should inherit the safe default true")
	}
	if agentCfg.MaxToolRounds != feishu.DefaultMaxToolRounds {
		t.Fatalf("legacy maxToolRounds should migrate to default, got %d", agentCfg.MaxToolRounds)
	}
}

func TestParseDesktopConfigPreservesExplicitGroupOnlyAtBotFalse(t *testing.T) {
	baseProxy := defaultConfig()
	baseAgent := feishu.DefaultConfig()
	data := []byte(`{
		"proxyConfig": {
			"host": "127.0.0.1",
			"port": "8095"
		},
		"feishuAgent": {
			"enabled": true,
			"brand": "feishu",
			"model": "kmodel",
			"groupOnlyAtBot": false,
			"maxToolRounds": 12
		}
	}`)

	_, agentCfg, ok := parseDesktopConfig(data, baseProxy, baseAgent)
	if !ok {
		t.Fatal("parseDesktopConfig returned ok=false")
	}
	if agentCfg.GroupOnlyAtBot {
		t.Fatal("explicit groupOnlyAtBot=false should be preserved")
	}
	if agentCfg.MaxToolRounds != 12 {
		t.Fatalf("explicit maxToolRounds should be preserved, got %d", agentCfg.MaxToolRounds)
	}
}

func TestSaveConfigPreservesExistingFeishuAgentConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	configDir := filepath.Join(tmp, ".config", "lingma-proxy")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.json")
	initial := []byte(`{
		"proxyConfig": {
			"host": "127.0.0.1",
			"port": 8095,
			"backend": "remote"
		},
		"feishuAgent": {
			"enabled": true,
			"brand": "feishu",
			"model": "kmodel",
			"botName": "aily",
			"botIdentity": "custom identity",
			"mcpEnabled": true,
			"safeFiles": {
				"enabled": true,
				"workspaceDir": "/tmp/agent-workspace",
				"extraPaths": [
					{"path": "/tmp/read-only", "mode": "read"},
					{"path": "/tmp/write", "mode": "write"}
				]
			},
			"groupOnlyAtBot": false,
			"maxToolRounds": 24
		}
	}`)
	if err := os.WriteFile(configPath, initial, 0644); err != nil {
		t.Fatal(err)
	}

	app := &App{cfg: defaultConfig(), agentCfg: feishu.DefaultConfig()}
	nextProxy := app.cfg
	nextProxy.Port = 18095
	if err := app.saveConfig(nextProxy); err != nil {
		t.Fatalf("saveConfig failed: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	_, agentCfg, ok := parseDesktopConfig(data, defaultConfig(), feishu.DefaultConfig())
	if !ok {
		t.Fatal("saved desktop config should parse")
	}
	if agentCfg.BotName != "aily" {
		t.Fatalf("botName was not preserved: %#v", agentCfg)
	}
	if agentCfg.BotIdentity != "custom identity" {
		t.Fatalf("botIdentity was not preserved: %#v", agentCfg)
	}
	if !agentCfg.MCPEnabled {
		t.Fatalf("mcpEnabled was not preserved: %#v", agentCfg)
	}
	if agentCfg.GroupOnlyAtBot {
		t.Fatalf("groupOnlyAtBot=false was not preserved: %#v", agentCfg)
	}
	if agentCfg.SafeFiles.WorkspaceDir != "/tmp/agent-workspace" || len(agentCfg.SafeFiles.ExtraPaths) != 2 {
		t.Fatalf("safeFiles config was not preserved: %#v", agentCfg.SafeFiles)
	}
}

func TestShouldRetryFeishuAutoStartForTransientProbeFailures(t *testing.T) {
	retryable := []string{
		"Node/npm/npx 未就绪",
		"lark-cli 未安装",
		"必需的 lark-* skills 未安装完整：缺少 lark-im",
		"飞书 CLI 尚未完成应用初始化",
		"飞书 CLI 尚未完成用户授权",
	}
	for _, msg := range retryable {
		if !shouldRetryFeishuAutoStart(errors.New(msg)) {
			t.Fatalf("should retry transient startup error %q", msg)
		}
	}
	if shouldRetryFeishuAutoStart(errors.New("event consume failed")) {
		t.Fatal("should not retry non-prerequisite agent errors")
	}
}

func TestEmitLogDedupesRecentNonAdjacentDuplicates(t *testing.T) {
	app := &App{}
	app.emitLogWithSourceMeta("feishu-agent", "info", "same message", "s1", "c1", "m1")
	app.emitLogWithSourceMeta("feishu-agent", "info", "different message", "s1", "c1", "m1")
	app.emitLogWithSourceMeta("feishu-agent", "info", "same message", "s1", "c1", "m1")

	if got, want := len(app.logs), 2; got != want {
		t.Fatalf("logs len = %d, want %d: %#v", got, want, app.logs)
	}
}

func TestEmitLogDedupesAgainstExistingRecentLogs(t *testing.T) {
	app := &App{}
	app.logs = []AppLog{{
		Source:    "feishu-agent",
		Level:     "info",
		SessionID: "s1",
		ChatID:    "c1",
		MessageID: "m1",
		Message:   "same message",
	}}
	app.emitLogWithSourceMeta("feishu-agent", "info", "same message", "s1", "c1", "m1")

	if got, want := len(app.logs), 1; got != want {
		t.Fatalf("logs len = %d, want %d: %#v", got, want, app.logs)
	}
}

func TestGetLogDetailUsesIDForSameSecondLogs(t *testing.T) {
	app := &App{}
	createdAt := "2026-05-27T10:47:35+08:00"
	app.logs = []AppLog{
		{ID: "first", CreatedAt: createdAt, Time: "10:47:35", Source: "feishu-agent", Level: "info", Message: "first full message"},
		{ID: "second", CreatedAt: createdAt, Time: "10:47:35", Source: "feishu-agent", Level: "info", Message: "second full message"},
	}

	got, err := app.GetLogDetail("first")
	if err != nil {
		t.Fatalf("GetLogDetail by id failed: %v", err)
	}
	if got.Message != "first full message" {
		t.Fatalf("GetLogDetail returned wrong same-second log: %#v", got)
	}
}
