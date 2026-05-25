package main

import (
	"errors"
	"lingma-ipc-proxy/internal/feishu"
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
	baseBridge := feishu.DefaultConfig()
	data := []byte(`{
		"proxyConfig": {
			"host": "127.0.0.1",
			"port": "8095"
		},
		"feishuBridge": {
			"enabled": true,
			"brand": "feishu",
			"model": "kmodel",
			"maxToolRounds": 5
		}
	}`)

	_, bridgeCfg, ok := parseDesktopConfig(data, baseProxy, baseBridge)
	if !ok {
		t.Fatal("parseDesktopConfig returned ok=false")
	}
	if !bridgeCfg.GroupOnlyAtBot {
		t.Fatal("missing groupOnlyAtBot in old config should inherit the safe default true")
	}
	if bridgeCfg.MaxToolRounds != feishu.DefaultMaxToolRounds {
		t.Fatalf("legacy maxToolRounds should migrate to default, got %d", bridgeCfg.MaxToolRounds)
	}
}

func TestParseDesktopConfigPreservesExplicitGroupOnlyAtBotFalse(t *testing.T) {
	baseProxy := defaultConfig()
	baseBridge := feishu.DefaultConfig()
	data := []byte(`{
		"proxyConfig": {
			"host": "127.0.0.1",
			"port": "8095"
		},
		"feishuBridge": {
			"enabled": true,
			"brand": "feishu",
			"model": "kmodel",
			"groupOnlyAtBot": false,
			"maxToolRounds": 12
		}
	}`)

	_, bridgeCfg, ok := parseDesktopConfig(data, baseProxy, baseBridge)
	if !ok {
		t.Fatal("parseDesktopConfig returned ok=false")
	}
	if bridgeCfg.GroupOnlyAtBot {
		t.Fatal("explicit groupOnlyAtBot=false should be preserved")
	}
	if bridgeCfg.MaxToolRounds != 12 {
		t.Fatalf("explicit maxToolRounds should be preserved, got %d", bridgeCfg.MaxToolRounds)
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
		t.Fatal("should not retry non-prerequisite bridge errors")
	}
}
