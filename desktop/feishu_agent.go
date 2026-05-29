package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lingma-ipc-proxy/internal/feishu"
	"lingma-ipc-proxy/internal/lingmaipc"
	"lingma-ipc-proxy/internal/service"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type desktopConfigEnvelope struct {
	ProxyConfig  *proxyConfigFile `json:"proxyConfig,omitempty"`
	FeishuAgent *feishu.Config   `json:"feishuAgent,omitempty"`
}

type proxyConfigFile struct {
	Host                  string   `json:"host"`
	Port                  int      `json:"port"`
	Backend               string   `json:"backend"`
	Transport             string   `json:"transport"`
	Pipe                  string   `json:"pipe"`
	WebSocketURL          string   `json:"websocket_url"`
	RemoteBaseURL         string   `json:"remote_base_url"`
	RemoteAuthFile        string   `json:"remote_auth_file"`
	RemoteProxyURL        string   `json:"remote_proxy_url"`
	RemoteVersion         string   `json:"remote_version"`
	Cwd                   string   `json:"cwd"`
	CurrentFilePath       string   `json:"current_file_path"`
	Mode                  string   `json:"mode"`
	Model                 string   `json:"model"`
	ShellType             string   `json:"shell_type"`
	SessionMode           string   `json:"session_mode"`
	TimeoutSeconds        int      `json:"timeout"`
	WarmupTimeoutSeconds  int      `json:"warmup_timeout"`
	RemoteFallbackEnabled *bool    `json:"remote_fallback_enabled"`
	RemoteFallbackModels  []string `json:"remote_fallback_models"`
}

type MCPJSONFile struct {
	Path        string `json:"path"`
	Content     string `json:"content"`
	ServerCount int    `json:"serverCount"`
}

type FeishuAgentSkillImportResult = feishu.AgentSkillImportResult
type FeishuAgentSkill = feishu.AgentSkill

type FeishuAgentCleanupOptions struct {
	IncludeImportedSkills bool `json:"includeImportedSkills"`
	IncludeMCPConfig      bool `json:"includeMcpConfig"`
}

func loadDesktopConfig() (service.Config, feishu.Config) {
	cfg := defaultConfig()
	agentCfg := feishu.DefaultConfig()

	for _, configPath := range configSearchPaths() {
		info, err := os.Stat(configPath)
		if err != nil || info.IsDir() {
			continue
		}
		data, err := os.ReadFile(configPath)
		if err != nil {
			continue
		}
		nextProxy, nextAgent, ok := parseDesktopConfig(data, cfg, agentCfg)
		if !ok {
			continue
		}
		cfg = nextProxy
		agentCfg = nextAgent
		break
	}
	return cfg, agentCfg
}

func parseDesktopConfig(data []byte, baseProxy service.Config, baseAgent feishu.Config) (service.Config, feishu.Config, bool) {
	cfg := baseProxy
	agentCfg := baseAgent

	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return cfg, agentCfg, false
	}
	if rawProxy, ok := probe["proxyConfig"]; ok {
		var proxyFile proxyConfigFile
		if err := json.Unmarshal(rawProxy, &proxyFile); err == nil {
			applyProxyConfigFile(&cfg, proxyFile)
		}
		rawAgent, ok := probe["feishuAgent"]
		if !ok {
			rawAgent, ok = probe["feishuBridge"] // backward compat
		}
		if ok {
			savedAgent := baseAgent
			if err := json.Unmarshal(rawAgent, &savedAgent); err == nil {
				agentCfg = feishu.NormalizeConfig(savedAgent)
			}
		}
		return cfg, agentCfg, true
	}

	var legacy proxyConfigFile
	if err := json.Unmarshal(data, &legacy); err != nil {
		return cfg, agentCfg, false
	}
	applyProxyConfigFile(&cfg, legacy)
	return cfg, agentCfg, true
}

func loadSavedFeishuAgentConfig(baseAgent feishu.Config) (feishu.Config, bool) {
	dir, err := lingmaProxyConfigDir()
	if err != nil {
		return baseAgent, false
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return baseAgent, false
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return baseAgent, false
	}
	rawAgent, ok := probe["feishuAgent"]
	if !ok {
		rawAgent, ok = probe["feishuBridge"] // backward compat
	}
	if !ok {
		return baseAgent, false
	}
	savedAgent := baseAgent
	if err := json.Unmarshal(rawAgent, &savedAgent); err != nil {
		return baseAgent, false
	}
	return feishu.NormalizeConfig(savedAgent), true
}

func applyProxyConfigFile(cfg *service.Config, fileCfg proxyConfigFile) {
	if fileCfg.Host != "" {
		cfg.Host = fileCfg.Host
	}
	if fileCfg.Port > 0 {
		cfg.Port = fileCfg.Port
	}
	if fileCfg.Backend != "" {
		cfg.Backend = service.BackendMode(fileCfg.Backend)
	}
	if fileCfg.Transport != "" {
		if t, err := lingmaipc.ParseTransport(fileCfg.Transport); err == nil {
			cfg.Transport = t
		}
	}
	if fileCfg.Pipe != "" {
		cfg.Pipe = fileCfg.Pipe
	}
	if fileCfg.WebSocketURL != "" {
		cfg.WebSocketURL = fileCfg.WebSocketURL
	}
	if fileCfg.RemoteBaseURL != "" {
		cfg.RemoteBaseURL = fileCfg.RemoteBaseURL
	}
	if fileCfg.RemoteAuthFile != "" {
		cfg.RemoteAuthFile = fileCfg.RemoteAuthFile
	}
	if fileCfg.RemoteProxyURL != "" {
		cfg.RemoteProxyURL = fileCfg.RemoteProxyURL
	}
	if fileCfg.RemoteVersion != "" {
		cfg.RemoteVersion = fileCfg.RemoteVersion
	}
	if fileCfg.Cwd != "" {
		cfg.Cwd = fileCfg.Cwd
	}
	if fileCfg.CurrentFilePath != "" {
		cfg.CurrentFilePath = fileCfg.CurrentFilePath
	}
	if fileCfg.Mode != "" {
		cfg.Mode = fileCfg.Mode
	}
	if fileCfg.Model != "" {
		cfg.Model = fileCfg.Model
	}
	if fileCfg.ShellType != "" {
		cfg.ShellType = fileCfg.ShellType
	}
	if fileCfg.SessionMode != "" {
		cfg.SessionMode = service.SessionMode(fileCfg.SessionMode)
	}
	if fileCfg.TimeoutSeconds >= 0 {
		cfg.Timeout = time.Duration(fileCfg.TimeoutSeconds) * time.Second
	}
	if fileCfg.WarmupTimeoutSeconds > 0 {
		cfg.WarmupTimeout = time.Duration(fileCfg.WarmupTimeoutSeconds) * time.Second
	}
	if fileCfg.RemoteFallbackEnabled != nil {
		cfg.RemoteFallbackEnabled = *fileCfg.RemoteFallbackEnabled
	}
	if len(fileCfg.RemoteFallbackModels) > 0 {
		cfg.RemoteFallbackModels = cleanConfigStrings(fileCfg.RemoteFallbackModels)
	}
}

func buildProxyConfigFile(cfg service.Config) proxyConfigFile {
	return proxyConfigFile{
		Host:                  cfg.Host,
		Port:                  cfg.Port,
		Backend:               string(cfg.Backend),
		Transport:             string(cfg.Transport),
		Pipe:                  cfg.Pipe,
		WebSocketURL:          cfg.WebSocketURL,
		RemoteBaseURL:         cfg.RemoteBaseURL,
		RemoteAuthFile:        cfg.RemoteAuthFile,
		RemoteProxyURL:        cfg.RemoteProxyURL,
		RemoteVersion:         cfg.RemoteVersion,
		Cwd:                   cfg.Cwd,
		CurrentFilePath:       cfg.CurrentFilePath,
		Mode:                  cfg.Mode,
		Model:                 cfg.Model,
		ShellType:             cfg.ShellType,
		SessionMode:           string(cfg.SessionMode),
		TimeoutSeconds:        int(cfg.Timeout.Seconds()),
		WarmupTimeoutSeconds:  int(cfg.WarmupTimeout.Seconds()),
		RemoteFallbackEnabled: &cfg.RemoteFallbackEnabled,
		RemoteFallbackModels:  cfg.RemoteFallbackModels,
	}
}

func (a *App) saveDesktopConfig(proxyCfg service.Config, agentCfg feishu.Config) error {
	dir, err := lingmaProxyConfigDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	proxyFile := buildProxyConfigFile(proxyCfg)
	normalizedAgent := feishu.NormalizeConfig(agentCfg)
	envelope := desktopConfigEnvelope{
		ProxyConfig:  &proxyFile,
		FeishuAgent: &normalizedAgent,
	}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), data, 0644)
}

func lingmaProxyConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "lingma-proxy"), nil
}

func customMCPConfigFilePath() (string, error) {
	dir, err := lingmaProxyConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "mcp.json"), nil
}

func (a *App) GetFeishuAgentMCPJSON() (MCPJSONFile, error) {
	path, err := customMCPConfigFilePath()
	if err != nil {
		return MCPJSONFile{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		data = []byte(defaultCustomMCPJSON())
	} else if err != nil {
		return MCPJSONFile{}, err
	}
	servers, validateErr := feishu.ValidateMCPJSONConfig(path, data)
	count := len(servers)
	result := MCPJSONFile{Path: path, Content: string(data), ServerCount: count}
	if validateErr != nil && strings.TrimSpace(string(data)) != strings.TrimSpace(defaultCustomMCPJSON()) {
		return result, validateErr
	}
	return result, nil
}

func (a *App) SaveFeishuAgentMCPJSON(content string) (MCPJSONFile, error) {
	path, err := customMCPConfigFilePath()
	if err != nil {
		return MCPJSONFile{}, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		content = defaultCustomMCPJSON()
	}
	servers, err := feishu.ValidateMCPJSONConfig(path, []byte(content))
	if err != nil {
		return MCPJSONFile{Path: path, Content: content}, err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return MCPJSONFile{}, err
	}
	var formatted any
	data := []byte(content)
	if err := json.Unmarshal(data, &formatted); err == nil {
		if pretty, err := json.MarshalIndent(formatted, "", "  "); err == nil {
			data = append(pretty, '\n')
			content = string(data)
		}
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return MCPJSONFile{}, err
	}
	feishu.SetCustomMCPConfigPath(path)
	if a.agent != nil {
		a.agent.SetConfig(a.GetFeishuAgentConfig())
		_ = a.agent.Probe(context.Background())
	}
	return MCPJSONFile{Path: path, Content: content, ServerCount: len(servers)}, nil
}

func defaultCustomMCPJSON() string {
	return `{
  "mcpServers": {
    "example": {
      "command": "npx",
      "args": ["-y", "your-mcp-server"]
    }
  }
}`
}

func (a *App) saveConfig(cfg service.Config) error {
	a.mu.RLock()
	agentCfg := a.agentCfg
	a.mu.RUnlock()
	if savedAgent, ok := loadSavedFeishuAgentConfig(agentCfg); ok {
		agentCfg = savedAgent
		a.mu.Lock()
		a.agentCfg = savedAgent
		if a.agent != nil {
			a.agent.SetConfig(savedAgent)
		}
		a.mu.Unlock()
	}
	return a.saveDesktopConfig(cfg, agentCfg)
}

func (a *App) GetFeishuAgentConfig() feishu.Config {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.agentCfg
}

func (a *App) UpdateFeishuAgentConfig(cfg feishu.Config) error {
	cfg = feishu.NormalizeConfig(cfg)
	a.mu.Lock()
	a.agentCfg = cfg
	if a.agent != nil {
		a.agent.SetConfig(cfg)
	}
	proxyCfg := a.cfg
	a.mu.Unlock()
	if err := a.saveDesktopConfig(proxyCfg, cfg); err != nil {
		return err
	}
	return nil
}

func (a *App) GetFeishuAgentStatus() feishu.Status {
	if a.agent == nil {
		manager := feishu.NewManager(feishu.ManagerOptions{})
		manager.SetConfig(a.GetFeishuAgentConfig())
		return manager.Status()
	}
	a.agent.SetConfig(a.GetFeishuAgentConfig())
	return a.agent.Status()
}

func (a *App) RefreshFeishuAgentStatus() feishu.Status {
	if a.agent == nil {
		manager := feishu.NewManager(feishu.ManagerOptions{})
		manager.SetConfig(a.GetFeishuAgentConfig())
		status := manager.RefreshProbe(context.Background())
		a.logFeishuProbeStatus(status)
		return status
	}
	a.agent.SetConfig(a.GetFeishuAgentConfig())
	status := a.agent.RefreshProbe(context.Background())
	a.logFeishuProbeStatus(status)
	return status
}

func (a *App) logFeishuProbeStatus(status feishu.Status) {
	message := fmt.Sprintf("Feishu probe paths: node=%s npm=%s npx=%s lark-cli=%s",
		displayBinaryPath(status.Node.Path),
		displayBinaryPath(status.NPM.Path),
		displayBinaryPath(status.NPX.Path),
		displayBinaryPath(status.CLI.Path),
	)
	now := time.Now()
	a.mu.Lock()
	shouldLog := message != a.lastFeishuProbeLog || now.Sub(a.lastFeishuProbeLogAt) >= 5*time.Minute
	if shouldLog {
		a.lastFeishuProbeLog = message
		a.lastFeishuProbeLogAt = now
	}
	a.mu.Unlock()
	if shouldLog {
		a.emitLogWithSource("feishu-agent", "info", message)
	}
}

func displayBinaryPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "<missing>"
	}
	return trimmed
}

func (a *App) InstallFeishuCLI() error {
	if a.agent == nil {
		return fmt.Errorf("feishu agent manager not initialized")
	}
	return a.agent.InstallCLI(context.Background())
}

func (a *App) ReinstallFeishuSkills() error {
	if a.agent == nil {
		return fmt.Errorf("feishu agent manager not initialized")
	}
	return a.agent.ReinstallSkills(context.Background())
}

func (a *App) CleanupFeishuAgentArtifacts(opts FeishuAgentCleanupOptions) ([]string, error) {
	if a.agent == nil {
		return nil, fmt.Errorf("feishu agent manager not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	results, err := a.agent.CleanupArtifacts(ctx, feishu.CleanupOptions{
		IncludeImportedSkills: opts.IncludeImportedSkills,
	})
	if opts.IncludeMCPConfig {
		if mcpResults, mcpErr := a.cleanupFeishuAgentMCPConfig(); mcpErr != nil {
			if err != nil {
				err = fmt.Errorf("%v; %w", err, mcpErr)
			} else {
				err = mcpErr
			}
		} else {
			results = append(results, mcpResults...)
		}
	}
	if err == nil {
		a.emitLogWithSource("feishu-agent", "info", "Feishu Agent CLI/Skills/授权信息已清理")
	}
	return results, err
}

func (a *App) cleanupFeishuAgentMCPConfig() ([]string, error) {
	path, err := customMCPConfigFilePath()
	if err != nil {
		return nil, err
	}
	removed := false
	if err := os.Remove(path); err == nil {
		removed = true
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("清理自定义 MCP JSON 失败：%w", err)
	}

	a.mu.Lock()
	a.agentCfg.MCPEnabled = false
	a.agentCfg.MCPServers = nil
	agentCfg := a.agentCfg
	proxyCfg := a.cfg
	if a.agent != nil {
		a.agent.SetConfig(agentCfg)
	}
	a.mu.Unlock()
	if err := a.saveDesktopConfig(proxyCfg, agentCfg); err != nil {
		return nil, err
	}
	feishu.SetCustomMCPConfigPath(path)
	if removed {
		return []string{"已清理自定义 MCP JSON，并关闭 MCP"}, nil
	}
	return []string{"未发现自定义 MCP JSON，已关闭 MCP"}, nil
}

func (a *App) StartFeishuCLISetupNew() error {
	if a.agent == nil {
		return fmt.Errorf("feishu agent manager not initialized")
	}
	return a.agent.StartSetupNew(context.Background())
}

func (a *App) StartFeishuCLILogin() error {
	if a.agent == nil {
		return fmt.Errorf("feishu agent manager not initialized")
	}
	return a.agent.StartLogin(context.Background())
}

func (a *App) StartFeishuAgent() error {
	if a.agent == nil {
		return fmt.Errorf("feishu agent manager not initialized")
	}
	a.agent.SetConfig(a.GetFeishuAgentConfig())
	return a.agent.Start(context.Background())
}

func (a *App) StopFeishuAgent() error {
	if a.agent == nil {
		return nil
	}
	return a.agent.Stop()
}

func (a *App) GetFeishuAgentSkills() []feishu.AgentSkill {
	if a.agent == nil {
		return nil
	}
	return a.agent.ListAgentSkills()
}

func (a *App) ChooseFeishuAgentSkillZip() (string, error) {
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择 Skill zip",
		Filters: []runtime.FileFilter{
			{DisplayName: "Skill zip (*.zip)", Pattern: "*.zip"},
		},
	})
}

func (a *App) ChooseFeishuAgentSkillFolder() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择包含 SKILL.md 的文件夹",
	})
}

func (a *App) ImportFeishuAgentSkillPath(path string) (feishu.AgentSkillImportResult, error) {
	if a.agent == nil {
		return feishu.AgentSkillImportResult{}, fmt.Errorf("feishu agent manager not initialized")
	}
	result, err := a.agent.ImportSkillPath(context.Background(), path)
	if err == nil {
		a.emitLogWithSource("feishu-agent", "info", fmt.Sprintf("Feishu Agent Skill 导入完成：%d 个", len(result.Imported)))
	}
	return result, err
}

func (a *App) ReloadFeishuAgentSkills() error {
	if a.agent == nil {
		return fmt.Errorf("feishu agent manager not initialized")
	}
	return a.agent.ReloadAgentSkills(context.Background())
}

func (a *App) SetFeishuAgentSkillEnabled(id string, enabled bool) error {
	if a.agent == nil {
		return fmt.Errorf("feishu agent manager not initialized")
	}
	return a.agent.SetAgentSkillEnabled(context.Background(), id, enabled)
}

func (a *App) DeleteFeishuAgentSkill(id string) error {
	if a.agent == nil {
		return fmt.Errorf("feishu agent manager not initialized")
	}
	return a.agent.DeleteAgentSkill(context.Background(), id)
}
