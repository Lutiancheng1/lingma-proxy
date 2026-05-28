package feishu

import (
	"strings"
	"testing"
)

func TestMicrocompactToolResults_BelowKeepRecent(t *testing.T) {
	long := strings.Repeat("a", 500)
	msgs := []map[string]any{
		{"role": "user", "content": "hi"},
		{"role": "tool", "tool_call_id": "1", "content": long},
		{"role": "tool", "tool_call_id": "2", "content": long},
	}
	got := microcompactToolResults(msgs, nil)
	if got[1]["content"] != long || got[2]["content"] != long {
		t.Fatalf("expected verbatim retention when tool count <= keepRecent")
	}
}

func TestMicrocompactToolResults_TruncatesOldKeepsRecent(t *testing.T) {
	long := strings.Repeat("a", 500)
	msgs := []map[string]any{
		{"role": "tool", "tool_call_id": "1", "content": long},
		{"role": "tool", "tool_call_id": "2", "content": long},
		{"role": "tool", "tool_call_id": "3", "content": long},
		{"role": "tool", "tool_call_id": "4", "content": long},
		{"role": "tool", "tool_call_id": "5", "content": long},
	}
	got := microcompactToolResults(msgs, nil)
	// First two should be stubbed, last three kept verbatim.
	if got[0]["content"] == long || got[1]["content"] == long {
		t.Fatalf("expected first two tool messages to be compressed")
	}
	if !strings.Contains(got[0]["content"].(string), "已压缩") {
		t.Fatalf("expected stub marker, got %q", got[0]["content"])
	}
	if got[2]["content"] != long || got[3]["content"] != long || got[4]["content"] != long {
		t.Fatalf("recent three tool messages must remain verbatim")
	}
	// Schema preserved.
	for _, m := range got {
		if m["tool_call_id"] == nil {
			t.Fatalf("tool_call_id stripped: %v", m)
		}
	}
}

func TestMicrocompactToolResults_DoesNotMutateInput(t *testing.T) {
	long := strings.Repeat("a", 500)
	msgs := []map[string]any{
		{"role": "tool", "tool_call_id": "1", "content": long},
		{"role": "tool", "tool_call_id": "2", "content": long},
		{"role": "tool", "tool_call_id": "3", "content": long},
		{"role": "tool", "tool_call_id": "4", "content": long},
	}
	_ = microcompactToolResults(msgs, nil)
	if msgs[0]["content"] != long {
		t.Fatalf("original slice was mutated")
	}
}
