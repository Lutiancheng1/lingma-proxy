package feishu

import (
	"reflect"
	"testing"
)

func TestSealOrphanToolCalls_NoToolCalls(t *testing.T) {
	msgs := []map[string]any{
		{"role": "user", "content": "hi"},
		{"role": "assistant", "content": "hello"},
	}
	got, gotRaw, missing := sealOrphanToolCalls(msgs, msgs, "x")
	if len(missing) != 0 {
		t.Fatalf("missing should be empty, got %v", missing)
	}
	if !reflect.DeepEqual(got, msgs) || !reflect.DeepEqual(gotRaw, msgs) {
		t.Fatalf("messages mutated unexpectedly")
	}
}

func TestSealOrphanToolCalls_AllAnswered(t *testing.T) {
	msgs := []map[string]any{
		{"role": "user", "content": "hi"},
		{"role": "assistant", "tool_calls": []ToolCall{{ID: "a"}, {ID: "b"}}},
		{"role": "tool", "tool_call_id": "a", "content": "ok"},
		{"role": "tool", "tool_call_id": "b", "content": "ok"},
	}
	_, _, missing := sealOrphanToolCalls(msgs, msgs, "x")
	if len(missing) != 0 {
		t.Fatalf("expected no missing, got %v", missing)
	}
}

func TestSealOrphanToolCalls_PartiallyAnswered(t *testing.T) {
	msgs := []map[string]any{
		{"role": "user", "content": "hi"},
		{"role": "assistant", "tool_calls": []ToolCall{{ID: "a"}, {ID: "b"}, {ID: "c"}}},
		{"role": "tool", "tool_call_id": "a", "content": "ok"},
	}
	got, gotRaw, missing := sealOrphanToolCalls(msgs, msgs, "[interrupted]")
	if !reflect.DeepEqual(missing, []string{"b", "c"}) {
		t.Fatalf("missing = %v, want [b c]", missing)
	}
	if len(got) != 5 || len(gotRaw) != 5 {
		t.Fatalf("len(got)=%d len(gotRaw)=%d, want 5 5", len(got), len(gotRaw))
	}
	last := got[len(got)-1]
	if last["tool_call_id"] != "c" || last["is_error"] != true {
		t.Fatalf("last entry not sealed: %v", last)
	}
}

func TestSealOrphanToolCalls_AnyShape(t *testing.T) {
	msgs := []map[string]any{
		{"role": "assistant", "tool_calls": []any{
			map[string]any{"id": "a"},
			map[string]any{"id": "b"},
		}},
	}
	_, _, missing := sealOrphanToolCalls(msgs, msgs, "x")
	if !reflect.DeepEqual(missing, []string{"a", "b"}) {
		t.Fatalf("missing = %v", missing)
	}
}
