package feishu

import (
	"strings"
	"testing"
	"time"
)

func TestRenderFeishuHistoryBackfill(t *testing.T) {
	raw := `{
		"data": {
			"messages": [
				{
					"message_id": "om_current",
					"create_time": "2026-05-26T10:00:02+08:00",
					"msg_type": "text",
					"sender": {"name": "卢天成"},
					"content": "{\"text\":\"继续\"}"
				},
				{
					"message_id": "om_prev",
					"create_time": "2026-05-26T10:00:01+08:00",
					"msg_type": "text",
					"sender": {"name": "aily"},
					"content": "{\"text\":\"请先完成授权\"}"
				}
			]
		}
	}`
	got := renderFeishuHistoryBackfill(raw, "om_current", time.Time{}, "")
	if !strings.Contains(got, "飞书历史回填") {
		t.Fatalf("missing backfill header: %s", got)
	}
	if !strings.Contains(got, "aily: 请先完成授权") {
		t.Fatalf("missing previous message: %s", got)
	}
	if strings.Contains(got, "继续") {
		t.Fatalf("current message should be skipped: %s", got)
	}
}

func TestRenderFeishuHistoryBackfillHonorsResetBoundary(t *testing.T) {
	raw := `{
		"data": {
			"messages": [
				{
					"message_id": "om_after",
					"create_time": "2026-05-26T10:00:03+08:00",
					"msg_type": "text",
					"sender": {"name": "卢天成"},
					"content": "{\"text\":\"新任务\"}"
				},
				{
					"message_id": "om_reset",
					"create_time": "2026-05-26T10:00:02+08:00",
					"msg_type": "text",
					"sender": {"name": "卢天成"},
					"content": "{\"text\":\"/reset\"}"
				},
				{
					"message_id": "om_before",
					"create_time": "2026-05-26T10:00:01+08:00",
					"msg_type": "text",
					"sender": {"name": "aily"},
					"content": "{\"text\":\"你的姓名是卢天成\"}"
				}
			]
		}
	}`
	resetAt := time.Date(2026, 5, 26, 10, 0, 2, 0, time.FixedZone("CST", 8*3600))
	got := renderFeishuHistoryBackfill(raw, "om_current", resetAt, "om_reset")
	if strings.Contains(got, "你的姓名是卢天成") || strings.Contains(got, "/reset") {
		t.Fatalf("reset boundary should exclude old/reset messages: %s", got)
	}
	if !strings.Contains(got, "新任务") {
		t.Fatalf("message after reset should remain: %s", got)
	}
}

func TestRenderFeishuHistoryBackfillKeepsSameSecondAfterReset(t *testing.T) {
	raw := `{
		"data": {
			"messages": [
				{
					"message_id": "om_after_same_second",
					"create_time": "2026-05-26T10:00:02+08:00",
					"msg_type": "text",
					"sender": {"name": "卢天成"},
					"content": "{\"text\":\"reset 后立刻继续\"}"
				},
				{
					"message_id": "om_reset",
					"create_time": "2026-05-26T10:00:02+08:00",
					"msg_type": "text",
					"sender": {"name": "卢天成"},
					"content": "{\"text\":\"/reset\"}"
				}
			]
		}
	}`
	resetAt := time.Date(2026, 5, 26, 10, 0, 2, 900*int(time.Millisecond), time.FixedZone("CST", 8*3600))
	got := renderFeishuHistoryBackfill(raw, "om_current", resetAt, "om_reset")
	if strings.Contains(got, "/reset") {
		t.Fatalf("reset command should still be excluded by message id: %s", got)
	}
	if !strings.Contains(got, "reset 后立刻继续") {
		t.Fatalf("same-second message after reset should remain: %s", got)
	}
}
