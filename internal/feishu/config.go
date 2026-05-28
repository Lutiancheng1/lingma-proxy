package feishu

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	DefaultBrand         = "feishu"
	DefaultModel         = "kmodel"
	DefaultMaxToolRounds = 24
	legacyMaxToolRounds  = 5
)

type Config struct {
	Enabled        bool              `json:"enabled"`
	AutoStart      bool              `json:"autoStart"`
	Brand          string            `json:"brand"`
	Model          string            `json:"model"`
	BotName        string            `json:"botName"`
	BotIdentity    string            `json:"botIdentity"`
	MCPEnabled     bool              `json:"mcpEnabled"`
	MCPServers     []MCPServerConfig `json:"mcpServers,omitempty"`
	Context        ContextConfig     `json:"context,omitempty"`
	SafeFiles      SafeFilesConfig   `json:"safeFiles,omitempty"`
	MaxToolRounds  int               `json:"maxToolRounds"`
	GroupOnlyAtBot bool              `json:"groupOnlyAtBot"`
}

type SafeFilesConfig struct {
	Configured   bool                 `json:"configured,omitempty"`
	Enabled      bool                 `json:"enabled"`
	WorkspaceDir string               `json:"workspaceDir"`
	ExtraPaths   []SafeFilePathConfig `json:"extraPaths,omitempty"`
}

type SafeFilePathConfig struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
}

func DefaultConfig() Config {
	return Config{
		Enabled:        false,
		AutoStart:      false,
		Brand:          DefaultBrand,
		Model:          DefaultModel,
		Context:        DefaultContextConfig(),
		SafeFiles:      DefaultSafeFilesConfig(),
		MaxToolRounds:  DefaultMaxToolRounds,
		GroupOnlyAtBot: true,
	}
}

func NormalizeConfig(cfg Config) Config {
	if strings.TrimSpace(cfg.Brand) == "" {
		cfg.Brand = DefaultBrand
	}
	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = DefaultModel
	}
	cfg.BotName = limitBotName(cfg.BotName)
	cfg.BotIdentity = limitBotIdentity(cfg.BotIdentity)
	cfg.MCPServers = normalizeMCPServerConfigs(cfg.MCPServers)
	cfg.Context = normalizeContextConfig(cfg.Context)
	cfg.SafeFiles = normalizeSafeFilesConfig(cfg.SafeFiles)
	if cfg.MaxToolRounds <= 0 || cfg.MaxToolRounds == legacyMaxToolRounds {
		cfg.MaxToolRounds = DefaultMaxToolRounds
	}
	return cfg
}

func DefaultSafeFilesConfig() SafeFilesConfig {
	return SafeFilesConfig{
		Configured:   true,
		Enabled:      true,
		WorkspaceDir: defaultSafeFilesWorkspaceDir(),
	}
}

func normalizeSafeFilesConfig(cfg SafeFilesConfig) SafeFilesConfig {
	if !cfg.Configured && strings.TrimSpace(cfg.WorkspaceDir) == "" && !cfg.Enabled && len(cfg.ExtraPaths) == 0 {
		cfg.Enabled = true
	}
	if strings.TrimSpace(cfg.WorkspaceDir) == "" {
		cfg.WorkspaceDir = defaultSafeFilesWorkspaceDir()
	}
	cfg.WorkspaceDir = filepath.Clean(strings.TrimSpace(cfg.WorkspaceDir))
	out := SafeFilesConfig{
		Configured:   true,
		Enabled:      cfg.Enabled,
		WorkspaceDir: cfg.WorkspaceDir,
	}
	seen := map[string]struct{}{}
	for _, item := range cfg.ExtraPaths {
		path := filepath.Clean(strings.TrimSpace(item.Path))
		if path == "" || path == "." {
			continue
		}
		mode := normalizeSafeFileMode(item.Mode)
		key := strings.ToLower(path) + "\x00" + mode
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out.ExtraPaths = append(out.ExtraPaths, SafeFilePathConfig{Path: path, Mode: mode})
	}
	return out
}

func normalizeSafeFileMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "write", "read_write", "read-write":
		return "write"
	case "delete", "read_write_delete", "read-write-delete":
		return "delete"
	default:
		return "read"
	}
}

func defaultSafeFilesWorkspaceDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(configDir) == "" {
		if home, homeErr := os.UserHomeDir(); homeErr == nil {
			configDir = filepath.Join(home, ".config")
		} else {
			configDir = os.TempDir()
		}
	}
	return filepath.Join(configDir, "lingma-proxy", "workspace")
}

func limitBotName(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= 40 {
		return value
	}
	return strings.TrimSpace(string(runes[:40]))
}

func limitBotIdentity(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= 2000 {
		return value
	}
	return strings.TrimSpace(string(runes[:2000]))
}
