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

func TestMCPListResourcesPaginates(t *testing.T) {
	client, cleanup := newTestMCPClient(t, func(req mcpRPCMessage) mcpRPCMessage {
		if req.Method == "tools/list" {
			data, _ := json.Marshal(map[string]any{"tools": []map[string]any{}})
			return mcpRPCMessage{JSONRPC: "2.0", ID: req.ID, Result: data}
		}
		var params map[string]any
		if raw, ok := req.Params.(map[string]any); ok {
			params = raw
		}
		cursor, _ := params["cursor"].(string)
		result := map[string]any{}
		if cursor == "" {
			result = map[string]any{
				"resources":  []map[string]any{{"uri": "file:///a.txt", "name": "a.txt", "mimeType": "text/plain"}},
				"nextCursor": "p2",
			}
		} else {
			result = map[string]any{
				"resources": []map[string]any{{"uri": "file:///b.txt", "name": "b.txt", "mimeType": "text/plain"}},
			}
		}
		data, _ := json.Marshal(result)
		return mcpRPCMessage{JSONRPC: "2.0", ID: req.ID, Result: data}
	})
	defer cleanup()
	resources, _, err := client.listResources(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 2 || resources[0].URI != "file:///a.txt" || resources[1].URI != "file:///b.txt" {
		t.Fatalf("unexpected resources: %#v", resources)
	}
}

func TestMCPListPromptsPaginates(t *testing.T) {
	client, cleanup := newTestMCPClient(t, func(req mcpRPCMessage) mcpRPCMessage {
		if req.Method == "tools/list" {
			data, _ := json.Marshal(map[string]any{"tools": []map[string]any{}})
			return mcpRPCMessage{JSONRPC: "2.0", ID: req.ID, Result: data}
		}
		var params map[string]any
		if raw, ok := req.Params.(map[string]any); ok {
			params = raw
		}
		cursor, _ := params["cursor"].(string)
		result := map[string]any{}
		if cursor == "" {
			result = map[string]any{
				"prompts":    []map[string]any{{"name": "review", "description": "code review", "arguments": []map[string]any{{"name": "code", "required": true}}}},
				"nextCursor": "p2",
			}
		} else {
			result = map[string]any{
				"prompts": []map[string]any{{"name": "explain", "description": "explain code"}},
			}
		}
		data, _ := json.Marshal(result)
		return mcpRPCMessage{JSONRPC: "2.0", ID: req.ID, Result: data}
	})
	defer cleanup()
	prompts, err := client.listPrompts(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 2 || prompts[0].Name != "review" || prompts[1].Name != "explain" {
		t.Fatalf("unexpected prompts: %#v", prompts)
	}
	if len(prompts[0].Arguments) != 1 || prompts[0].Arguments[0].Name != "code" {
		t.Fatalf("unexpected arguments: %#v", prompts[0].Arguments)
	}
}

func TestMCPReadResource(t *testing.T) {
	client, cleanup := newTestMCPClient(t, func(req mcpRPCMessage) mcpRPCMessage {
		if req.Method == "tools/list" {
			data, _ := json.Marshal(map[string]any{"tools": []map[string]any{}})
			return mcpRPCMessage{JSONRPC: "2.0", ID: req.ID, Result: data}
		}
		result := map[string]any{
			"contents": []map[string]any{{"uri": "file:///a.txt", "mimeType": "text/plain", "text": "hello world"}},
		}
		data, _ := json.Marshal(result)
		return mcpRPCMessage{JSONRPC: "2.0", ID: req.ID, Result: data}
	})
	defer cleanup()
	text, err := client.readResource(context.Background(), "file:///a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if text != "hello world" {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestMCPGetPrompt(t *testing.T) {
	client, cleanup := newTestMCPClient(t, func(req mcpRPCMessage) mcpRPCMessage {
		if req.Method == "tools/list" {
			data, _ := json.Marshal(map[string]any{"tools": []map[string]any{}})
			return mcpRPCMessage{JSONRPC: "2.0", ID: req.ID, Result: data}
		}
		result := map[string]any{
			"description": "Code review prompt",
			"messages": []map[string]any{
				{"role": "user", "content": map[string]any{"type": "text", "text": "Please review this code."}},
			},
		}
		data, _ := json.Marshal(result)
		return mcpRPCMessage{JSONRPC: "2.0", ID: req.ID, Result: data}
	})
	defer cleanup()
	text, err := client.getPrompt(context.Background(), "review", map[string]any{"code": "fmt.Println()"})
	if err != nil {
		t.Fatal(err)
	}
	if !containsAll(text, "Code review prompt", "Please review") {
		t.Fatalf("unexpected prompt text: %q", text)
	}
}

func TestMCPNotificationRouting(t *testing.T) {
	// Use custom pipes so we can inject notifications before the response.
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	go func() {
		defer stdoutWriter.Close()
		scanner := bufio.NewScanner(stdinReader)
		for scanner.Scan() {
			var req mcpRPCMessage
			if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
				continue
			}
			// Send notifications before the actual response.
			notifications := []mcpRPCMessage{
				{JSONRPC: "2.0", Method: "notifications/tools/list_changed"},
				{JSONRPC: "2.0", Method: "notifications/resources/list_changed"},
				{JSONRPC: "2.0", Method: "notifications/resources/updated"},
				{JSONRPC: "2.0", Method: "notifications/prompts/list_changed"},
			}
			for _, n := range notifications {
				data, _ := json.Marshal(n)
				_, _ = stdoutWriter.Write(append(data, '\n'))
			}
			// Then send the actual response.
			data, _ := json.Marshal(map[string]any{"tools": []map[string]any{}})
			resp := mcpRPCMessage{JSONRPC: "2.0", ID: req.ID, Result: data}
			respData, _ := json.Marshal(resp)
			_, _ = stdoutWriter.Write(append(respData, '\n'))
		}
	}()
	client := &mcpClient{
		stdin:  stdinWriter,
		stdout: bufio.NewReader(stdoutReader),
		close: func() {
			_ = stdinWriter.Close()
			_ = stdinReader.Close()
			_ = stdoutReader.Close()
		},
	}
	defer client.close()

	_, err := client.listTools(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if !client.toolsChanged.Load() {
		t.Fatal("toolsChanged should be set")
	}
	if !client.resourcesChanged.Load() {
		t.Fatal("resourcesChanged should be set")
	}
	if !client.promptsChanged.Load() {
		t.Fatal("promptsChanged should be set")
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
