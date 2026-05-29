package feishu

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	schedulePollInterval      = 15 * time.Second
	scheduleTaskTimeout       = 10 * time.Minute
	scheduleMaxToolRounds     = 8
	defaultScheduleTimezone   = "Asia/Shanghai"
	minScheduleEverySeconds   = 60
	maxScheduleTasksPerPoll   = 3
	scheduleMessageChunkLimit = 3200
	scheduleSilentMarker      = "[SILENT]"
)

type ScheduledTask struct {
	ID             string
	ChatID         string
	Name           string
	Prompt         string
	ScheduleKind   string
	At             string
	EverySeconds   int
	Timezone       string
	Model          string
	Enabled        bool
	DeleteAfterRun bool
	NextRunAt      string
	LastRunAt      string
	LastStatus     string
	LastError      string
	CreatedAt      string
	UpdatedAt      string
}

type scheduleRunnerState struct {
	running map[string]struct{}
}

func newScheduleTaskID(chatID, name string) string {
	var random [8]byte
	_, _ = rand.Read(random[:])
	seed := fmt.Sprintf("%s:%s:%d:%x", chatID, name, time.Now().UnixNano(), random)
	sum := sha256.Sum256([]byte(seed))
	return "sched_" + hex.EncodeToString(sum[:8])
}

func (m *Manager) runScheduledTaskLoop(ctx context.Context) {
	if m.store == nil {
		return
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			m.runDueScheduledTasks(ctx)
			timer.Reset(schedulePollInterval)
		}
	}
}

func (m *Manager) runDueScheduledTasks(ctx context.Context) {
	if m.store == nil {
		return
	}
	tasks, err := m.store.DueScheduledTasks(ctx, time.Now(), maxScheduleTasksPerPoll)
	if err != nil {
		m.logf("warn", "Feishu Agent 定时任务扫描失败："+err.Error())
		return
	}
	for _, task := range tasks {
		if !m.markScheduleTaskRunning(task.ID) {
			continue
		}
		m.runScheduledTask(ctx, task, false)
		m.unmarkScheduleTaskRunning(task.ID)
	}
}

func (m *Manager) markScheduleTaskRunning(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.scheduleRunner.running == nil {
		m.scheduleRunner.running = make(map[string]struct{})
	}
	if _, ok := m.scheduleRunner.running[id]; ok {
		return false
	}
	m.scheduleRunner.running[id] = struct{}{}
	return true
}

func (m *Manager) unmarkScheduleTaskRunning(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.scheduleRunner.running, id)
}

func (m *Manager) runScheduledTask(parent context.Context, task ScheduledTask, manual bool) {
	meta := LogMeta{SessionID: task.ID, ChatID: task.ChatID}
	started := time.Now()
	task.LastRunAt = started.Format(time.RFC3339)
	runCtx, cancel := context.WithTimeout(parent, scheduleTaskTimeout)
	defer cancel()
	m.logf("info", "Feishu Agent 定时任务开始："+scheduleTaskLabel(task), meta)
	output, err := m.executeScheduledLLMTask(runCtx, task, meta)
	status := "success"
	errText := ""
	if err != nil {
		status = "error"
		errText = err.Error()
		output = "定时任务执行失败：" + err.Error()
		m.logf("warn", "Feishu Agent 定时任务失败："+err.Error(), meta)
	}
	if strings.TrimSpace(output) != "" && !strings.EqualFold(strings.TrimSpace(output), scheduleSilentMarker) {
		if sendErr := m.sendScheduledTaskMessage(context.Background(), task, output); sendErr != nil {
			status = "send_error"
			errText = sendErr.Error()
			m.logf("warn", "Feishu Agent 定时任务投递失败："+sendErr.Error(), meta)
		}
	}
	nextRunAt, enabled := nextScheduleAfterRun(task, started)
	if manual && task.ScheduleKind == "at" && strings.TrimSpace(nextRunAt) == "" {
		enabled = false
	}
	if m.store != nil {
		if err := m.store.FinishScheduledTaskRun(context.Background(), task, status, output, errText, time.Now(), nextRunAt, enabled); err != nil {
			m.logf("warn", "Feishu Agent 定时任务状态保存失败："+err.Error(), meta)
		}
	}
	m.logf("info", "Feishu Agent 定时任务结束："+scheduleTaskLabel(task)+" status="+status, meta)
}

func (m *Manager) executeScheduledLLMTask(ctx context.Context, task ScheduledTask, meta LogMeta) (string, error) {
	proxyURL := ""
	if m.opts.ProxyURL != nil {
		proxyURL = strings.TrimSpace(m.opts.ProxyURL())
	}
	if proxyURL == "" {
		return "", fmt.Errorf("当前代理地址为空")
	}
	cfg := m.Config()
	model := strings.TrimSpace(task.Model)
	if model == "" {
		model = cfg.Model
	}
	if model == "" {
		model = DefaultModel
	}
	if cfg.MCPEnabled {
		m.mcp.Sync(ctx, cfg)
	}
	mcpTools := m.mcp.Tools()
	skills, _ := discoverSkillsForPrompt(ctx)
	importedSkillListing := ""
	if m.skillService != nil {
		importedSkillListing = m.skillService.PromptListing(40)
	}
	systemPrompt := buildSystemPrompt(skills, cfg.BotIdentity, buildMCPPromptSection(mcpTools, m.mcp.Resources(), m.mcp.Prompts()), importedSkillListing) + "\n\n" +
		"当前正在执行 Feishu Agent 定时任务。最终回复会由 Agent 自动投递到飞书聊天；不要调用 lark_im_send 或 im +messages-send 自行发送本次结果。若没有新内容，请只回复 [SILENT]。"
	messages := []map[string]any{
		{"role": "system", "content": systemPrompt},
		{"role": "user", "content": task.Prompt},
	}
	if larkSkillContext := buildRelevantLarkSkillContext(skills, task.Prompt); strings.TrimSpace(larkSkillContext) != "" {
		messages = append([]map[string]any{{"role": "system", "content": systemPrompt}, {"role": "system", "content": larkSkillContext}}, map[string]any{"role": "user", "content": task.Prompt})
	}
	toolDefs := toolDefinitionsWithMCP(mcpTools)
	consecutiveFailures := 0
	for i := 0; i < scheduleMaxToolRounds; i++ {
		resp, err := callLLMForConversation(ctx, proxyURL, model, messages, false, toolDefs)
		if err != nil {
			return "", err
		}
		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("模型没有返回可用结果")
		}
		msg := resp.Choices[0].Message
		assistant := map[string]any{"role": "assistant", "content": msg.Content}
		if len(msg.ToolCalls) > 0 {
			assistant["tool_calls"] = msg.ToolCalls
		}
		messages = append(messages, assistant)
		if len(msg.ToolCalls) == 0 {
			return strings.TrimSpace(msg.Content), nil
		}
		for _, tc := range msg.ToolCalls {
			var args map[string]any
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
			result := m.executeScheduledToolCall(ctx, task, tc, args)
			content := result.Output
			if result.IsError {
				consecutiveFailures++
				content = content + "\n\n[scheduled-task guidance] 请根据错误调整参数；不要原样重复。飞书 CLI 用法不确定时，先调用 lark_skill_view 或 --help。"
			} else {
				consecutiveFailures = 0
			}
			m.logf("info", fmt.Sprintf("Feishu Agent scheduled tool result: %s %s", tc.Function.Name, content), meta)
			messages = append(messages, map[string]any{
				"role":         "tool",
				"tool_call_id": tc.ID,
				"content":      content,
				"is_error":     result.IsError,
			})
			if consecutiveFailures >= 4 {
				return "", fmt.Errorf("定时任务工具连续失败：%s", summarizeText(content, 240))
			}
		}
	}
	return "", fmt.Errorf("定时任务达到最大工具轮次，未生成最终回复")
}

func (m *Manager) executeScheduledToolCall(ctx context.Context, task ScheduledTask, tc ToolCall, args map[string]any) ToolExecutionResult {
	if tc.Function.Name == "schedule_task" {
		return ToolExecutionResult{Output: "[error] 定时任务执行过程中不允许递归创建或修改定时任务。", IsError: true}
	}
	if isAgentSkillTool(tc.Function.Name) {
		return m.executeAgentSkillTool(ctx, "schedule:"+task.ID, tc.ID, tc.Function.Name, args)
	}
	return executeToolContextWithRuntime(ctx, m.Config(), m.mcp, tc.Function.Name, args)
}

func (m *Manager) sendScheduledTaskMessage(ctx context.Context, task ScheduledTask, output string) error {
	body := strings.TrimSpace(output)
	if body == "" || strings.EqualFold(body, scheduleSilentMarker) {
		return nil
	}
	title := "定时任务"
	if strings.TrimSpace(task.Name) != "" {
		title = "定时任务：" + strings.TrimSpace(task.Name)
	}
	full := "**" + title + "**\n\n" + body
	parts := splitMarkdownReply(full, scheduleMessageChunkLimit)
	for i, part := range parts {
		if len(parts) > 1 {
			part = fmt.Sprintf("（%d/%d）\n\n%s", i+1, len(parts), part)
		}
		var outputBytes []byte
		err := m.runFeishuCardOperation(ctx, "scheduled message", func(opCtx context.Context) error {
			cmdCtx, cancel := context.WithTimeout(opCtx, 30*time.Second)
			defer cancel()
			cmd := commandContextWithEnv(cmdCtx, "lark-cli", "im", "+messages-send", "--as", "bot", "--chat-id", task.ChatID, "--markdown", part)
			var runErr error
			outputBytes, runErr = cmd.CombinedOutput()
			return runErr
		})
		if err != nil {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(decodeCommandOutput(outputBytes)))
		}
	}
	return nil
}

func (m *Manager) executeScheduleTool(ctx context.Context, chatID string, defaultModel string, args map[string]any) ToolExecutionResult {
	if m.store == nil {
		return ToolExecutionResult{Output: "[error] 定时任务存储未初始化", IsError: true}
	}
	action := strings.ToLower(strings.TrimSpace(stringArg(args, "action")))
	if action == "" {
		action = "list"
	}
	switch action {
	case "create":
		task, err := buildScheduledTaskFromArgs(chatID, defaultModel, args, time.Now())
		if err != nil {
			return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
		}
		if err := m.store.SaveScheduledTask(ctx, task); err != nil {
			return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
		}
		return ToolExecutionResult{Output: scheduleTaskJSON(map[string]any{
			"ok":          true,
			"action":      "create",
			"task_id":     task.ID,
			"name":        task.Name,
			"next_run_at": task.NextRunAt,
			"schedule":    scheduleTaskLabel(task),
		})}
	case "list":
		tasks, err := m.store.ListScheduledTasks(ctx, chatID, true)
		if err != nil {
			return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
		}
		return ToolExecutionResult{Output: renderScheduledTasks(tasks)}
	case "delete", "remove":
		id := stringArg(args, "task_id")
		if id == "" {
			id = stringArg(args, "id")
		}
		if strings.TrimSpace(id) == "" {
			return ToolExecutionResult{Output: "[error] task_id 不能为空", IsError: true}
		}
		if err := m.store.DeleteScheduledTask(ctx, id); err != nil {
			return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
		}
		return ToolExecutionResult{Output: "已删除定时任务：" + id}
	case "pause", "resume":
		id := stringArg(args, "task_id")
		if id == "" {
			id = stringArg(args, "id")
		}
		if strings.TrimSpace(id) == "" {
			return ToolExecutionResult{Output: "[error] task_id 不能为空", IsError: true}
		}
		enabled := action == "resume"
		if err := m.store.SetScheduledTaskEnabled(ctx, id, enabled); err != nil {
			return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
		}
		if enabled {
			return ToolExecutionResult{Output: "已恢复定时任务：" + id}
		}
		return ToolExecutionResult{Output: "已暂停定时任务：" + id}
	case "run_now":
		id := stringArg(args, "task_id")
		if id == "" {
			id = stringArg(args, "id")
		}
		task, err := m.store.GetScheduledTask(ctx, id)
		if err != nil {
			return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
		}
		if !m.markScheduleTaskRunning(task.ID) {
			return ToolExecutionResult{Output: "[error] 该定时任务正在执行中", IsError: true}
		}
		go func() {
			defer m.unmarkScheduleTaskRunning(task.ID)
			m.runScheduledTask(context.Background(), task, true)
		}()
		return ToolExecutionResult{Output: "已触发定时任务：" + task.ID}
	default:
		return ToolExecutionResult{Output: "[error] unsupported schedule action: " + action, IsError: true}
	}
}

func buildScheduledTaskFromArgs(defaultChatID, defaultModel string, args map[string]any, now time.Time) (ScheduledTask, error) {
	chatID := strings.TrimSpace(stringArg(args, "chat_id"))
	if chatID == "" {
		chatID = strings.TrimSpace(defaultChatID)
	}
	if chatID == "" {
		return ScheduledTask{}, fmt.Errorf("chat_id 不能为空")
	}
	prompt := strings.TrimSpace(stringArg(args, "prompt"))
	if prompt == "" {
		return ScheduledTask{}, fmt.Errorf("prompt 不能为空")
	}
	name := strings.TrimSpace(stringArg(args, "name"))
	if name == "" {
		name = summarizeText(prompt, 24)
	}
	tzName := strings.TrimSpace(stringArg(args, "timezone"))
	if tzName == "" {
		tzName = defaultScheduleTimezone
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return ScheduledTask{}, fmt.Errorf("timezone 无效：%s", tzName)
	}
	kind := strings.ToLower(strings.TrimSpace(stringArg(args, "schedule_kind")))
	everySeconds := intArg(args, "every_seconds")
	delaySeconds := intArg(args, "delay_seconds")
	atText := strings.TrimSpace(stringArg(args, "at"))
	if kind == "" {
		if everySeconds > 0 {
			kind = "every"
		} else {
			kind = "at"
		}
	}
	if kind != "at" && kind != "every" {
		return ScheduledTask{}, fmt.Errorf("schedule_kind 只支持 at 或 every")
	}
	if kind == "every" && everySeconds < minScheduleEverySeconds {
		return ScheduledTask{}, fmt.Errorf("every_seconds 至少 %d 秒", minScheduleEverySeconds)
	}
	nextRun, atValue, err := computeInitialScheduleRun(now, loc, kind, atText, delaySeconds, everySeconds)
	if err != nil {
		return ScheduledTask{}, err
	}
	deleteAfter := boolArg(args, "delete_after_run")
	if kind == "at" && !hasBoolArg(args, "delete_after_run") {
		deleteAfter = false
	}
	model := strings.TrimSpace(stringArg(args, "model"))
	if model == "" {
		model = strings.TrimSpace(defaultModel)
	}
	return ScheduledTask{
		ID:             newScheduleTaskID(chatID, name),
		ChatID:         chatID,
		Name:           name,
		Prompt:         prompt,
		ScheduleKind:   kind,
		At:             atValue,
		EverySeconds:   everySeconds,
		Timezone:       tzName,
		Model:          model,
		Enabled:        true,
		DeleteAfterRun: deleteAfter,
		NextRunAt:      nextRun.Format(time.RFC3339),
		CreatedAt:      now.Format(time.RFC3339),
		UpdatedAt:      now.Format(time.RFC3339),
	}, nil
}

func computeInitialScheduleRun(now time.Time, loc *time.Location, kind string, atText string, delaySeconds int, everySeconds int) (time.Time, string, error) {
	if delaySeconds > 0 {
		next := now.Add(time.Duration(delaySeconds) * time.Second)
		return next, next.In(loc).Format(time.RFC3339), nil
	}
	if strings.TrimSpace(atText) != "" {
		parsed, err := parseScheduleTime(atText, loc)
		if err != nil {
			return time.Time{}, "", err
		}
		if parsed.Before(now) && kind == "every" {
			for parsed.Before(now) {
				parsed = parsed.Add(time.Duration(everySeconds) * time.Second)
			}
		}
		return parsed, parsed.In(loc).Format(time.RFC3339), nil
	}
	if kind == "every" {
		next := now.Add(time.Duration(everySeconds) * time.Second)
		return next, "", nil
	}
	return time.Time{}, "", fmt.Errorf("一次性定时任务需要 at 或 delay_seconds")
}

func parseScheduleTime(value string, loc *time.Location) (time.Time, error) {
	value = strings.TrimSpace(value)
	layouts := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
	}
	for _, layout := range layouts {
		if layout == time.RFC3339 {
			if t, err := time.Parse(layout, value); err == nil {
				return t, nil
			}
			continue
		}
		if t, err := time.ParseInLocation(layout, value, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析 at 时间，请使用 RFC3339 或 YYYY-MM-DD HH:mm")
}

func nextScheduleAfterRun(task ScheduledTask, started time.Time) (string, bool) {
	if task.ScheduleKind == "every" && task.EverySeconds >= minScheduleEverySeconds {
		next := started.Add(time.Duration(task.EverySeconds) * time.Second)
		return next.Format(time.RFC3339), true
	}
	if task.DeleteAfterRun {
		return "", false
	}
	return "", false
}

func renderScheduledTasks(tasks []ScheduledTask) string {
	if len(tasks) == 0 {
		return "当前会话没有定时任务。"
	}
	lines := []string{"当前会话定时任务："}
	for _, task := range tasks {
		status := "启用"
		if !task.Enabled {
			status = "暂停"
		}
		lines = append(lines, fmt.Sprintf("- %s（%s，%s）：%s；下次执行：%s", task.Name, task.ID, status, scheduleTaskLabel(task), emptyAsDash(task.NextRunAt)))
	}
	return strings.Join(lines, "\n")
}

func scheduleTaskLabel(task ScheduledTask) string {
	switch task.ScheduleKind {
	case "every":
		if strings.TrimSpace(task.At) != "" {
			return fmt.Sprintf("每 %s，从 %s 开始", formatDurationSeconds(task.EverySeconds), task.At)
		}
		return "每 " + formatDurationSeconds(task.EverySeconds)
	default:
		return "一次性 " + emptyAsDash(task.NextRunAt)
	}
}

func formatDurationSeconds(seconds int) string {
	if seconds%86400 == 0 {
		return fmt.Sprintf("%d 天", seconds/86400)
	}
	if seconds%3600 == 0 {
		return fmt.Sprintf("%d 小时", seconds/3600)
	}
	if seconds%60 == 0 {
		return fmt.Sprintf("%d 分钟", seconds/60)
	}
	return fmt.Sprintf("%d 秒", seconds)
}

func emptyAsDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return strings.TrimSpace(value)
}

func scheduleTaskJSON(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}

func intArg(args map[string]any, key string) int {
	if args == nil {
		return 0
	}
	switch v := args[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	default:
		return 0
	}
}

func hasBoolArg(args map[string]any, key string) bool {
	if args == nil {
		return false
	}
	_, ok := args[key]
	return ok
}

func isScheduleTool(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), "schedule_task")
}

func (m *Manager) commandScheduleText(ctx context.Context, chatID string, model string, args []string) string {
	if m.store == nil {
		return "定时任务存储未初始化。"
	}
	if len(args) == 0 || strings.EqualFold(args[0], "list") {
		tasks, err := m.store.ListScheduledTasks(ctx, chatID, true)
		if err != nil {
			return "定时任务列表读取失败：" + err.Error()
		}
		return renderScheduledTasks(tasks)
	}
	action := strings.ToLower(args[0])
	if len(args) < 2 {
		return "用法：/schedule list | /schedule delete <id> | /schedule pause <id> | /schedule resume <id> | /schedule run <id>"
	}
	id := strings.TrimSpace(args[1])
	switch action {
	case "delete", "remove":
		if err := m.store.DeleteScheduledTask(ctx, id); err != nil {
			return "删除失败：" + err.Error()
		}
		return "已删除定时任务：" + id
	case "pause":
		if err := m.store.SetScheduledTaskEnabled(ctx, id, false); err != nil {
			return "暂停失败：" + err.Error()
		}
		return "已暂停定时任务：" + id
	case "resume":
		task, err := m.store.GetScheduledTask(ctx, id)
		if err != nil {
			return "恢复失败：" + err.Error()
		}
		if strings.TrimSpace(task.NextRunAt) == "" {
			nextRun, _, err := computeInitialScheduleRun(time.Now(), time.Local, task.ScheduleKind, task.At, 0, task.EverySeconds)
			if err == nil {
				task.NextRunAt = nextRun.Format(time.RFC3339)
				_ = m.store.SaveScheduledTask(ctx, task)
			}
		}
		if err := m.store.SetScheduledTaskEnabled(ctx, id, true); err != nil {
			return "恢复失败：" + err.Error()
		}
		return "已恢复定时任务：" + id
	case "run":
		task, err := m.store.GetScheduledTask(ctx, id)
		if err != nil {
			if err == sql.ErrNoRows {
				return "没有找到定时任务：" + id
			}
			return "读取失败：" + err.Error()
		}
		if !m.markScheduleTaskRunning(task.ID) {
			return "该定时任务正在执行中。"
		}
		go func() {
			defer m.unmarkScheduleTaskRunning(task.ID)
			if strings.TrimSpace(task.Model) == "" {
				task.Model = model
			}
			m.runScheduledTask(context.Background(), task, true)
		}()
		return "已触发定时任务：" + id
	default:
		return "用法：/schedule list | /schedule delete <id> | /schedule pause <id> | /schedule resume <id> | /schedule run <id>"
	}
}
