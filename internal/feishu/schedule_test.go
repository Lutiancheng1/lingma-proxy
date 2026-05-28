package feishu

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestScheduledTaskStoreCreateListDueAndFinish(t *testing.T) {
	ctx := context.Background()
	store, err := newBridgeStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	task := ScheduledTask{
		ID:             "sched_test",
		ChatID:         "oc_test",
		Name:           "每日摘要",
		Prompt:         "总结今天 AI 新闻",
		ScheduleKind:   "every",
		EverySeconds:   3600,
		Timezone:       defaultScheduleTimezone,
		Model:          "kmodel",
		Enabled:        true,
		DeleteAfterRun: false,
		NextRunAt:      now.Add(-time.Minute).Format(time.RFC3339),
		CreatedAt:      now.Format(time.RFC3339),
		UpdatedAt:      now.Format(time.RFC3339),
	}
	if err := store.SaveScheduledTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	listed, err := store.ListScheduledTasks(ctx, "oc_test", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != task.ID {
		t.Fatalf("listed = %#v", listed)
	}
	due, err := store.DueScheduledTasks(ctx, now, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != task.ID {
		t.Fatalf("due = %#v", due)
	}
	next := now.Add(time.Hour).Format(time.RFC3339)
	task.LastRunAt = now.Format(time.RFC3339)
	if err := store.FinishScheduledTaskRun(ctx, task, "success", "ok", "", now, next, true); err != nil {
		t.Fatal(err)
	}
	updated, err := store.GetScheduledTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.NextRunAt != next || updated.LastStatus != "success" || !updated.Enabled {
		t.Fatalf("updated = %#v", updated)
	}
}

func TestBuildScheduledTaskFromArgsParsesOneShotDelay(t *testing.T) {
	now := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	task, err := buildScheduledTaskFromArgs("oc_test", "kmodel", map[string]any{
		"name":          "稍后提醒",
		"prompt":        "提醒我提交周报",
		"schedule_kind": "at",
		"delay_seconds": float64(120),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if task.ChatID != "oc_test" || task.Model != "kmodel" || task.ScheduleKind != "at" {
		t.Fatalf("task = %#v", task)
	}
	if task.NextRunAt != now.Add(120*time.Second).Format(time.RFC3339) {
		t.Fatalf("next_run_at = %s", task.NextRunAt)
	}
}

func TestBuildScheduledTaskFromArgsParsesRecurringAnchor(t *testing.T) {
	now := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	task, err := buildScheduledTaskFromArgs("oc_test", "kmodel", map[string]any{
		"name":          "日报",
		"prompt":        "每天总结 AI 新闻",
		"schedule_kind": "every",
		"every_seconds": float64(86400),
		"at":            "2026-05-28 09:00",
		"timezone":      "Asia/Shanghai",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if task.ScheduleKind != "every" || task.EverySeconds != 86400 {
		t.Fatalf("task = %#v", task)
	}
	if !strings.Contains(task.NextRunAt, "2026-05-29T09:00:00+08:00") {
		t.Fatalf("expected past anchor to roll forward, got %s", task.NextRunAt)
	}
}

func TestExecuteScheduleToolCreateAndList(t *testing.T) {
	manager := NewManager(ManagerOptions{DataDir: t.TempDir()})
	result := manager.executeScheduleTool(context.Background(), "oc_test", "kmodel", map[string]any{
		"action":        "create",
		"name":          "测试任务",
		"prompt":        "到点回复测试",
		"delay_seconds": float64(300),
	})
	if result.IsError {
		t.Fatalf("create failed: %s", result.Output)
	}
	if !strings.Contains(result.Output, "sched_") {
		t.Fatalf("create output = %s", result.Output)
	}
	list := manager.commandScheduleText(context.Background(), "oc_test", "kmodel", nil)
	if !strings.Contains(list, "测试任务") || !strings.Contains(list, "sched_") {
		t.Fatalf("list = %s", list)
	}
}
