package feishu

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
	feishuMarkdownReplyChunkLimit    = 2800
	defaultBotDisplayName            = "飞书 Bridge"
	maxFeishuImageAttachments        = 4
	maxFeishuImageBytes              = 8 * 1024 * 1024
	feishuImageDownloadTimeout       = 90 * time.Second
	feishuVisionResponseTimeout      = 120 * time.Second
)

var (
	discoverSkillsForPrompt           = discoverSkills
	callLLMForConversation            = callLLM
	callLLMStreamForConversation      = callLLMStream
	callLLMPlainStreamForConversation = callLLMPlainStream
	callLLMPlainStreamForFinal        = callLLMPlainStream
)

type ManagerOptions struct {
	ProxyURL func() string
	Logf     func(level, message string, meta LogMeta)
	Emit     func(status Status)
	Persist  func()
	DataDir  string
}

type LogMeta struct {
	SessionID string
	ChatID    string
	MessageID string
}

type conversationState struct {
	History           []map[string]any
	CompactBoundary   int // index into History; history[:CompactBoundary] is folded into Summary
	Summary           string
	StructuredSummary StructuredSummary
	SummaryRange      string
	LastCompactedAt   time.Time
	Summarizing       bool
	ModelOverride     string
	Language          string // "" | "zh" | "en"
	ShowThinking      *bool  // nil => default true
	PromptTokens      int
	OutputTokens      int
	CacheReadTokens   int
	CacheWriteTokens  int
	Turns             int
	UsageByModel      map[string]*conversationUsage
	EstimatorScale    float64
	LastBudget        ContextBudgetSnapshot
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

type conversationInput struct {
	Text      string
	Content   any
	HasImages bool
}

// Sentinel cancel causes — distinguish between user /stop, automatic preempt
// by a newer message, and other reasons. Used with context.WithCancelCause so
// callers can branch on context.Cause(ctx) instead of guessing.
var (
	errCancelStopped   = errors.New("feishu: stopped by user")
	errCancelPreempted = errors.New("feishu: preempted by new message")
)

type ConversationSnapshot struct {
	History           []map[string]any              `json:"history,omitempty"`
	CompactBoundary   int                           `json:"compact_boundary,omitempty"`
	Summary           string                        `json:"summary,omitempty"`
	StructuredSummary StructuredSummary             `json:"structured_summary,omitempty"`
	SummaryRange      string                        `json:"summary_range,omitempty"`
	LastCompactedAt   string                        `json:"last_compacted_at,omitempty"`
	ModelOverride     string                        `json:"model_override,omitempty"`
	Language          string                        `json:"language,omitempty"`
	ShowThinking      *bool                         `json:"show_thinking,omitempty"`
	PromptTokens      int                           `json:"prompt_tokens,omitempty"`
	OutputTokens      int                           `json:"output_tokens,omitempty"`
	CacheReadTokens   int                           `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens  int                           `json:"cache_write_tokens,omitempty"`
	Turns             int                           `json:"turns,omitempty"`
	UsageByModel      map[string]*conversationUsage `json:"usage_by_model,omitempty"`
	EstimatorScale    float64                       `json:"estimator_scale,omitempty"`
	LastBudget        ContextBudgetSnapshot         `json:"last_budget,omitempty"`
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

func (s conversationState) toSnapshot() ConversationSnapshot {
	usageCopy := make(map[string]*conversationUsage, len(s.UsageByModel))
	for k, v := range s.UsageByModel {
		if v == nil {
			continue
		}
		cp := *v
		usageCopy[k] = &cp
	}
	var thinking *bool
	if s.ShowThinking != nil {
		val := *s.ShowThinking
		thinking = &val
	}
	lastCompacted := ""
	if !s.LastCompactedAt.IsZero() {
		lastCompacted = s.LastCompactedAt.Format(time.RFC3339)
	}
	return ConversationSnapshot{
		History:           cloneMessages(s.History),
		CompactBoundary:   s.CompactBoundary,
		Summary:           strings.TrimSpace(s.Summary),
		StructuredSummary: s.StructuredSummary,
		SummaryRange:      strings.TrimSpace(s.SummaryRange),
		LastCompactedAt:   lastCompacted,
		ModelOverride:     strings.TrimSpace(s.ModelOverride),
		Language:          strings.TrimSpace(s.Language),
		ShowThinking:      thinking,
		PromptTokens:      s.PromptTokens,
		OutputTokens:      s.OutputTokens,
		CacheReadTokens:   s.CacheReadTokens,
		CacheWriteTokens:  s.CacheWriteTokens,
		Turns:             s.Turns,
		UsageByModel:      usageCopy,
		EstimatorScale:    s.EstimatorScale,
		LastBudget:        s.LastBudget,
	}
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

	conversations  map[string]conversationState
	runs           map[string]*conversationRunState
	mcp            *MCPRuntime
	store          *bridgeStore
	skillService   *BridgeSkillService
	skillApprovals map[string]map[string]struct{}
	skillViews     map[string]map[string]struct{}

	// refreshGuard ensures only one refreshStatus runs at a time. The desktop
	// polls every ~2.5s; without serialization, a slow `npx skills ls` causes
	// queued probes to pile up and saturate npm's cache lock.
	refreshGuard sync.Mutex
}

func NewManager(opts ManagerOptions) *Manager {
	manager := &Manager{
		cfg: DefaultConfig(),
		status: Status{
			Platform:       goruntime.GOOS,
			Arch:           goruntime.GOARCH,
			RequiredSkills: append([]string(nil), fallbackRequiredSkillNames...),
			CurrentModel:   DefaultModel,
		},
		opts:           opts,
		conversations:  make(map[string]conversationState),
		runs:           make(map[string]*conversationRunState),
		mcp:            NewMCPRuntime(),
		skillApprovals: make(map[string]map[string]struct{}),
		skillViews:     make(map[string]map[string]struct{}),
	}
	if strings.TrimSpace(opts.DataDir) != "" {
		if store, err := newBridgeStore(opts.DataDir); err == nil {
			manager.store = store
		}
	}
	if svc, err := NewBridgeSkillService(opts.DataDir, manager.store); err == nil {
		manager.skillService = svc
		manager.status.SkillCount = len(svc.List(true))
	} else {
		manager.skillService, _ = NewBridgeSkillService("", nil)
	}
	return manager
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

func (m *Manager) ImportSkillPath(ctx context.Context, path string) (BridgeSkillImportResult, error) {
	if m.skillService == nil {
		return BridgeSkillImportResult{}, fmt.Errorf("Skill 服务未初始化")
	}
	result, err := m.skillService.ImportPath(ctx, path)
	m.mu.Lock()
	m.status.SkillCount = len(m.skillService.List(true))
	m.mu.Unlock()
	m.notifyConversationChanged()
	return result, err
}

func (m *Manager) ListBridgeSkills() []BridgeSkill {
	if m.skillService == nil {
		return nil
	}
	return m.skillService.List(false)
}

func (m *Manager) ReloadBridgeSkills(ctx context.Context) error {
	if m.skillService == nil {
		return fmt.Errorf("Skill 服务未初始化")
	}
	if err := m.skillService.Reload(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	m.status.SkillCount = len(m.skillService.List(true))
	m.mu.Unlock()
	return nil
}

func (m *Manager) SetBridgeSkillEnabled(ctx context.Context, id string, enabled bool) error {
	if m.skillService == nil {
		return fmt.Errorf("Skill 服务未初始化")
	}
	if err := m.skillService.SetEnabled(ctx, id, enabled); err != nil {
		return err
	}
	m.mu.Lock()
	m.status.SkillCount = len(m.skillService.List(true))
	m.mu.Unlock()
	m.notifyConversationChanged()
	return nil
}

func (m *Manager) DeleteBridgeSkill(ctx context.Context, id string) error {
	if m.skillService == nil {
		return fmt.Errorf("Skill 服务未初始化")
	}
	if err := m.skillService.Delete(ctx, id); err != nil {
		return err
	}
	m.mu.Lock()
	m.status.SkillCount = len(m.skillService.List(true))
	m.mu.Unlock()
	m.notifyConversationChanged()
	return nil
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
	if m.skillService != nil {
		m.status.SkillCount = len(m.skillService.List(true))
	}
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

func (m *Manager) startRecommendedLogin(ctx context.Context) (string, error) {
	m.mu.Lock()
	if m.status.LoginRunning {
		url := m.status.LoginURL
		m.mu.Unlock()
		return strings.TrimSpace(url), nil
	}
	m.mu.Unlock()

	urlCh := make(chan string, 1)
	if err := m.startLoginWithArgs(ctx, []string{"lark-cli", "auth", "login", "--recommend"}, urlCh); err != nil {
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

func cliLocationHint() string {
	cli := detectBinary("lark-cli", "--version")
	if !cli.Found {
		return "本机未检测到 lark-cli，请在 Lingma Proxy 的 Feishu Bridge 设置页点击“安装 CLI 与 Skills”。"
	}
	if strings.TrimSpace(cli.Path) == "" {
		return "本机已检测到 lark-cli，但未能解析到完整路径。"
	}
	return "本机 lark-cli 路径：" + cli.Path
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
	m.mu.Unlock()

	m.resetEventBus(ctx)

	runCtx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
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

func (m *Manager) resetEventBus(ctx context.Context) {
	stopCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd := commandContextWithEnv(stopCtx, "lark-cli", "event", "stop", "--force", "--json")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if stopCtx.Err() != nil {
			m.logf("warn", "Feishu bridge 重置事件总线超时，将继续尝试启动监听")
			return
		}
		m.logf("warn", "Feishu bridge 重置事件总线失败，将继续尝试启动监听："+strings.TrimSpace(decodeCommandOutput(output)))
		return
	}
	if trimmed := strings.TrimSpace(decodeCommandOutput(output)); trimmed != "" {
		m.logf("info", "Feishu bridge 已重置事件总线："+summarizeText(trimmed, 180))
	} else {
		m.logf("info", "Feishu bridge 已重置事件总线")
	}
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
	if strings.TrimSpace(event.MessageID) == "" || strings.TrimSpace(event.ChatID) == "" {
		return
	}
	if strings.TrimSpace(event.Content) == "" && !eventMayContainImage(event) {
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
	input := m.buildConversationInput(ctx, event)
	m.setTurnSkillScriptApprovals(conversationKey, input.Text)
	defer m.clearTurnSkillState(conversationKey)
	commandReply, handled := m.handleConversationCommand(ctx, conversationKey, proxyURL, model, input.Text, logMeta)
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
	importedSkillListing := ""
	if m.skillService != nil {
		importedSkillListing = m.skillService.PromptListing(40)
	}
	messages := []map[string]any{{"role": "system", "content": buildSystemPrompt(skills, cfg.BotIdentity, buildMCPPromptSection(mcpTools), importedSkillListing)}}
	if larkSkillContext := buildRelevantLarkSkillContext(skills, input.Text); strings.TrimSpace(larkSkillContext) != "" {
		messages = append(messages, map[string]any{
			"role":    "system",
			"content": larkSkillContext,
		})
	}
	state := m.getConversationState(conversationKey)
	if strings.TrimSpace(state.Summary) != "" {
		messages = append(messages, map[string]any{
			"role":    "system",
			"content": "当前飞书会话压缩摘要：\n" + strings.TrimSpace(state.Summary),
		})
	}
	if len(state.History) == 0 {
		if backfill := m.fetchFeishuConversationBackfill(ctx, event.ChatID, event.MessageID, logMeta); strings.TrimSpace(backfill) != "" {
			messages = append(messages, map[string]any{
				"role":    "system",
				"content": backfill,
			})
		}
	}
	rawHistory := cloneMessages(state.activeHistory())
	messages = append(messages, cloneMessages(rawHistory)...)
	userMessage := map[string]any{"role": "user", "content": input.Content}
	messages = append(messages, userMessage)
	rawHistory = append(rawHistory, cloneMessage(userMessage))
	budget := estimateContextBudget(model, cfg.Context, messages, toolDefs, importedSkillListing, state)
	m.setConversationBudget(conversationKey, budget)
	if budget.Watermark == "blocking" {
		replyText := fmt.Sprintf("当前会话上下文已接近模型上限（约 %d%%，%d / %d tokens）。请先发送 /compact 压缩上下文，或 /reset 开启新会话后再继续。", budget.UsedPercent, budget.EstimatedTokens, budget.ContextWindow)
		if err := m.replyToMessage(ctx, event.MessageID, replyText); err != nil {
			m.logf("warn", "Feishu bridge 回复消息失败："+err.Error(), logMeta)
		}
		return
	}
	consecutiveToolFailures := 0
	const maxConsecutiveToolFailures = 8
	const maxRepeatedToolFailures = 4
	forceToolUse := shouldForceToolUse(input.Text)
	plainVisionTurn := input.HasImages && !forceToolUse
	rounds := cfg.MaxToolRounds
	if rounds <= 0 {
		rounds = DefaultMaxToolRounds
	}
	if rounds > DefaultMaxToolRounds {
		rounds = DefaultMaxToolRounds
	}
	m.logf("info", fmt.Sprintf("Feishu bridge 开始处理: model=%s forceToolUse=%t plainVision=%t rounds=%d", model, forceToolUse, plainVisionTurn, rounds), logMeta)
	card := newCardWriter(m, event.MessageID, botDisplayName(cfg), model, logMeta)
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
		var resp *llmResponse
		var err error
		requestMessages := applyBudgetCompaction(messages, cfg.Context, budget)
		if plainVisionTurn {
			m.logf("info", fmt.Sprintf("Feishu bridge 视觉请求开始: model=%s timeout=%s", model, feishuVisionResponseTimeout), logMeta)
			visionCtx, visionCancel := context.WithTimeout(ctx, feishuVisionResponseTimeout)
			resp, err = callLLMPlainStreamForConversation(visionCtx, proxyURL, model, requestMessages, streamingDelta{
				onText: func(chunk string) {
					if chunk == "" {
						return
					}
					streamingReply.WriteString(chunk)
					streamHadText = true
					card.SetReply(streamingReply.String())
				},
			})
			visionCancel()
		} else {
			resp, err = callLLMStreamForConversation(ctx, proxyURL, model, requestMessages, forceToolUse, toolDefs, streamingDelta{
				onText: func(chunk string) {
					if chunk == "" {
						return
					}
					streamingReply.WriteString(chunk)
					streamHadText = true
					card.SetReply(streamingReply.String())
				},
			})
		}
		if err != nil && !streamHadText {
			if ctx.Err() == nil && isDeadlineError(err) {
				replyText = "模型响应超时，Bridge 已停止本轮处理。图片消息可能触发了当前模型或代理端的长时间无响应，请换用支持视觉输入的模型后重试。"
				card.SetStatus("error", "模型响应超时")
				card.AppendStep(cardStep{Kind: "error", Title: "模型响应超时", Body: err.Error()})
				m.logf("warn", "Feishu bridge 流式调用超时："+err.Error(), logMeta)
				break
			}
			m.logf("info", "Feishu bridge 流式调用失败，回退非流式："+err.Error(), logMeta)
			if plainVisionTurn {
				visionCtx, visionCancel := context.WithTimeout(ctx, feishuVisionResponseTimeout)
				resp, err = callLLMPlain(visionCtx, proxyURL, model, requestMessages)
				visionCancel()
			} else {
				resp, err = callLLMForConversation(ctx, proxyURL, model, requestMessages, forceToolUse, toolDefs)
			}
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
		usage := usageFromResponse(resp)
		m.accumulateUsage(conversationKey, model, usage)
		m.adjustEstimator(conversationKey, budget.EstimatedTokens, usage.Prompt+usage.CacheRead+usage.CacheWrite)
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
			card.SetStatus("done", "已完成")
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
			var result ToolExecutionResult
			if isBridgeSkillTool(tc.Function.Name) {
				result = m.executeBridgeSkillTool(ctx, conversationKey, tc.ID, tc.Function.Name, args)
			} else {
				result = executeToolContextWithRuntime(ctx, cfg, m.mcp, tc.Function.Name, args)
			}
			toolCallsExecuted = true
			content := result.Output
			if result.NeedsLogin {
				loginURL, authErr := m.startRecommendedLogin(context.Background())
				if authErr != nil {
					replyText = "当前操作需要飞书用户授权。我已尝试由 Bridge 自动发起授权，但这次没有成功。请到 Lingma Proxy 的 Feishu Bridge 设置页重新点击“登录授权”，完成后再让我继续。\n\n" + cliLocationHint()
					content = replyText
					card.UpdateLastStep(func(step *cardStep) {
						step.Kind = "error"
						step.Done = true
						step.Body = "需要用户授权（自动授权失败）"
					})
					card.RefreshStructure()
					m.logf("warn", "Feishu bridge 自动发起用户授权失败："+authErr.Error(), logMeta)
				} else {
					replyText = "当前操作需要飞书用户授权。我已经为你发起授权流程，请打开下面的授权链接完成授权；授权完成后再对我说一次，我会继续处理。" + loginHint(loginURL)
					if strings.TrimSpace(loginURL) == "" {
						replyText = "当前操作需要飞书用户授权。我已经为你发起授权流程，但暂时没有捕获到完整授权链接。请到 Lingma Proxy 的 Feishu Bridge 设置页点击“打开授权链接”；授权完成后再对我说一次，我会继续处理。\n\n" + cliLocationHint()
					}
					content = replyText
					card.UpdateLastStep(func(step *cardStep) {
						step.Kind = "error"
						step.Done = true
						step.Body = "需要用户授权（已自动发起）"
					})
					card.RefreshStructure()
					m.logf("info", "Feishu bridge 检测到需要用户授权，已自动发起登录", logMeta)
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
				card.SetStatus("error", "需要授权")
				break conversation
			}
			if len(result.MissingScopes) > 0 {
				scopes := uniqueNonEmpty(result.MissingScopes)
				scopeLabel := strings.Join(scopes, ", ")
				loginURL, authErr := m.startScopeLogin(context.Background(), scopes...)
				if authErr != nil {
					replyText = fmt.Sprintf("当前操作缺少权限 %s。我已尝试由 Bridge 自动发起授权，但这次没有成功。请到 Lingma Proxy 的 Feishu Bridge 设置页重新点击“登录授权”，完成后再让我继续。\n\n%s", scopeLabel, cliLocationHint())
					content = replyText
					card.UpdateLastStep(func(step *cardStep) {
						step.Kind = "error"
						step.Done = true
						step.Body = "缺少权限：" + scopeLabel + "（自动授权失败）"
					})
					card.RefreshStructure()
					m.logf("warn", "Feishu bridge 自动发起 scope 授权失败："+authErr.Error(), logMeta)
				} else {
					replyText = fmt.Sprintf("当前操作缺少权限 %s。我已经为你发起授权流程，请先在浏览器完成授权。如果 Lingma Proxy 已打开，请直接到 Feishu Bridge 设置页点击“打开授权链接”；授权完成后再对我说一次，我会继续处理。%s", scopeLabel, loginHint(loginURL))
					if strings.TrimSpace(loginURL) == "" {
						replyText = fmt.Sprintf("当前操作缺少权限 %s。我已经为你发起授权流程，但暂时没有捕获到完整授权链接。请到 Lingma Proxy 的 Feishu Bridge 设置页点击“打开授权链接”；授权完成后再对我说一次，我会继续处理。\n\n%s", scopeLabel, cliLocationHint())
					}
					content = replyText
					card.UpdateLastStep(func(step *cardStep) {
						step.Kind = "error"
						step.Done = true
						step.Body = "需要授权 " + scopeLabel + "（已自动发起）"
					})
					card.RefreshStructure()
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
			if m.store != nil {
				if memoryID, err := m.store.SaveToolMemory(ctx, conversationKey, tc.Function.Name, args, content, result.IsError); err == nil && memoryID != "" && len(content) > 1200 {
					if !shouldPreserveToolResultForModel(content) {
						content = summarizeText(content, 1200) + "\n\n[完整工具结果已保存为 " + memoryID + "；如需要完整内容，请基于该引用继续请求。]"
					}
				}
			}
			m.logf("info", fmt.Sprintf("Feishu bridge tool result: %s %s", tc.Function.Name, summarizeText(result.Output, 160)), logMeta)
			card.UpdateLastStep(func(step *cardStep) {
				step.Done = true
				step.Body = step.Body + "\n结果：" + summarizeText(result.Output, 1200)
			})
			card.RefreshStructure()
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
		card.SetStatus("done", "已完成")
	}
	card.Finalize(replyText, "")
	m.logf("info", "Feishu bridge 卡片回复已完成: message="+trimmedID(event.MessageID), logMeta)
}

func shouldPreserveToolResultForModel(content string) bool {
	content = strings.TrimSpace(content)
	if content == "" {
		return false
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return false
	}
	return strings.TrimSpace(fmt.Sprint(payload["kind"])) == "drive_search"
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
		if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
			m.logf("warn", "Feishu bridge 最终答复流式超时："+err.Error(), meta)
			return fallbackToolResultReply(lastToolOutput)
		}
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
	snapshot := state.toSnapshot()
	m.mu.Unlock()
	if m.store != nil {
		if err := m.store.SaveConversationSnapshot(context.Background(), chatID, snapshot); err != nil {
			m.logf("warn", "Feishu bridge SQLite 会话写入失败，已降级内存态："+err.Error())
		}
	}
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
		History:           cloneMessages(state.History),
		CompactBoundary:   state.CompactBoundary,
		Summary:           state.Summary,
		StructuredSummary: state.StructuredSummary,
		SummaryRange:      state.SummaryRange,
		LastCompactedAt:   state.LastCompactedAt,
		ModelOverride:     state.ModelOverride,
		Language:          state.Language,
		ShowThinking:      state.ShowThinking,
		PromptTokens:      state.PromptTokens,
		OutputTokens:      state.OutputTokens,
		CacheReadTokens:   state.CacheReadTokens,
		CacheWriteTokens:  state.CacheWriteTokens,
		Turns:             state.Turns,
		UsageByModel:      state.UsageByModel,
		EstimatorScale:    state.EstimatorScale,
		LastBudget:        state.LastBudget,
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

func applyBudgetCompaction(messages []map[string]any, cfg ContextConfig, budget ContextBudgetSnapshot) []map[string]any {
	cfg = normalizeContextConfig(cfg)
	if budget.Watermark == "ok" {
		return messages
	}
	compacted := microcompactToolResults(messages)
	if budget.Watermark != "critical" && budget.Watermark != "compact" {
		return compacted
	}
	keepRecent := cfg.ToolResultRetention
	if keepRecent <= 0 {
		keepRecent = defaultToolResultRetention
	}
	toolSeen := 0
	for i := len(compacted) - 1; i >= 0; i-- {
		role, _ := compacted[i]["role"].(string)
		if role != "tool" {
			continue
		}
		toolSeen++
		if toolSeen <= keepRecent {
			continue
		}
		next := cloneMessage(compacted[i])
		next["content"] = "[old tool result compacted; full content is stored in Feishu Bridge tool memory if persistence is available]"
		compacted[i] = next
	}
	return compacted
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
	resourceParts := make([]string, 0, len(batch))
	eventIDs := make([]string, 0, len(batch))
	for _, item := range batch {
		text := normalizeEventConversationText(item)
		if text != "" {
			parts = append(parts, text)
		}
		if len(extractFeishuImageKeys(item.Content)) > 0 {
			resourceParts = append(resourceParts, strings.TrimSpace(item.Content))
		}
		if strings.TrimSpace(item.EventID) != "" {
			eventIDs = append(eventIDs, strings.TrimSpace(item.EventID))
		}
	}
	if len(parts) > 0 || len(resourceParts) > 0 {
		merged.Content = strings.Join(append(resourceParts, parts...), "\n\n")
	}
	if len(eventIDs) > 0 {
		merged.EventID = strings.Join(eventIDs, "+")
	}
	return merged
}

func (m *Manager) buildConversationInput(ctx context.Context, event incomingEvent) conversationInput {
	text := normalizeEventConversationText(event)
	quoteID := strings.TrimSpace(event.ReplyToMessageID)
	if quoteID != "" && quoteID != strings.TrimSpace(event.MessageID) {
		quote, err := m.fetchQuotedMessage(ctx, quoteID)
		if err != nil {
			m.logf("warn", "Feishu bridge 引用消息读取失败："+err.Error(), LogMeta{
				ChatID:    event.ChatID,
				MessageID: event.MessageID,
			})
		} else if quote = strings.TrimSpace(quote); quote != "" {
			text = fmt.Sprintf("<quoted_message id=\"%s\">\n%s\n</quoted_message>\n\n%s", quoteID, quote, text)
		}
	}
	return m.buildMultimodalConversationInput(ctx, event, text)
}

func (m *Manager) fetchQuotedMessage(ctx context.Context, messageID string) (string, error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return "", nil
	}
	cmd := commandContextWithEnv(ctx, "lark-cli", "im", "messages", "get", "--as", "bot", "--message-id", messageID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(decodeCommandOutput(output)))
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
	return strings.TrimSpace(decodeCommandOutput(output)), nil
}

func (m *Manager) buildMultimodalConversationInput(ctx context.Context, event incomingEvent, text string) conversationInput {
	imageKeys := extractFeishuImageKeys(event.Content)
	if len(imageKeys) == 0 {
		if strings.EqualFold(strings.TrimSpace(event.MessageType), "image") && strings.TrimSpace(text) == "" {
			text = "用户发送了一张图片，但消息事件里没有 image_key，Bridge 无法下载图片内容。"
		}
		return conversationInput{Text: text, Content: text}
	}
	if len(imageKeys) > maxFeishuImageAttachments {
		imageKeys = imageKeys[:maxFeishuImageAttachments]
	}
	parts := make([]map[string]any, 0, len(imageKeys)+1)
	promptText := strings.TrimSpace(text)
	if promptText == "" {
		promptText = fmt.Sprintf("用户发送了 %d 张图片。请根据图片内容回答。", len(imageKeys))
	}
	parts = append(parts, map[string]any{"type": "text", "text": promptText})
	for _, imageKey := range imageKeys {
		dataURL, err := m.downloadFeishuImageDataURL(ctx, event.MessageID, imageKey)
		if err != nil {
			m.logf("warn", fmt.Sprintf("Feishu bridge 图片下载失败: key=%s err=%s", imageKey, err.Error()), LogMeta{
				ChatID:    event.ChatID,
				MessageID: event.MessageID,
			})
			parts[0]["text"] = strings.TrimSpace(parts[0]["text"].(string) + fmt.Sprintf("\n\n[图片 %s 下载失败：%s]", imageKey, err.Error()))
			continue
		}
		m.logf("info", fmt.Sprintf("Feishu bridge 图片下载成功: key=%s payload=%d chars", imageKey, len(dataURL)), LogMeta{
			ChatID:    event.ChatID,
			MessageID: event.MessageID,
		})
		parts = append(parts, map[string]any{
			"type": "image_url",
			"image_url": map[string]any{
				"url": dataURL,
			},
		})
	}
	if len(parts) == 1 {
		return conversationInput{Text: parts[0]["text"].(string), Content: parts[0]["text"], HasImages: true}
	}
	return conversationInput{Text: promptText, Content: parts, HasImages: true}
}

func eventMayContainImage(event incomingEvent) bool {
	msgType := strings.ToLower(strings.TrimSpace(event.MessageType))
	return msgType == "image" || strings.Contains(strings.ToLower(event.Content), "image_key") || strings.Contains(event.Content, "img_")
}

func extractFeishuImageKeys(content string) []string {
	seen := map[string]struct{}{}
	keys := make([]string, 0, 2)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if !strings.HasPrefix(value, "img_") {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		keys = append(keys, value)
	}
	var payload any
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &payload); err == nil {
		walkJSONStrings(payload, func(key, value string) {
			if strings.EqualFold(key, "image_key") || strings.HasPrefix(value, "img_") {
				add(value)
			}
		})
	}
	for _, field := range strings.FieldsFunc(content, func(r rune) bool {
		return !(r == '_' || r == '-' || r == '.' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z')
	}) {
		add(field)
	}
	return keys
}

func walkJSONStrings(value any, visit func(key, value string)) {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if s, ok := item.(string); ok {
				visit(key, s)
				continue
			}
			walkJSONStrings(item, visit)
		}
	case []any:
		for _, item := range v {
			walkJSONStrings(item, visit)
		}
	}
}

func (m *Manager) downloadFeishuImageDataURL(ctx context.Context, messageID string, imageKey string) (string, error) {
	messageID = strings.TrimSpace(messageID)
	imageKey = safeFeishuResourceName(imageKey)
	if messageID == "" || imageKey == "" {
		return "", errors.New("message_id 或 image_key 为空")
	}
	cacheRoot, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(cacheRoot) == "" {
		cacheRoot = os.TempDir()
	}
	dir := filepath.Join(cacheRoot, "lingma-ipc-proxy", "feishu-images", safeFeishuResourceName(messageID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	outputName := imageKey + ".dat"
	downloadCtx, cancel := context.WithTimeout(ctx, feishuImageDownloadTimeout)
	defer cancel()
	cmd := commandContextWithEnv(downloadCtx, "lark-cli", "im", "+messages-resources-download",
		"--as", "bot",
		"--message-id", messageID,
		"--file-key", imageKey,
		"--type", "image",
		"--output", outputName,
	)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(downloadCtx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("图片下载超时（%s）", feishuImageDownloadTimeout)
		}
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(decodeCommandOutput(output)))
	}
	path := filepath.Join(dir, outputName)
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Size() > maxFeishuImageBytes {
		return "", fmt.Errorf("图片过大（%.1f MB），当前上限 %.1f MB", float64(info.Size())/1024/1024, float64(maxFeishuImageBytes)/1024/1024)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	mimeType := http.DetectContentType(data)
	if !strings.HasPrefix(mimeType, "image/") {
		mimeType = "image/png"
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

func safeFeishuResourceName(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		}
	}
	return b.String()
}

func normalizeConversationText(text string) string {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "- ")
	return strings.TrimSpace(text)
}

func normalizeEventConversationText(event incomingEvent) string {
	text := normalizeConversationText(event.Content)
	if !eventMayContainImage(event) {
		return text
	}
	content := strings.TrimSpace(event.Content)
	if content == "" || !strings.HasPrefix(content, "{") {
		return stripFeishuImagePlaceholders(text)
	}
	for _, field := range []string{"text", "plain_text", "markdown"} {
		if found := firstJSONStringField([]byte(content), field); found != "" {
			return stripFeishuImagePlaceholders(normalizeConversationText(found))
		}
	}
	if len(extractFeishuImageKeys(content)) > 0 {
		return ""
	}
	return text
}

func stripFeishuImagePlaceholders(text string) string {
	for {
		start := strings.Index(text, "[Image:")
		if start < 0 {
			return strings.TrimSpace(text)
		}
		end := strings.Index(text[start:], "]")
		if end < 0 {
			return strings.TrimSpace(text[:start])
		}
		text = text[:start] + text[start+end+1:]
	}
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
			"- /context：查看本会话上下文预算、压缩水位和 Skill 占用\n" +
			"- /skills：列出用户导入并启用的 Feishu Bridge Skills\n" +
			"- /skill <name>：查看某个 Skill 摘要\n" +
			"- /reload-skills：重新扫描用户导入 Skills 和官方 lark-cli Skills\n" +
			"- /skill-run <skill> <script> confirm：确认执行 Skill scripts/ 下的脚本\n" +
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
		if m.store != nil {
			if err := m.store.ClearConversation(ctx, chatID); err != nil {
				m.logf("warn", "Feishu bridge 清理持久化会话失败："+err.Error(), meta)
			}
		}
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
	case "/context":
		m.logf("info", "Feishu bridge 会话命令: /context", meta)
		return m.commandContextText(chatID), true
	case "/skills":
		m.logf("info", "Feishu bridge 会话命令: /skills", meta)
		return m.commandSkillsText(), true
	case "/skill":
		m.logf("info", "Feishu bridge 会话命令: /skill", meta)
		if len(args) == 0 {
			return "用法：/skill <name-or-id>", true
		}
		return m.commandSkillText(strings.Join(args, " ")), true
	case "/reload-skills":
		m.logf("info", "Feishu bridge 会话命令: /reload-skills", meta)
		clearSkillManifestCache()
		if m.skillService == nil {
			return "官方 lark-cli Skills 缓存已清理；下一轮请求会重新扫描本机安装状态。用户导入 Skill 服务未初始化。", true
		}
		if err := m.skillService.Reload(ctx); err != nil {
			return "Skills 重新扫描失败：" + err.Error(), true
		}
		officialSkills, _ := discoverSkillsForPrompt(ctx)
		readyOfficial := 0
		for _, skill := range officialSkills {
			if skill.Found {
				readyOfficial++
			}
		}
		m.mu.Lock()
		m.status.SkillCount = len(m.skillService.List(true))
		m.status.Skills = officialSkills
		m.status.SkillsReady = skillsReady(officialSkills)
		m.mu.Unlock()
		return fmt.Sprintf("Skills 已重新扫描。用户导入 Skill 启用 %d 个；官方 lark-cli Skills 就绪 %d/%d 个。", len(m.skillService.List(true)), readyOfficial, len(officialSkills)), true
	case "/skill-run":
		m.logf("info", "Feishu bridge 会话命令: /skill-run", meta)
		if len(args) < 3 || !strings.EqualFold(args[len(args)-1], "confirm") {
			return "用法：/skill-run <skill> <script> confirm。脚本执行前必须由用户显式确认。", true
		}
		output, err := m.runSkillScriptCommand(ctx, args[0], args[1], args[2:len(args)-1])
		if err != nil {
			return "Skill 脚本执行失败：" + err.Error() + "\n\n输出：\n" + output, true
		}
		return "Skill 脚本执行完成：\n" + output, true
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

func (m *Manager) setConversationBudget(chatID string, budget ContextBudgetSnapshot) {
	if chatID == "" {
		return
	}
	m.mu.Lock()
	state := m.conversations[chatID]
	state.LastBudget = budget
	m.conversations[chatID] = state
	m.mu.Unlock()
}

func (m *Manager) adjustEstimator(chatID string, estimated int, actual int) {
	if chatID == "" || estimated <= 0 || actual <= 0 {
		return
	}
	m.mu.Lock()
	state := m.conversations[chatID]
	state.EstimatorScale = updateEstimatorScale(state.EstimatorScale, estimated, actual)
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
	name := botDisplayNameOrDefault(cfg)
	groupRule := "群聊默认仅在 @我时响应"
	if !cfg.GroupOnlyAtBot {
		groupRule = "群聊会响应所有消息"
	}
	skills := m.skillSummaryLine()
	return strings.Join([]string{
		"嗨，我是 " + name + "。我可以帮你在飞书内调用 Lingma 代理 + lark CLI 完成对话、文档、日程、知识库等操作。",
		"",
		"- 当前模型：`" + model + "`",
		"- " + groupRule,
		"- " + skills,
		"- " + m.mcpSummaryLine(),
		"",
		"发送 /help 查看完整命令列表。",
	}, "\n")
}

func botDisplayName(cfg Config) string {
	if name := strings.TrimSpace(cfg.BotName); name != "" {
		return limitBotName(name)
	}
	return ""
}

func botDisplayNameOrDefault(cfg Config) string {
	if name := botDisplayName(cfg); name != "" {
		return name
	}
	return defaultBotDisplayName
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

func (m *Manager) commandContextText(chatID string) string {
	m.mu.RLock()
	state, ok := m.conversations[chatID]
	m.mu.RUnlock()
	if !ok {
		return "当前会话还没有上下文统计。"
	}
	budget := state.LastBudget
	if budget.ContextWindow == 0 {
		cfg := m.Config()
		budget = estimateContextBudget(m.resolveSessionModel(chatID, cfg.Model), cfg.Context, state.activeHistory(), nil, "", state)
	}
	summaryState := "无"
	if strings.TrimSpace(state.Summary) != "" {
		summaryState = "已有"
	}
	lastCompact := "从未"
	if !state.LastCompactedAt.IsZero() {
		lastCompact = state.LastCompactedAt.Format("2006-01-02 15:04:05")
	}
	return fmt.Sprintf("上下文状态：\n- 模型：%s\n- 估算占用：%d%%（约 %d / %d tokens）\n- 剩余额度：%d tokens\n- 工具结果占用：约 %d tokens\n- Skill 占用：约 %d tokens\n- 水位：%s\n- 下一步策略：%s\n- 摘要：%s（范围：%s）\n- 最近压缩：%s",
		budget.Model, budget.UsedPercent, budget.EstimatedTokens, budget.ContextWindow,
		budget.RemainingTokens, budget.ToolResultTokens, budget.SkillTokens,
		budget.Watermark, budget.NextAction, summaryState, fallbackText(state.SummaryRange, "未记录"), lastCompact)
}

func (m *Manager) commandSkillsText() string {
	if m.skillService == nil {
		return "Skill 服务未初始化。"
	}
	skills := m.skillService.List(false)
	if len(skills) == 0 {
		return "当前未导入用户 Skill。可在 Lingma Proxy → Feishu Bridge 高级设置中导入 zip 或文件夹。"
	}
	lines := []string{"Feishu Bridge Skills："}
	for _, skill := range skills {
		state := "启用"
		if !skill.Enabled {
			state = "停用"
		}
		if skill.Error != "" {
			state = "错误：" + summarizeText(skill.Error, 80)
		}
		lines = append(lines, fmt.Sprintf("- `%s`（%s，%s）：%s", skill.Name, skill.ID, state, summarizeText(skill.Description, 140)))
	}
	return strings.Join(lines, "\n")
}

func (m *Manager) commandSkillText(name string) string {
	if m.skillService == nil {
		return "Skill 服务未初始化。"
	}
	skill, ok := m.skillService.Find(name)
	if !ok {
		return "未找到 Skill：" + name
	}
	lines := []string{
		"Skill：" + skill.Name,
		"- ID：" + skill.ID,
		"- 状态：" + map[bool]string{true: "启用", false: "停用"}[skill.Enabled],
		"- 路径：" + skill.Path,
	}
	if skill.Version != "" {
		lines = append(lines, "- 版本："+skill.Version)
	}
	if skill.Description != "" {
		lines = append(lines, "- 描述："+skill.Description)
	}
	if skill.WhenToUse != "" {
		lines = append(lines, "- 使用场景："+skill.WhenToUse)
	}
	if len(skill.Scripts) > 0 {
		lines = append(lines, "- scripts："+strings.Join(skill.Scripts, ", "))
	}
	return strings.Join(lines, "\n")
}

func fallbackText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
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
	summary, structured, err := summarizeConversation(ctx, proxyURL, model, state.Summary, state.History)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	next := m.conversations[chatID]
	next.Summary = summary
	next.StructuredSummary = structured
	if compact {
		// Set boundary to keep recent N messages active; older ones stay in
		// History but are folded into Summary so /undo can rewind across.
		const keepActive = 6
		if len(next.History) > keepActive {
			next.CompactBoundary = len(next.History) - keepActive
		} else {
			next.CompactBoundary = 0
		}
		next.LastCompactedAt = time.Now()
		next.SummaryRange = fmt.Sprintf("1-%d", len(next.History))
	}
	m.conversations[chatID] = next
	m.mu.Unlock()
	m.notifyConversationChanged()
	if m.store != nil {
		_ = m.store.SaveSummary(ctx, chatID, model, structured, 0, len(state.History), estimateMessagesTokens(state.History), estimateTextTokens(summary))
	}
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
	if !cfg.Context.AutoCompact {
		return
	}
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
	overTokens := (state.PromptTokens >= autoCompactTokenThreshold || state.LastBudget.Watermark == "compact" || state.LastBudget.Watermark == "critical") && len(state.History) > persistedConversationRecentLimit
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
	shouldCompact := overMessages || overTokens
	m.conversations[chatID] = state
	m.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		summary, structured, err := summarizeConversation(ctx, proxyURL, model, existingSummary, history)

		m.mu.Lock()
		state, ok := m.conversations[chatID]
		if !ok {
			m.mu.Unlock()
			return
		}
		state.Summarizing = false
		if err == nil && strings.TrimSpace(summary) != "" {
			trimmed := strings.TrimSpace(summary)
			if strings.TrimSpace(state.Summary) == "" || shouldCompact {
				state.Summary = trimmed
				state.StructuredSummary = structured
			}
			if shouldCompact {
				if len(state.History) > persistedConversationRecentLimit {
					state.CompactBoundary = len(state.History) - persistedConversationRecentLimit
				} else {
					state.CompactBoundary = 0
				}
				state.LastCompactedAt = time.Now()
				state.SummaryRange = fmt.Sprintf("1-%d", len(state.History))
				// Reset the running prompt counter; subsequent calls will re-fill it
				// against the now-shorter active window. Keep cumulative cache/output
				// for /cost reporting.
				state.PromptTokens = 0
			}
		}
		m.conversations[chatID] = state
		m.mu.Unlock()

		if err == nil && strings.TrimSpace(summary) != "" {
			if m.store != nil {
				_ = m.store.SaveSummary(context.Background(), chatID, model, structured, 0, len(history), estimateMessagesTokens(history), estimateTextTokens(summary))
			}
			m.notifyConversationChanged()
		}
	}()
}

func summarizeConversation(ctx context.Context, proxyURL string, model string, existingSummary string, history []map[string]any) (string, StructuredSummary, error) {
	if strings.TrimSpace(proxyURL) == "" || strings.TrimSpace(model) == "" {
		return "", StructuredSummary{}, fmt.Errorf("missing proxy context")
	}
	sanitized := sanitizeMessagesForSummary(history)
	serialized, err := json.Marshal(sanitized)
	if err != nil {
		return "", StructuredSummary{}, err
	}
	prompt := "请用中文把下面这段飞书工作会话压缩成可续接的结构化摘要。只输出 JSON，不要 Markdown，不要前言。字段必须包含：primary_goal、user_preferences、confirmed_decisions、pending_actions、open_questions、important_entities、artifacts、tool_results、errors_and_recoveries、next_step。数组字段输出字符串数组。摘要要保留授权状态、待继续任务、关键文档/文件/图片引用和工具失败恢复信息。"
	if strings.TrimSpace(existingSummary) != "" {
		prompt += "\n\n已有摘要（如有价值可合并更新）：\n" + strings.TrimSpace(existingSummary)
	}
	prompt += "\n\n原始会话（JSON）：\n" + string(serialized)
	resp, err := callLLMPlain(ctx, proxyURL, model, []map[string]any{
		{"role": "system", "content": "你是一个会话压缩器，只输出可解析 JSON。不要调用工具。"},
		{"role": "user", "content": prompt},
	})
	if err != nil {
		return "", StructuredSummary{}, err
	}
	if len(resp.Choices) == 0 {
		return "", StructuredSummary{}, nil
	}
	raw := strings.TrimSpace(resp.Choices[0].Message.Content)
	structured := parseStructuredSummary(raw)
	if isEmptyStructuredSummary(structured) {
		structured.PrimaryGoal = summarizeText(raw, 500)
	}
	return renderStructuredSummary(structured), structured, nil
}

func keepRecentConversation(history []map[string]any, max int) []map[string]any {
	if max <= 0 || len(history) <= max {
		return cloneMessages(history)
	}
	start := len(history) - max
	return cloneMessages(history[start:])
}

func sanitizeMessagesForSummary(history []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(history))
	for _, msg := range history {
		next := cloneMessage(msg)
		content, ok := next["content"].(string)
		if ok && len(content) > 1800 {
			next["content"] = summarizeText(content, 900) + "\n[content compacted for summary]"
		}
		if contentBlocks, ok := next["content"].([]any); ok {
			blocks := make([]any, 0, len(contentBlocks))
			for _, block := range contentBlocks {
				item, _ := block.(map[string]any)
				if item == nil {
					blocks = append(blocks, block)
					continue
				}
				if typ, _ := item["type"].(string); typ == "image_url" || typ == "image" {
					blocks = append(blocks, map[string]any{"type": "text", "text": "[image attachment]"})
					continue
				}
				blocks = append(blocks, item)
			}
			next["content"] = blocks
		}
		out = append(out, next)
	}
	return out
}

func parseStructuredSummary(raw string) StructuredSummary {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	var summary StructuredSummary
	_ = json.Unmarshal([]byte(raw), &summary)
	return summary
}

func isEmptyStructuredSummary(summary StructuredSummary) bool {
	return strings.TrimSpace(summary.PrimaryGoal) == "" &&
		len(summary.UserPreferences) == 0 &&
		len(summary.ConfirmedDecisions) == 0 &&
		len(summary.PendingActions) == 0 &&
		len(summary.OpenQuestions) == 0 &&
		len(summary.ImportantEntities) == 0 &&
		len(summary.Artifacts) == 0 &&
		len(summary.ToolResults) == 0 &&
		len(summary.ErrorsAndRecoveries) == 0 &&
		strings.TrimSpace(summary.NextStep) == ""
}

func renderStructuredSummary(summary StructuredSummary) string {
	lines := make([]string, 0, 16)
	if summary.PrimaryGoal != "" {
		lines = append(lines, "当前目标："+summary.PrimaryGoal)
	}
	appendList := func(title string, items []string) {
		clean := uniqueNonEmpty(items)
		if len(clean) == 0 {
			return
		}
		lines = append(lines, title+"："+strings.Join(clean, "；"))
	}
	appendList("用户偏好", summary.UserPreferences)
	appendList("已确认决策", summary.ConfirmedDecisions)
	appendList("待办动作", summary.PendingActions)
	appendList("开放问题", summary.OpenQuestions)
	appendList("关键对象", summary.ImportantEntities)
	appendList("产物引用", summary.Artifacts)
	appendList("工具结果", summary.ToolResults)
	appendList("错误与恢复", summary.ErrorsAndRecoveries)
	if summary.NextStep != "" {
		lines = append(lines, "下一步："+summary.NextStep)
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
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
		next := state.toSnapshot()
		next.History = history
		next.CompactBoundary = boundary
		next.Summary = summary
		out[chatID] = next
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
		var lastCompacted time.Time
		if strings.TrimSpace(state.LastCompactedAt) != "" {
			lastCompacted, _ = time.Parse(time.RFC3339, strings.TrimSpace(state.LastCompactedAt))
		}
		m.conversations[chatID] = conversationState{
			History:           history,
			CompactBoundary:   boundary,
			Summary:           strings.TrimSpace(state.Summary),
			StructuredSummary: state.StructuredSummary,
			SummaryRange:      strings.TrimSpace(state.SummaryRange),
			LastCompactedAt:   lastCompacted,
			ModelOverride:     strings.TrimSpace(state.ModelOverride),
			Language:          strings.TrimSpace(state.Language),
			ShowThinking:      thinking,
			PromptTokens:      state.PromptTokens,
			OutputTokens:      state.OutputTokens,
			CacheReadTokens:   state.CacheReadTokens,
			CacheWriteTokens:  state.CacheWriteTokens,
			Turns:             state.Turns,
			UsageByModel:      usageCopy,
			EstimatorScale:    state.EstimatorScale,
			LastBudget:        state.LastBudget,
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
	text := strings.TrimSpace(decodeCommandOutput(output))
	if text == "" {
		return err.Error()
	}
	return fmt.Sprintf("%v: %s", err, text)
}

func isDeadlineError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "context deadline exceeded")
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
	parts := splitMarkdownReply(reply, feishuMarkdownReplyChunkLimit)
	if len(parts) == 0 {
		return nil
	}
	for i, part := range parts {
		if len(parts) > 1 {
			part = fmt.Sprintf("（%d/%d）\n\n%s", i+1, len(parts), part)
		}
		sendCtx := ctx
		if sendCtx == nil || sendCtx.Err() != nil {
			sendCtx = context.Background()
		}
		cmdCtx, cancel := context.WithTimeout(sendCtx, 30*time.Second)
		cmd := commandContextWithEnv(cmdCtx, "lark-cli", "im", "+messages-reply", "--as", "bot", "--message-id", messageID, "--markdown", part)
		output, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(decodeCommandOutput(output)))
		}
	}
	return nil
}

func splitMarkdownReply(reply string, limit int) []string {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return nil
	}
	if limit <= 0 || len([]rune(reply)) <= limit {
		return []string{reply}
	}
	var parts []string
	var current strings.Builder
	flush := func() {
		text := strings.TrimSpace(current.String())
		if text != "" {
			parts = append(parts, text)
		}
		current.Reset()
	}
	appendRunes := func(text string) {
		runes := []rune(text)
		for len(runes) > 0 {
			remaining := limit - len([]rune(current.String()))
			if remaining <= 0 {
				flush()
				remaining = limit
			}
			if remaining > len(runes) {
				remaining = len(runes)
			}
			current.WriteString(string(runes[:remaining]))
			runes = runes[remaining:]
			if len([]rune(current.String())) >= limit {
				flush()
			}
		}
	}
	for _, line := range strings.SplitAfter(reply, "\n") {
		if len([]rune(line)) > limit {
			appendRunes(line)
			continue
		}
		if current.Len() > 0 && len([]rune(current.String()))+len([]rune(line)) > limit {
			flush()
		}
		current.WriteString(line)
	}
	flush()
	return parts
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
		if id := strings.TrimSpace(firstJSONStringField(output, "message_id")); id != "" {
			m.logf("warn", "Feishu bridge send card reply 进程异常但已返回 message_id，按成功处理："+err.Error(), LogMeta{MessageID: rootMessageID})
			return id, nil
		}
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(decodeCommandOutput(output)))
	}
	id := strings.TrimSpace(firstJSONStringField(output, "message_id"))
	if id == "" {
		// Fallback: some lark-cli builds nest under data.message_id but
		// firstJSONStringField walks recursively; if still empty we treat the
		// command as failed to keep the caller's fallback path honest.
		return "", fmt.Errorf("send card reply: no message_id in response: %s", strings.TrimSpace(decodeCommandOutput(output)))
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
	cmd, cleanup, err := larkAPICommandWithJSONData(ctx, "PATCH", endpoint, "bot", body)
	if err != nil {
		return err
	}
	defer cleanup()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(decodeCommandOutput(output)))
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
	cmd, cleanup, err := larkAPICommandWithJSONData(ctx, "POST", "/open-apis/cardkit/v1/cards", "bot", body)
	if err != nil {
		return "", err
	}
	defer cleanup()
	output, err := cmd.CombinedOutput()
	if err != nil {
		if id := strings.TrimSpace(firstJSONStringField(output, "card_id")); id != "" {
			m.logf("warn", "Feishu bridge create card entity 进程异常但已返回 card_id，按成功处理："+err.Error())
			return id, nil
		}
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(decodeCommandOutput(output)))
	}
	id := strings.TrimSpace(firstJSONStringField(output, "card_id"))
	if id == "" {
		return "", fmt.Errorf("create card entity: no card_id in response: %s", strings.TrimSpace(decodeCommandOutput(output)))
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
		if id := strings.TrimSpace(firstJSONStringField(output, "message_id")); id != "" {
			m.logf("warn", "Feishu bridge send card entity message 进程异常但已返回 message_id，按成功处理："+err.Error(), LogMeta{MessageID: rootMessageID})
			return id, nil
		}
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(decodeCommandOutput(output)))
	}
	id := strings.TrimSpace(firstJSONStringField(output, "message_id"))
	if id == "" {
		return "", fmt.Errorf("send card entity message: no message_id in response: %s", strings.TrimSpace(decodeCommandOutput(output)))
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
	cmd, cleanup, err := larkAPICommandWithJSONData(ctx, "PUT", endpoint, "bot", body)
	if err != nil {
		return err
	}
	defer cleanup()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(decodeCommandOutput(output)))
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
	cmd, cleanup, err := larkAPICommandWithJSONData(ctx, "PUT", endpoint, "bot", body)
	if err != nil {
		return err
	}
	defer cleanup()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(decodeCommandOutput(output)))
	}
	return nil
}

// updateCardSettings disables CardKit streaming without replacing the full
// card body. This mirrors Feishu's recommended streaming-card close flow and
// avoids re-sending large reply/tool content during finalization.
func (m *Manager) updateCardSettings(ctx context.Context, cardEntityID string, summary string, sequence int) error {
	settingsJSON, err := json.Marshal(map[string]any{
		"config": map[string]any{
			"streaming_mode": false,
			"summary":        map[string]any{"content": summary},
		},
	})
	if err != nil {
		return fmt.Errorf("marshal card settings: %w", err)
	}
	body, err := json.Marshal(map[string]any{
		"settings": string(settingsJSON),
		"sequence": sequence,
	})
	if err != nil {
		return fmt.Errorf("marshal update card settings body: %w", err)
	}
	endpoint := "/open-apis/cardkit/v1/cards/" + cardEntityID + "/settings"
	cmd, cleanup, err := larkAPICommandWithJSONData(ctx, "PATCH", endpoint, "bot", body)
	if err != nil {
		return err
	}
	defer cleanup()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(decodeCommandOutput(output)))
	}
	return nil
}

func larkAPICommandWithJSONData(ctx context.Context, method string, endpoint string, as string, body []byte) (*exec.Cmd, func(), error) {
	dataArg := string(body)
	cleanup := func() {}
	if goruntime.GOOS == "windows" || len(dataArg) > 6000 {
		dataArg = "-"
	}
	cmd := commandContextWithEnv(ctx, "lark-cli", "api",
		method, endpoint,
		"--as", as,
		"--data", dataArg,
	)
	if dataArg == "-" {
		cmd.Stdin = bytes.NewReader(body)
	}
	return cmd, cleanup, nil
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
