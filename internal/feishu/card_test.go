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
