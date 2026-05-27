package feishu

import (
	"strings"
	"testing"
)

func TestEstimateContextBudgetWatermarks(t *testing.T) {
	cfg := DefaultContextConfig()
	cfg.ContextWindowOverride = 1000
	messages := []map[string]any{
		{"role": "system", "content": "rules"},
		{"role": "user", "content": "请总结"},
		{"role": "tool", "content": strings.Repeat("工具结果", 400)},
	}

	budget := estimateContextBudget("kmodel", cfg, messages, nil, "", conversationState{})
	if budget.ContextWindow != 1000 {
		t.Fatalf("context override not applied: %#v", budget)
	}
	if budget.Watermark == "ok" {
		t.Fatalf("expected non-ok watermark for large prompt, got %#v", budget)
	}
	if budget.ToolResultTokens == 0 {
		t.Fatalf("expected tool result tokens")
	}
}

func TestApplyBudgetCompactionRespectsWatermark(t *testing.T) {
	messages := []map[string]any{
		{"role": "system", "content": "rules"},
		{"role": "tool", "content": strings.Repeat("old tool ", 80)},
		{"role": "tool", "content": strings.Repeat("new tool ", 80)},
		{"role": "user", "content": "继续"},
	}
	okBudget := ContextBudgetSnapshot{Watermark: "ok"}
	unchanged := applyBudgetCompaction(messages, DefaultContextConfig(), okBudget)
	if unchanged[1]["content"] != messages[1]["content"] {
		t.Fatalf("ok watermark should not compact tool results")
	}

	compactBudget := ContextBudgetSnapshot{Watermark: "compact"}
	cfg := DefaultContextConfig()
	cfg.ToolResultRetention = 1
	compacted := applyBudgetCompaction(messages, cfg, compactBudget)
	content, _ := compacted[1]["content"].(string)
	if !strings.Contains(content, "old tool result compacted") {
		t.Fatalf("compact watermark should compact older tool results, got %q", content)
	}
}

func TestForcePromptTooLongCompactionForcesCriticalPath(t *testing.T) {
	messages := []map[string]any{
		{"role": "system", "content": "rules"},
		{"role": "tool", "content": strings.Repeat("old tool ", 80)},
		{"role": "tool", "content": strings.Repeat("mid tool ", 80)},
		{"role": "tool", "content": strings.Repeat("new tool ", 80)},
		{"role": "user", "content": "继续"},
	}
	cfg := DefaultContextConfig()
	cfg.ToolResultRetention = 1
	compacted := forcePromptTooLongCompaction(messages, cfg, ContextBudgetSnapshot{Watermark: "ok"})
	oldContent, _ := compacted[1]["content"].(string)
	if !strings.Contains(oldContent, "old tool result compacted") {
		t.Fatalf("prompt-too-long retry should force compaction, got %q", oldContent)
	}
}
