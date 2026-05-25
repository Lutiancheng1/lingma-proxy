package feishu

import "strings"

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
	BotIdentity    string            `json:"botIdentity"`
	MCPEnabled     bool              `json:"mcpEnabled"`
	MCPServers     []MCPServerConfig `json:"mcpServers,omitempty"`
	MaxToolRounds  int               `json:"maxToolRounds"`
	GroupOnlyAtBot bool              `json:"groupOnlyAtBot"`
}

func DefaultConfig() Config {
	return Config{
		Enabled:        false,
		AutoStart:      false,
		Brand:          DefaultBrand,
		Model:          DefaultModel,
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
	cfg.BotIdentity = limitBotIdentity(cfg.BotIdentity)
	cfg.MCPServers = normalizeMCPServerConfigs(cfg.MCPServers)
	if cfg.MaxToolRounds <= 0 || cfg.MaxToolRounds == legacyMaxToolRounds {
		cfg.MaxToolRounds = DefaultMaxToolRounds
	}
	return cfg
}

func limitBotIdentity(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= 2000 {
		return value
	}
	return strings.TrimSpace(string(runes[:2000]))
}
