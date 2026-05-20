package feishu

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	goruntime "runtime"
	"strings"
	"sync"
	"time"
)

const (
	replyWindowTTL                   = 30 * time.Minute
	replyWindowCleanupInterval       = 5 * time.Minute
	persistedConversationRecentLimit = 8
)

type ManagerOptions struct {
	ProxyURL func() string
	Logf     func(level, message string, meta LogMeta)
	Emit     func(status Status)
	Persist  func()
}

type LogMeta struct {
	SessionID string
	ChatID    string
	MessageID string
}

type conversationState struct {
	History     []map[string]any
	Summary     string
	Summarizing bool
}

type ConversationSnapshot struct {
	History []map[string]any `json:"history,omitempty"`
	Summary string           `json:"summary,omitempty"`
}

type Manager struct {
	mu sync.RWMutex

	cfg    Config
	status Status
	opts   ManagerOptions

	cancelFunc context.CancelFunc
	stdin      io.WriteCloser

	setupCancel context.CancelFunc
	loginCancel context.CancelFunc

	conversations map[string]conversationState
}

func NewManager(opts ManagerOptions) *Manager {
	return &Manager{
		cfg: DefaultConfig(),
		status: Status{
			Platform:       goruntime.GOOS,
			Arch:           goruntime.GOARCH,
			RequiredSkills: append([]string(nil), requiredSkillNames...),
			CurrentModel:   DefaultModel,
		},
		opts:          opts,
		conversations: make(map[string]conversationState),
	}
}

func (m *Manager) SetConfig(cfg Config) {
	m.mu.Lock()
	m.cfg = NormalizeConfig(cfg)
	m.status.CurrentModel = m.cfg.Model
	m.mu.Unlock()
}

func (m *Manager) Config() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

func (m *Manager) Probe(ctx context.Context) Status {
	m.refreshStatus(ctx)
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

func (m *Manager) refreshStatus(ctx context.Context) {
	node, npm, npx := detectNodeAndNPM()
	cli := detectBinary("lark-cli", "--version")
	skills, _ := discoverSkills(ctx)
	configStatus := readCLIConfigStatus()
	authStatus := readAuthStatus(ctx)

	m.mu.Lock()
	m.status.Platform = goruntime.GOOS
	m.status.Arch = goruntime.GOARCH
	m.status.Node = node
	m.status.NPM = npm
	m.status.NPX = npx
	m.status.CLI = cli
	m.status.Skills = skills
	m.status.SkillsReady = skillsReady(skills)
	m.status.Config = configStatus
	m.status.Auth = authStatus
	if authStatus.Authorized && !m.status.LoginRunning {
		m.status.LoginURL = ""
	}
	m.status.LastCheckedAt = timestampNow()
	if m.status.CurrentModel == "" {
		m.status.CurrentModel = m.cfg.Model
	}
	status := m.status
	m.mu.Unlock()
	m.emit(status)
}

func (m *Manager) emit(status Status) {
	if m.opts.Emit != nil {
		m.opts.Emit(status)
	}
}

func (m *Manager) notifyConversationChanged() {
	if m.opts.Persist != nil {
		m.opts.Persist()
	}
}

func (m *Manager) logf(level, message string, meta ...LogMeta) {
	if m.opts.Logf != nil {
		var payload LogMeta
		if len(meta) > 0 {
			payload = meta[0]
		}
		m.opts.Logf(level, message, payload)
	}
}

func (m *Manager) InstallCLI(ctx context.Context) error {
	m.mu.Lock()
	m.status.InstallRunning = true
	m.status.LastError = ""
	status := m.status
	m.mu.Unlock()
	m.emit(status)

	err := installCLI(ctx)
	m.refreshStatus(ctx)

	m.mu.Lock()
	m.status.InstallRunning = false
	if err != nil {
		m.status.LastError = err.Error()
	}
	status = m.status
	m.mu.Unlock()
	m.emit(status)

	if err != nil {
		return err
	}
	m.logf("info", "飞书 CLI 安装完成")
	return nil
}

func (m *Manager) StartSetupNew(ctx context.Context) error {
	m.mu.Lock()
	if m.status.SetupRunning {
		m.mu.Unlock()
		return fmt.Errorf("初始化流程已在运行")
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.setupCancel = cancel
	m.status.SetupRunning = true
	m.status.SetupURL = ""
	m.status.LastError = ""
	m.status.LastOutput = ""
	status := m.status
	m.mu.Unlock()
	m.emit(status)

	go func() {
		cmd := commandContextWithEnv(runCtx, "lark-cli", "config", "init", "--new", "--lang", "zh")
		err := runStreamingCommand(runCtx, cmd, func(line string) {
			line = formatOutputLine(line)
			if line == "" {
				return
			}
			m.mu.Lock()
			m.status.LastOutput = line
			status := m.status
			m.mu.Unlock()
			m.emit(status)
		}, func(url string) {
			m.mu.Lock()
			m.status.SetupURL = url
			status := m.status
			m.mu.Unlock()
			m.emit(status)
		})
		m.refreshStatus(context.Background())
		m.mu.Lock()
		m.status.SetupRunning = false
		m.setupCancel = nil
		if err != nil {
			m.status.LastError = err.Error()
		}
		status := m.status
		m.mu.Unlock()
		m.emit(status)
	}()
	return nil
}

func (m *Manager) BindWithAppSecret(ctx context.Context, appID string, appSecret string) error {
	appID = strings.TrimSpace(appID)
	appSecret = strings.TrimSpace(appSecret)
	if appID == "" || appSecret == "" {
		return fmt.Errorf("app id 和 app secret 不能为空")
	}
	if err := storeSecret("lingma-proxy-feishu-app-secret", appID, appSecret); err != nil {
		return err
	}
	cmd := commandContextWithEnv(ctx, "lark-cli", "config", "init", "--app-id", appID, "--app-secret-stdin", "--brand", "feishu")
	cmd.Stdin = strings.NewReader(appSecret)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("绑定飞书应用失败: %w: %s", err, strings.TrimSpace(string(output)))
	}
	m.refreshStatus(ctx)
	m.mu.Lock()
	m.cfg.AppID = appID
	m.cfg.SetupMode = SetupModeManual
	m.status.LastOutput = strings.TrimSpace(string(output))
	status := m.status
	m.mu.Unlock()
	m.emit(status)
	m.logf("info", "飞书 CLI 已绑定现有应用")
	return nil
}

func (m *Manager) StartLogin(ctx context.Context) error {
	return m.startLoginWithArgs(ctx, []string{"lark-cli", "auth", "login", "--recommend"})
}

func (m *Manager) startScopeLogin(ctx context.Context, scopes ...string) (string, error) {
	scopes = uniqueNonEmpty(scopes)
	if len(scopes) == 0 {
		return "", fmt.Errorf("scope 不能为空")
	}
	scopeArg := strings.Join(scopes, ",")

	m.mu.Lock()
	if m.status.LoginRunning {
		url := m.status.LoginURL
		m.mu.Unlock()
		return url, nil
	}
	m.mu.Unlock()

	urlCh := make(chan string, 1)
	err := m.startLoginWithArgs(ctx, []string{"lark-cli", "auth", "login", "--scope", scopeArg}, urlCh)
	if err != nil {
		return "", err
	}

	select {
	case url := <-urlCh:
		return strings.TrimSpace(url), nil
	case <-time.After(3 * time.Second):
		m.mu.RLock()
		url := m.status.LoginURL
		m.mu.RUnlock()
		return strings.TrimSpace(url), nil
	}
}

func (m *Manager) startLoginWithArgs(ctx context.Context, args []string, urlCh ...chan string) error {
	m.mu.Lock()
	if m.status.LoginRunning {
		m.mu.Unlock()
		return fmt.Errorf("授权流程已在运行")
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.loginCancel = cancel
	m.status.LoginRunning = true
	m.status.LoginURL = ""
	m.status.LastError = ""
	m.status.LastOutput = ""
	status := m.status
	m.mu.Unlock()
	m.emit(status)

	go func() {
		cmd := commandContextWithEnv(runCtx, args[0], args[1:]...)
		err := runStreamingCommand(runCtx, cmd, func(line string) {
			line = formatOutputLine(line)
			if line == "" {
				return
			}
			m.mu.Lock()
			m.status.LastOutput = line
			status := m.status
			m.mu.Unlock()
			m.emit(status)
		}, func(url string) {
			m.mu.Lock()
			m.status.LoginURL = url
			status := m.status
			m.mu.Unlock()
			m.emit(status)
			if len(urlCh) > 0 && urlCh[0] != nil {
				select {
				case urlCh[0] <- url:
				default:
				}
			}
		})
		m.refreshStatus(context.Background())
		m.mu.Lock()
		m.status.LoginRunning = false
		m.loginCancel = nil
		if err != nil {
			m.status.LastError = err.Error()
		}
		status := m.status
		m.mu.Unlock()
		m.emit(status)
	}()
	return nil
}

func (m *Manager) Start(ctx context.Context) error {
	m.refreshStatus(ctx)
	m.mu.Lock()
	if m.status.Running {
		m.mu.Unlock()
		return nil
	}
	if !m.status.Node.Found || !m.status.NPM.Found || !m.status.NPX.Found {
		m.mu.Unlock()
		return fmt.Errorf("Node/npm/npx 未就绪")
	}
	if !m.status.CLI.Found {
		m.mu.Unlock()
		return fmt.Errorf("lark-cli 未安装")
	}
	if !m.status.SkillsReady {
		m.mu.Unlock()
		return fmt.Errorf("必需的 lark-* skills 未安装完整")
	}
	if !m.status.Config.Configured {
		m.mu.Unlock()
		return fmt.Errorf("飞书 CLI 尚未完成应用初始化")
	}
	if !m.status.Auth.Authorized {
		m.mu.Unlock()
		return fmt.Errorf("飞书 CLI 尚未完成用户授权")
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.cancelFunc = cancel
	m.status.LastError = ""
	status := m.status
	m.mu.Unlock()
	m.emit(status)

	cmd := commandContextWithEnv(runCtx, "lark-cli", "event", "consume", "im.message.receive_v1", "--as", "bot")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return err
	}

	m.mu.Lock()
	m.stdin = stdin
	m.status.Running = true
	m.status.LastStartedAt = timestampNow()
	status = m.status
	m.mu.Unlock()
	m.emit(status)
	m.logf("info", "Feishu bridge 已启动，等待消息中")

	go m.readEvents(runCtx, stdout)
	go m.readEventStderr(runCtx, stderr)
	go func() {
		err := cmd.Wait()
		m.mu.Lock()
		m.cancelFunc = nil
		m.stdin = nil
		m.status.Running = false
		if err != nil && runCtx.Err() == nil {
			m.status.LastError = err.Error()
		}
		status := m.status
		m.mu.Unlock()
		m.emit(status)
		if err != nil && runCtx.Err() == nil {
			m.logf("error", "Feishu bridge 已退出："+err.Error())
		}
	}()
	return nil
}

func (m *Manager) Stop() error {
	m.mu.Lock()
	cancel := m.cancelFunc
	stdin := m.stdin
	m.cancelFunc = nil
	m.stdin = nil
	m.status.Running = false
	status := m.status
	m.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if cancel != nil {
		cancel()
	}
	m.emit(status)
	return nil
}

type incomingEvent struct {
	ChatID      string `json:"chat_id"`
	ChatType    string `json:"chat_type"`
	Content     string `json:"content"`
	CreateTime  string `json:"create_time"`
	EventID     string `json:"event_id"`
	MessageID   string `json:"message_id"`
	SenderID    string `json:"sender_id"`
	MessageType string `json:"message_type"`
}

func (m *Manager) readEvents(ctx context.Context, stdout io.ReadCloser) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event incomingEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		go m.handleEvent(ctx, event)
	}
}

func (m *Manager) readEventStderr(ctx context.Context, stderr io.ReadCloser) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := formatOutputLine(scanner.Text())
		if line == "" {
			continue
		}
		m.mu.Lock()
		m.status.LastOutput = line
		status := m.status
		m.mu.Unlock()
		m.emit(status)
		if strings.Contains(line, "Error:") {
			m.logf("warn", line)
		}
	}
}

var replyWindow sync.Map
var replyWindowCleanupOnce sync.Once

func startReplyWindowCleanupLoop() {
	replyWindowCleanupOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(replyWindowCleanupInterval)
			defer ticker.Stop()
			for range ticker.C {
				cutoff := time.Now().Add(-replyWindowTTL)
				replyWindow.Range(func(key, value any) bool {
					ts, ok := value.(time.Time)
					if !ok || ts.Before(cutoff) {
						replyWindow.Delete(key)
					}
					return true
				})
			}
		}()
	})
}

func (m *Manager) handleEvent(ctx context.Context, event incomingEvent) {
	startReplyWindowCleanupLoop()
	if strings.TrimSpace(event.Content) == "" || strings.TrimSpace(event.MessageID) == "" || strings.TrimSpace(event.ChatID) == "" {
		return
	}
	dedupeKey := strings.TrimSpace(event.EventID)
	if dedupeKey == "" {
		dedupeKey = event.MessageID + ":" + event.CreateTime
	}
	if _, loaded := replyWindow.LoadOrStore(dedupeKey, time.Now()); loaded {
		return
	}
	logMeta := LogMeta{
		SessionID: dedupeKey,
		ChatID:    strings.TrimSpace(event.ChatID),
		MessageID: strings.TrimSpace(event.MessageID),
	}
	m.logf("info", fmt.Sprintf("Feishu bridge 收到消息: chat=%s type=%s text=%s",
		trimmedID(event.ChatID),
		valueOrFallback(strings.TrimSpace(event.MessageType), "unknown"),
		summarizeText(event.Content, 120),
	), logMeta)
	proxyURL := ""
	if m.opts.ProxyURL != nil {
		proxyURL = strings.TrimSpace(m.opts.ProxyURL())
	}
	if proxyURL == "" {
		m.logf("warn", "Feishu bridge 收到消息，但当前代理地址为空", logMeta)
		return
	}
	cfg := m.Config()
	model := cfg.Model
	if model == "" {
		model = DefaultModel
	}
	skills, _ := discoverSkills(context.Background())
	conversationKey := conversationKeyForEvent(event)
	normalizedContent := normalizeConversationText(event.Content)
	commandReply, handled := m.handleConversationCommand(ctx, conversationKey, proxyURL, model, normalizedContent, logMeta)
	if handled {
		if err := m.replyToMessage(ctx, event.MessageID, commandReply); err != nil {
			m.logf("warn", "Feishu bridge 回复消息失败："+err.Error(), logMeta)
		} else {
			m.logf("info", "Feishu bridge 回复已发送: message="+trimmedID(event.MessageID), logMeta)
		}
		return
	}

	messages := []map[string]any{{"role": "system", "content": buildSystemPrompt(skills)}}
	state := m.getConversationState(conversationKey)
	if strings.TrimSpace(state.Summary) != "" {
		messages = append(messages, map[string]any{
			"role":    "system",
			"content": "当前飞书会话压缩摘要：\n" + strings.TrimSpace(state.Summary),
		})
	}
	rawHistory := cloneMessages(state.History)
	messages = append(messages, cloneMessages(rawHistory)...)
	userMessage := map[string]any{"role": "user", "content": normalizedContent}
	messages = append(messages, userMessage)
	rawHistory = append(rawHistory, cloneMessage(userMessage))
	forceToolUse := shouldForceToolUse(event.Content)
	rounds := cfg.MaxToolRounds
	if rounds <= 0 {
		rounds = DefaultMaxToolRounds
	}
	m.logf("info", fmt.Sprintf("Feishu bridge 开始处理: model=%s forceToolUse=%t rounds=%d", model, forceToolUse, rounds), logMeta)
	replyText := ""
conversation:
	for i := 0; i < rounds; i++ {
		resp, err := callLLM(ctx, proxyURL, model, messages, forceToolUse)
		if err != nil {
			replyText = "抱歉，LLM 服务暂时不可用，请稍后再试。"
			m.logf("warn", "Feishu bridge 调用代理失败："+err.Error(), logMeta)
			break
		}
		if len(resp.Choices) == 0 {
			replyText = "抱歉，我暂时没有拿到可用回复。"
			break
		}
		msg := resp.Choices[0].Message
		assistant := map[string]any{
			"role":    "assistant",
			"content": msg.Content,
		}
		if len(msg.ToolCalls) > 0 {
			assistant["tool_calls"] = msg.ToolCalls
		}
		messages = append(messages, assistant)
		rawHistory = append(rawHistory, cloneMessage(assistant))
		if len(msg.ToolCalls) == 0 {
			replyText = strings.TrimSpace(msg.Content)
			m.logf("info", "Feishu bridge 生成直接回复（无工具调用）", logMeta)
			break
		}
		for _, tc := range msg.ToolCalls {
			var args map[string]any
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
			m.logf("info", fmt.Sprintf("Feishu bridge tool call: %s %s", tc.Function.Name, compactJSON(args)), logMeta)
			result := executeTool(tc.Function.Name, args)
			content := result.Output
			if len(result.MissingScopes) > 0 {
				scopes := uniqueNonEmpty(result.MissingScopes)
				scopeLabel := strings.Join(scopes, ", ")
				loginURL, authErr := m.startScopeLogin(context.Background(), scopes...)
				if authErr != nil {
					replyText = fmt.Sprintf("当前操作缺少权限 %s。我已尝试为你发起授权，但这次没有成功。请到 Lingma Proxy 的 Feishu Bridge 设置页重新点击“登录授权”，完成后再让我继续。", scopeLabel)
					content = replyText
					m.logf("warn", "Feishu bridge 自动发起 scope 授权失败："+authErr.Error(), logMeta)
				} else {
					replyText = fmt.Sprintf("当前操作缺少权限 %s。我已经为你发起授权流程，请先在浏览器完成授权。如果 Lingma Proxy 已打开，请直接到 Feishu Bridge 设置页点击“打开授权链接”；授权完成后再对我说一次，我会继续处理。%s", scopeLabel, loginHint(loginURL))
					content = replyText
					m.logf("info", "Feishu bridge 检测到缺少权限，已自动发起授权："+scopeLabel, logMeta)
				}
				messages = append(messages, map[string]any{
					"role":         "tool",
					"tool_call_id": tc.ID,
					"content":      content,
				})
				rawHistory = append(rawHistory, map[string]any{
					"role":         "tool",
					"tool_call_id": tc.ID,
					"content":      content,
				})
				break conversation
			}
			m.logf("info", fmt.Sprintf("Feishu bridge tool result: %s %s", tc.Function.Name, summarizeText(result.Output, 160)), logMeta)
			messages = append(messages, map[string]any{
				"role":         "tool",
				"tool_call_id": tc.ID,
				"content":      content,
			})
			rawHistory = append(rawHistory, map[string]any{
				"role":         "tool",
				"tool_call_id": tc.ID,
				"content":      content,
			})
		}
	}
	if strings.TrimSpace(replyText) == "" {
		replyText = "已完成处理。"
	}
	if !endsWithAssistantReply(rawHistory, replyText) {
		rawHistory = append(rawHistory, map[string]any{"role": "assistant", "content": replyText})
	}
	m.storeConversation(conversationKey, rawHistory)
	m.logf("info", "Feishu bridge 准备回复: "+summarizeText(replyText, 160), logMeta)
	if err := m.replyToMessage(ctx, event.MessageID, replyText); err != nil {
		m.logf("warn", "Feishu bridge 回复消息失败："+err.Error(), logMeta)
	} else {
		m.logf("info", "Feishu bridge 回复已发送: message="+trimmedID(event.MessageID), logMeta)
	}
}

func (m *Manager) storeConversation(chatID string, history []map[string]any) {
	if chatID == "" {
		return
	}
	m.mu.Lock()
	state := m.conversations[chatID]
	state.History = cloneMessages(history)
	m.conversations[chatID] = state
	m.mu.Unlock()
	m.notifyConversationChanged()
	m.scheduleConversationSummary(chatID)
}

func (m *Manager) getConversationState(chatID string) conversationState {
	if chatID == "" {
		return conversationState{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, ok := m.conversations[chatID]
	if !ok {
		return conversationState{}
	}
	return conversationState{
		History: cloneMessages(state.History),
		Summary: state.Summary,
	}
}

func cloneMessages(history []map[string]any) []map[string]any {
	cloned := make([]map[string]any, 0, len(history))
	for _, item := range history {
		cloned = append(cloned, cloneMessage(item))
	}
	return cloned
}

func cloneMessage(message map[string]any) map[string]any {
	cloned := make(map[string]any, len(message))
	for key, value := range message {
		cloned[key] = value
	}
	return cloned
}

func endsWithAssistantReply(messages []map[string]any, reply string) bool {
	if len(messages) == 0 {
		return false
	}
	last := messages[len(messages)-1]
	role, _ := last["role"].(string)
	content, _ := last["content"].(string)
	return role == "assistant" && strings.TrimSpace(content) == strings.TrimSpace(reply)
}

func normalizeConversationText(text string) string {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "- ")
	return strings.TrimSpace(text)
}

func conversationKeyForEvent(event incomingEvent) string {
	chatID := strings.TrimSpace(event.ChatID)
	if chatID == "" {
		return ""
	}
	chatType := strings.ToLower(strings.TrimSpace(event.ChatType))
	senderID := strings.TrimSpace(event.SenderID)
	if chatType == "p2p" || senderID == "" {
		return chatID
	}
	return chatID + "::" + senderID
}

func (m *Manager) handleConversationCommand(ctx context.Context, chatID string, proxyURL string, model string, text string, meta LogMeta) (string, bool) {
	command := strings.ToLower(strings.TrimSpace(text))
	switch command {
	case "/help":
		m.logf("info", "Feishu bridge 会话命令: /help", meta)
		return "可用会话命令：\n- /help：查看命令帮助\n- /compact：手动压缩当前会话上下文\n- /summary：查看当前会话摘要\n- /reset：清空当前飞书会话上下文\n\n默认可以直接自然语言使用我；只有在你想手动管理上下文时，再使用这些命令。", true
	case "/reset":
		m.mu.Lock()
		delete(m.conversations, chatID)
		m.mu.Unlock()
		m.notifyConversationChanged()
		m.logf("info", "Feishu bridge 会话命令: /reset", meta)
		return "当前飞书会话上下文已清空。接下来我会把后续消息当成一个新的任务重新开始。", true
	case "/summary":
		m.logf("info", "Feishu bridge 会话命令: /summary", meta)
		summary, err := m.ensureConversationSummary(ctx, chatID, proxyURL, model, false)
		if err != nil {
			return "当前会话摘要生成失败，请稍后再试。", true
		}
		if strings.TrimSpace(summary) == "" {
			return "当前会话还没有可展示的摘要。你可以先继续对话，或在对话较长时发送 /compact 手动压缩。", true
		}
		return "当前会话摘要：\n" + summary, true
	case "/compact":
		m.logf("info", "Feishu bridge 会话命令: /compact", meta)
		summary, err := m.ensureConversationSummary(ctx, chatID, proxyURL, model, true)
		if err != nil {
			return "当前会话压缩失败，请稍后再试。", true
		}
		if strings.TrimSpace(summary) == "" {
			return "当前会话内容较少，暂时没有需要压缩的上下文。", true
		}
		return "当前飞书会话已压缩完成。后续我会基于这段摘要继续处理：\n" + summary, true
	default:
		return "", false
	}
}

func (m *Manager) ensureConversationSummary(ctx context.Context, chatID string, proxyURL string, model string, compact bool) (string, error) {
	if chatID == "" {
		return "", nil
	}
	m.mu.RLock()
	state, ok := m.conversations[chatID]
	m.mu.RUnlock()
	if !ok || len(state.History) == 0 {
		return strings.TrimSpace(state.Summary), nil
	}
	if !compact && strings.TrimSpace(state.Summary) != "" {
		return strings.TrimSpace(state.Summary), nil
	}
	summary, err := summarizeConversation(ctx, proxyURL, model, state.Summary, state.History)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	next := m.conversations[chatID]
	next.Summary = summary
	if compact {
		next.History = keepRecentConversation(next.History, 6)
	}
	m.conversations[chatID] = next
	m.mu.Unlock()
	m.notifyConversationChanged()
	return summary, nil
}

func (m *Manager) scheduleConversationSummary(chatID string) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return
	}
	proxyURL := ""
	if m.opts.ProxyURL != nil {
		proxyURL = strings.TrimSpace(m.opts.ProxyURL())
	}
	if proxyURL == "" {
		return
	}
	cfg := m.Config()
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = DefaultModel
	}

	m.mu.Lock()
	state, ok := m.conversations[chatID]
	if !ok || state.Summarizing || strings.TrimSpace(state.Summary) != "" || len(state.History) <= persistedConversationRecentLimit {
		m.mu.Unlock()
		return
	}
	state.Summarizing = true
	history := cloneMessages(state.History)
	m.conversations[chatID] = state
	m.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		summary, err := summarizeConversation(ctx, proxyURL, model, "", history)

		m.mu.Lock()
		state, ok := m.conversations[chatID]
		if !ok {
			m.mu.Unlock()
			return
		}
		state.Summarizing = false
		if err == nil && strings.TrimSpace(summary) != "" && strings.TrimSpace(state.Summary) == "" {
			state.Summary = strings.TrimSpace(summary)
		}
		m.conversations[chatID] = state
		m.mu.Unlock()

		if err == nil && strings.TrimSpace(summary) != "" {
			m.notifyConversationChanged()
		}
	}()
}

func summarizeConversation(ctx context.Context, proxyURL string, model string, existingSummary string, history []map[string]any) (string, error) {
	if strings.TrimSpace(proxyURL) == "" || strings.TrimSpace(model) == "" {
		return "", fmt.Errorf("missing proxy context")
	}
	serialized, err := json.Marshal(history)
	if err != nil {
		return "", err
	}
	prompt := "请用中文把下面这段飞书工作会话压缩成一段可续接摘要，要求包含：当前目标、已完成步骤、关键对象、待办事项、重要约束。控制在 8 行内，直接输出摘要正文，不要加前言。"
	if strings.TrimSpace(existingSummary) != "" {
		prompt += "\n\n已有摘要（如有价值可合并更新）：\n" + strings.TrimSpace(existingSummary)
	}
	prompt += "\n\n原始会话（JSON）：\n" + string(serialized)
	resp, err := callLLMPlain(ctx, proxyURL, model, []map[string]any{
		{"role": "system", "content": "你是一个会话压缩器，只输出简洁、可续接的中文摘要正文。"},
		{"role": "user", "content": prompt},
	})
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", nil
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}

func keepRecentConversation(history []map[string]any, max int) []map[string]any {
	if max <= 0 || len(history) <= max {
		return cloneMessages(history)
	}
	start := len(history) - max
	return cloneMessages(history[start:])
}

func buildPersistedConversationSummary(history []map[string]any) string {
	if len(history) == 0 {
		return ""
	}
	lines := make([]string, 0, 6)
	lines = append(lines, fmt.Sprintf("此前会话共 %d 条消息，以下为自动持久化摘要。", len(history)))
	for _, item := range keepRecentConversation(history, 4) {
		role, _ := item["role"].(string)
		content, _ := item["content"].(string)
		content = summarizeText(content, 120)
		if strings.TrimSpace(content) == "" {
			continue
		}
		switch role {
		case "user":
			lines = append(lines, "用户："+content)
		case "assistant":
			lines = append(lines, "助手："+content)
		case "tool":
			lines = append(lines, "工具结果："+content)
		default:
			lines = append(lines, content)
		}
	}
	return strings.Join(lines, "\n")
}

func (m *Manager) ConversationSnapshot() map[string]ConversationSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.conversations) == 0 {
		return nil
	}
	out := make(map[string]ConversationSnapshot, len(m.conversations))
	for chatID, state := range m.conversations {
		history := cloneMessages(state.History)
		summary := strings.TrimSpace(state.Summary)
		if len(history) > persistedConversationRecentLimit {
			if summary == "" {
				summary = buildPersistedConversationSummary(history[:len(history)-persistedConversationRecentLimit])
			}
			history = keepRecentConversation(history, persistedConversationRecentLimit)
		}
		out[chatID] = ConversationSnapshot{
			History: history,
			Summary: summary,
		}
	}
	return out
}

func (m *Manager) LoadConversationSnapshot(snapshot map[string]ConversationSnapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.conversations = make(map[string]conversationState, len(snapshot))
	for chatID, state := range snapshot {
		chatID = strings.TrimSpace(chatID)
		if chatID == "" {
			continue
		}
		m.conversations[chatID] = conversationState{
			History: cloneMessages(state.History),
			Summary: strings.TrimSpace(state.Summary),
		}
	}
}

func loginHint(url string) string {
	url = strings.TrimSpace(url)
	if url == "" {
		return ""
	}
	return "授权链接：" + url
}

func compactJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	text := strings.TrimSpace(string(data))
	if len(text) > 280 {
		return text[:280] + "..."
	}
	return text
}

func summarizeText(text string, limit int) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	text = strings.Join(strings.Fields(text), " ")
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}

func trimmedID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 12 {
		return value
	}
	return value[:12] + "..."
}

func valueOrFallback(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func (m *Manager) replyToMessage(ctx context.Context, messageID string, reply string) error {
	cmd := commandContextWithEnv(ctx, "lark-cli", "im", "+messages-reply", "--as", "bot", "--message-id", messageID, "--markdown", reply)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
