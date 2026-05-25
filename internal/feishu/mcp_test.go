package feishu

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadMCPServersFromTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `
[mcp_servers.context7]
type = "stdio"
command = "npx"
args = ["-y", "@upstash/context7-mcp@latest"]
env = { FOO = "bar", BAZ = "qux" }

[mcp_servers.node_repl]
command = "/Applications/Codex.app/Contents/Resources/node_repl"
args = []

[mcp_servers.node_repl.env]
CODEX_HOME = "/Users/test/.codex"
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	servers := readMCPServersFromTOML(path)
	if len(servers) != 2 {
		t.Fatalf("servers len = %d, want 2: %#v", len(servers), servers)
	}
	if servers[0].Name != "context7" || servers[0].Command != "npx" || len(servers[0].Args) != 2 {
		t.Fatalf("unexpected context7 server: %#v", servers[0])
	}
	if servers[0].SourceClient == "" {
		t.Fatalf("source client should be populated")
	}
	if servers[0].Env["FOO"] != "bar" {
		t.Fatalf("inline env not parsed: %#v", servers[0].Env)
	}
	if servers[1].Name != "node_repl" || servers[1].Env["CODEX_HOME"] == "" {
		t.Fatalf("nested env not parsed: %#v", servers[1])
	}
}

func TestValidateMCPJSONConfig(t *testing.T) {
	servers, err := ValidateMCPJSONConfig("/tmp/mcp.json", []byte(`{
		"mcpServers": {
			"context7": {
				"command": "npx",
				"args": ["-y", "@upstash/context7-mcp@latest"]
			}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].Name != "context7" {
		t.Fatalf("servers = %#v", servers)
	}
}

func TestValidateMCPJSONConfigRejectsInvalidJSON(t *testing.T) {
	if _, err := ValidateMCPJSONConfig("/tmp/mcp.json", []byte(`{"mcpServers":`)); err == nil {
		t.Fatal("expected invalid JSON error")
	}
}
