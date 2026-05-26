package feishu

import (
	"strings"
	"testing"
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
	got := renderFeishuHistoryBackfill(raw, "om_current")
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
