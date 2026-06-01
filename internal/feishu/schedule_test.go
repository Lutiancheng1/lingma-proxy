package feishu

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScheduledTaskStoreCreateListDueAndFinish(t *testing.T) {
	ctx := context.Background()
	store, err := newAgentStore(t.TempDir())
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

func TestScheduledTaskStoreReturnsNextRunTime(t *testing.T) {
	ctx := context.Background()
	store, err := newAgentStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	later := now.Add(10 * time.Minute)
	earlier := now.Add(2 * time.Minute)
	for _, task := range []ScheduledTask{
		{
			ID:           "sched_later",
			ChatID:       "oc_test",
			Name:         "later",
			Prompt:       "later",
			ScheduleKind: "at",
			Timezone:     defaultScheduleTimezone,
			Enabled:      true,
			NextRunAt:    later.Format(time.RFC3339),
			CreatedAt:    now.Format(time.RFC3339),
			UpdatedAt:    now.Format(time.RFC3339),
		},
		{
			ID:           "sched_earlier",
			ChatID:       "oc_test",
			Name:         "earlier",
			Prompt:       "earlier",
			ScheduleKind: "at",
			Timezone:     defaultScheduleTimezone,
			Enabled:      true,
			NextRunAt:    earlier.Format(time.RFC3339),
			CreatedAt:    now.Format(time.RFC3339),
			UpdatedAt:    now.Format(time.RFC3339),
		},
	} {
		if err := store.SaveScheduledTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}

	next, ok, err := store.NextScheduledTaskTime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !next.Equal(earlier) {
		t.Fatalf("next=%s ok=%v want %s", next, ok, earlier)
	}
}

func TestNextScheduleWaitUsesNearestTask(t *testing.T) {
	now := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	manager := NewManager(ManagerOptions{DataDir: t.TempDir()})
	task := ScheduledTask{
		ID:           "sched_near",
		ChatID:       "oc_test",
		Name:         "near",
		Prompt:       "near",
		ScheduleKind: "at",
		Timezone:     defaultScheduleTimezone,
		Enabled:      true,
		NextRunAt:    now.Add(3 * time.Second).Format(time.RFC3339),
		CreatedAt:    now.Format(time.RFC3339),
		UpdatedAt:    now.Format(time.RFC3339),
	}
	if err := manager.store.SaveScheduledTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	wait := manager.nextScheduleWait(context.Background(), now)
	if wait < 2900*time.Millisecond || wait > 3100*time.Millisecond {
		t.Fatalf("wait=%s, want about 3s", wait)
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
	message, direct := directScheduleMessage(task)
	if !direct || message != "提交周报" {
		t.Fatalf("expected direct reminder, direct=%v message=%q prompt=%q", direct, message, task.Prompt)
	}
}

func TestBuildScheduledTaskFromArgsPreservesAgentTask(t *testing.T) {
	now := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	task, err := buildScheduledTaskFromArgs("oc_test", "kmodel", map[string]any{
		"name":          "新闻摘要",
		"prompt":        "每天总结 AI 新闻",
		"delivery_mode": "agent",
		"schedule_kind": "every",
		"every_seconds": float64(3600),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, direct := directScheduleMessage(task); direct {
		t.Fatalf("agent task should not be treated as direct reminder: %#v", task)
	}
	if task.Prompt != "每天总结 AI 新闻" {
		t.Fatalf("prompt = %q", task.Prompt)
	}
}

func TestDirectScheduleMessageSupportsExplicitMode(t *testing.T) {
	now := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	task, err := buildScheduledTaskFromArgs("oc_test", "kmodel", map[string]any{
		"name":          "吃药",
		"prompt":        "吃药",
		"delivery_mode": "direct",
		"delay_seconds": float64(300),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	message, direct := directScheduleMessage(task)
	if !direct || message != "吃药" {
		t.Fatalf("expected explicit direct reminder, direct=%v message=%q prompt=%q", direct, message, task.Prompt)
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

func TestExecuteScheduleToolCreatesBuiltinAIRadarTemplate(t *testing.T) {
	manager := NewManager(ManagerOptions{DataDir: t.TempDir()})
	result := manager.executeScheduleTool(context.Background(), "oc_test", "kmodel", map[string]any{
		"action":   "create_builtin",
		"template": "ai_radar_daily",
	})
	if result.IsError {
		t.Fatalf("create_builtin failed: %s", result.Output)
	}
	if !strings.Contains(result.Output, `"template": "ai_radar_daily"`) || !strings.Contains(result.Output, `"AI Radar 日报"`) {
		t.Fatalf("create_builtin output = %s", result.Output)
	}
	tasks, err := manager.store.ListScheduledTasks(context.Background(), "oc_test", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks=%#v", tasks)
	}
	payload, ok := decodeBuiltinSchedulePrompt(tasks[0].Prompt)
	if !ok || payload.Template != scheduleTemplateAIRadar {
		t.Fatalf("builtin payload not stored: %#v prompt=%q", payload, tasks[0].Prompt)
	}
}

func TestExecuteAIRadarDailyTaskUsesAgentWorkspaceByDefault(t *testing.T) {
	previousBaseURL := aiRadarBaseURL
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/items" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if r.URL.Query().Get("mode") != "selected" || r.URL.Query().Get("since") == "" {
			t.Fatalf("query=%s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"items":[{"id":"1","title":"模型更新","url":"https://example.com/model","summary":"新的模型能力发布","category":"ai-models","publishedAt":%q}],"hasNext":false}`, time.Now().UTC().Add(-time.Hour).Format(time.RFC3339))
	}))
	defer server.Close()
	aiRadarBaseURL = server.URL
	t.Cleanup(func() { aiRadarBaseURL = previousBaseURL })

	workspace := filepath.Join(t.TempDir(), "workspace")
	manager := NewManager(ManagerOptions{})
	cfg := manager.Config()
	cfg.SafeFiles.WorkspaceDir = workspace
	manager.SetConfig(cfg)
	output, err := manager.executeBuiltinScheduledTask(context.Background(), ScheduledTask{
		ID:   "sched_ai_radar",
		Name: "AI Radar 日报",
		Prompt: encodeBuiltinSchedulePrompt(builtinSchedulePayload{
			Template: scheduleTemplateAIRadar,
		}),
	}, LogMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "AI Radar 日报") || !strings.Contains(output, "模型更新") || !strings.Contains(output, "https://example.com/model") {
		t.Fatalf("output=%s", output)
	}
	statePath := filepath.Join(workspace, "ai-radar", "state.json")
	stateBytes, err := os.ReadFile(statePath)
	if err != nil || !strings.Contains(string(stateBytes), "last_success_at") {
		t.Fatalf("state err=%v state=%s", err, string(stateBytes))
	}
}

func TestScheduledTaskMessageUsesCardKitThenFallbacks(t *testing.T) {
	replyLog := installFakeLarkCLI(t)
	manager := NewManager(ManagerOptions{})
	task := ScheduledTask{ID: "sched_card", ChatID: "oc_card", Name: "吃药", Model: "kmodel"}

	if err := manager.sendScheduledTaskMessage(context.Background(), task, "吃药"); err != nil {
		t.Fatal(err)
	}
	got := string(mustReadFileContainingEventually(t, replyLog, `im +messages-send --as bot --chat-id oc_card --msg-type interactive`))
	if !strings.Contains(got, `"type":"card"`) {
		t.Fatalf("expected CardKit card entity send, got: %s", got)
	}
}

func TestScheduledTaskMessageFallsBackToLegacyCard(t *testing.T) {
	replyLog := installFakeLarkCLI(t)
	t.Setenv("FEISHU_FAIL_CARDKIT_SEND", "1")
	manager := NewManager(ManagerOptions{})
	task := ScheduledTask{ID: "sched_legacy", ChatID: "oc_legacy", Name: "吃药", Model: "kmodel"}

	if err := manager.sendScheduledTaskMessage(context.Background(), task, "吃药"); err != nil {
		t.Fatal(err)
	}
	got := string(mustReadFileContainingEventually(t, replyLog, "wide_screen_mode"))
	if !strings.Contains(got, `"wide_screen_mode":true`) || !strings.Contains(got, `"tag":"div"`) {
		t.Fatalf("expected legacy interactive card fallback, got: %s", got)
	}
}

func TestScheduledTaskMessageFallsBackToMarkdown(t *testing.T) {
	replyLog := installFakeLarkCLI(t)
	t.Setenv("FEISHU_FAIL_CARDKIT_SEND", "1")
	t.Setenv("FEISHU_FAIL_LEGACY_CARD_SEND", "1")
	manager := NewManager(ManagerOptions{})
	task := ScheduledTask{ID: "sched_markdown", ChatID: "oc_markdown", Name: "吃药", Model: "kmodel"}

	if err := manager.sendScheduledTaskMessage(context.Background(), task, "吃药"); err != nil {
		t.Fatal(err)
	}
	got := string(mustReadFileContainingEventually(t, replyLog, "--markdown"))
	if !strings.Contains(got, "**定时任务：吃药**") {
		t.Fatalf("expected markdown fallback, got: %s", got)
	}
}
