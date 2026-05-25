package feishu

import (
	"context"
	"errors"
	"os"
	"testing"
)

// TestMain installs package-level defaults for the streaming LLM hooks so
// existing message-handling tests (which only stub the non-streaming path)
// don't accidentally hit the live upstream proxy. Each individual test can
// still override these to exercise streaming behaviour explicitly.
func TestMain(m *testing.M) {
	callLLMStreamForConversation = func(ctx context.Context, proxyURL string, model string, messages []map[string]any, forceToolUse bool, tools []map[string]any, deltas streamingDelta) (*llmResponse, error) {
		return nil, errors.New("streaming disabled in unit tests")
	}
	callLLMPlainStreamForFinal = func(ctx context.Context, proxyURL string, model string, messages []map[string]any, deltas streamingDelta) (*llmResponse, error) {
		return nil, errors.New("streaming disabled in unit tests")
	}
	os.Exit(m.Run())
}
