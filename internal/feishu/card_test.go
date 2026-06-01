package feishu

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRenderFinalCardV2UsesCollapsedToolPanels(t *testing.T) {
	cardJSON, err := renderFinalCardV2(cardState{
		Status:      "done",
		StatusLabel: "完成",
		Model:       "kmodel",
		Steps: []cardStep{{
			Kind:  "tool",
			Title: "mcp__context7__query_docs",
			Body:  "参数：`{\"query\":\"Vue 3\"}`\n结果：Vue docs",
			Done:  true,
		}},
		Reply: "总结正文",
	})
	if err != nil {
		t.Fatal(err)
	}
	var card map[string]any
	if err := json.Unmarshal([]byte(cardJSON), &card); err != nil {
		t.Fatal(err)
	}
	body := card["body"].(map[string]any)
	elements := body["elements"].([]any)
	found := false
	for _, raw := range elements {
		element := raw.(map[string]any)
		if element["tag"] != "collapsible_panel" {
			continue
		}
		found = true
		if element["expanded"] != false {
			t.Fatalf("tool panel should default collapsed: %#v", element["expanded"])
		}
	}
	if !found {
		t.Fatalf("expected collapsed tool panel in %s", cardJSON)
	}
}

func TestRenderFinalCardV2UsesCollapsedThoughtPanels(t *testing.T) {
	cardJSON, err := renderFinalCardV2(cardState{
		Status:      "done",
		StatusLabel: "完成",
		Model:       "kmodel",
		Steps: []cardStep{{
			Kind:  "thought",
			Title: "思考",
			Body:  "我会先检查授权路径，再读取文件。",
		}},
		Reply: "当前已授权 workspace，可继续指定要读取的文件。",
	})
	if err != nil {
		t.Fatal(err)
	}
	var card map[string]any
	if err := json.Unmarshal([]byte(cardJSON), &card); err != nil {
		t.Fatal(err)
	}
	body := card["body"].(map[string]any)
	elements := body["elements"].([]any)
	found := false
	for _, raw := range elements {
		element := raw.(map[string]any)
		if element["tag"] != "collapsible_panel" {
			continue
		}
		found = true
		if element["expanded"] != false {
			t.Fatalf("thought panel should default collapsed: %#v", element["expanded"])
		}
		if !strings.Contains(element["element_id"].(string), "thought_panel") {
			t.Fatalf("thought panel should use thought element id: %#v", element["element_id"])
		}
	}
	if !found {
		t.Fatalf("expected collapsed thought panel in %s", cardJSON)
	}
}

func TestRenderStreamingCardV2CreatesStableElementSlots(t *testing.T) {
	cardJSON, err := renderStreamingCardV2(cardState{
		Status:      "thinking",
		StatusLabel: "正在思考",
		Model:       "kmodel",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cardJSON, `"element_id":"steps_md"`) {
		t.Fatalf("streaming card should include stable steps element: %s", cardJSON)
	}
	if !strings.Contains(cardJSON, `"element_id":"reply_md"`) {
		t.Fatalf("streaming card should include stable reply element: %s", cardJSON)
	}
	if !strings.Contains(cardJSON, `"element_id":"hint_md"`) {
		t.Fatalf("streaming card should include stable hint element: %s", cardJSON)
	}
	if strings.Contains(cardJSON, "collapsible_panel") {
		t.Fatalf("streaming card should not start with final collapsible panels: %s", cardJSON)
	}
}

func TestRenderStreamingCardV2KeepsDoneToolsCollapsed(t *testing.T) {
	cardJSON, err := renderStreamingCardV2(cardState{
		Status:      "tool",
		StatusLabel: "调用工具",
		Model:       "kmodel",
		Steps: []cardStep{{
			Kind:  "tool",
			Title: "lark_cli_exec",
			Body:  "参数：`{\"argv\":[\"drive\",\"files\",\"list\"]}`\n结果：ok",
			Done:  true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cardJSON, "collapsible_panel") || !strings.Contains(cardJSON, "lark_cli_exec") {
		t.Fatalf("streaming card should expose completed tools as collapsed panels: %s", cardJSON)
	}
	if !strings.Contains(cardJSON, `"content":"","element_id":"steps_md"`) {
		t.Fatalf("streaming card should leave static steps summary empty for tool-only updates: %s", cardJSON)
	}
	if !strings.Contains(cardJSON, `"element_id":"reply_md"`) {
		t.Fatalf("streaming card should keep reply slot for later deltas: %s", cardJSON)
	}
}

func TestRenderFinalCardV2SummarizesLongToolBodies(t *testing.T) {
	longBody := "参数：" + strings.Repeat("很长的工具参数和结果", 200)
	cardJSON, err := renderFinalCardV2(cardState{
		Status:      "done",
		StatusLabel: "完成",
		Steps: []cardStep{{
			Kind:  "tool",
			Title: "lark_docs_create",
			Body:  longBody,
			Done:  true,
		}},
		Reply: "完整结果已经生成。",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cardJSON, longBody) {
		t.Fatal("final card should not embed the full long tool body")
	}
	if !strings.Contains(cardJSON, "...") {
		t.Fatalf("final card should include a summarized tool body: %s", cardJSON)
	}
}

func TestCompactFinalCardStateKeepsCollapsedToolSummary(t *testing.T) {
	state := compactFinalCardState(cardState{
		Steps: []cardStep{{
			Kind:  "tool",
			Title: "lark_cli_exec",
			Body:  strings.Repeat("tool-output", 200),
			Done:  true,
		}},
		Reply: strings.Repeat("完整回复", 500),
	})
	if len(state.Steps) != 1 {
		t.Fatalf("compact final card should keep compact tool steps: %#v", state.Steps)
	}
	if state.Steps[0].Title != "lark_cli_exec" {
		t.Fatalf("compact final card lost tool title: %#v", state.Steps[0])
	}
	if len([]rune(state.Steps[0].Body)) > compactFinalStepBodyLimit+8 {
		t.Fatalf("compact final tool body too long: %d", len([]rune(state.Steps[0].Body)))
	}
	cardJSON, err := renderFinalCardV2(state)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cardJSON, "collapsible_panel") || !strings.Contains(cardJSON, "lark_cli_exec") {
		t.Fatalf("compact final card should keep collapsed tool panel: %s", cardJSON)
	}
	if strings.Contains(state.Reply, "Markdown 补发") {
		t.Fatalf("compact reply should not mention markdown fallback: %q", state.Reply)
	}
	if strings.Contains(state.Reply, "卡片中保留摘要") {
		t.Fatalf("compact reply should not duplicate compact hint: %q", state.Reply)
	}
	if strings.Contains(state.Hint, "卡片中保留摘要") || strings.Contains(state.Hint, "完整回复较长") {
		t.Fatalf("compact hint should not claim summary delivery: %q", state.Hint)
	}
	if !strings.Contains(state.Hint, "平台限制") {
		t.Fatalf("compact hint missing platform limit message: %q", state.Hint)
	}
	if len([]rune(state.Reply)) > compactFinalReplyLimit+80 {
		t.Fatalf("compact reply too long: %d runes", len([]rune(state.Reply)))
	}
}

func TestSplitFinalCardStatesKeepsLongReplyComplete(t *testing.T) {
	reply := strings.Repeat("长回复\n", 5000)
	parts, err := splitFinalCardStates(cardState{
		Status:      "done",
		StatusLabel: "完成",
		Title:       "aily",
		Steps: []cardStep{{
			Kind:  "tool",
			Title: "lark_cli_exec",
			Body:  "参数：`{\"argv\":[\"drive\",\"+search\"]}`\n结果：ok",
			Done:  true,
		}},
		Reply: reply,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) < 2 {
		t.Fatalf("long final reply should be split into multiple cards, got %d", len(parts))
	}
	var joined strings.Builder
	for i, part := range parts {
		cardJSON, renderErr := renderFinalCardV2(part)
		if renderErr != nil {
			t.Fatal(renderErr)
		}
		if shouldUseCompactFinalDelivery(cardJSON, part) {
			t.Fatalf("part %d still exceeds final card budget: %d", i+1, len([]byte(cardJSON)))
		}
		joined.WriteString(strings.TrimSpace(part.Reply))
	}
	if strings.ReplaceAll(joined.String(), "\n", "") != strings.ReplaceAll(strings.TrimSpace(reply), "\n", "") {
		t.Fatal("split final cards should preserve the complete reply text")
	}
	if len(parts[0].Steps) == 0 {
		t.Fatal("first split card should preserve tool panels")
	}
	if len(parts[1].Steps) != 0 {
		t.Fatal("continuation cards should not repeat tool panels")
	}
}

func TestShouldUseCompactFinalDeliveryForLargeCardJSON(t *testing.T) {
	cardJSON := strings.Repeat("x", finalCardJSONSoftLimit+1)
	if !shouldUseCompactFinalDelivery(cardJSON, cardState{Reply: "短回复"}) {
		t.Fatal("oversized final card JSON should use compact delivery")
	}
}

func TestShouldUseCompactFinalDeliveryUsesByteBudget(t *testing.T) {
	cardJSON := strings.Repeat("你", finalCardJSONSoftLimit/2)
	if !shouldUseCompactFinalDelivery(cardJSON, cardState{Reply: "短回复"}) {
		t.Fatal("multibyte card JSON should be checked by bytes, not runes")
	}
}

func TestFinalCardOperationTimeoutScalesForLongReplies(t *testing.T) {
	short := finalCardOperationTimeout(cardState{Reply: "短回复"})
	long := finalCardOperationTimeout(cardState{Reply: strings.Repeat("长回复", 4000)})
	if short != 42*time.Second {
		t.Fatalf("short final card timeout = %s", short)
	}
	if long <= short {
		t.Fatalf("long final card timeout should grow, short=%s long=%s", short, long)
	}
	if long > 3*time.Minute {
		t.Fatalf("long final card timeout should be capped, got %s", long)
	}
}

func TestStreamingReplyContentCapsLongPreview(t *testing.T) {
	longReply := strings.Repeat("长回复", 2000)
	preview := streamingReplyContent(longReply)
	if strings.Contains(preview, longReply) {
		t.Fatal("streaming card should not embed full long reply")
	}
	if strings.Contains(preview, "分段发送") || strings.Contains(preview, "卡片中保留摘要") {
		t.Fatalf("streaming preview should not promise fallback or summary: %q", preview)
	}
	if !strings.Contains(preview, "完整内容") {
		t.Fatalf("streaming preview should tell users final content will be complete: %q", preview)
	}
	if len([]rune(preview)) > streamingReplyPreviewLimit+80 {
		t.Fatalf("streaming preview too long: %d runes", len([]rune(preview)))
	}
}

func TestCardHeaderTitleUsesBotNameStatusFormat(t *testing.T) {
	got := cardHeaderTitle(cardState{Title: "Aily", StatusLabel: "正在思考"})
	if got != "Aily 正在思考" {
		t.Fatalf("card title = %q, want bot status format", got)
	}
}

func TestCardHeaderTitleFallsBackToStatusOnly(t *testing.T) {
	got := cardHeaderTitle(cardState{StatusLabel: "已完成"})
	if got != "已完成" {
		t.Fatalf("card title = %q, want status only", got)
	}
}

func TestRenderStepsMarkdownOmitsTools(t *testing.T) {
	rendered := renderStepsMarkdown([]cardStep{{
		Kind:  "tool",
		Title: "mcp__context7__query_docs",
		Body:  "参数：`{\"query\":\"Vue 3\"}`\n结果：very long tool output",
		Done:  true,
	}})
	if strings.TrimSpace(rendered) != "" {
		t.Fatalf("streaming step summary should omit tools entirely: %q", rendered)
	}
}
