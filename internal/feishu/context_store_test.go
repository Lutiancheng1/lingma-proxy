package feishu

import (
	"context"
	"testing"
)

func TestClearConversationRemovesPersistedState(t *testing.T) {
	ctx := context.Background()
	store, err := newBridgeStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	key := "oc_test_clear"
	snapshot := ConversationSnapshot{
		ModelOverride:   "kmodel",
		Summary:         "old summary",
		CompactBoundary: 1,
		History: []map[string]any{
			{"role": "user", "content": "hello"},
			{"role": "assistant", "content": "world"},
		},
	}
	if err := store.SaveConversationSnapshot(ctx, key, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSummary(ctx, key, "kmodel", StructuredSummary{PrimaryGoal: "old goal"}, 0, 2, 100, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveToolMemory(ctx, key, "lark_cli_exec", map[string]any{"argv": []any{"auth", "list"}}, "result", false); err != nil {
		t.Fatal(err)
	}

	if err := store.ClearConversation(ctx, key); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"conversations", "conversation_messages", "conversation_summaries", "tool_memories", "artifacts", "skill_invocations"} {
		var count int
		if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE conversation_key = ?", key).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s count = %d, want 0", table, count)
		}
	}
}
