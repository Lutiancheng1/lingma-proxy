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
)

type desktopConfigEnvelope struct {
	ProxyConfig  *proxyConfigFile `json:"proxyConfig,omitempty"`
	FeishuBridge *feishu.Config   `json:"feishuBridge,omitempty"`
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

func loadDesktopConfig() (service.Config, feishu.Config) {
	cfg := defaultConfig()
	bridgeCfg := feishu.DefaultConfig()

	for _, configPath := range configSearchPaths() {
		info, err := os.Stat(configPath)
		if err != nil || info.IsDir() {
			continue
		}
		data, err := os.ReadFile(configPath)
		if err != nil {
			continue
		}
		nextProxy, nextBridge, ok := parseDesktopConfig(data, cfg, bridgeCfg)
		if !ok {
			continue
		}
		cfg = nextProxy
		bridgeCfg = nextBridge
		break
	}
	return cfg, bridgeCfg
}

func parseDesktopConfig(data []byte, baseProxy service.Config, baseBridge feishu.Config) (service.Config, feishu.Config, bool) {
	cfg := baseProxy
	bridgeCfg := baseBridge

	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return cfg, bridgeCfg, false
	}
	if rawProxy, ok := probe["proxyConfig"]; ok {
		var proxyFile proxyConfigFile
		if err := json.Unmarshal(rawProxy, &proxyFile); err == nil {
			applyProxyConfigFile(&cfg, proxyFile)
		}
		if rawBridge, ok := probe["feishuBridge"]; ok {
			var savedBridge feishu.Config
			if err := json.Unmarshal(rawBridge, &savedBridge); err == nil {
				bridgeCfg = feishu.NormalizeConfig(savedBridge)
			}
		}
		return cfg, bridgeCfg, true
	}

	var legacy proxyConfigFile
	if err := json.Unmarshal(data, &legacy); err != nil {
		return cfg, bridgeCfg, false
	}
	applyProxyConfigFile(&cfg, legacy)
	return cfg, bridgeCfg, true
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

func (a *App) saveDesktopConfig(proxyCfg service.Config, bridgeCfg feishu.Config) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".config", "lingma-proxy")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	proxyFile := buildProxyConfigFile(proxyCfg)
	normalizedBridge := feishu.NormalizeConfig(bridgeCfg)
	envelope := desktopConfigEnvelope{
		ProxyConfig:  &proxyFile,
		FeishuBridge: &normalizedBridge,
	}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), data, 0644)
}

func (a *App) saveConfig(cfg service.Config) error {
	a.mu.RLock()
	bridgeCfg := a.bridgeCfg
	a.mu.RUnlock()
	return a.saveDesktopConfig(cfg, bridgeCfg)
}

func (a *App) GetFeishuBridgeConfig() feishu.Config {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.bridgeCfg
}

func (a *App) UpdateFeishuBridgeConfig(cfg feishu.Config) error {
	cfg = feishu.NormalizeConfig(cfg)
	a.mu.Lock()
	a.bridgeCfg = cfg
	if a.bridge != nil {
		a.bridge.SetConfig(cfg)
	}
	proxyCfg := a.cfg
	a.mu.Unlock()
	if err := a.saveDesktopConfig(proxyCfg, cfg); err != nil {
		return err
	}
	return nil
}

func (a *App) GetFeishuBridgeStatus() feishu.Status {
	if a.bridge == nil {
		manager := feishu.NewManager(feishu.ManagerOptions{})
		manager.SetConfig(a.GetFeishuBridgeConfig())
		return manager.Status()
	}
	a.bridge.SetConfig(a.GetFeishuBridgeConfig())
	return a.bridge.Status()
}

func (a *App) RefreshFeishuBridgeStatus() feishu.Status {
	if a.bridge == nil {
		manager := feishu.NewManager(feishu.ManagerOptions{})
		manager.SetConfig(a.GetFeishuBridgeConfig())
		status := manager.Probe(context.Background())
		a.logFeishuProbeStatus(status)
		return status
	}
	a.bridge.SetConfig(a.GetFeishuBridgeConfig())
	status := a.bridge.Probe(context.Background())
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
		a.emitLogWithSource("feishu-bridge", "info", message)
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
	if a.bridge == nil {
		return fmt.Errorf("feishu bridge manager not initialized")
	}
	return a.bridge.InstallCLI(context.Background())
}

func (a *App) StartFeishuCLISetupNew() error {
	if a.bridge == nil {
		return fmt.Errorf("feishu bridge manager not initialized")
	}
	return a.bridge.StartSetupNew(context.Background())
}

func (a *App) StartFeishuCLILogin() error {
	if a.bridge == nil {
		return fmt.Errorf("feishu bridge manager not initialized")
	}
	return a.bridge.StartLogin(context.Background())
}

func (a *App) StartFeishuBridge() error {
	if a.bridge == nil {
		return fmt.Errorf("feishu bridge manager not initialized")
	}
	a.bridge.SetConfig(a.GetFeishuBridgeConfig())
	return a.bridge.Start(context.Background())
}

func (a *App) StopFeishuBridge() error {
	if a.bridge == nil {
		return nil
	}
	return a.bridge.Stop()
}
