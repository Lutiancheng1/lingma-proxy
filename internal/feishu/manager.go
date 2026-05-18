package feishu

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	goruntime "runtime"
	"strings"
	"sync"
	"time"
)

type ManagerOptions struct {
	ProxyURL func() string
	Logf     func(level, message string)
	Emit     func(status Status)
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
		opts: opts,
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
	m.refreshStatus(context.Background())
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

func (m *Manager) logf(level, message string) {
	if m.opts.Logf != nil {
		m.opts.Logf(level, message)
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
		cmd := exec.CommandContext(runCtx, "lark-cli", "config", "init", "--new", "--lang", "zh")
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
	cmd := exec.CommandContext(ctx, "lark-cli", "config", "init", "--app-id", appID, "--app-secret-stdin", "--brand", "feishu")
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
		cmd := exec.CommandContext(runCtx, "lark-cli", "auth", "login", "--recommend")
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

	cmd := exec.CommandContext(runCtx, "lark-cli", "event", "consume", "im.message.receive_v1", "--as", "bot")
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

func (m *Manager) handleEvent(ctx context.Context, event incomingEvent) {
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
	proxyURL := ""
	if m.opts.ProxyURL != nil {
		proxyURL = strings.TrimSpace(m.opts.ProxyURL())
	}
	if proxyURL == "" {
		m.logf("warn", "Feishu bridge 收到消息，但当前代理地址为空")
		return
	}
	cfg := m.Config()
	model := cfg.Model
	if model == "" {
		model = DefaultModel
	}
	skills, _ := discoverSkills(context.Background())
	messages := []map[string]any{
		{"role": "system", "content": buildSystemPrompt(skills)},
		{"role": "user", "content": event.Content},
	}
	forceToolUse := shouldForceToolUse(event.Content)
	rounds := cfg.MaxToolRounds
	if rounds <= 0 {
		rounds = DefaultMaxToolRounds
	}
	replyText := ""
	for i := 0; i < rounds; i++ {
		resp, err := callLLM(ctx, proxyURL, model, messages, forceToolUse)
		if err != nil {
			replyText = "抱歉，LLM 服务暂时不可用，请稍后再试。"
			m.logf("warn", "Feishu bridge 调用代理失败："+err.Error())
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
		if len(msg.ToolCalls) == 0 {
			replyText = strings.TrimSpace(msg.Content)
			break
		}
		for _, tc := range msg.ToolCalls {
			var args map[string]any
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
			result := executeTool(tc.Function.Name, args)
			messages = append(messages, map[string]any{
				"role":         "tool",
				"tool_call_id": tc.ID,
				"content":      result,
			})
		}
	}
	if strings.TrimSpace(replyText) == "" {
		replyText = "已完成处理。"
	}
	if err := m.replyToMessage(ctx, event.MessageID, replyText); err != nil {
		m.logf("warn", "Feishu bridge 回复消息失败："+err.Error())
	}
}

func (m *Manager) replyToMessage(ctx context.Context, messageID string, reply string) error {
	cmd := exec.CommandContext(ctx, "lark-cli", "im", "+messages-reply", "--as", "bot", "--message-id", messageID, "--markdown", reply)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
