package feishu

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const mcpProtocolVersion = "2025-06-18"

type mcpTool struct {
	Server      string
	Name        string
	Function    string
	Description string
	InputSchema map[string]any
}

type mcpResource struct {
	Server      string
	URI         string
	Name        string
	Title       string
	Description string
	MimeType    string
}

type mcpResourceTemplate struct {
	Server      string
	URITemplate string
	Name        string
	Title       string
	Description string
	MimeType    string
}

type mcpPrompt struct {
	Server      string
	Name        string
	Title       string
	Description string
	Arguments   []mcpPromptArgument
}

type mcpPromptArgument struct {
	Name        string
	Description string
	Required    bool
}

type mcpSession struct {
	server              MCPServerConfig
	client              *mcpClient
	tools               []mcpTool
	resources           []mcpResource
	resourceTemplates   []mcpResourceTemplate
	prompts             []mcpPrompt
	available           bool
	message             string
	lastChecked         string
	supportsListChanged bool
	supportsResources   bool
	supportsPrompts     bool
}

type MCPRuntime struct {
	mu        sync.Mutex
	sessions  map[string]*mcpSession
	tools     map[string]mcpTool
	resources map[string]mcpResource // key: "server:uri"
	prompts   map[string]mcpPrompt   // key: "server:name"
	statuses  []MCPServerStatus
}

type mcpConfigFile struct {
	MCPServers json.RawMessage `json:"mcpServers"`
	Servers    json.RawMessage `json:"servers"`
	Context    json.RawMessage `json:"context_servers"`
}

type mcpServerFile struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

type MCPJSONValidation struct {
	ServerCount int    `json:"serverCount"`
	Error       string `json:"error,omitempty"`
}

type mcpRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  any             `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type mcpToolCallResult struct {
	Text    string
	IsError bool
}

type mcpClient struct {
	mu               sync.Mutex
	cmd              string
	args             []string
	env              map[string]string
	stdin            io.WriteCloser
	stdout           *bufio.Reader
	close            func()
	nextID           int64
	toolsChanged     atomic.Bool
	resourcesChanged atomic.Bool
	promptsChanged   atomic.Bool

	// Server metadata returned during the initialize handshake (MCP spec §Lifecycle).
	serverCapabilities map[string]any
	serverInfo         map[string]any
	serverProtocol     string
}

func NewMCPRuntime() *MCPRuntime {
	return &MCPRuntime{
		sessions:  map[string]*mcpSession{},
		tools:     map[string]mcpTool{},
		resources: map[string]mcpResource{},
		prompts:   map[string]mcpPrompt{},
	}
}

func (r *MCPRuntime) Sync(ctx context.Context, cfg Config) []MCPServerStatus {
	if r == nil {
		return probeMCPServers(ctx, cfg)
	}
	servers := discoverMCPServers(cfg)
	nextNames := map[string]bool{}
	statuses := make([]MCPServerStatus, 0, len(servers))
	toolMap := map[string]mcpTool{}
	resourceMap := map[string]mcpResource{}
	promptMap := map[string]mcpPrompt{}

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, server := range servers {
		nextNames[server.Name] = true
		status := mcpStatusFromConfig(server)
		if !cfg.MCPEnabled || !server.Enabled {
			status.Message = "未启用"
			if session := r.sessions[server.Name]; session != nil {
				if session.client != nil {
					session.client.close()
				}
				delete(r.sessions, server.Name)
			}
			statuses = append(statuses, status)
			continue
		}
		session := r.sessions[server.Name]
		if session == nil || mcpServerFingerprint(session.server) != mcpServerFingerprint(server) {
			if session != nil {
				if session.client != nil {
					session.client.close()
				}
			}
			session = &mcpSession{server: server}
			client, tools, err := connectMCPServer(ctx, server)
			session.lastChecked = timestampNow()
			if err != nil {
				session.available = false
				session.message = err.Error()
			} else {
				session.client = client
				session.tools = withMCPFunctionNames(server.Name, tools)
				session.available = true
				session.message = ""
				session.supportsListChanged = mcpBoolCapability(client.serverCapabilities, "tools", "listChanged")
				session.supportsResources = client.serverCapabilities != nil && client.serverCapabilities["resources"] != nil
				session.supportsPrompts = client.serverCapabilities != nil && client.serverCapabilities["prompts"] != nil
				if session.supportsResources {
					if resources, templates, err := client.listResources(ctx, server.Name); err == nil {
						session.resources = resources
						session.resourceTemplates = templates
					}
				}
				if session.supportsPrompts {
					if prompts, err := client.listPrompts(ctx, server.Name); err == nil {
						session.prompts = prompts
					}
				}
			}
			r.sessions[server.Name] = session
		} else {
			session.lastChecked = timestampNow()
			changed := session.client != nil && session.client.toolsChanged.Swap(false)
			if !changed && session.supportsListChanged && len(session.tools) > 0 {
				// Server supports listChanged and hasn't notified us — skip re-fetch.
				session.available = true
				session.message = ""
			} else {
				tools, err := session.listTools(ctx)
				if err != nil {
					session.available = false
					session.message = err.Error()
				} else {
					session.tools = withMCPFunctionNames(server.Name, tools)
					session.available = true
					session.message = ""
				}
			}
			// Refresh resources if server supports them and notified a change.
			if session.supportsResources && session.client != nil {
				resChanged := session.client.resourcesChanged.Swap(false)
				if resChanged || len(session.resources) == 0 {
					if resources, templates, err := session.client.listResources(ctx, server.Name); err == nil {
						session.resources = resources
						session.resourceTemplates = templates
					}
				}
			}
			// Refresh prompts if server supports them and notified a change.
			if session.supportsPrompts && session.client != nil {
				promptChanged := session.client.promptsChanged.Swap(false)
				if promptChanged || len(session.prompts) == 0 {
					if prompts, err := session.client.listPrompts(ctx, server.Name); err == nil {
						session.prompts = prompts
					}
				}
			}
		}
		status.Available = session.available
		status.Message = session.message
		status.ToolCount = len(session.tools)
		for _, tool := range session.tools {
			status.Tools = append(status.Tools, MCPToolStatus{Name: tool.Name, Function: tool.Function, Description: tool.Description})
			if tool.Function != "" {
				toolMap[tool.Function] = tool
			}
		}
		status.ResourceCount = len(session.resources)
		for _, res := range session.resources {
			status.Resources = append(status.Resources, MCPResourceStatus{URI: res.URI, Name: res.Name, Description: res.Description, MimeType: res.MimeType})
			resourceMap[res.URI] = res
		}
		status.PromptCount = len(session.prompts)
		for _, p := range session.prompts {
			status.Prompts = append(status.Prompts, MCPPromptStatus{Name: p.Name, Description: p.Description})
			promptMap[server.Name+":"+p.Name] = p
		}
		statuses = append(statuses, status)
	}
	for name, session := range r.sessions {
		if !nextNames[name] || !cfg.MCPEnabled {
			if session.client != nil {
				session.client.close()
			}
			delete(r.sessions, name)
		}
	}
	r.statuses = statuses
	r.tools = toolMap
	r.resources = resourceMap
	r.prompts = promptMap
	return cloneMCPStatuses(statuses)
}

func (r *MCPRuntime) Stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, session := range r.sessions {
		if session.client != nil {
			session.client.close()
		}
		delete(r.sessions, name)
	}
	r.tools = map[string]mcpTool{}
	r.resources = map[string]mcpResource{}
	r.prompts = map[string]mcpPrompt{}
}

func (r *MCPRuntime) Tools() []mcpTool {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]mcpTool, 0, len(r.tools))
	for _, tool := range r.tools {
		out = append(out, tool)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Server == out[j].Server {
			return out[i].Name < out[j].Name
		}
		return out[i].Server < out[j].Server
	})
	return out
}

func (r *MCPRuntime) Statuses() []MCPServerStatus {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneMCPStatuses(r.statuses)
}

func (r *MCPRuntime) Resources() []mcpResource {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]mcpResource, 0, len(r.resources))
	for _, res := range r.resources {
		out = append(out, res)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Server == out[j].Server {
			return out[i].URI < out[j].URI
		}
		return out[i].Server < out[j].Server
	})
	return out
}

func (r *MCPRuntime) Prompts() []mcpPrompt {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]mcpPrompt, 0, len(r.prompts))
	for _, p := range r.prompts {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Server == out[j].Server {
			return out[i].Name < out[j].Name
		}
		return out[i].Server < out[j].Server
	})
	return out
}

func (r *MCPRuntime) ReadResource(ctx context.Context, uri string) (string, error) {
	if r == nil {
		return "", fmt.Errorf("mcp runtime not initialized")
	}
	r.mu.Lock()
	res, ok := r.resources[uri]
	session := r.sessions[res.Server]
	if !ok || session == nil || session.client == nil {
		// URI not in index — try each session that supports resources.
		for _, s := range r.sessions {
			if s.client != nil && s.supportsResources {
				session = s
				ok = true
				break
			}
		}
	}
	r.mu.Unlock()
	if !ok || session == nil || session.client == nil {
		return "", fmt.Errorf("mcp resource not found: %s", uri)
	}
	return session.client.readResource(ctx, uri)
}

func (r *MCPRuntime) GetPrompt(ctx context.Context, name string, arguments map[string]any) (string, error) {
	if r == nil {
		return "", fmt.Errorf("mcp runtime not initialized")
	}
	r.mu.Lock()
	prompt, ok := r.prompts[name]
	session := r.sessions[prompt.Server]
	r.mu.Unlock()
	if !ok || session == nil || session.client == nil {
		return "", fmt.Errorf("mcp prompt not found: %s", name)
	}
	return session.client.getPrompt(ctx, prompt.Name, arguments)
}

func (r *MCPRuntime) CallTool(ctx context.Context, function string, arguments map[string]any) (mcpToolCallResult, error) {
	if r == nil {
		return mcpToolCallResult{}, fmt.Errorf("mcp runtime not initialized")
	}
	r.mu.Lock()
	tool, ok := r.tools[function]
	session := r.sessions[tool.Server]
	r.mu.Unlock()
	if !ok || session == nil || session.client == nil {
		return mcpToolCallResult{}, fmt.Errorf("mcp tool not found or unavailable: %s", function)
	}
	return session.callTool(ctx, tool.Name, arguments)
}

func (r *MCPRuntime) IsMCPFunction(name string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.tools[name]
	return ok
}

func mcpStatusFromConfig(server MCPServerConfig) MCPServerStatus {
	return MCPServerStatus{
		Name:         server.Name,
		Source:       server.Source,
		SourceClient: server.SourceClient,
		Command:      server.Command,
		Args:         append([]string(nil), server.Args...),
		Enabled:      server.Enabled,
	}
}

func normalizeMCPServerConfigs(configs []MCPServerConfig) []MCPServerConfig {
	byName := map[string]int{}
	byFingerprint := map[string]int{}
	out := make([]MCPServerConfig, 0, len(configs))
	for _, cfg := range configs {
		cfg.Name = safeMCPName(cfg.Name)
		cfg.Command = strings.TrimSpace(cfg.Command)
		cfg.Source = strings.TrimSpace(cfg.Source)
		cfg.SourceClient = strings.TrimSpace(firstNonEmptyString(cfg.SourceClient, mcpSourceClient(cfg.Source)))
		cfg.Args = cleanMCPStringList(cfg.Args)
		fp := mcpServerFingerprint(cfg)
		nameKey := strings.ToLower(cfg.Name)
		if cfg.Name == "" || cfg.Command == "" {
			continue
		}
		if len(cfg.Env) == 0 {
			cfg.Env = nil
		}
		if idx, ok := byName[nameKey]; ok {
			out[idx] = mergeMCPServerConfig(out[idx], cfg)
			continue
		}
		if fp != "" {
			if idx, ok := byFingerprint[fp]; ok {
				out[idx] = mergeMCPServerConfig(out[idx], cfg)
				byName[nameKey] = idx
				continue
			}
		}
		byName[nameKey] = len(out)
		if fp != "" {
			byFingerprint[fp] = len(out)
		}
		out = append(out, cfg)
	}
	return out
}

func mergeMCPServerConfig(existing, incoming MCPServerConfig) MCPServerConfig {
	if incoming.Enabled && !existing.Enabled {
		incoming.Source = shorterSource(existing.Source, incoming.Source)
		incoming.SourceClient = mergeSourceClient(existing.SourceClient, incoming.SourceClient)
		if len(incoming.Env) == 0 && len(existing.Env) > 0 {
			incoming.Env = existing.Env
		}
		return incoming
	}
	existing.Enabled = existing.Enabled || incoming.Enabled
	existing.Source = shorterSource(existing.Source, incoming.Source)
	existing.SourceClient = mergeSourceClient(existing.SourceClient, incoming.SourceClient)
	if len(existing.Env) == 0 && len(incoming.Env) > 0 {
		existing.Env = incoming.Env
	}
	return existing
}

func discoverMCPServers(cfg Config) []MCPServerConfig {
	merged := map[string]MCPServerConfig{}
	fingerprints := map[string]string{}
	for _, saved := range normalizeMCPServerConfigs(cfg.MCPServers) {
		key := strings.ToLower(saved.Name)
		merged[key] = saved
		if fp := mcpServerFingerprint(saved); fp != "" {
			fingerprints[fp] = key
		}
	}
	for _, discovered := range discoverMCPServersFromDisk() {
		fp := mcpServerFingerprint(discovered)
		key := strings.ToLower(discovered.Name)
		if existingKey := fingerprints[fp]; fp != "" && existingKey != "" && existingKey != key {
			existing := merged[existingKey]
			existing.Source = shorterSource(existing.Source, discovered.Source)
			existing.SourceClient = mergeSourceClient(existing.SourceClient, discovered.SourceClient)
			merged[existingKey] = existing
			continue
		}
		if saved, ok := merged[key]; ok {
			discovered.Name = saved.Name
			discovered.Enabled = saved.Enabled
			if saved.Source != "" {
				discovered.Source = saved.Source
			}
			discovered.SourceClient = mergeSourceClient(saved.SourceClient, discovered.SourceClient)
		}
		merged[key] = discovered
		if fp != "" {
			fingerprints[fp] = key
		}
	}
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]MCPServerConfig, 0, len(keys))
	for _, key := range keys {
		out = append(out, merged[key])
	}
	return out
}

func discoverMCPServersFromDisk() []MCPServerConfig {
	out := make([]MCPServerConfig, 0)
	seen := map[string]bool{}
	seenFingerprint := map[string]bool{}
	add := func(server MCPServerConfig) {
		server.Name = safeMCPName(server.Name)
		server.SourceClient = strings.TrimSpace(firstNonEmptyString(server.SourceClient, mcpSourceClient(server.Source)))
		fp := mcpServerFingerprint(server)
		nameKey := strings.ToLower(server.Name)
		if server.Name == "" || server.Command == "" || seen[nameKey] || (fp != "" && seenFingerprint[fp]) {
			return
		}
		seen[nameKey] = true
		if fp != "" {
			seenFingerprint[fp] = true
		}
		out = append(out, server)
	}
	for _, path := range mcpJSONConfigSearchPaths() {
		servers := readMCPConfigFile(path)
		for _, server := range servers {
			add(server)
		}
	}
	for _, path := range mcpTOMLConfigSearchPaths() {
		servers := readMCPServersFromTOML(path)
		for _, server := range servers {
			add(server)
		}
	}
	return out
}

func SetCustomMCPConfigPath(path string) {
	customMCPConfigPath = strings.TrimSpace(path)
}

var customMCPConfigPath string

func mcpJSONConfigSearchPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	candidates := []string{}
	if customMCPConfigPath != "" {
		candidates = append(candidates, customMCPConfigPath)
	}
	candidates = append(candidates,
		filepath.Join(home, ".cursor", "mcp.json"),
		filepath.Join(home, ".cursor", "settings.json"),
		filepath.Join(home, ".claude.json"),
		filepath.Join(home, ".claude", "mcp.json"),
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(home, ".mcp.json"),
		filepath.Join(home, ".gemini", "antigravity", "mcp_config.json"),
		filepath.Join(home, ".codeium", "windsurf", "mcp_config.json"),
		filepath.Join(home, ".continue", "config.json"),
		filepath.Join(home, ".config", "Claude", "claude_desktop_config.json"),
		filepath.Join(home, ".config", "claude", "claude_desktop_config.json"),
		filepath.Join(home, ".codex", "mcp.json"),
		filepath.Join(home, ".qoder", "mcp.json"),
		filepath.Join(home, ".qoder", "settings.json"),
		filepath.Join(home, ".lingma", "mcp.json"),
		filepath.Join(home, ".lingma", "settings.json"),
	)
	candidates = append(candidates, platformMCPJSONPaths(home)...)
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		candidates = append(candidates,
			filepath.Join(cwd, ".mcp.json"),
			filepath.Join(cwd, ".cursor", "mcp.json"),
			filepath.Join(cwd, ".vscode", "mcp.json"),
			filepath.Join(cwd, ".continue", "mcpServers", "mcp.json"),
		)
		if matches, err := filepath.Glob(filepath.Join(cwd, ".continue", "mcpServers", "*.json")); err == nil {
			candidates = append(candidates, matches...)
		}
	}
	for _, pattern := range platformMCPJSONGlobs(home) {
		if matches, err := filepath.Glob(pattern); err == nil {
			candidates = append(candidates, matches...)
		}
	}
	return candidates
}

func platformMCPJSONPaths(home string) []string {
	switch runtime.GOOS {
	case "windows":
		appData := firstNonEmptyString(os.Getenv("APPDATA"), filepath.Join(home, "AppData", "Roaming"))
		localAppData := firstNonEmptyString(os.Getenv("LOCALAPPDATA"), filepath.Join(home, "AppData", "Local"))
		return []string{
			filepath.Join(appData, "Claude", "claude_desktop_config.json"),
			filepath.Join(appData, "Code", "User", "mcp.json"),
			filepath.Join(appData, "Code", "User", "settings.json"),
			filepath.Join(appData, "Code - Insiders", "User", "mcp.json"),
			filepath.Join(appData, "Code - Insiders", "User", "settings.json"),
			filepath.Join(appData, "VSCodium", "User", "mcp.json"),
			filepath.Join(appData, "VSCodium", "User", "settings.json"),
			filepath.Join(appData, "Cursor", "User", "mcp.json"),
			filepath.Join(appData, "Cursor", "User", "settings.json"),
			filepath.Join(appData, "Windsurf", "User", "mcp.json"),
			filepath.Join(appData, "Windsurf", "User", "settings.json"),
			filepath.Join(appData, "Qoder", "User", "mcp.json"),
			filepath.Join(appData, "Qoder", "User", "settings.json"),
			filepath.Join(appData, "QoderCN", "User", "mcp.json"),
			filepath.Join(appData, "QoderCN", "User", "settings.json"),
			filepath.Join(appData, "Lingma", "User", "mcp.json"),
			filepath.Join(appData, "Lingma", "User", "settings.json"),
			filepath.Join(appData, "Zed", "settings.json"),
			filepath.Join(home, ".codeium", "windsurf", "mcp_config.json"),
			filepath.Join(home, ".gemini", "antigravity", "mcp_config.json"),
			filepath.Join(localAppData, "Qoder", "SharedClientCache", "mcp.json"),
			filepath.Join(localAppData, "Qoder", "SharedClientCache", "extension", "local", "mcp.json"),
			filepath.Join(localAppData, "QoderCN", "SharedClientCache", "mcp.json"),
			filepath.Join(localAppData, "QoderCN", "SharedClientCache", "extension", "local", "mcp.json"),
			filepath.Join(localAppData, "Lingma", "SharedClientCache", "mcp.json"),
			filepath.Join(localAppData, "Lingma", "SharedClientCache", "extension", "local", "mcp.json"),
			filepath.Join(localAppData, "Antigravity", "mcp_config.json"),
			filepath.Join(localAppData, "Google", "Antigravity", "mcp_config.json"),
		}
	case "darwin":
		return []string{
			filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json"),
			filepath.Join(home, "Library", "Application Support", "Code", "User", "mcp.json"),
			filepath.Join(home, "Library", "Application Support", "Code", "User", "settings.json"),
			filepath.Join(home, "Library", "Application Support", "Code - Insiders", "User", "mcp.json"),
			filepath.Join(home, "Library", "Application Support", "Code - Insiders", "User", "settings.json"),
			filepath.Join(home, "Library", "Application Support", "VSCodium", "User", "mcp.json"),
			filepath.Join(home, "Library", "Application Support", "VSCodium", "User", "settings.json"),
			filepath.Join(home, "Library", "Application Support", "Cursor", "User", "mcp.json"),
			filepath.Join(home, "Library", "Application Support", "Cursor", "User", "settings.json"),
			filepath.Join(home, "Library", "Application Support", "Windsurf", "User", "mcp.json"),
			filepath.Join(home, "Library", "Application Support", "Windsurf", "User", "settings.json"),
			filepath.Join(home, "Library", "Application Support", "Zed", "settings.json"),
			filepath.Join(home, "Library", "Application Support", "Lingma", "SharedClientCache", "mcp.json"),
			filepath.Join(home, "Library", "Application Support", "Lingma", "SharedClientCache", "extension", "local", "mcp.json"),
			filepath.Join(home, "Library", "Application Support", "Lingma", "User", "mcp.json"),
			filepath.Join(home, "Library", "Application Support", "Lingma", "User", "settings.json"),
			filepath.Join(home, "Library", "Application Support", "LingmaIDE", "SharedClientCache", "mcp.json"),
			filepath.Join(home, "Library", "Application Support", "LingmaIDE", "SharedClientCache", "extension", "local", "mcp.json"),
			filepath.Join(home, "Library", "Application Support", "LingmaIDE", "User", "mcp.json"),
			filepath.Join(home, "Library", "Application Support", "LingmaIDE", "User", "settings.json"),
			filepath.Join(home, "Library", "Application Support", "Qoder", "SharedClientCache", "mcp.json"),
			filepath.Join(home, "Library", "Application Support", "Qoder", "SharedClientCache", "extension", "local", "mcp.json"),
			filepath.Join(home, "Library", "Application Support", "Qoder", "User", "mcp.json"),
			filepath.Join(home, "Library", "Application Support", "Qoder", "User", "settings.json"),
			filepath.Join(home, "Library", "Application Support", "QoderCN", "SharedClientCache", "mcp.json"),
			filepath.Join(home, "Library", "Application Support", "QoderCN", "SharedClientCache", "extension", "local", "mcp.json"),
			filepath.Join(home, "Library", "Application Support", "QoderCN", "User", "mcp.json"),
			filepath.Join(home, "Library", "Application Support", "QoderCN", "User", "settings.json"),
			filepath.Join(home, "Library", "Application Support", "Antigravity", "mcp_config.json"),
			filepath.Join(home, "Library", "Application Support", "Google", "Antigravity", "mcp_config.json"),
		}
	default:
		xdg := firstNonEmptyString(os.Getenv("XDG_CONFIG_HOME"), filepath.Join(home, ".config"))
		return []string{
			filepath.Join(xdg, "Claude", "claude_desktop_config.json"),
			filepath.Join(xdg, "Code", "User", "mcp.json"),
			filepath.Join(xdg, "Code", "User", "settings.json"),
			filepath.Join(xdg, "Code - Insiders", "User", "mcp.json"),
			filepath.Join(xdg, "Code - Insiders", "User", "settings.json"),
			filepath.Join(xdg, "VSCodium", "User", "mcp.json"),
			filepath.Join(xdg, "VSCodium", "User", "settings.json"),
			filepath.Join(xdg, "Cursor", "User", "mcp.json"),
			filepath.Join(xdg, "Cursor", "User", "settings.json"),
			filepath.Join(xdg, "Windsurf", "User", "mcp.json"),
			filepath.Join(xdg, "Windsurf", "User", "settings.json"),
			filepath.Join(xdg, "Qoder", "User", "mcp.json"),
			filepath.Join(xdg, "QoderCN", "User", "mcp.json"),
			filepath.Join(xdg, "Lingma", "User", "mcp.json"),
			filepath.Join(xdg, "zed", "settings.json"),
			filepath.Join(home, ".codeium", "windsurf", "mcp_config.json"),
			filepath.Join(home, ".gemini", "antigravity", "mcp_config.json"),
		}
	}
}

func platformMCPJSONGlobs(home string) []string {
	switch runtime.GOOS {
	case "windows":
		appData := firstNonEmptyString(os.Getenv("APPDATA"), filepath.Join(home, "AppData", "Roaming"))
		localAppData := firstNonEmptyString(os.Getenv("LOCALAPPDATA"), filepath.Join(home, "AppData", "Local"))
		return []string{
			filepath.Join(appData, "Code", "User", "globalStorage", "*", "settings", "*mcp*.json"),
			filepath.Join(appData, "Code - Insiders", "User", "globalStorage", "*", "settings", "*mcp*.json"),
			filepath.Join(appData, "Cursor", "User", "globalStorage", "*", "settings", "*mcp*.json"),
			filepath.Join(appData, "Windsurf", "User", "globalStorage", "*", "settings", "*mcp*.json"),
			filepath.Join(localAppData, "Qoder*", "SharedClientCache", "*mcp*.json"),
			filepath.Join(localAppData, "Qoder*", "SharedClientCache", "*", "*mcp*.json"),
			filepath.Join(localAppData, "Lingma*", "SharedClientCache", "*mcp*.json"),
			filepath.Join(localAppData, "Lingma*", "SharedClientCache", "*", "*mcp*.json"),
			filepath.Join(localAppData, "*Antigravity*", "*mcp*.json"),
		}
	case "darwin":
		return []string{
			filepath.Join(home, "Library", "Application Support", "Code", "User", "globalStorage", "*", "settings", "*mcp*.json"),
			filepath.Join(home, "Library", "Application Support", "Code - Insiders", "User", "globalStorage", "*", "settings", "*mcp*.json"),
			filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage", "*", "settings", "*mcp*.json"),
			filepath.Join(home, "Library", "Application Support", "Windsurf", "User", "globalStorage", "*", "settings", "*mcp*.json"),
			filepath.Join(home, ".continue", "mcpServers", "*.json"),
			filepath.Join(home, "Library", "Application Support", "Lingma*", "SharedClientCache", "*mcp*.json"),
			filepath.Join(home, "Library", "Application Support", "Lingma*", "SharedClientCache", "*", "*mcp*.json"),
			filepath.Join(home, "Library", "Application Support", "Lingma*", "User", "*mcp*.json"),
			filepath.Join(home, "Library", "Application Support", "Qoder*", "SharedClientCache", "*mcp*.json"),
			filepath.Join(home, "Library", "Application Support", "Qoder*", "SharedClientCache", "*", "*mcp*.json"),
			filepath.Join(home, "Library", "Application Support", "Qoder*", "User", "*mcp*.json"),
			filepath.Join(home, "Library", "Application Support", "*Antigravity*", "*mcp*.json"),
			filepath.Join(home, "Library", "Application Support", "*", "*mcp*.json"),
			filepath.Join(home, "Library", "Application Support", "*", "*", "*mcp*.json"),
		}
	default:
		xdg := firstNonEmptyString(os.Getenv("XDG_CONFIG_HOME"), filepath.Join(home, ".config"))
		return []string{
			filepath.Join(xdg, "Code", "User", "globalStorage", "*", "settings", "*mcp*.json"),
			filepath.Join(xdg, "Cursor", "User", "globalStorage", "*", "settings", "*mcp*.json"),
			filepath.Join(xdg, "Windsurf", "User", "globalStorage", "*", "settings", "*mcp*.json"),
			filepath.Join(xdg, "Qoder*", "**", "*mcp*.json"),
			filepath.Join(xdg, "Lingma*", "**", "*mcp*.json"),
		}
	}
}

func mcpTOMLConfigSearchPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	candidates := []string{
		filepath.Join(home, ".codex", "config.toml"),
	}
	if matches, err := filepath.Glob(filepath.Join(home, ".codex-profiles", "*", "config.toml")); err == nil {
		candidates = append(candidates, matches...)
	}
	return candidates
}

func ValidateMCPJSONConfig(path string, data []byte) ([]MCPServerConfig, error) {
	var probe mcpConfigFile
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("JSON 解析失败：%w", err)
	}
	servers := readMCPConfigBytes(path, data)
	if len(servers) == 0 {
		return nil, fmt.Errorf("未解析到 MCP server，请使用 mcpServers、servers 或 context_servers 字段")
	}
	return normalizeMCPServerConfigs(servers), nil
}

func readMCPConfigFile(path string) []MCPServerConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return readMCPConfigBytes(path, data)
}

func readMCPConfigBytes(path string, data []byte) []MCPServerConfig {
	var file mcpConfigFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil
	}
	var out []MCPServerConfig
	for _, raw := range []json.RawMessage{file.MCPServers, file.Servers, file.Context} {
		out = append(out, parseMCPServerJSONBlock(path, raw)...)
	}
	return out
}

func parseMCPServerJSONBlock(path string, raw json.RawMessage) []MCPServerConfig {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var object map[string]mcpServerFile
	if err := json.Unmarshal(raw, &object); err == nil && len(object) > 0 {
		names := make([]string, 0, len(object))
		for name := range object {
			names = append(names, name)
		}
		sort.Strings(names)
		out := make([]MCPServerConfig, 0, len(names))
		for _, name := range names {
			if server := mcpServerFileToConfig(path, name, object[name]); server.Name != "" {
				out = append(out, server)
			}
		}
		return out
	}
	var array []mcpServerFile
	if err := json.Unmarshal(raw, &array); err == nil {
		out := make([]MCPServerConfig, 0, len(array))
		for _, item := range array {
			if server := mcpServerFileToConfig(path, item.Name, item); server.Name != "" {
				out = append(out, server)
			}
		}
		return out
	}
	return nil
}

func mcpServerFileToConfig(path, name string, raw mcpServerFile) MCPServerConfig {
	command := strings.TrimSpace(raw.Command)
	name = safeMCPName(firstNonEmptyString(name, raw.Name))
	if name == "" || command == "" {
		return MCPServerConfig{}
	}
	return MCPServerConfig{
		Name:         name,
		Source:       path,
		SourceClient: mcpSourceClient(path),
		Command:      command,
		Args:         cleanMCPStringList(raw.Args),
		Env:          raw.Env,
		Enabled:      false,
	}
}

func readMCPServersFromTOML(path string) []MCPServerConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var current *MCPServerConfig
	currentEnv := false
	servers := map[string]*MCPServerConfig{}
	lines := strings.Split(string(data), "\n")
	for _, raw := range lines {
		line := stripTOMLComment(strings.TrimSpace(raw))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section := strings.Trim(line, "[]")
			current = nil
			currentEnv = false
			if strings.HasPrefix(section, "mcp_servers.") {
				name := strings.TrimPrefix(section, "mcp_servers.")
				if strings.HasSuffix(name, ".env") {
					name = strings.TrimSuffix(name, ".env")
					currentEnv = true
				}
				name = safeMCPName(strings.Trim(name, `"`))
				if name == "" {
					continue
				}
				if servers[name] == nil {
					servers[name] = &MCPServerConfig{Name: name, Source: path, SourceClient: mcpSourceClient(path)}
				}
				current = servers[name]
			}
			continue
		}
		if current == nil {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if currentEnv {
			if parsed, ok := parseTOMLString(value); ok {
				if current.Env == nil {
					current.Env = map[string]string{}
				}
				current.Env[key] = parsed
			}
			continue
		}
		switch key {
		case "command":
			if parsed, ok := parseTOMLString(value); ok {
				current.Command = parsed
			}
		case "args":
			current.Args = parseTOMLStringArray(value)
		case "env":
			if env := parseTOMLInlineEnv(value); len(env) > 0 {
				current.Env = env
			}
		}
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]MCPServerConfig, 0, len(names))
	for _, name := range names {
		server := *servers[name]
		server.Command = strings.TrimSpace(server.Command)
		if server.Command == "" {
			continue
		}
		server.Args = cleanMCPStringList(server.Args)
		out = append(out, server)
	}
	return out
}

func probeMCPServers(ctx context.Context, cfg Config) []MCPServerStatus {
	servers := discoverMCPServers(cfg)
	statuses := make([]MCPServerStatus, 0, len(servers))
	for _, server := range servers {
		status := mcpStatusFromConfig(server)
		if !cfg.MCPEnabled || !server.Enabled {
			status.Message = "未启用"
			statuses = append(statuses, status)
			continue
		}
		tools, err := listMCPTools(ctx, server)
		if err != nil {
			status.Message = err.Error()
			statuses = append(statuses, status)
			continue
		}
		status.Available = true
		status.ToolCount = len(tools)
		for _, tool := range tools {
			status.Tools = append(status.Tools, MCPToolStatus{Name: tool.Name, Function: tool.Function, Description: tool.Description})
		}
		statuses = append(statuses, status)
	}
	return statuses
}

func listEnabledMCPTools(ctx context.Context, cfg Config) []mcpTool {
	if !cfg.MCPEnabled {
		return nil
	}
	var tools []mcpTool
	for _, server := range discoverMCPServers(cfg) {
		if !server.Enabled {
			continue
		}
		items, err := listMCPTools(ctx, server)
		if err != nil {
			continue
		}
		tools = append(tools, items...)
	}
	return tools
}

func buildMCPPromptSection(tools []mcpTool, resources []mcpResource, prompts []mcpPrompt) string {
	if len(tools) == 0 && len(resources) == 0 && len(prompts) == 0 {
		return ""
	}
	var b strings.Builder
	if len(tools) > 0 {
		b.WriteString("已启用 MCP 动态工具。需要使用非飞书能力时，优先直接调用下面列出的 `mcp__server__tool` 工具；不要臆造未列出的工具。只有没有对应动态工具时，才退回 `mcp_call`。\n")
		limit := len(tools)
		if limit > 80 {
			limit = 80
		}
		for i := 0; i < limit; i++ {
			tool := tools[i]
			b.WriteString("- ")
			b.WriteString(tool.Function)
			b.WriteString("：")
			b.WriteString(tool.Server)
			b.WriteString("/")
			b.WriteString(tool.Name)
			if desc := strings.TrimSpace(tool.Description); desc != "" {
				b.WriteString("：")
				b.WriteString(summarizeText(desc, 140))
			}
			b.WriteString("\n")
		}
		if len(tools) > limit {
			b.WriteString(fmt.Sprintf("- ... 另有 %d 个 MCP 工具未列出；未列出的工具不要臆造调用。\n", len(tools)-limit))
		}
	}
	if len(resources) > 0 {
		b.WriteString("\n可用 MCP 资源（可用 mcp_resource_read 读取）：\n")
		limit := len(resources)
		if limit > 30 {
			limit = 30
		}
		for i := 0; i < limit; i++ {
			res := resources[i]
			b.WriteString("- ")
			b.WriteString(res.URI)
			if name := strings.TrimSpace(res.Title); name != "" {
				b.WriteString("：" + name)
			} else if name := strings.TrimSpace(res.Name); name != "" {
				b.WriteString("：" + name)
			}
			if desc := strings.TrimSpace(res.Description); desc != "" {
				b.WriteString("：" + summarizeText(desc, 80))
			}
			b.WriteString("\n")
		}
		if len(resources) > limit {
			b.WriteString(fmt.Sprintf("- ... 另有 %d 个资源未列出。\n", len(resources)-limit))
		}
	}
	if len(prompts) > 0 {
		b.WriteString("\n可用 MCP 提示词模板（可用 mcp_prompt_get 获取）：\n")
		limit := len(prompts)
		if limit > 30 {
			limit = 30
		}
		for i := 0; i < limit; i++ {
			p := prompts[i]
			b.WriteString("- ")
			b.WriteString(p.Server + ":" + p.Name)
			if title := strings.TrimSpace(p.Title); title != "" {
				b.WriteString("：" + title)
			}
			if desc := strings.TrimSpace(p.Description); desc != "" {
				b.WriteString("：" + summarizeText(desc, 80))
			}
			b.WriteString("\n")
		}
		if len(prompts) > limit {
			b.WriteString(fmt.Sprintf("- ... 另有 %d 个提示词未列出。\n", len(prompts)-limit))
		}
	}
	return strings.TrimSpace(b.String())
}

// executeMCPToolContext is the fallback MCP tool execution path used when no
// MCPRuntime is available (e.g. direct executeToolContextWithConfig calls).
// Normal Manager operation goes through MCPRuntime.CallTool which reuses a
// persistent stdio session instead of reconnecting per call.
func executeMCPToolContext(parent context.Context, cfg Config, args map[string]any) ToolExecutionResult {
	if !cfg.MCPEnabled {
		return ToolExecutionResult{Output: "[error] MCP 未启用", IsError: true}
	}
	serverName := safeMCPName(stringArg(args, "server"))
	toolName := strings.TrimSpace(stringArg(args, "tool"))
	if serverName == "" || toolName == "" {
		return ToolExecutionResult{Output: "[error] server 和 tool 不能为空", IsError: true}
	}
	var arguments map[string]any
	if raw, ok := args["arguments"].(map[string]any); ok {
		arguments = raw
	} else {
		arguments = map[string]any{}
	}
	var server MCPServerConfig
	found := false
	for _, item := range discoverMCPServers(cfg) {
		if item.Name == serverName {
			server = item
			found = true
			break
		}
	}
	if !found {
		return ToolExecutionResult{Output: "[error] MCP server not found: " + serverName, IsError: true}
	}
	if !server.Enabled {
		return ToolExecutionResult{Output: "[error] MCP server 未启用: " + serverName, IsError: true}
	}
	ctx, cancel := context.WithTimeout(parent, toolTimeout())
	defer cancel()
	result, err := callMCPTool(ctx, server, toolName, arguments)
	if err != nil {
		return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
	}
	text := strings.TrimSpace(result.Text)
	if text == "" {
		text = "[no output]"
	}
	return ToolExecutionResult{Output: text, IsError: result.IsError}
}

func listMCPTools(ctx context.Context, server MCPServerConfig) ([]mcpTool, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	client, tools, err := connectMCPServer(ctx, server)
	if err != nil {
		return nil, err
	}
	defer client.close()
	return withMCPFunctionNames(server.Name, tools), nil
}

func connectMCPServer(ctx context.Context, server MCPServerConfig) (*mcpClient, []mcpTool, error) {
	client, err := startMCPClient(ctx, server)
	if err != nil {
		return nil, nil, err
	}
	if err := client.initialize(ctx); err != nil {
		client.close()
		return nil, nil, err
	}
	tools, err := client.listTools(ctx, server.Name)
	if err != nil {
		client.close()
		return nil, nil, err
	}
	return client, tools, nil
}

func (s *mcpSession) listTools(ctx context.Context) ([]mcpTool, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("mcp session not connected")
	}
	return s.client.listTools(ctx, s.server.Name)
}

func (s *mcpSession) callTool(ctx context.Context, toolName string, arguments map[string]any) (mcpToolCallResult, error) {
	if s == nil || s.client == nil {
		return mcpToolCallResult{}, fmt.Errorf("mcp session not connected")
	}
	var result any
	if err := s.client.request(ctx, "tools/call", map[string]any{
		"name":      toolName,
		"arguments": arguments,
	}, &result); err != nil {
		return mcpToolCallResult{}, err
	}
	return stringifyMCPResult(result), nil
}

func (c *mcpClient) listTools(ctx context.Context, serverName string) ([]mcpTool, error) {
	const maxPages = 20
	var tools []mcpTool
	cursor := ""
	for page := 0; page < maxPages; page++ {
		params := any(nil)
		if cursor != "" {
			params = map[string]any{"cursor": cursor}
		}
		var parsed struct {
			Tools []struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				InputSchema map[string]any `json:"inputSchema"`
			} `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := c.request(ctx, "tools/list", params, &parsed); err != nil {
			return nil, err
		}
		for _, item := range parsed.Tools {
			name := strings.TrimSpace(item.Name)
			if name == "" {
				continue
			}
			tools = append(tools, mcpTool{
				Server:      serverName,
				Name:        name,
				Description: strings.TrimSpace(item.Description),
				InputSchema: item.InputSchema,
			})
		}
		cursor = strings.TrimSpace(parsed.NextCursor)
		if cursor == "" {
			return tools, nil
		}
	}
	return tools, nil
}

func (c *mcpClient) listResources(ctx context.Context, serverName string) ([]mcpResource, []mcpResourceTemplate, error) {
	const maxPages = 20
	var resources []mcpResource
	cursor := ""
	for page := 0; page < maxPages; page++ {
		params := any(nil)
		if cursor != "" {
			params = map[string]any{"cursor": cursor}
		}
		var parsed struct {
			Resources []struct {
				URI         string `json:"uri"`
				Name        string `json:"name"`
				Title       string `json:"title"`
				Description string `json:"description"`
				MimeType    string `json:"mimeType"`
			} `json:"resources"`
			NextCursor string `json:"nextCursor"`
		}
		if err := c.request(ctx, "resources/list", params, &parsed); err != nil {
			return nil, nil, err
		}
		for _, item := range parsed.Resources {
			uri := strings.TrimSpace(item.URI)
			if uri == "" {
				continue
			}
			resources = append(resources, mcpResource{
				Server:      serverName,
				URI:         uri,
				Name:        strings.TrimSpace(item.Name),
				Title:       strings.TrimSpace(item.Title),
				Description: strings.TrimSpace(item.Description),
				MimeType:    strings.TrimSpace(item.MimeType),
			})
		}
		cursor = strings.TrimSpace(parsed.NextCursor)
		if cursor == "" {
			break
		}
	}

	var templates []mcpResourceTemplate
	cursor = ""
	for page := 0; page < maxPages; page++ {
		params := any(nil)
		if cursor != "" {
			params = map[string]any{"cursor": cursor}
		}
		var parsed struct {
			ResourceTemplates []struct {
				URITemplate string `json:"uriTemplate"`
				Name        string `json:"name"`
				Title       string `json:"title"`
				Description string `json:"description"`
				MimeType    string `json:"mimeType"`
			} `json:"resourceTemplates"`
			NextCursor string `json:"nextCursor"`
		}
		if err := c.request(ctx, "resources/templates/list", params, &parsed); err != nil {
			// Not all servers support templates; ignore errors.
			break
		}
		for _, item := range parsed.ResourceTemplates {
			tpl := strings.TrimSpace(item.URITemplate)
			if tpl == "" {
				continue
			}
			templates = append(templates, mcpResourceTemplate{
				Server:      serverName,
				URITemplate: tpl,
				Name:        strings.TrimSpace(item.Name),
				Title:       strings.TrimSpace(item.Title),
				Description: strings.TrimSpace(item.Description),
				MimeType:    strings.TrimSpace(item.MimeType),
			})
		}
		cursor = strings.TrimSpace(parsed.NextCursor)
		if cursor == "" {
			break
		}
	}
	return resources, templates, nil
}

func (c *mcpClient) readResource(ctx context.Context, uri string) (string, error) {
	var result struct {
		Contents []struct {
			URI      string `json:"uri"`
			MimeType string `json:"mimeType"`
			Text     string `json:"text"`
			Blob     string `json:"blob"`
		} `json:"contents"`
	}
	if err := c.request(ctx, "resources/read", map[string]any{"uri": uri}, &result); err != nil {
		return "", err
	}
	if len(result.Contents) == 0 {
		return "", nil
	}
	// Prefer text content; fall back to blob indicator.
	if text := strings.TrimSpace(result.Contents[0].Text); text != "" {
		return text, nil
	}
	if blob := strings.TrimSpace(result.Contents[0].Blob); blob != "" {
		return "[binary content, " + fmt.Sprintf("%d", len(blob)) + " bytes base64]", nil
	}
	return "", nil
}

func (c *mcpClient) listPrompts(ctx context.Context, serverName string) ([]mcpPrompt, error) {
	const maxPages = 20
	var prompts []mcpPrompt
	cursor := ""
	for page := 0; page < maxPages; page++ {
		params := any(nil)
		if cursor != "" {
			params = map[string]any{"cursor": cursor}
		}
		var parsed struct {
			Prompts []struct {
				Name        string `json:"name"`
				Title       string `json:"title"`
				Description string `json:"description"`
				Arguments   []struct {
					Name        string `json:"name"`
					Description string `json:"description"`
					Required    bool   `json:"required"`
				} `json:"arguments"`
			} `json:"prompts"`
			NextCursor string `json:"nextCursor"`
		}
		if err := c.request(ctx, "prompts/list", params, &parsed); err != nil {
			return nil, err
		}
		for _, item := range parsed.Prompts {
			name := strings.TrimSpace(item.Name)
			if name == "" {
				continue
			}
			var args []mcpPromptArgument
			for _, a := range item.Arguments {
				args = append(args, mcpPromptArgument{
					Name:        strings.TrimSpace(a.Name),
					Description: strings.TrimSpace(a.Description),
					Required:    a.Required,
				})
			}
			prompts = append(prompts, mcpPrompt{
				Server:      serverName,
				Name:        name,
				Title:       strings.TrimSpace(item.Title),
				Description: strings.TrimSpace(item.Description),
				Arguments:   args,
			})
		}
		cursor = strings.TrimSpace(parsed.NextCursor)
		if cursor == "" {
			return prompts, nil
		}
	}
	return prompts, nil
}

func (c *mcpClient) getPrompt(ctx context.Context, name string, arguments map[string]any) (string, error) {
	params := map[string]any{"name": name}
	if len(arguments) > 0 {
		params["arguments"] = arguments
	}
	var result struct {
		Description string `json:"description"`
		Messages    []struct {
			Role    string `json:"role"`
			Content struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := c.request(ctx, "prompts/get", params, &result); err != nil {
		return "", err
	}
	var parts []string
	if desc := strings.TrimSpace(result.Description); desc != "" {
		parts = append(parts, desc)
	}
	for _, msg := range result.Messages {
		text := strings.TrimSpace(msg.Content.Text)
		if text == "" {
			continue
		}
		parts = append(parts, "["+msg.Role+"] "+text)
	}
	return strings.Join(parts, "\n\n"), nil
}

// callMCPTool creates a one-shot MCP connection, calls a tool, and tears down
// the connection. This is the fallback path — production code uses
// MCPRuntime.CallTool which maintains a persistent stdio session.
func callMCPTool(ctx context.Context, server MCPServerConfig, toolName string, arguments map[string]any) (mcpToolCallResult, error) {
	client, _, err := connectMCPServer(ctx, server)
	if err != nil {
		return mcpToolCallResult{}, err
	}
	defer client.close()
	var result any
	if err := client.request(ctx, "tools/call", map[string]any{
		"name":      toolName,
		"arguments": arguments,
	}, &result); err != nil {
		return mcpToolCallResult{}, err
	}
	return stringifyMCPResult(result), nil
}

func startMCPClient(ctx context.Context, server MCPServerConfig) (*mcpClient, error) {
	cmd := commandContextWithEnv(ctx, server.Command, server.Args...)
	if len(server.Env) > 0 {
		env := cmd.Env
		if len(env) == 0 {
			env = os.Environ()
		}
		for key, value := range server.Env {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			env = append(env, key+"="+value)
		}
		cmd.Env = env
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	if stderr != nil {
		go io.Copy(io.Discard, stderr)
	}
	client := &mcpClient{
		cmd:    server.Command,
		args:   server.Args,
		env:    server.Env,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
		close: func() {
			_ = stdin.Close()
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		},
	}
	return client, nil
}

func (c *mcpClient) initialize(ctx context.Context) error {
	var result struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ServerInfo      map[string]any `json:"serverInfo"`
		Instructions    string         `json:"instructions"`
	}
	if err := c.request(ctx, "initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "lingma-ipc-proxy-feishu-bridge",
			"version": "1.0.0",
		},
	}, &result); err != nil {
		return err
	}
	c.serverCapabilities = result.Capabilities
	c.serverInfo = result.ServerInfo
	c.serverProtocol = result.ProtocolVersion
	return c.notify(ctx, "notifications/initialized", nil)
}

func (c *mcpClient) request(ctx context.Context, method string, params any, out any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := atomic.AddInt64(&c.nextID, 1)
	msg := mcpRPCMessage{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	if err := c.write(ctx, msg); err != nil {
		return err
	}
	for {
		resp, err := c.read(ctx)
		if err != nil {
			return err
		}
		switch strings.TrimSpace(resp.Method) {
		case "notifications/tools/list_changed":
			c.toolsChanged.Store(true)
			continue
		case "notifications/resources/list_changed", "notifications/resources/updated":
			c.resourcesChanged.Store(true)
			continue
		case "notifications/prompts/list_changed":
			c.promptsChanged.Store(true)
			continue
		}
		if resp.ID == nil {
			continue
		}
		if fmt.Sprint(resp.ID) != fmt.Sprint(id) {
			continue
		}
		if resp.Error != nil {
			return fmt.Errorf("mcp %s failed: %s", method, resp.Error.Message)
		}
		if out == nil || len(resp.Result) == 0 {
			return nil
		}
		return json.Unmarshal(resp.Result, out)
	}
}

func (c *mcpClient) notify(ctx context.Context, method string, params any) error {
	return c.write(ctx, mcpRPCMessage{JSONRPC: "2.0", Method: method, Params: params})
}

func (c *mcpClient) write(ctx context.Context, msg mcpRPCMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	done := make(chan error, 1)
	go func() {
		_, err := c.stdin.Write(data)
		done <- err
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	}
}

func (c *mcpClient) read(ctx context.Context) (mcpRPCMessage, error) {
	type readResult struct {
		msg mcpRPCMessage
		err error
	}
	done := make(chan readResult, 1)
	go func() {
		line, err := c.stdout.ReadBytes('\n')
		if err != nil {
			done <- readResult{err: err}
			return
		}
		var msg mcpRPCMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			done <- readResult{err: err}
			return
		}
		done <- readResult{msg: msg}
	}()
	select {
	case <-ctx.Done():
		return mcpRPCMessage{}, ctx.Err()
	case result := <-done:
		return result.msg, result.err
	}
}

func stringifyMCPResult(result any) mcpToolCallResult {
	if result == nil {
		return mcpToolCallResult{}
	}
	if payload, ok := result.(map[string]any); ok {
		isError, _ := payload["isError"].(bool)
		if structured, ok := payload["structuredContent"]; ok {
			if data, err := json.MarshalIndent(structured, "", "  "); err == nil && len(data) > 0 {
				text := string(data)
				if contentText := mcpContentText(payload); contentText != "" {
					text = contentText + "\n\nstructuredContent:\n" + text
				}
				return mcpToolCallResult{Text: text, IsError: isError}
			}
		}
		if text := mcpContentText(payload); text != "" {
			return mcpToolCallResult{Text: text, IsError: isError}
		}
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return mcpToolCallResult{Text: fmt.Sprint(result), IsError: isError}
		}
		return mcpToolCallResult{Text: string(data), IsError: isError}
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcpToolCallResult{Text: fmt.Sprint(result)}
	}
	return mcpToolCallResult{Text: string(data)}
}

func mcpContentText(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	if content, ok := payload["content"].([]any); ok {
		parts := make([]string, 0, len(content))
		for _, raw := range content {
			item, _ := raw.(map[string]any)
			if item == nil {
				continue
			}
			ctype := strings.TrimSpace(fmt.Sprint(item["type"]))
			switch ctype {
			case "text", "":
				if text := strings.TrimSpace(fmt.Sprint(item["text"])); text != "" && text != "<nil>" {
					parts = append(parts, text)
				}
			case "image":
				mime := strings.TrimSpace(fmt.Sprint(item["mimeType"]))
				if mime == "" || mime == "<nil>" {
					mime = "image"
				}
				parts = append(parts, "[image: "+mime+"]")
			case "resource":
				uri := ""
				if res, ok := item["resource"].(map[string]any); ok {
					uri = strings.TrimSpace(fmt.Sprint(res["uri"]))
				}
				if uri == "" || uri == "<nil>" {
					uri = "unknown"
				}
				parts = append(parts, "[resource: "+uri+"]")
			default:
				parts = append(parts, "["+ctype+": content omitted]")
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	return ""
}

func safeMCPName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	replacer := strings.NewReplacer(" ", "_", "/", "_", "\\", "_", ":", "_", ".", "_")
	name = replacer.Replace(name)
	if len(name) > 80 {
		name = name[:80]
	}
	return name
}

func cleanMCPStringList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func stripTOMLComment(line string) string {
	inString := false
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inString {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if r == '#' && !inString {
			return strings.TrimSpace(line[:i])
		}
	}
	return line
}

func parseTOMLString(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	var parsed string
	if err := json.Unmarshal([]byte(value), &parsed); err == nil {
		return parsed, true
	}
	return strings.Trim(value, `"`), true
}

func parseTOMLStringArray(value string) []string {
	value = strings.TrimSpace(value)
	var parsed []string
	if err := json.Unmarshal([]byte(value), &parsed); err == nil {
		return cleanMCPStringList(parsed)
	}
	return nil
}

func parseTOMLInlineEnv(value string) map[string]string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "{") || !strings.HasSuffix(value, "}") {
		return nil
	}
	body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "{"), "}"))
	if body == "" {
		return nil
	}
	env := map[string]string{}
	for _, part := range splitTOMLInlineParts(body) {
		key, rawValue, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.Trim(key, `"`))
		if key == "" {
			continue
		}
		if parsed, ok := parseTOMLString(strings.TrimSpace(rawValue)); ok {
			env[key] = parsed
		}
	}
	return env
}

func splitTOMLInlineParts(body string) []string {
	var parts []string
	start := 0
	inString := false
	escaped := false
	for i, r := range body {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inString {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if r == ',' && !inString {
			parts = append(parts, strings.TrimSpace(body[start:i]))
			start = i + 1
		}
	}
	parts = append(parts, strings.TrimSpace(body[start:]))
	return parts
}

func mcpBoolCapability(capabilities map[string]any, keys ...string) bool {
	if capabilities == nil {
		return false
	}
	m := capabilities
	for i, key := range keys {
		val, ok := m[key]
		if !ok {
			return false
		}
		if i == len(keys)-1 {
			b, _ := val.(bool)
			return b
		}
		child, ok := val.(map[string]any)
		if !ok {
			return false
		}
		m = child
	}
	return false
}

func mcpServerFingerprint(server MCPServerConfig) string {
	command := strings.TrimSpace(server.Command)
	if command == "" {
		return ""
	}
	parts := append([]string{command}, cleanMCPStringList(server.Args)...)
	return strings.Join(parts, "\x00")
}

func withMCPFunctionNames(serverName string, tools []mcpTool) []mcpTool {
	out := make([]mcpTool, 0, len(tools))
	seen := map[string]bool{}
	for _, tool := range tools {
		tool.Server = safeMCPName(firstNonEmptyString(tool.Server, serverName))
		if tool.Server == "" || strings.TrimSpace(tool.Name) == "" {
			continue
		}
		tool.Function = mcpFunctionName(tool.Server, tool.Name)
		if seen[tool.Function] {
			tool.Function = mcpFunctionName(tool.Server, tool.Name+"_"+shortMCPHash(tool.Server+"/"+tool.Name))
		}
		seen[tool.Function] = true
		if tool.InputSchema == nil {
			tool.InputSchema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, tool)
	}
	return out
}

func mcpFunctionName(serverName, toolName string) string {
	server := sanitizeMCPFunctionSegment(serverName)
	tool := sanitizeMCPFunctionSegment(toolName)
	base := "mcp__" + server + "__" + tool
	if len(base) <= 64 {
		return base
	}
	hash := shortMCPHash(serverName + "/" + toolName)
	maxBody := 64 - len("__") - len(hash)
	body := strings.Trim(base, "_")
	if len(body) > maxBody {
		body = body[:maxBody]
	}
	return strings.Trim(body, "_") + "__" + hash
}

func sanitizeMCPFunctionSegment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "tool"
	}
	return out
}

func shortMCPHash(value string) string {
	return fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(value)))
}

func cloneMCPStatuses(statuses []MCPServerStatus) []MCPServerStatus {
	out := make([]MCPServerStatus, len(statuses))
	for i, status := range statuses {
		out[i] = status
		out[i].Args = append([]string(nil), status.Args...)
		out[i].Tools = append([]MCPToolStatus(nil), status.Tools...)
		out[i].Resources = append([]MCPResourceStatus(nil), status.Resources...)
		out[i].Prompts = append([]MCPPromptStatus(nil), status.Prompts...)
	}
	return out
}

func mcpSourceClient(path string) string {
	lower := strings.ToLower(filepath.ToSlash(path))
	switch {
	case strings.Contains(lower, "qodercn"):
		return "QoderCN"
	case strings.Contains(lower, "qoder"):
		return "Qoder"
	case strings.Contains(lower, "lingmaide"), strings.Contains(lower, "lingma"):
		return "Lingma"
	case strings.Contains(lower, "antigravity"):
		return "Antigravity"
	case strings.Contains(lower, "cursor"):
		return "Cursor"
	case strings.Contains(lower, "claude"):
		return "Claude"
	case strings.Contains(lower, "codex"):
		return "Codex"
	case strings.Contains(lower, "windsurf"), strings.Contains(lower, "codeium"):
		return "Windsurf"
	case strings.Contains(lower, "vscodium"):
		return "VSCodium"
	case strings.Contains(lower, "/code/"), strings.Contains(lower, "code - insiders"), strings.Contains(lower, "/.vscode/"):
		return "VS Code"
	case strings.Contains(lower, "zed"):
		return "Zed"
	case strings.Contains(lower, "continue"):
		return "Continue"
	case strings.Contains(lower, "cline"):
		return "Cline"
	case strings.Contains(lower, "roo"):
		return "Roo"
	default:
		return "本机配置"
	}
}

func mergeSourceClient(a, b string) string {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" {
		return b
	}
	if b == "" || strings.EqualFold(a, b) {
		return a
	}
	parts := strings.Split(a, " / ")
	for _, part := range parts {
		if strings.EqualFold(strings.TrimSpace(part), b) {
			return a
		}
	}
	return a + " / " + b
}

func shorterSource(a, b string) string {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	if len(b) < len(a) {
		return b
	}
	return a
}
