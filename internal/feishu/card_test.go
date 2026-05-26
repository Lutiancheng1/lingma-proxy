package feishu

import (
	"encoding/json"
	"strings"
	"testing"
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
	if !strings.Contains(state.Reply, "完整回复已通过分段 Markdown 补发") {
		t.Fatalf("compact reply missing fallback hint: %q", state.Reply)
	}
	if len([]rune(state.Reply)) > compactFinalReplyLimit+80 {
		t.Fatalf("compact reply too long: %d runes", len([]rune(state.Reply)))
	}
}

func TestShouldUseCompactFinalDeliveryForLongReply(t *testing.T) {
	cardJSON, err := renderFinalCardV2(cardState{
		Status:      "done",
		StatusLabel: "完成",
		Reply:       strings.Repeat("长回复", 2000),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !shouldUseCompactFinalDelivery(cardJSON, cardState{Reply: strings.Repeat("长回复", 2000)}) {
		t.Fatal("long final reply should use compact card plus markdown fallback")
	}
}

func TestShouldUseCompactFinalDeliveryForLargeCardJSON(t *testing.T) {
	cardJSON := strings.Repeat("x", finalCardJSONSoftLimit+1)
	if !shouldUseCompactFinalDelivery(cardJSON, cardState{Reply: "短回复"}) {
		t.Fatal("oversized final card JSON should use compact delivery")
	}
}

func TestStreamingReplyContentCapsLongPreview(t *testing.T) {
	longReply := strings.Repeat("长回复", 2000)
	preview := streamingReplyContent(longReply)
	if strings.Contains(preview, longReply) {
		t.Fatal("streaming card should not embed full long reply")
	}
	if !strings.Contains(preview, "完整内容将在完成后分段发送") {
		t.Fatalf("streaming preview should tell users final content will be chunked: %q", preview)
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

func TestRenderStepsMarkdownOmitsToolBodies(t *testing.T) {
	rendered := renderStepsMarkdown([]cardStep{{
		Kind:  "tool",
		Title: "mcp__context7__query_docs",
		Body:  "参数：`{\"query\":\"Vue 3\"}`\n结果：very long tool output",
		Done:  true,
	}})
	if strings.Contains(rendered, "very long tool output") || strings.Contains(rendered, "参数") {
		t.Fatalf("streaming step summary should omit tool body: %q", rendered)
	}
}
