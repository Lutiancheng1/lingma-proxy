package feishu

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
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

func TestMCPListToolsPaginates(t *testing.T) {
	client, cleanup := newTestMCPClient(t, func(req mcpRPCMessage) mcpRPCMessage {
		var params map[string]any
		if raw, ok := req.Params.(map[string]any); ok {
			params = raw
		}
		cursor, _ := params["cursor"].(string)
		result := map[string]any{}
		if cursor == "" {
			result = map[string]any{
				"tools":      []map[string]any{{"name": "first", "description": "page one", "inputSchema": map[string]any{"type": "object"}}},
				"nextCursor": "page-2",
			}
		} else {
			result = map[string]any{
				"tools": []map[string]any{{"name": "second", "description": "page two", "inputSchema": map[string]any{"type": "object"}}},
			}
		}
		data, _ := json.Marshal(result)
		return mcpRPCMessage{JSONRPC: "2.0", ID: req.ID, Result: data}
	})
	defer cleanup()
	tools, err := client.listTools(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Name != "first" || tools[1].Name != "second" {
		t.Fatalf("unexpected tools: %#v", tools)
	}
}

func TestMCPStringifyPreservesIsErrorAndStructuredContent(t *testing.T) {
	result := stringifyMCPResult(map[string]any{
		"isError": true,
		"content": []any{map[string]any{
			"type": "text",
			"text": "tool failed",
		}},
		"structuredContent": map[string]any{"code": "bad_input"},
	})
	if !result.IsError {
		t.Fatal("MCP isError should be preserved")
	}
	if result.Text == "" || !containsAll(result.Text, "tool failed", "structuredContent", "bad_input") {
		t.Fatalf("structured/text content not preserved: %#v", result)
	}
}

func newTestMCPClient(t *testing.T, handler func(mcpRPCMessage) mcpRPCMessage) (*mcpClient, func()) {
	t.Helper()
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer stdoutWriter.Close()
		scanner := bufio.NewScanner(stdinReader)
		for scanner.Scan() {
			var req mcpRPCMessage
			if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
				continue
			}
			resp := handler(req)
			data, _ := json.Marshal(resp)
			_, _ = stdoutWriter.Write(append(data, '\n'))
		}
	}()
	client := &mcpClient{
		stdin:  stdinWriter,
		stdout: bufio.NewReader(stdoutReader),
		close: func() {
			_ = stdinWriter.Close()
			_ = stdinReader.Close()
			_ = stdoutReader.Close()
			<-done
		},
	}
	return client, client.close
}

func containsAll(text string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(text, needle) {
			return false
		}
	}
	return true
}
