package feishu

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	goruntime "runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	replyWindowTTL                   = 30 * time.Minute
	replyWindowCleanupInterval       = 5 * time.Minute
	persistedConversationRecentLimit = 8
	conversationDebounceDelay        = 600 * time.Millisecond
	autoCompactTokenThreshold        = 60000
)

var (
	discoverSkillsForPrompt      = discoverSkills
	callLLMForConversation       = callLLM
	callLLMStreamForConversation = callLLMStream
	callLLMPlainStreamForFinal   = callLLMPlainStream
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
	History          []map[string]any
	CompactBoundary  int // index into History; history[:CompactBoundary] is folded into Summary
	Summary          string
	Summarizing      bool
	ModelOverride    string
	Language         string // "" | "zh" | "en"
	ShowThinking     *bool  // nil => default true
	PromptTokens     int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	Turns            int
	UsageByModel     map[string]*conversationUsage
}

type conversationUsage struct {
	Prompt     int
	Output     int
	CacheRead  int
	CacheWrite int
	Calls      int
}

type conversationRunState struct {
	Queue      []incomingEvent
	Processing bool
	Preempted  bool
	Cancel     context.CancelCauseFunc
}

// Sentinel cancel causes — distinguish between user /stop, automatic preempt
// by a newer message, and other reasons. Used with context.WithCancelCause so
// callers can branch on context.Cause(ctx) instead of guessing.
var (
	errCancelStopped   = errors.New("feishu: stopped by user")
	errCancelPreempted = errors.New("feishu: preempted by new message")
)

type ConversationSnapshot struct {
	History          []map[string]any              `json:"history,omitempty"`
	CompactBoundary  int                           `json:"compact_boundary,omitempty"`
	Summary          string                        `json:"summary,omitempty"`
	ModelOverride    string                        `json:"model_override,omitempty"`
	Language         string                        `json:"language,omitempty"`
	ShowThinking     *bool                         `json:"show_thinking,omitempty"`
	PromptTokens     int                           `json:"prompt_tokens,omitempty"`
	OutputTokens     int                           `json:"output_tokens,omitempty"`
	CacheReadTokens  int                           `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int                           `json:"cache_write_tokens,omitempty"`
	Turns            int                           `json:"turns,omitempty"`
	UsageByModel     map[string]*conversationUsage `json:"usage_by_model,omitempty"`
}

func (u conversationUsage) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Prompt     int `json:"prompt"`
		Output     int `json:"output"`
		CacheRead  int `json:"cache_read"`
		CacheWrite int `json:"cache_write"`
		Calls      int `json:"calls"`
	}{u.Prompt, u.Output, u.CacheRead, u.CacheWrite, u.Calls})
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
	runs          map[string]*conversationRunState
	mcp           *MCPRuntime

	// refreshGuard ensures only one refreshStatus runs at a time. The desktop
	// polls every ~2.5s; without serialization, a slow `npx skills ls` causes
	// queued probes to pile up and saturate npm's cache lock.
	refreshGuard sync.Mutex
}

func NewManager(opts ManagerOptions) *Manager {
	return &Manager{
		cfg: DefaultConfig(),
		status: Status{
			Platform:       goruntime.GOOS,
			Arch:           goruntime.GOARCH,
			RequiredSkills: append([]string(nil), fallbackRequiredSkillNames...),
			CurrentModel:   DefaultModel,
		},
		opts:          opts,
		conversations: make(map[string]conversationState),
		runs:          make(map[string]*conversationRunState),
		mcp:           NewMCPRuntime(),
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
	m.tryRefreshStatus(ctx)
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

// tryRefreshStatus runs refreshStatus only if no other refresh is in flight.
// Used by the desktop's ~2.5s status poll. Without this guard, a slow
// `npx skills ls` (cold npm cache) plus repeat polls causes processes to
// pile up and saturate npm's cache lock.
func (m *Manager) tryRefreshStatus(ctx context.Context) {
	if !m.refreshGuard.TryLock() {
		return
	}
	defer m.refreshGuard.Unlock()
	m.doRefreshStatus(ctx)
}

// refreshStatus blocks until any in-flight refresh finishes, then probes.
// Used by install/login flows where the caller needs the status to reflect
// the post-action reality before clearing InstallRunning/LoginRunning.
func (m *Manager) refreshStatus(ctx context.Context) {
	m.refreshGuard.Lock()
	defer m.refreshGuard.Unlock()
	m.doRefreshStatus(ctx)
}

func (m *Manager) doRefreshStatus(ctx context.Context) {
	node, npm, npx := detectNodeAndNPM()
	cli := detectBinary("lark-cli", "--version")
	configStatus := readCLIConfigStatus()
	authStatus := AuthStatus{Message: "未授权"}
	var skills []SkillStatus
	skillsProbeErr := errors.New("lark-cli not found")
	if cli.Found {
		skills, skillsProbeErr = discoverSkills(ctx)
		authStatus = readAuthStatus(ctx)
	} else {
		authStatus.Message = "lark-cli 未安装，暂不检测授权状态"
	}

	m.mu.Lock()
	m.status.Platform = goruntime.GOOS
	m.status.Arch = goruntime.GOARCH
	m.status.Node = node
	m.status.NPM = npm
	m.status.NPX = npx
	m.status.CLI = cli
	if skillsProbeErr == nil {
		m.status.Skills = skills
		m.status.SkillsReady = skillsReady(skills)
		if len(skills) > 0 {
			names := make([]string, 0, len(skills))
			for _, s := range skills {
				names = append(names, s.Name)
			}
			m.status.RequiredSkills = names
		}
	} else if !cli.Found {
		m.status.Skills = nil
		m.status.SkillsReady = false
	}
	m.status.MCPServers = m.mcp.Sync(ctx, m.cfg)
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
	m.status.LastOutput = "准备安装飞书 CLI..."
	status := m.status
	m.mu.Unlock()
	m.emit(status)

	err := installCLI(ctx, func(line string) {
		line = formatOutputLine(line)
		if line == "" {
			return
		}
		m.mu.Lock()
		m.status.LastOutput = line
		status := m.status
		m.mu.Unlock()
		m.emit(status)
		if shouldLogCLIProgress(line) {
			m.logf("info", "飞书 CLI 安装进度："+line)
		}
	})
	m.refreshStatus(ctx)

	m.mu.Lock()
	m.status.InstallRunning = false
	if err != nil {
		m.status.LastError = userFacingInstallError(err)
	}
	status = m.status
	m.mu.Unlock()
	m.emit(status)

	if err != nil {
		m.logf("error", "飞书 CLI 安装失败："+err.Error())
		return err
	}
	m.logf("info", "飞书 CLI 安装完成")
	return nil
}

// ReinstallSkills re-runs only the skills installation step, without touching
// Node.js or @larksuite/cli. Used by the "重新安装 Skills" UI affordance when
// the discovery step reports a gap (e.g. a single skill missing because the
// initial install raced or the upstream manifest grew). Reuses the same status
// machinery as InstallCLI so the progress UI stays consistent.
func (m *Manager) ReinstallSkills(ctx context.Context) error {
	m.mu.Lock()
	m.status.InstallRunning = true
	m.status.LastError = ""
	m.status.LastOutput = "重新安装飞书 Skills..."
	status := m.status
	m.mu.Unlock()
	m.emit(status)

	err := installSkills(ctx, func(line string) {
		line = formatOutputLine(line)
		if line == "" {
			return
		}
		m.mu.Lock()
		m.status.LastOutput = line
		status := m.status
		m.mu.Unlock()
		m.emit(status)
		if shouldLogCLIProgress(line) {
			m.logf("info", "飞书 Skills 安装进度："+line)
		}
	})
	m.refreshStatus(ctx)

	m.mu.Lock()
	m.status.InstallRunning = false
	if err != nil {
		m.status.LastError = "Skills 重新安装失败：请查看日志或重试。"
	}
	status = m.status
	m.mu.Unlock()
	m.emit(status)

	if err != nil {
		m.logf("error", "飞书 Skills 重新安装失败："+err.Error())
		return err
	}
	m.logf("info", "飞书 Skills 重新安装完成")
	return nil
}

func userFacingInstallError(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	switch {
	case strings.Contains(text, "prerequisite missing"):
		return "飞书 CLI 安装失败：缺少 Node.js/npm/npx，请先安装 Node.js 后重试。"
	case strings.Contains(text, "below required"):
		return fmt.Sprintf("飞书 CLI 安装失败：当前安装链路要求 Node.js >=%s，请升级 Node.js 后重试。", minimumNodeVersionText())
	default:
		return "飞书 CLI 安装失败：请查看日志或反馈包中的详细 npm/npx 输出。"
	}
}

func shouldLogCLIProgress(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "error") ||
		strings.Contains(lower, "failed") ||
		strings.Contains(lower, "unsupported") ||
		strings.Contains(lower, "warn") ||
		strings.Contains(trimmed, "失败") ||
		strings.Contains(trimmed, "错误") {
		return true
	}
	return strings.Contains(trimmed, "安装飞书 CLI") ||
		strings.Contains(trimmed, "验证 lark-cli") ||
		strings.Contains(trimmed, "lark-cli version") ||
		strings.Contains(trimmed, "Using Node.js") ||
		strings.Contains(trimmed, "Using npm global prefix") ||
		strings.Contains(trimmed, "Check lark-cli skills") ||
		strings.Contains(trimmed, "Install lark-cli")
}

func (m *Manager) StartSetupNew(ctx context.Context) error {
	if err := requireCLIReady(); err != nil {
		return err
	}
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

func (m *Manager) StartLogin(ctx context.Context) error {
	if err := requireCLIReady(); err != nil {
		return err
	}
	return m.startLoginWithArgs(ctx, []string{"lark-cli", "auth", "login", "--recommend"})
}

func requireCLIReady() error {
	node, npm, npx := detectNodeAndNPM()
	if !node.Found || !npm.Found || !npx.Found {
		return fmt.Errorf("Node.js/npm/npx 未就绪，请先安装 Node.js 后重试")
	}
	if major, minor := parseNodeVersion(node.Version); !nodeVersionSupported(major, minor) {
		return fmt.Errorf("Node.js 版本 %s 低于飞书 CLI 当前安装链路要求的 >=%s", node.Version, minimumNodeVersionText())
	}
	if cli := detectBinary("lark-cli", "--version"); !cli.Found {
		return fmt.Errorf("lark-cli 未安装，请先点击“安装 CLI 与 Skills”")
	}
	return nil
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
		missing := missingSkillNames(m.status.Skills, 8)
		m.mu.Unlock()
		if len(missing) > 0 {
			return fmt.Errorf("必需的 lark-* skills 未安装完整：缺少 %s", strings.Join(missing, ", "))
		}
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
	m.status.MCPServers = m.mcp.Sync(runCtx, m.cfg)
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
	go m.runMCPHealthChecks(runCtx)
	go func() {
		err := cmd.Wait()
		m.mcp.Stop()
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
	m.mcp.Stop()
	m.emit(status)
	return nil
}

func (m *Manager) runMCPHealthChecks(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cfg := m.Config()
			statuses := m.mcp.Sync(ctx, cfg)
			m.mu.Lock()
			m.status.MCPServers = statuses
			status := m.status
			m.mu.Unlock()
			m.emit(status)
		}
	}
}

type incomingEvent struct {
	ChatID           string   `json:"chat_id"`
	ChatType         string   `json:"chat_type"`
	Content          string   `json:"content"`
	CreateTime       string   `json:"create_time"`
	EventID          string   `json:"event_id"`
	MessageID        string   `json:"message_id"`
	SenderID         string   `json:"sender_id"`
	MessageType      string   `json:"message_type"`
	ReplyToMessageID string   `json:"reply_to_message_id"`
	MentionOpenIDs   []string `json:"-"`
	MentionUserIDs   []string `json:"-"`
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
		event.ReplyToMessageID = firstNonEmptyString(
			event.ReplyToMessageID,
			firstJSONStringField([]byte(line), "reply_to_message_id"),
			firstJSONStringField([]byte(line), "parent_message_id"),
			firstJSONStringField([]byte(line), "root_message_id"),
			firstJSONStringField([]byte(line), "reply_message_id"),
		)
		event.MentionOpenIDs, event.MentionUserIDs = extractMentionIDs([]byte(line))
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
	conversationKey := conversationKeyForEvent(event)
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
	if m.shouldIgnoreGroupMessage(event) {
		m.logf("info", "Feishu bridge 群聊消息未 @机器人，忽略", logMeta)
		return
	}
	if m.handleImmediateConversationCommand(ctx, conversationKey, event, logMeta) {
		return
	}
	m.enqueueConversationEvent(ctx, conversationKey, event, logMeta)
}

func (m *Manager) handleImmediateConversationCommand(ctx context.Context, conversationKey string, event incomingEvent, meta LogMeta) bool {
	command := strings.ToLower(strings.TrimSpace(normalizeConversationText(event.Content)))
	if command != "/stop" {
		return false
	}
	interrupted := m.stopConversationRun(conversationKey)
	reply := "当前没有正在处理的 Feishu Bridge 任务。"
	if interrupted {
		reply = "已请求停止当前 Feishu Bridge 任务。"
	}
	if err := m.replyToMessage(ctx, event.MessageID, reply); err != nil {
		m.logf("warn", "Feishu bridge /stop 回复失败："+err.Error(), meta)
	}
	m.logf("info", fmt.Sprintf("Feishu bridge 会话命令: /stop interrupted=%t", interrupted), meta)
	return true
}

func (m *Manager) enqueueConversationEvent(ctx context.Context, conversationKey string, event incomingEvent, meta LogMeta) {
	if strings.TrimSpace(conversationKey) == "" {
		m.logf("warn", "Feishu bridge 收到消息，但无法解析会话 ID", meta)
		return
	}
	m.mu.Lock()
	run := m.runs[conversationKey]
	if run == nil {
		run = &conversationRunState{}
		m.runs[conversationKey] = run
	}
	run.Queue = append(run.Queue, event)
	queueSize := len(run.Queue)
	if run.Processing {
		cancel := run.Cancel
		if cancel != nil {
			run.Preempted = true
			run.Cancel = nil
		}
		m.mu.Unlock()
		if cancel != nil {
			cancel(errCancelPreempted)
			m.logf("info", fmt.Sprintf("Feishu bridge 抢占当前任务，队列将合并处理: queue=%d", queueSize), meta)
		} else {
			m.logf("info", fmt.Sprintf("Feishu bridge 消息已排队: queue=%d", queueSize), meta)
		}
		return
	}
	run.Processing = true
	m.mu.Unlock()
	m.logf("info", "Feishu bridge 会话调度已启动", meta)
	m.processConversationQueue(ctx, conversationKey)
}

func (m *Manager) processConversationQueue(parent context.Context, conversationKey string) {
	for {
		timer := time.NewTimer(conversationDebounceDelay)
		select {
		case <-parent.Done():
			timer.Stop()
			m.clearConversationRun(conversationKey)
			return
		case <-timer.C:
		}

		m.mu.Lock()
		run := m.runs[conversationKey]
		if run == nil || len(run.Queue) == 0 {
			if run != nil {
				run.Processing = false
				run.Cancel = nil
			}
			m.mu.Unlock()
			return
		}
		batch := append([]incomingEvent(nil), run.Queue...)
		run.Queue = nil
		runCtx, cancel := context.WithCancelCause(parent)
		run.Cancel = cancel
		m.mu.Unlock()

		m.processConversationBatch(runCtx, conversationKey, batch)

		cancel(nil)
		m.mu.Lock()
		run = m.runs[conversationKey]
		if run == nil {
			m.mu.Unlock()
			return
		}
		run.Cancel = nil
		run.Preempted = false
		if len(run.Queue) == 0 {
			run.Processing = false
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()
	}
}

func (m *Manager) clearConversationRun(conversationKey string) {
	m.mu.Lock()
	delete(m.runs, conversationKey)
	m.mu.Unlock()
}

func (m *Manager) wasConversationPreempted(conversationKey string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	run := m.runs[conversationKey]
	return run != nil && run.Preempted
}

// cancelOutcome decides how to surface a context cancellation: a manual /stop
// becomes "stopped"; a preemption by the next user message becomes "preempted"
// so the user sees that we're already working on the new request. Inspects
// context.Cause(ctx) first (richer signal), then falls back to the legacy
// Preempted flag for any path that did not flow through cancel-with-cause.
func (m *Manager) cancelOutcome(ctx context.Context, conversationKey string, baseLog string) (reply string, status string, label string, logMsg string) {
	cause := context.Cause(ctx)
	if errors.Is(cause, errCancelPreempted) || (cause == nil && m.wasConversationPreempted(conversationKey)) {
		return "收到新的消息，已中止上一轮处理，正在处理最新内容。",
			"preempted", "已被新消息抢占",
			baseLog + "（被新消息抢占）"
	}
	return "当前任务已停止。", "stopped", "已停止", baseLog
}

func (m *Manager) stopConversationRun(conversationKey string) bool {
	m.mu.Lock()
	run := m.runs[conversationKey]
	if run == nil {
		m.mu.Unlock()
		return false
	}
	cancel := run.Cancel
	run.Queue = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel(errCancelStopped)
		return true
	}
	return run.Processing
}

func (m *Manager) processConversationBatch(ctx context.Context, conversationKey string, batch []incomingEvent) {
	if len(batch) == 0 {
		return
	}
	event := mergeConversationBatch(batch)
	dedupeKey := strings.TrimSpace(event.EventID)
	if dedupeKey == "" {
		dedupeKey = event.MessageID + ":" + event.CreateTime
	}
	logMeta := LogMeta{
		SessionID: dedupeKey,
		ChatID:    strings.TrimSpace(event.ChatID),
		MessageID: strings.TrimSpace(event.MessageID),
	}
	proxyURL := ""
	if m.opts.ProxyURL != nil {
		proxyURL = strings.TrimSpace(m.opts.ProxyURL())
	}
	if proxyURL == "" {
		m.logf("warn", "Feishu bridge 收到消息，但当前代理地址为空", logMeta)
		return
	}
	typing := m.addTypingReaction(event.MessageID, logMeta)
	defer m.removeTypingReaction(typing, logMeta)
	cfg := m.Config()
	model := cfg.Model
	if model == "" {
		model = DefaultModel
	}
	if override := m.sessionModelOverride(conversationKey); override != "" {
		model = override
	}
	skills, _ := discoverSkillsForPrompt(ctx)
	normalizedContent := m.buildConversationInput(ctx, event)
	commandReply, handled := m.handleConversationCommand(ctx, conversationKey, proxyURL, model, normalizedContent, logMeta)
	if handled {
		if err := m.replyToMessage(ctx, event.MessageID, commandReply); err != nil {
			m.logf("warn", "Feishu bridge 回复消息失败："+err.Error(), logMeta)
		} else {
			m.logf("info", "Feishu bridge 回复已发送: message="+trimmedID(event.MessageID), logMeta)
		}
		return
	}

	if cfg.MCPEnabled {
		statuses := m.mcp.Sync(ctx, cfg)
		m.mu.Lock()
		m.status.MCPServers = statuses
		m.mu.Unlock()
	}
	mcpTools := m.mcp.Tools()
	toolDefs := toolDefinitionsWithMCP(mcpTools)
	messages := []map[string]any{{"role": "system", "content": buildSystemPrompt(skills, cfg.BotIdentity, buildMCPPromptSection(mcpTools))}}
	state := m.getConversationState(conversationKey)
	if strings.TrimSpace(state.Summary) != "" {
		messages = append(messages, map[string]any{
			"role":    "system",
			"content": "当前飞书会话压缩摘要：\n" + strings.TrimSpace(state.Summary),
		})
	}
	rawHistory := cloneMessages(state.activeHistory())
	messages = append(messages, cloneMessages(rawHistory)...)
	userMessage := map[string]any{"role": "user", "content": normalizedContent}
	messages = append(messages, userMessage)
	rawHistory = append(rawHistory, cloneMessage(userMessage))
	consecutiveToolFailures := 0
	const maxConsecutiveToolFailures = 8
	const maxRepeatedToolFailures = 4
	forceToolUse := shouldForceToolUse(event.Content)
	rounds := cfg.MaxToolRounds
	if rounds <= 0 {
		rounds = DefaultMaxToolRounds
	}
	if rounds > DefaultMaxToolRounds {
		rounds = DefaultMaxToolRounds
	}
	m.logf("info", fmt.Sprintf("Feishu bridge 开始处理: model=%s forceToolUse=%t rounds=%d", model, forceToolUse, rounds), logMeta)
	card := newCardWriter(m, event.MessageID, model, logMeta)
	card.SetStatus("thinking", "正在思考")
	replyText := ""
	lastToolOutput := ""
	toolCallsExecuted := false
	failureFingerprints := map[string]int{}
conversation:
	for i := 0; i < rounds; i++ {
		if ctx.Err() != nil {
			text, status, label, log := m.cancelOutcome(ctx, conversationKey, "Feishu bridge 处理已停止")
			replyText = text
			card.SetStatus(status, label)
			m.logf("info", log, logMeta)
			break
		}
		// Stream the assistant turn so the card replies typewriter-style.
		// We accumulate text deltas locally and push the running prefix to
		// the cardWriter, whose existing throttle keeps feishu patch_message
		// QPS well within budget. If streaming fails before any data is
		// received, fall back to the non-streaming path so a misbehaving
		// proxy doesn't break replies entirely.
		var streamingReply strings.Builder
		streamHadText := false
		resp, err := callLLMStreamForConversation(ctx, proxyURL, model, microcompactToolResults(messages), forceToolUse, toolDefs, streamingDelta{
			onText: func(chunk string) {
				if chunk == "" {
					return
				}
				streamingReply.WriteString(chunk)
				streamHadText = true
				card.SetReply(streamingReply.String())
			},
		})
		if err != nil && !streamHadText {
			m.logf("info", "Feishu bridge 流式调用失败，回退非流式："+err.Error(), logMeta)
			resp, err = callLLMForConversation(ctx, proxyURL, model, microcompactToolResults(messages), forceToolUse, toolDefs)
		}
		if err != nil {
			if ctx.Err() != nil {
				text, status, label, log := m.cancelOutcome(ctx, conversationKey, "Feishu bridge 处理已停止")
				replyText = text
				card.SetStatus(status, label)
				m.logf("info", log, logMeta)
				break
			}
			replyText = "抱歉，LLM 服务暂时不可用，请稍后再试。"
			card.SetStatus("error", "调用代理失败")
			card.AppendStep(cardStep{Kind: "error", Title: "调用代理失败", Body: err.Error()})
			m.logf("warn", "Feishu bridge 调用代理失败："+err.Error(), logMeta)
			break
		}
		if len(resp.Choices) == 0 {
			replyText = "抱歉，我暂时没有拿到可用回复。"
			card.SetStatus("error", "无可用回复")
			break
		}
		m.accumulateUsage(conversationKey, model, usageFromResponse(resp))
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
			card.SetStatus("done", "完成")
			m.logf("info", "Feishu bridge 生成直接回复（无工具调用）", logMeta)
			break
		}
		if thought := strings.TrimSpace(msg.Content); thought != "" {
			card.AppendStep(cardStep{Kind: "thought", Title: "思考", Body: summarizeText(thought, 240)})
		}
		// We streamed the assistant's interim text into the reply slot; now
		// that we know this turn ends in tool_calls, clear the reply area
		// so the user doesn't see the thought text twice (once as a
		// "思考" step, once as a partial reply).
		card.SetReply("")
		for _, tc := range msg.ToolCalls {
			if ctx.Err() != nil {
				text, status, label, log := m.cancelOutcome(ctx, conversationKey, "Feishu bridge 工具调用前已停止")
				replyText = text
				card.SetStatus(status, label)
				m.logf("info", log, logMeta)
				break conversation
			}
			var args map[string]any
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
			argsSummary := compactJSON(args)
			m.logf("info", fmt.Sprintf("Feishu bridge tool call: %s %s", tc.Function.Name, argsSummary), logMeta)
			card.SetStatus("tool", "调用工具")
			card.AppendStep(cardStep{
				Kind:  "tool",
				Title: tc.Function.Name,
				Tool:  tc.Function.Name,
				Body:  "参数：`" + summarizeText(argsSummary, 200) + "`",
			})
			result := executeToolContextWithRuntime(ctx, cfg, m.mcp, tc.Function.Name, args)
			toolCallsExecuted = true
			content := result.Output
			if len(result.MissingScopes) > 0 {
				scopes := uniqueNonEmpty(result.MissingScopes)
				scopeLabel := strings.Join(scopes, ", ")
				loginURL, authErr := m.startScopeLogin(context.Background(), scopes...)
				if authErr != nil {
					replyText = fmt.Sprintf("当前操作缺少权限 %s。我已尝试为你发起授权，但这次没有成功。请到 Lingma Proxy 的 Feishu Bridge 设置页重新点击“登录授权”，完成后再让我继续。", scopeLabel)
					content = replyText
					card.UpdateLastStep(func(step *cardStep) {
						step.Kind = "error"
						step.Done = true
						step.Body = "缺少权限：" + scopeLabel + "（自动授权失败）"
					})
					m.logf("warn", "Feishu bridge 自动发起 scope 授权失败："+authErr.Error(), logMeta)
				} else {
					replyText = fmt.Sprintf("当前操作缺少权限 %s。我已经为你发起授权流程，请先在浏览器完成授权。如果 Lingma Proxy 已打开，请直接到 Feishu Bridge 设置页点击“打开授权链接”；授权完成后再对我说一次，我会继续处理。%s", scopeLabel, loginHint(loginURL))
					content = replyText
					card.UpdateLastStep(func(step *cardStep) {
						step.Kind = "error"
						step.Done = true
						step.Body = "需要授权 " + scopeLabel + "（已自动发起）"
					})
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
				card.SetStatus("error", "缺少权限")
				break conversation
			}
			lastToolOutput = content
			m.logf("info", fmt.Sprintf("Feishu bridge tool result: %s %s", tc.Function.Name, summarizeText(result.Output, 160)), logMeta)
			card.UpdateLastStep(func(step *cardStep) {
				step.Done = true
				step.Body = step.Body + "\n结果：" + summarizeText(result.Output, 1200)
			})
			toolMsg := map[string]any{
				"role":         "tool",
				"tool_call_id": tc.ID,
				"content":      content,
			}
			if result.IsError {
				toolMsg["is_error"] = true
				consecutiveToolFailures++
				fp := toolFailureFingerprint(tc.Function.Name, args, content)
				failureFingerprints[fp]++
				if failureFingerprints[fp] >= maxRepeatedToolFailures {
					replyText = fmt.Sprintf("同一个工具调用连续失败 %d 次，已停止继续重试。失败工具：%s。最后一次错误：%s", failureFingerprints[fp], tc.Function.Name, summarizeText(content, 220))
					card.SetStatus("error", "重复工具失败")
					card.AppendStep(cardStep{Kind: "error", Title: "重复失败，已终止", Body: replyText})
					m.logf("warn", "Feishu bridge 重复工具失败已终止", logMeta)
					break conversation
				}
				if failureFingerprints[fp] >= 2 {
					guidance := toolRetryGuidance(tc.Function.Name, args, failureFingerprints[fp])
					if guidance != "" {
						content = content + "\n\n" + guidance
						toolMsg["content"] = content
					}
				}
			} else {
				consecutiveToolFailures = 0
			}
			messages = append(messages, toolMsg)
			rawHistory = append(rawHistory, cloneMessage(toolMsg))
			if consecutiveToolFailures >= maxConsecutiveToolFailures {
				replyText = fmt.Sprintf("连续 %d 次工具调用失败，已停止以避免循环。最后一次错误：%s", consecutiveToolFailures, summarizeText(content, 200))
				card.SetStatus("error", "工具连续失败")
				card.AppendStep(cardStep{Kind: "error", Title: "已终止", Body: replyText})
				m.logf("warn", "Feishu bridge 连续工具调用失败已终止", logMeta)
				break conversation
			}
		}
	}
	if strings.TrimSpace(replyText) == "" && toolCallsExecuted {
		card.SetStatus("thinking", "整合工具结果")
		replyText = m.synthesizeToolFinalReply(ctx, proxyURL, model, messages, lastToolOutput, logMeta, card)
	}
	if strings.TrimSpace(replyText) == "" {
		replyText = "抱歉，我执行了处理但没有拿到可用结果。请换个更具体的关键词或文档范围再试。"
	}
	var orphanIDs []string
	messages, rawHistory, orphanIDs = sealOrphanToolCalls(messages, rawHistory, "[interrupted: tool call did not complete]")
	if len(orphanIDs) > 0 {
		m.logf("info", fmt.Sprintf("Feishu bridge 为未完成的工具调用补占位 tool_result: %s", strings.Join(orphanIDs, ",")), logMeta)
	}
	if !endsWithAssistantReply(rawHistory, replyText) {
		rawHistory = append(rawHistory, map[string]any{"role": "assistant", "content": replyText})
	}
	m.storeConversation(conversationKey, rawHistory)
	m.logf("info", "Feishu bridge 准备回复: "+summarizeText(replyText, 160), logMeta)
	if card.IsBroken() {
		replyCtx := ctx
		if ctx.Err() != nil {
			replyCtx = context.Background()
		}
		if err := m.replyToMessage(replyCtx, event.MessageID, replyText); err != nil {
			m.logf("warn", "Feishu bridge 回复消息失败："+err.Error(), logMeta)
		} else {
			m.logf("info", "Feishu bridge 回复已发送: message="+trimmedID(event.MessageID), logMeta)
		}
		return
	}
	if card.Status() != "done" && card.Status() != "stopped" && card.Status() != "error" {
		card.SetStatus("done", "完成")
	}
	card.Finalize(replyText, "")
	m.logf("info", "Feishu bridge 卡片回复已完成: message="+trimmedID(event.MessageID), logMeta)
}

func toolFailureFingerprint(toolName string, args map[string]any, output string) string {
	argsJSON := compactJSON(args)
	if len(argsJSON) > 600 {
		argsJSON = argsJSON[:600]
	}
	return strings.TrimSpace(toolName) + "|" + argsJSON + "|" + summarizeText(output, 240)
}

func toolRetryGuidance(toolName string, args map[string]any, repeated int) string {
	if strings.TrimSpace(toolName) != "lark_cli_exec" {
		return fmt.Sprintf("[retry guidance] 同一工具调用已失败 %d 次。下一轮请先检查参数是否缺失，必要时换一个工具或缩小任务范围，不要原样重复。", repeated)
	}
	argv := stringListArg(args, "argv")
	if len(argv) > 0 && strings.EqualFold(argv[0], "lark-cli") {
		argv = argv[1:]
	}
	domain := ""
	if len(argv) > 0 {
		domain = strings.TrimSpace(argv[0])
	}
	helpCmd := "lark-cli --help"
	if domain != "" {
		helpCmd = "lark-cli " + domain + " --help"
	}
	return fmt.Sprintf("[retry guidance] 同一个 lark-cli 调用已失败 %d 次。不要原样重复。下一轮请先调用 lark_cli_exec 执行 `%s` 查看真实用法，再根据 help 输出重试。skill 快捷命令通常需要 + 前缀，例如 `im +chat-list`、`im +messages-search`、`calendar +agenda`、`docs +fetch`。", repeated, helpCmd)
}

func (m *Manager) synthesizeToolFinalReply(ctx context.Context, proxyURL string, model string, messages []map[string]any, lastToolOutput string, meta LogMeta, card *cardWriter) string {
	m.logf("info", "Feishu bridge 工具轮次结束，尝试生成最终答复", meta)
	finalMessages := cloneMessages(messages)
	finalMessages = append(finalMessages, map[string]any{
		"role":    "system",
		"content": "工具调用阶段已经结束。请只基于上面的工具结果，用中文直接回答用户的问题；不要再要求用户自己执行命令；如果结果为空或不足，请明确说明没有查到，并给出下一步建议。",
	})
	var streamingReply strings.Builder
	streamHadText := false
	deltas := streamingDelta{
		onText: func(chunk string) {
			if chunk == "" {
				return
			}
			streamingReply.WriteString(chunk)
			streamHadText = true
			if card != nil {
				card.SetReply(streamingReply.String())
			}
		},
	}
	resp, err := callLLMPlainStreamForFinal(ctx, proxyURL, model, finalMessages, deltas)
	if err != nil && !streamHadText {
		m.logf("info", "Feishu bridge 最终答复流式失败，回退非流式："+err.Error(), meta)
		resp, err = callLLMPlain(ctx, proxyURL, model, finalMessages)
	}
	if err != nil {
		m.logf("warn", "Feishu bridge 最终答复生成失败："+err.Error(), meta)
		return fallbackToolResultReply(lastToolOutput)
	}
	if len(resp.Choices) == 0 {
		m.logf("warn", "Feishu bridge 最终答复生成无 choices", meta)
		return fallbackToolResultReply(lastToolOutput)
	}
	reply := strings.TrimSpace(resp.Choices[0].Message.Content)
	if reply == "" {
		m.logf("warn", "Feishu bridge 最终答复为空，回退到工具结果摘要", meta)
		return fallbackToolResultReply(lastToolOutput)
	}
	return reply
}

func fallbackToolResultReply(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	return "我已经执行了查询，但模型没有生成最终总结。以下是最后一次工具结果摘要：\n\n" + truncatePreserveLines(output, 2800)
}

func (m *Manager) storeConversation(chatID string, activeTail []map[string]any) {
	if chatID == "" {
		return
	}
	m.mu.Lock()
	state := m.conversations[chatID]
	boundary := state.CompactBoundary
	if boundary < 0 || boundary > len(state.History) {
		boundary = len(state.History)
	}
	preserved := cloneMessages(state.History[:boundary])
	state.History = append(preserved, cloneMessages(activeTail)...)
	state.CompactBoundary = boundary
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
		History:         cloneMessages(state.History),
		CompactBoundary: state.CompactBoundary,
		Summary:         state.Summary,
		ModelOverride:   state.ModelOverride,
		Language:        state.Language,
		ShowThinking:    state.ShowThinking,
		PromptTokens:    state.PromptTokens,
		OutputTokens:    state.OutputTokens,
		Turns:           state.Turns,
	}
}

// activeHistory returns the slice of state.History after the compact boundary
// — the messages that should be sent to the LLM verbatim. Anything before the
// boundary has been folded into Summary but kept in state.History so /undo,
// /rewind, /resume can step back across compactions.
func (s conversationState) activeHistory() []map[string]any {
	if s.CompactBoundary <= 0 || s.CompactBoundary >= len(s.History) {
		if s.CompactBoundary >= len(s.History) {
			return nil
		}
		return s.History
	}
	return s.History[s.CompactBoundary:]
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

// microcompactToolResults returns a shallow-cloned message slice where the
// content of older tool_result messages is truncated to a short stub. The most
// recent N tool results are kept verbatim so the model still has full output to
// reason over, while distant ones are replaced with a one-line summary of the
// original — schema and tool_call_id are preserved so the API call still
// validates. Mirrors the "microcompact" pass in free-code: cheap context
// pressure relief without losing turn structure.
func microcompactToolResults(messages []map[string]any) []map[string]any {
	const keepRecent = 3
	const stubLimit = 200
	toolIdx := make([]int, 0, len(messages))
	for i, msg := range messages {
		role, _ := msg["role"].(string)
		if role == "tool" {
			toolIdx = append(toolIdx, i)
		}
	}
	if len(toolIdx) <= keepRecent {
		return messages
	}
	cutoff := toolIdx[len(toolIdx)-keepRecent]
	out := make([]map[string]any, len(messages))
	for i, msg := range messages {
		if i < cutoff {
			role, _ := msg["role"].(string)
			if role == "tool" {
				cloned := cloneMessage(msg)
				if content, ok := cloned["content"].(string); ok && len(content) > stubLimit {
					cloned["content"] = summarizeText(content, stubLimit) + "\n[已压缩，仅保留摘要]"
				}
				out[i] = cloned
				continue
			}
		}
		out[i] = msg
	}
	return out
}

// assistant.tool_calls and synthesises a placeholder tool_result for any
// tool_call_id that has not been answered. Required after preempt/cancel/error
// breaks, otherwise the next API call rejects the unpaired tool_use.
func sealOrphanToolCalls(messages, rawHistory []map[string]any, placeholder string) ([]map[string]any, []map[string]any, []string) {
	assistantIdx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		role, _ := messages[i]["role"].(string)
		if role == "assistant" {
			if _, ok := messages[i]["tool_calls"]; ok {
				assistantIdx = i
			}
			break
		}
		if role == "tool" {
			continue
		}
		break
	}
	if assistantIdx < 0 {
		return messages, rawHistory, nil
	}
	calls := extractToolCallIDs(messages[assistantIdx])
	if len(calls) == 0 {
		return messages, rawHistory, nil
	}
	answered := make(map[string]struct{}, len(calls))
	for i := assistantIdx + 1; i < len(messages); i++ {
		role, _ := messages[i]["role"].(string)
		if role != "tool" {
			break
		}
		if id, _ := messages[i]["tool_call_id"].(string); id != "" {
			answered[id] = struct{}{}
		}
	}
	missing := make([]string, 0, len(calls))
	for _, id := range calls {
		if _, ok := answered[id]; ok {
			continue
		}
		missing = append(missing, id)
		entry := map[string]any{
			"role":         "tool",
			"tool_call_id": id,
			"content":      placeholder,
			"is_error":     true,
		}
		messages = append(messages, entry)
		rawHistory = append(rawHistory, cloneMessage(entry))
	}
	return messages, rawHistory, missing
}

func extractToolCallIDs(message map[string]any) []string {
	raw, ok := message["tool_calls"]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case []ToolCall:
		ids := make([]string, 0, len(v))
		for _, tc := range v {
			if id := strings.TrimSpace(tc.ID); id != "" {
				ids = append(ids, id)
			}
		}
		return ids
	case []map[string]any:
		ids := make([]string, 0, len(v))
		for _, item := range v {
			if id, _ := item["id"].(string); strings.TrimSpace(id) != "" {
				ids = append(ids, id)
			}
		}
		return ids
	case []any:
		ids := make([]string, 0, len(v))
		for _, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if id, _ := m["id"].(string); strings.TrimSpace(id) != "" {
				ids = append(ids, id)
			}
		}
		return ids
	}
	return nil
}

func mergeConversationBatch(batch []incomingEvent) incomingEvent {
	if len(batch) == 0 {
		return incomingEvent{}
	}
	if len(batch) == 1 {
		return batch[0]
	}
	merged := batch[len(batch)-1]
	parts := make([]string, 0, len(batch))
	eventIDs := make([]string, 0, len(batch))
	for _, item := range batch {
		text := normalizeConversationText(item.Content)
		if text != "" {
			parts = append(parts, text)
		}
		if strings.TrimSpace(item.EventID) != "" {
			eventIDs = append(eventIDs, strings.TrimSpace(item.EventID))
		}
	}
	if len(parts) > 0 {
		merged.Content = strings.Join(parts, "\n\n")
	}
	if len(eventIDs) > 0 {
		merged.EventID = strings.Join(eventIDs, "+")
	}
	return merged
}

func (m *Manager) buildConversationInput(ctx context.Context, event incomingEvent) string {
	text := normalizeConversationText(event.Content)
	quoteID := strings.TrimSpace(event.ReplyToMessageID)
	if quoteID == "" || quoteID == strings.TrimSpace(event.MessageID) {
		return text
	}
	quote, err := m.fetchQuotedMessage(ctx, quoteID)
	if err != nil {
		m.logf("warn", "Feishu bridge 引用消息读取失败："+err.Error(), LogMeta{
			ChatID:    event.ChatID,
			MessageID: event.MessageID,
		})
		return text
	}
	quote = strings.TrimSpace(quote)
	if quote == "" {
		return text
	}
	return fmt.Sprintf("<quoted_message id=\"%s\">\n%s\n</quoted_message>\n\n%s", quoteID, quote, text)
}

func (m *Manager) fetchQuotedMessage(ctx context.Context, messageID string) (string, error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return "", nil
	}
	cmd := commandContextWithEnv(ctx, "lark-cli", "im", "messages", "get", "--as", "bot", "--message-id", messageID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	text := firstNonEmptyString(
		firstJSONStringField(output, "text"),
		firstJSONStringField(output, "content"),
		firstJSONStringField(output, "plain_text"),
		firstJSONStringField(output, "markdown"),
	)
	if text != "" {
		return text, nil
	}
	return strings.TrimSpace(string(output)), nil
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
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || !strings.HasPrefix(trimmed, "/") {
		return "", false
	}
	parts := strings.Fields(trimmed)
	command := strings.ToLower(parts[0])
	args := parts[1:]
	switch command {
	case "/help":
		m.logf("info", "Feishu bridge 会话命令: /help", meta)
		return "可用会话命令（仅对当前会话生效）：\n" +
			"- /help：查看命令帮助\n" +
			"- /init：让我自我介绍当前能力（model、skills、群聊行为）\n" +
			"- /status：查看本会话运行状态\n" +
			"- /mcp：查看已启用 MCP server 与具体工具列表\n" +
			"- /models：列出代理可用模型\n" +
			"- /model <name>：切换本会话模型；/model 不带参数查看；/model default 恢复全局默认\n" +
			"- /cost：查看本会话累计 tokens 估算\n" +
			"- /retry：用最近一条用户消息重新跑一次（先自动 /undo）\n" +
			"- /undo：撤回最近一轮（assistant + 关联 tool 消息）\n" +
			"- /summary：查看本会话摘要\n" +
			"- /compact：手动压缩本会话上下文\n" +
			"- /reset、/clear、/new：清空本会话上下文\n" +
			"- /stop：停止当前正在处理的任务\n\n" +
			"默认可以直接自然语言使用我；只有在你想手动管理上下文时再使用这些命令。", true
	case "/reset", "/clear", "/new":
		m.mu.Lock()
		delete(m.conversations, chatID)
		m.mu.Unlock()
		m.notifyConversationChanged()
		m.logf("info", "Feishu bridge 会话命令: "+command, meta)
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
	case "/status":
		m.logf("info", "Feishu bridge 会话命令: /status", meta)
		return m.conversationStatusText(chatID), true
	case "/mcp":
		m.logf("info", "Feishu bridge 会话命令: /mcp", meta)
		return m.commandMCPText(ctx), true
	case "/init":
		m.logf("info", "Feishu bridge 会话命令: /init", meta)
		return m.commandInitText(chatID, model), true
	case "/cost":
		m.logf("info", "Feishu bridge 会话命令: /cost", meta)
		return m.commandCostText(chatID), true
	case "/undo":
		removed := m.undoLastTurn(chatID)
		if removed == 0 {
			return "本会话还没有可撤回的消息。", true
		}
		m.logf("info", fmt.Sprintf("Feishu bridge 会话命令: /undo removed=%d", removed), meta)
		return fmt.Sprintf("已撤回最近一轮，共回退 %d 条消息。", removed), true
	case "/retry":
		lastUser := m.popLastUserForRetry(chatID)
		if strings.TrimSpace(lastUser) == "" {
			return "本会话没有可重试的用户消息。", true
		}
		m.logf("info", "Feishu bridge 会话命令: /retry", meta)
		return "已撤回最近一轮。请把刚才那条消息再发一次（已为你保留原文）：\n\n```\n" + summarizeText(lastUser, 800) + "\n```", true
	case "/models":
		m.logf("info", "Feishu bridge 会话命令: /models", meta)
		ids, err := listProxyModels(ctx, proxyURL, 32)
		if err != nil {
			return "拉取模型列表失败：" + err.Error(), true
		}
		if len(ids) == 0 {
			return "代理目前没有返回可用模型。", true
		}
		current := m.resolveSessionModel(chatID, model)
		lines := []string{"代理当前可用模型："}
		for _, id := range ids {
			marker := "- "
			if id == current {
				marker = "- ✅ "
			}
			lines = append(lines, marker+id)
		}
		lines = append(lines, "", "使用 `/model <名称>` 可以切换本会话的模型；`/model` 不带参数会显示当前会话使用的模型。")
		return strings.Join(lines, "\n"), true
	case "/model":
		if len(args) == 0 {
			current := m.resolveSessionModel(chatID, model)
			global := strings.TrimSpace(m.Config().Model)
			if global == "" {
				global = DefaultModel
			}
			override := m.sessionModelOverride(chatID)
			line := "本会话当前模型：`" + current + "`"
			if override != "" {
				line += "（会话覆盖）\n全局默认：`" + global + "`\n如要恢复使用全局默认，发送 `/model default`。"
			} else {
				line += "（来自全局默认）\n如要切换，请发送 `/model <名称>`。"
			}
			return line, true
		}
		target := strings.TrimSpace(args[0])
		if target == "" {
			return "用法：/model <名称>，或 /model default 恢复全局默认。", true
		}
		if strings.EqualFold(target, "default") || strings.EqualFold(target, "reset") {
			m.setSessionModelOverride(chatID, "")
			global := strings.TrimSpace(m.Config().Model)
			if global == "" {
				global = DefaultModel
			}
			m.logf("info", "Feishu bridge 会话命令: /model default", meta)
			return "已恢复使用全局默认模型 `" + global + "`。", true
		}
		ids, err := listProxyModels(ctx, proxyURL, 64)
		if err == nil && len(ids) > 0 {
			matched := ""
			for _, id := range ids {
				if id == target {
					matched = id
					break
				}
			}
			if matched == "" {
				for _, id := range ids {
					if strings.EqualFold(id, target) {
						matched = id
						break
					}
				}
			}
			if matched == "" {
				return "代理可用模型里没有找到 `" + target + "`。可以先用 /models 查看完整列表。", true
			}
			target = matched
		}
		m.setSessionModelOverride(chatID, target)
		m.logf("info", "Feishu bridge 会话命令: /model "+target, meta)
		return "已将本会话模型切换为 `" + target + "`。当前更改仅对本会话生效；如要恢复使用全局默认，发送 `/model default`。", true
	default:
		return "", false
	}
}

func (m *Manager) sessionModelOverride(chatID string) string {
	if chatID == "" {
		return ""
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, ok := m.conversations[chatID]
	if !ok {
		return ""
	}
	return strings.TrimSpace(state.ModelOverride)
}

func (m *Manager) setSessionModelOverride(chatID string, model string) {
	if chatID == "" {
		return
	}
	m.mu.Lock()
	state := m.conversations[chatID]
	state.ModelOverride = strings.TrimSpace(model)
	m.conversations[chatID] = state
	m.mu.Unlock()
	m.notifyConversationChanged()
}

func (m *Manager) resolveSessionModel(chatID string, fallback string) string {
	override := m.sessionModelOverride(chatID)
	if override != "" {
		return override
	}
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	cfg := m.Config()
	if strings.TrimSpace(cfg.Model) != "" {
		return cfg.Model
	}
	return DefaultModel
}

func (m *Manager) accumulateUsage(chatID, model string, usage callUsage) {
	if chatID == "" {
		return
	}
	m.mu.Lock()
	state := m.conversations[chatID]
	state.PromptTokens += usage.Prompt
	state.OutputTokens += usage.Completion
	state.CacheReadTokens += usage.CacheRead
	state.CacheWriteTokens += usage.CacheWrite
	state.Turns++
	if state.UsageByModel == nil {
		state.UsageByModel = map[string]*conversationUsage{}
	}
	key := strings.TrimSpace(model)
	if key == "" {
		key = "(unknown)"
	}
	bucket := state.UsageByModel[key]
	if bucket == nil {
		bucket = &conversationUsage{}
		state.UsageByModel[key] = bucket
	}
	bucket.Prompt += usage.Prompt
	bucket.Output += usage.Completion
	bucket.CacheRead += usage.CacheRead
	bucket.CacheWrite += usage.CacheWrite
	bucket.Calls++
	m.conversations[chatID] = state
	m.mu.Unlock()
}

type callUsage struct {
	Prompt     int
	Completion int
	CacheRead  int
	CacheWrite int
}

func usageFromResponse(resp *llmResponse) callUsage {
	if resp == nil {
		return callUsage{}
	}
	cacheRead := resp.Usage.CacheReadInputTokens
	if cacheRead == 0 {
		cacheRead = resp.Usage.PromptTokensDetails.CachedTokens
	}
	return callUsage{
		Prompt:     resp.Usage.PromptTokens,
		Completion: resp.Usage.CompletionTokens,
		CacheRead:  cacheRead,
		CacheWrite: resp.Usage.CacheCreationTokens,
	}
}

func (m *Manager) commandInitText(chatID string, fallbackModel string) string {
	model := m.resolveSessionModel(chatID, fallbackModel)
	cfg := m.Config()
	groupRule := "群聊默认仅在 @我时响应"
	if !cfg.GroupOnlyAtBot {
		groupRule = "群聊会响应所有消息"
	}
	skills := m.skillSummaryLine()
	return strings.Join([]string{
		"嗨，我是 Lingma · 飞书 Bridge。我可以帮你在飞书内调用 Lingma 代理 + lark CLI 完成对话、文档、日程、知识库等操作。",
		"",
		"- 当前模型：`" + model + "`",
		"- " + groupRule,
		"- " + skills,
		"- " + m.mcpSummaryLine(),
		"",
		"发送 /help 查看完整命令列表。",
	}, "\n")
}

func (m *Manager) skillSummaryLine() string {
	m.mu.RLock()
	skills := m.status.Skills
	ready := m.status.SkillsReady
	m.mu.RUnlock()
	total := len(skills)
	if total == 0 {
		return "Skills：未检测到（请先安装 lark skills）"
	}
	hits := 0
	for _, s := range skills {
		if s.Found {
			hits++
		}
	}
	state := "缺失"
	if ready {
		state = "就绪"
	}
	return fmt.Sprintf("Skills：%d/%d %s", hits, total, state)
}

func (m *Manager) commandMCPText(ctx context.Context) string {
	cfg := m.Config()
	statuses := m.mcp.Sync(ctx, cfg)
	m.mu.Lock()
	m.status.MCPServers = statuses
	m.mu.Unlock()
	if !cfg.MCPEnabled {
		discovered := len(statuses)
		if discovered == 0 {
			return "MCP：未启用，且暂未扫描到本机 MCP 配置。可在 Lingma Proxy 设置页 → Feishu Bridge → 高级设置中扫描和启用。"
		}
		return fmt.Sprintf("MCP：未启用。已扫描到 %d 个本机 MCP server，可在 Lingma Proxy 设置页 → Feishu Bridge → 高级设置中逐个启用。", discovered)
	}
	enabled := 0
	available := 0
	lines := []string{"MCP 已启用："}
	for _, server := range statuses {
		if !server.Enabled {
			continue
		}
		enabled++
		state := "不可用"
		if server.Available {
			available++
			state = fmt.Sprintf("可用，%d tools", server.ToolCount)
		} else if strings.TrimSpace(server.Message) != "" {
			state = "不可用：" + summarizeText(server.Message, 120)
		}
		source := strings.TrimSpace(server.SourceClient)
		if source == "" {
			source = "本机配置"
		}
		lines = append(lines, fmt.Sprintf("- `%s`（%s）：%s", server.Name, source, state))
		if server.Available && len(server.Tools) > 0 {
			limit := len(server.Tools)
			if limit > 20 {
				limit = 20
			}
			for i := 0; i < limit; i++ {
				tool := server.Tools[i]
				label := strings.TrimSpace(tool.Function)
				if label == "" {
					label = tool.Name
				}
				desc := strings.TrimSpace(tool.Description)
				if desc != "" {
					lines = append(lines, fmt.Sprintf("  - `%s`：%s", label, summarizeText(desc, 80)))
				} else {
					lines = append(lines, fmt.Sprintf("  - `%s`", label))
				}
			}
			if len(server.Tools) > limit {
				lines = append(lines, fmt.Sprintf("  - ... 另有 %d 个工具", len(server.Tools)-limit))
			}
		}
	}
	if enabled == 0 {
		return "MCP：总开关已开启，但还没有启用任何 server。请到 Lingma Proxy 设置页 → Feishu Bridge → 高级设置中选择 server。"
	}
	lines = append(lines, "", fmt.Sprintf("汇总：%d/%d 个已启用 server 当前可用。", available, enabled))
	return strings.Join(lines, "\n")
}

func (m *Manager) mcpSummaryLine() string {
	cfg := m.Config()
	if !cfg.MCPEnabled {
		return "MCP：未启用"
	}
	m.mu.RLock()
	statuses := m.status.MCPServers
	m.mu.RUnlock()
	enabled := 0
	available := 0
	tools := 0
	for _, server := range statuses {
		if !server.Enabled {
			continue
		}
		enabled++
		if server.Available {
			available++
			tools += server.ToolCount
		}
	}
	return fmt.Sprintf("MCP：%d/%d server 可用，%d tools", available, enabled, tools)
}

func (m *Manager) commandCostText(chatID string) string {
	if chatID == "" {
		return "当前会话尚无统计。"
	}
	m.mu.RLock()
	state, ok := m.conversations[chatID]
	m.mu.RUnlock()
	if !ok || (state.PromptTokens == 0 && state.OutputTokens == 0 && state.Turns == 0) {
		return "本会话尚未累计 token 使用，发起一次对话后再试。"
	}
	freshPrompt := state.PromptTokens - state.CacheReadTokens
	if freshPrompt < 0 {
		freshPrompt = 0
	}
	total := state.PromptTokens + state.OutputTokens
	var b strings.Builder
	fmt.Fprintf(&b, "本会话累计：\n- 轮次：%d\n- prompt tokens：%d（其中缓存命中 %d，新计费 %d）\n- cache write：%d\n- completion tokens：%d\n- 合计：%d",
		state.Turns, state.PromptTokens, state.CacheReadTokens, freshPrompt,
		state.CacheWriteTokens, state.OutputTokens, total)
	if len(state.UsageByModel) > 0 {
		keys := make([]string, 0, len(state.UsageByModel))
		for k := range state.UsageByModel {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("\n\n按模型拆分：")
		for _, k := range keys {
			u := state.UsageByModel[k]
			if u == nil {
				continue
			}
			fresh := u.Prompt - u.CacheRead
			if fresh < 0 {
				fresh = 0
			}
			fmt.Fprintf(&b, "\n- %s × %d：prompt %d（缓存 %d / 新 %d），completion %d",
				k, u.Calls, u.Prompt, u.CacheRead, fresh, u.Output)
		}
	}
	b.WriteString("\n\n注：数值来自代理 usage 字段，仅供参考；具体计费请以平台账单为准。")
	return b.String()
}

// undoLastTurn drops the trailing assistant reply and any tool messages that
// belong to it. Returns the number of messages removed. Operates only within
// the active region (after CompactBoundary) to avoid corrupting the folded
// summary.
func (m *Manager) undoLastTurn(chatID string) int {
	if chatID == "" {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.conversations[chatID]
	if !ok || len(state.History) == 0 {
		return 0
	}
	floor := state.CompactBoundary
	if floor < 0 {
		floor = 0
	}
	idx := len(state.History) - 1
	for idx >= floor {
		role, _ := state.History[idx]["role"].(string)
		if role == "user" {
			break
		}
		idx--
	}
	if idx < floor {
		removed := len(state.History) - floor
		state.History = cloneMessages(state.History[:floor])
		m.conversations[chatID] = state
		return removed
	}
	removed := len(state.History) - idx - 1
	if removed <= 0 {
		return 0
	}
	state.History = cloneMessages(state.History[:idx+1])
	m.conversations[chatID] = state
	return removed
}

// popLastUserForRetry removes the trailing assistant/tool messages plus the
// last user message, returning the user message text so the caller can ask the
// human to resend it (or feed it back automatically).
func (m *Manager) popLastUserForRetry(chatID string) string {
	if chatID == "" {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.conversations[chatID]
	if !ok || len(state.History) == 0 {
		return ""
	}
	floor := state.CompactBoundary
	if floor < 0 {
		floor = 0
	}
	idx := len(state.History) - 1
	for idx >= floor {
		role, _ := state.History[idx]["role"].(string)
		if role == "user" {
			break
		}
		idx--
	}
	if idx < floor {
		return ""
	}
	content, _ := state.History[idx]["content"].(string)
	state.History = cloneMessages(state.History[:idx])
	m.conversations[chatID] = state
	return content
}

func (m *Manager) conversationStatusText(chatID string) string {
	m.mu.RLock()
	state := m.conversations[chatID]
	run := m.runs[chatID]
	m.mu.RUnlock()
	running := run != nil && run.Processing
	queued := 0
	if run != nil {
		queued = len(run.Queue)
	}
	summaryState := "无"
	if strings.TrimSpace(state.Summary) != "" {
		summaryState = "已有"
	}
	active := len(state.activeHistory())
	folded := len(state.History) - active
	return fmt.Sprintf("Feishu Bridge 当前会话状态：\n- 运行中：%t\n- 排队消息：%d\n- 活跃消息：%d（已压缩 %d）\n- 摘要：%s", running, queued, active, folded, summaryState)
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
		// Set boundary to keep recent N messages active; older ones stay in
		// History but are folded into Summary so /undo can rewind across.
		const keepActive = 6
		if len(next.History) > keepActive {
			next.CompactBoundary = len(next.History) - keepActive
		} else {
			next.CompactBoundary = 0
		}
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
	if !ok || state.Summarizing {
		m.mu.Unlock()
		return
	}
	hasSummary := strings.TrimSpace(state.Summary) != ""
	overMessages := len(state.History) > persistedConversationRecentLimit
	// Token-driven autocompact: if the running prompt budget is approaching the
	// model context, fold the older history into a summary even if we already
	// have one — this is the second-pass "compact existing summary + new turns".
	overTokens := state.PromptTokens >= autoCompactTokenThreshold && len(state.History) > persistedConversationRecentLimit
	if hasSummary && !overTokens {
		m.mu.Unlock()
		return
	}
	if !hasSummary && !overMessages {
		m.mu.Unlock()
		return
	}
	state.Summarizing = true
	history := cloneMessages(state.History)
	existingSummary := state.Summary
	m.conversations[chatID] = state
	m.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		summary, err := summarizeConversation(ctx, proxyURL, model, existingSummary, history)

		m.mu.Lock()
		state, ok := m.conversations[chatID]
		if !ok {
			m.mu.Unlock()
			return
		}
		state.Summarizing = false
		if err == nil && strings.TrimSpace(summary) != "" {
			trimmed := strings.TrimSpace(summary)
			// First-pass summary: only adopt if no existing one (避免覆盖手动 /summary).
			// Token-driven recompact: always adopt and physically trim history so
			// subsequent prompts shrink back below the threshold.
			if strings.TrimSpace(state.Summary) == "" {
				state.Summary = trimmed
			} else if state.PromptTokens >= autoCompactTokenThreshold {
				state.Summary = trimmed
				if len(state.History) > persistedConversationRecentLimit {
					state.CompactBoundary = len(state.History) - persistedConversationRecentLimit
				} else {
					state.CompactBoundary = 0
				}
				// Reset the running prompt counter; subsequent calls will re-fill it
				// against the now-shorter active window. Keep cumulative cache/output
				// for /cost reporting.
				state.PromptTokens = 0
			}
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
		boundary := state.CompactBoundary
		if boundary < 0 || boundary > len(history) {
			boundary = 0
		}
		// Persisted summary fallback — only when we still don't have one.
		active := history
		if boundary > 0 {
			active = history[boundary:]
		}
		if len(active) > persistedConversationRecentLimit {
			extra := len(active) - persistedConversationRecentLimit
			if summary == "" {
				summary = buildPersistedConversationSummary(history[:boundary+extra])
			}
			boundary += extra
		}
		usageCopy := make(map[string]*conversationUsage, len(state.UsageByModel))
		for k, v := range state.UsageByModel {
			if v == nil {
				continue
			}
			cp := *v
			usageCopy[k] = &cp
		}
		var thinking *bool
		if state.ShowThinking != nil {
			val := *state.ShowThinking
			thinking = &val
		}
		out[chatID] = ConversationSnapshot{
			History:          history,
			CompactBoundary:  boundary,
			Summary:          summary,
			ModelOverride:    strings.TrimSpace(state.ModelOverride),
			Language:         strings.TrimSpace(state.Language),
			ShowThinking:     thinking,
			PromptTokens:     state.PromptTokens,
			OutputTokens:     state.OutputTokens,
			CacheReadTokens:  state.CacheReadTokens,
			CacheWriteTokens: state.CacheWriteTokens,
			Turns:            state.Turns,
			UsageByModel:     usageCopy,
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
		history := cloneMessages(state.History)
		boundary := state.CompactBoundary
		if boundary < 0 {
			boundary = 0
		}
		if boundary > len(history) {
			boundary = len(history)
		}
		var thinking *bool
		if state.ShowThinking != nil {
			val := *state.ShowThinking
			thinking = &val
		}
		usageCopy := make(map[string]*conversationUsage, len(state.UsageByModel))
		for k, v := range state.UsageByModel {
			if v == nil {
				continue
			}
			cp := *v
			usageCopy[k] = &cp
		}
		m.conversations[chatID] = conversationState{
			History:          history,
			CompactBoundary:  boundary,
			Summary:          strings.TrimSpace(state.Summary),
			ModelOverride:    strings.TrimSpace(state.ModelOverride),
			Language:         strings.TrimSpace(state.Language),
			ShowThinking:     thinking,
			PromptTokens:     state.PromptTokens,
			OutputTokens:     state.OutputTokens,
			CacheReadTokens:  state.CacheReadTokens,
			CacheWriteTokens: state.CacheWriteTokens,
			Turns:            state.Turns,
			UsageByModel:     usageCopy,
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

func truncatePreserveLines(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return text[:limit] + "\n... (truncated)"
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

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

type typingReactionState struct {
	MessageID  string
	ReactionID string
}

func (m *Manager) addTypingReaction(messageID string, meta LogMeta) typingReactionState {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return typingReactionState{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	params, _ := json.Marshal(map[string]string{"message_id": messageID})
	data, _ := json.Marshal(map[string]any{
		"reaction_type": map[string]string{"emoji_type": "Typing"},
	})
	cmd := commandContextWithEnv(ctx, "lark-cli", "im", "reactions", "create", "--as", "bot", "--params", string(params), "--data", string(data))
	output, err := cmd.CombinedOutput()
	if err != nil {
		m.logf("warn", "Feishu bridge 添加输入状态失败："+formatCommandFailure(err, output), meta)
		return typingReactionState{}
	}
	reactionID := firstJSONStringField(output, "reaction_id")
	if reactionID == "" {
		m.logf("warn", "Feishu bridge 添加输入状态成功但未返回 reaction_id", meta)
		return typingReactionState{MessageID: messageID}
	}
	m.logf("info", "Feishu bridge 输入状态已显示", meta)
	return typingReactionState{MessageID: messageID, ReactionID: reactionID}
}

func (m *Manager) removeTypingReaction(state typingReactionState, meta LogMeta) {
	if strings.TrimSpace(state.MessageID) == "" || strings.TrimSpace(state.ReactionID) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	params, _ := json.Marshal(map[string]string{
		"message_id":  state.MessageID,
		"reaction_id": state.ReactionID,
	})
	cmd := commandContextWithEnv(ctx, "lark-cli", "im", "reactions", "delete", "--as", "bot", "--params", string(params))
	output, err := cmd.CombinedOutput()
	if err != nil {
		m.logf("warn", "Feishu bridge 清理输入状态失败："+formatCommandFailure(err, output), meta)
		return
	}
	m.logf("info", "Feishu bridge 输入状态已清理", meta)
}

func formatCommandFailure(err error, output []byte) string {
	text := strings.TrimSpace(string(output))
	if text == "" {
		return err.Error()
	}
	return fmt.Sprintf("%v: %s", err, text)
}

func firstJSONStringField(data []byte, field string) string {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return ""
	}
	return findJSONStringField(value, field)
}

func findJSONStringField(value any, field string) string {
	switch typed := value.(type) {
	case map[string]any:
		if direct, ok := typed[field].(string); ok && strings.TrimSpace(direct) != "" {
			return strings.TrimSpace(direct)
		}
		for _, item := range typed {
			if found := findJSONStringField(item, field); found != "" {
				return found
			}
		}
	case []any:
		for _, item := range typed {
			if found := findJSONStringField(item, field); found != "" {
				return found
			}
		}
	}
	return ""
}

func (m *Manager) replyToMessage(ctx context.Context, messageID string, reply string) error {
	cmd := commandContextWithEnv(ctx, "lark-cli", "im", "+messages-reply", "--as", "bot", "--message-id", messageID, "--markdown", reply)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// sendCardReply posts an interactive (schema v1) card as a reply to rootMessageID
// and returns the new card message id. cardJSON is the full {config,header,elements}
// card payload, already encoded.
func (m *Manager) sendCardReply(ctx context.Context, rootMessageID string, cardJSON string) (string, error) {
	rootMessageID = strings.TrimSpace(rootMessageID)
	if rootMessageID == "" {
		return "", fmt.Errorf("send card reply: empty root message id")
	}
	cmd := commandContextWithEnv(ctx, "lark-cli", "im", "+messages-reply",
		"--as", "bot",
		"--message-id", rootMessageID,
		"--msg-type", "interactive",
		"--content", cardJSON,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	id := strings.TrimSpace(firstJSONStringField(output, "message_id"))
	if id == "" {
		// Fallback: some lark-cli builds nest under data.message_id but
		// firstJSONStringField walks recursively; if still empty we treat the
		// command as failed to keep the caller's fallback path honest.
		return "", fmt.Errorf("send card reply: no message_id in response: %s", strings.TrimSpace(string(output)))
	}
	return id, nil
}

// patchCardMessage updates an existing card message (cardMessageID) with a new
// interactive card payload. Uses the lark-cli pass-through `api` command since
// lark-cli has no native `card patch` verb.
func (m *Manager) patchCardMessage(ctx context.Context, cardMessageID string, cardJSON string) error {
	cardMessageID = strings.TrimSpace(cardMessageID)
	if cardMessageID == "" {
		return fmt.Errorf("patch card: empty message id")
	}
	body, err := json.Marshal(map[string]any{"content": cardJSON})
	if err != nil {
		return fmt.Errorf("patch card: marshal body: %w", err)
	}
	endpoint := "/open-apis/im/v1/messages/" + cardMessageID
	cmd := commandContextWithEnv(ctx, "lark-cli", "api",
		"PATCH", endpoint,
		"--as", "bot",
		"--data", string(body),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// createAndSendStreamingCard creates a CardKit card entity with streaming_mode
// enabled, then sends it as a reply to rootMessageID. Returns the card entity
// ID and the im message ID.
func (m *Manager) createAndSendStreamingCard(ctx context.Context, rootMessageID string, state cardState) (entityID, msgID string, err error) {
	cardJSON, renderErr := renderStreamingCardV2(state)
	if renderErr != nil {
		return "", "", renderErr
	}
	// Step 1: Create card entity
	entityID, err = m.createCardEntity(ctx, cardJSON)
	if err != nil {
		return "", "", fmt.Errorf("create card entity: %w", err)
	}
	// Step 2: Send card entity as a reply message
	msgID, err = m.sendCardEntityMessage(ctx, rootMessageID, entityID)
	if err != nil {
		return "", "", fmt.Errorf("send card entity message: %w", err)
	}
	return entityID, msgID, nil
}

// createCardEntity calls POST /open-apis/cardkit/v1/cards to create a card
// entity and returns its card_id.
func (m *Manager) createCardEntity(ctx context.Context, cardJSON string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"type": "card_json",
		"data": cardJSON,
	})
	if err != nil {
		return "", fmt.Errorf("marshal create card body: %w", err)
	}
	cmd := commandContextWithEnv(ctx, "lark-cli", "api",
		"POST", "/open-apis/cardkit/v1/cards",
		"--as", "bot",
		"--data", string(body),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	id := strings.TrimSpace(firstJSONStringField(output, "card_id"))
	if id == "" {
		return "", fmt.Errorf("create card entity: no card_id in response: %s", strings.TrimSpace(string(output)))
	}
	return id, nil
}

// sendCardEntityMessage sends an interactive message referencing an existing
// card entity. Returns the new message_id.
func (m *Manager) sendCardEntityMessage(ctx context.Context, rootMessageID string, cardEntityID string) (string, error) {
	content, err := json.Marshal(map[string]any{
		"type": "card",
		"data": map[string]any{"card_id": cardEntityID},
	})
	if err != nil {
		return "", fmt.Errorf("marshal card entity content: %w", err)
	}
	cmd := commandContextWithEnv(ctx, "lark-cli", "im", "+messages-reply",
		"--as", "bot",
		"--message-id", rootMessageID,
		"--msg-type", "interactive",
		"--content", string(content),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	id := strings.TrimSpace(firstJSONStringField(output, "message_id"))
	if id == "" {
		return "", fmt.Errorf("send card entity message: no message_id in response: %s", strings.TrimSpace(string(output)))
	}
	return id, nil
}

// streamUpdateCardContent calls PUT /open-apis/cardkit/v1/cards/:card_id/elements/:element_id/content
// to stream-update a single element's text content. This is the fast path for
// typewriter-style streaming — only the reply element is updated, not the whole card.
func (m *Manager) streamUpdateCardContent(ctx context.Context, cardEntityID string, elementID string, content string, sequence int) error {
	body, err := json.Marshal(map[string]any{
		"content":  content,
		"sequence": sequence,
	})
	if err != nil {
		return fmt.Errorf("marshal stream update body: %w", err)
	}
	endpoint := fmt.Sprintf("/open-apis/cardkit/v1/cards/%s/elements/%s/content", cardEntityID, elementID)
	cmd := commandContextWithEnv(ctx, "lark-cli", "api",
		"PUT", endpoint,
		"--as", "bot",
		"--data", string(body),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// updateCardEntity calls PUT /open-apis/cardkit/v1/cards/:card_id to do a
// full card update (used for final state when streaming ends).
func (m *Manager) updateCardEntity(ctx context.Context, cardEntityID string, cardJSON string, sequence int) error {
	body, err := json.Marshal(map[string]any{
		"card": map[string]any{
			"type": "card_json",
			"data": cardJSON,
		},
		"sequence": sequence,
	})
	if err != nil {
		return fmt.Errorf("marshal update card body: %w", err)
	}
	endpoint := "/open-apis/cardkit/v1/cards/" + cardEntityID
	cmd := commandContextWithEnv(ctx, "lark-cli", "api",
		"PUT", endpoint,
		"--as", "bot",
		"--data", string(body),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// shouldIgnoreGroupMessage returns true when GroupOnlyAtBot is enabled and the
// incoming message comes from a group chat without an @-mention of the bot
// itself. p2p chats are never ignored.
func (m *Manager) shouldIgnoreGroupMessage(event incomingEvent) bool {
	chatType := strings.ToLower(strings.TrimSpace(event.ChatType))
	if chatType == "" || chatType == "p2p" {
		return false
	}
	cfg := m.Config()
	if !cfg.GroupOnlyAtBot {
		return false
	}
	// lark-cli auth status reports the logged-in user open_id, not necessarily
	// the bot open_id mentioned in a group chat. Until we have a reliable bot
	// id source, treat any mention as a trigger and ignore only unmentioned
	// group messages. This preserves the safe default without dropping real
	// "@bot" messages.
	return len(event.MentionOpenIDs) == 0 && len(event.MentionUserIDs) == 0
}

// extractMentionIDs walks a raw lark-cli event JSON line and returns the
// open_id and user_id values found inside any "mentions[].id" array, anywhere
// in the payload. lark-cli currently surfaces mentions inside event.message.
func extractMentionIDs(raw []byte) ([]string, []string) {
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, nil
	}
	openIDs := []string{}
	userIDs := []string{}
	var walk func(v any)
	walk = func(v any) {
		switch typed := v.(type) {
		case map[string]any:
			if mentions, ok := typed["mentions"].([]any); ok {
				for _, m := range mentions {
					entry, ok := m.(map[string]any)
					if !ok {
						continue
					}
					if id, ok := entry["id"].(map[string]any); ok {
						if oid, ok := id["open_id"].(string); ok && strings.TrimSpace(oid) != "" {
							openIDs = append(openIDs, strings.TrimSpace(oid))
						}
						if uid, ok := id["user_id"].(string); ok && strings.TrimSpace(uid) != "" {
							userIDs = append(userIDs, strings.TrimSpace(uid))
						}
					}
				}
			}
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(root)
	return uniqueNonEmpty(openIDs), uniqueNonEmpty(userIDs)
}
