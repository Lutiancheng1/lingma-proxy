package feishu

import (
	"strings"
	"testing"
)

func TestNormalizeConfigMigratesLegacyToolRounds(t *testing.T) {
	cfg := NormalizeConfig(Config{
		Brand:         "feishu",
		Model:         "kmodel",
		MaxToolRounds: 5,
	})
	if cfg.MaxToolRounds != DefaultMaxToolRounds {
		t.Fatalf("legacy max tool rounds should migrate to default, got %d", cfg.MaxToolRounds)
	}
}

func TestNormalizeConfigPreservesExplicitNonLegacyToolRounds(t *testing.T) {
	cfg := NormalizeConfig(Config{
		Brand:         "feishu",
		Model:         "kmodel",
		MaxToolRounds: 12,
	})
	if cfg.MaxToolRounds != 12 {
		t.Fatalf("explicit non-legacy max tool rounds should be preserved, got %d", cfg.MaxToolRounds)
	}
}

func TestNormalizeConfigTrimsAndLimitsBotIdentity(t *testing.T) {
	cfg := NormalizeConfig(Config{
		Brand:       "feishu",
		Model:       "kmodel",
		BotIdentity: "  " + strings.Repeat("你是研发效能助手。", 300) + "  ",
	})
	if len([]rune(cfg.BotIdentity)) > 2000 {
		t.Fatalf("bot identity should be limited, got len=%d", len([]rune(cfg.BotIdentity)))
	}
	if strings.HasPrefix(cfg.BotIdentity, " ") || strings.HasSuffix(cfg.BotIdentity, " ") {
		t.Fatalf("bot identity should be trimmed: %q", cfg.BotIdentity[:20])
	}
}

func TestNormalizeConfigTrimsAndLimitsBotName(t *testing.T) {
	cfg := NormalizeConfig(Config{BotName: "  " + strings.Repeat("飞书助手", 20) + "  "})
	if len([]rune(cfg.BotName)) > 40 {
		t.Fatalf("bot name should be limited, got len=%d", len([]rune(cfg.BotName)))
	}
	if strings.HasPrefix(cfg.BotName, " ") || strings.HasSuffix(cfg.BotName, " ") {
		t.Fatalf("bot name should be trimmed: %q", cfg.BotName)
	}
}

func TestNormalizeConfigDedupesMCPServers(t *testing.T) {
	cfg := NormalizeConfig(Config{
		Brand: "feishu",
		Model: "kmodel",
		MCPServers: []MCPServerConfig{
			{Name: "playwright", Command: "npx", Args: []string{"-y", "playwright-mcp"}, Enabled: false},
			{Name: "Playwright", Command: "npx", Args: []string{"-y", "other"}},
			{Name: "browser", Command: "npx", Args: []string{"-y", "playwright-mcp"}, Enabled: true},
		},
	})
	if len(cfg.MCPServers) != 1 {
		t.Fatalf("expected duplicate MCP servers to collapse to 1, got %#v", cfg.MCPServers)
	}
	if !cfg.MCPServers[0].Enabled {
		t.Fatal("enabled duplicate MCP server config should be preserved")
	}
}
