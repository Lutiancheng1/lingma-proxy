package feishu

import (
	"context"
	"strings"
	"testing"
)

// TestStreamingReplyDeliveredToCardAndFinal verifies that when the LLM
// returns text via the streaming hook, each delta is forwarded to the card
// (via SetReply with the running prefix) and the final reply is the
// concatenated text.
func TestStreamingReplyDeliveredToCardAndFinal(t *testing.T) {
	replyLog := installFakeLarkCLI(t)
	oldDiscover := discoverSkillsForPrompt
	oldStream := callLLMStreamForConversation
	oldNonStream := callLLMForConversation
	t.Cleanup(func() {
		discoverSkillsForPrompt = oldDiscover
		callLLMStreamForConversation = oldStream
		callLLMForConversation = oldNonStream
	})
	discoverSkillsForPrompt = func(context.Context) ([]SkillStatus, error) {
		return []SkillStatus{{Name: "lark-im", Found: true}}, nil
	}

	chunks := []string{"流式", "打字机", "效果"}
	callLLMStreamForConversation = func(ctx context.Context, proxyURL string, model string, messages []map[string]any, forceToolUse bool, tools []map[string]any, deltas streamingDelta) (*llmResponse, error) {
		for _, c := range chunks {
			if deltas.onText != nil {
				deltas.onText(c)
			}
		}
		var resp llmResponse
		resp.Choices = append(resp.Choices, struct {
			Message struct {
				Content   string     `json:"content"`
				ToolCalls []ToolCall `json:"tool_calls"`
			} `json:"message"`
		}{})
		resp.Choices[0].Message.Content = strings.Join(chunks, "")
		return &resp, nil
	}
	callLLMForConversation = func(ctx context.Context, proxyURL string, model string, messages []map[string]any, forceToolUse bool, tools []map[string]any) (*llmResponse, error) {
		t.Fatal("non-streaming path should not be called when streaming succeeds")
		return nil, nil
	}

	manager := NewManager(ManagerOptions{
		ProxyURL: func() string { return "http://127.0.0.1:8095/v1/chat/completions" },
	})
	manager.SetConfig(Config{Model: "kmodel", MaxToolRounds: 5})
	manager.handleEvent(context.Background(), incomingEvent{
		ChatID:      "oc_stream_chat",
		ChatType:    "p2p",
		Content:     "在吗",
		CreateTime:  "1",
		EventID:     "evt_stream_1",
		MessageID:   "om_stream_message",
		SenderID:    "ou_stream_user",
		MessageType: "text",
	})

	full := strings.Join(chunks, "")
	got := string(mustReadFileContainingEventually(t, replyLog, full))
	if !strings.Contains(got, "im +messages-reply --as bot --message-id om_stream_message --msg-type interactive") {
		t.Fatalf("expected interactive card patch in reply log, got: %s", got)
	}
	// Each delta should have triggered at least one card patch with the
	// running prefix; the throttle may coalesce some, so we only require
	// that an intermediate prefix appears at least once.
	if !strings.Contains(got, chunks[0]) {
		t.Fatalf("first delta %q never appeared in card patches: %s", chunks[0], got)
	}
	if !strings.Contains(got, "/settings") {
		t.Fatalf("finalize should close CardKit streaming via settings endpoint, got: %s", got)
	}
	if !strings.Contains(got, "已完成") {
		t.Fatalf("finalize should refresh the card header to completed state, got: %s", got)
	}
}

// TestStreamingFallsBackToNonStreamWhenStreamFails ensures the non-stream
// code path still runs when the upstream rejects streaming and no text was
// received.
func TestStreamingFallsBackToNonStreamWhenStreamFails(t *testing.T) {
	replyLog := installFakeLarkCLI(t)
	oldDiscover := discoverSkillsForPrompt
	oldStream := callLLMStreamForConversation
	oldNonStream := callLLMForConversation
	t.Cleanup(func() {
		discoverSkillsForPrompt = oldDiscover
		callLLMStreamForConversation = oldStream
		callLLMForConversation = oldNonStream
	})
	discoverSkillsForPrompt = func(context.Context) ([]SkillStatus, error) {
		return []SkillStatus{{Name: "lark-im", Found: true}}, nil
	}
	streamCalled := 0
	callLLMStreamForConversation = func(ctx context.Context, proxyURL string, model string, messages []map[string]any, forceToolUse bool, tools []map[string]any, deltas streamingDelta) (*llmResponse, error) {
		streamCalled++
		return nil, errAssertion("stream broken")
	}
	nonStreamCalled := 0
	callLLMForConversation = func(ctx context.Context, proxyURL string, model string, messages []map[string]any, forceToolUse bool, tools []map[string]any) (*llmResponse, error) {
		nonStreamCalled++
		var resp llmResponse
		resp.Choices = append(resp.Choices, struct {
			Message struct {
				Content   string     `json:"content"`
				ToolCalls []ToolCall `json:"tool_calls"`
			} `json:"message"`
		}{})
		resp.Choices[0].Message.Content = "回退成功。"
		return &resp, nil
	}

	manager := NewManager(ManagerOptions{
		ProxyURL: func() string { return "http://127.0.0.1:8095/v1/chat/completions" },
	})
	manager.SetConfig(Config{Model: "kmodel", MaxToolRounds: 5})
	manager.handleEvent(context.Background(), incomingEvent{
		ChatID:      "oc_fallback_chat",
		ChatType:    "p2p",
		Content:     "test",
		CreateTime:  "1",
		EventID:     "evt_fb_1",
		MessageID:   "om_fb_message",
		SenderID:    "ou_fb_user",
		MessageType: "text",
	})
	mustReadFileContainingEventually(t, replyLog, "回退成功。")
	if streamCalled == 0 {
		t.Fatal("streaming hook was never invoked")
	}
	if nonStreamCalled == 0 {
		t.Fatal("non-streaming fallback was not invoked")
	}
}

type errAssertion string

func (e errAssertion) Error() string { return string(e) }
