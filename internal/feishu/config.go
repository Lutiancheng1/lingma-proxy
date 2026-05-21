package feishu

import "strings"

const (
	DefaultBrand         = "feishu"
	DefaultModel         = "kmodel"
	DefaultMaxToolRounds = 5
)

type Config struct {
	Enabled       bool   `json:"enabled"`
	AutoStart     bool   `json:"autoStart"`
	Brand         string `json:"brand"`
	Model         string `json:"model"`
	MaxToolRounds int    `json:"maxToolRounds"`
}

func DefaultConfig() Config {
	return Config{
		Enabled:       false,
		AutoStart:     false,
		Brand:         DefaultBrand,
		Model:         DefaultModel,
		MaxToolRounds: DefaultMaxToolRounds,
	}
}

func NormalizeConfig(cfg Config) Config {
	if strings.TrimSpace(cfg.Brand) == "" {
		cfg.Brand = DefaultBrand
	}
	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = DefaultModel
	}
	if cfg.MaxToolRounds <= 0 {
		cfg.MaxToolRounds = DefaultMaxToolRounds
	}
	return cfg
}
