package remote

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lingma-ipc-proxy/internal/toolemulation"
)

func TestNewKeepsZeroTimeoutUnlimited(t *testing.T) {
	client := New(Config{Timeout: 0})
	if client.client.Timeout != 0 {
		t.Fatalf("timeout = %v, want 0", client.client.Timeout)
	}
}

func TestNewKeepsPositiveTimeout(t *testing.T) {
	client := New(Config{Timeout: 7 * time.Second})
	if client.client.Timeout != 7*time.Second {
		t.Fatalf("timeout = %v, want 7s", client.client.Timeout)
	}
}

func TestValidateProxyURL(t *testing.T) {
	valid := []string{"", "http://127.0.0.1:7890", "https://proxy.example.com", "socks5://127.0.0.1:1080"}
	for _, value := range valid {
		if err := ValidateProxyURL(value); err != nil {
			t.Fatalf("ValidateProxyURL(%q) = %v, want nil", value, err)
		}
	}
	invalid := []string{"127.0.0.1:7890", "ftp://proxy.example.com", "http://"}
	for _, value := range invalid {
		if err := ValidateProxyURL(value); err == nil {
			t.Fatalf("ValidateProxyURL(%q) = nil, want error", value)
		}
	}
}

func TestProxySourcePrefersExplicitConfig(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://env-proxy:7890")
	got, source := ProxySource("http://explicit-proxy:7890")
	if got != "http://explicit-proxy:7890" || source != "explicit config" {
		t.Fatalf("ProxySource explicit = (%q, %q)", got, source)
	}
}

func TestProxySourceReadsEnvironment(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://env-proxy:7890")
	got, source := ProxySource("")
	if got != "http://env-proxy:7890" || source != "HTTPS_PROXY" {
		t.Fatalf("ProxySource env = (%q, %q)", got, source)
	}
}

func TestExtractBaseURLFromEndpointLog(t *testing.T) {
	got := extractBaseURLFromText(`2026-04-10 INFO Update endpoint success. endpoint config: https://ai-lingma-example-cn-beijing.rdc.aliyuncs.com`)
	want := "https://ai-lingma-example-cn-beijing.rdc.aliyuncs.com"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExtractBaseURLFromMarketplaceLog(t *testing.T) {
	got := extractBaseURLFromText(`2026-04-30 [info] [Marketplace] Using service url: https://ai-lingma-example-cn-beijing.rdc.aliyuncs.com/marketplace/_apis/public/gallery`)
	want := "https://ai-lingma-example-cn-beijing.rdc.aliyuncs.com"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExtractBaseURLFromRawWindowsLogURL(t *testing.T) {
	got := extractBaseURLFromText(`2026-05-06T12:00:00 endpoint=https://ai-lingma-example-cn-beijing.rdc.aliyuncs.com/algo/api/v2/model/list`)
	want := "https://ai-lingma-example-cn-beijing.rdc.aliyuncs.com"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExtractBaseURLFromChatGenerationURL(t *testing.T) {
	got := extractBaseURLFromText(`POST https://lingma.asiainfo.com/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_common`)
	want := "https://lingma.asiainfo.com"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExtractBaseURLFromQoderCLIEndpointLog(t *testing.T) {
	got := extractBaseURLFromText(`Lingma endpoint configured {"api": "https://ai-lingma-example-cn-beijing.rdc.aliyuncs.com/algo", "login": "https://ai-lingma-example-cn-beijing.rdc.aliyuncs.com/algo/lingma/login"}`)
	want := "https://ai-lingma-example-cn-beijing.rdc.aliyuncs.com"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExtractBaseURLPrefersRealAPIOverMarketplace(t *testing.T) {
	got := extractBaseURLFromText(`Using service url: https://marketplace.visualstudio.com/_apis/public/gallery
Fail to send inference request Post "https://ai-lingma-example-cn-beijing.rdc.aliyuncs.com/algo/api/v2/service/pro/sse/agent_chat_generation?Encode=1"`)
	want := "https://ai-lingma-example-cn-beijing.rdc.aliyuncs.com"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSortBaseURLHintsPrefersEnterpriseEndpoint(t *testing.T) {
	hints := sortBaseURLHints(uniqueBaseURLHints([]BaseURLHint{
		{URL: "https://lingma-api.tongyi.aliyun.com", Source: "old.log"},
		{URL: "https://ai-lingma-example-cn-beijing.rdc.aliyuncs.com", Source: "idea.log"},
		{URL: DefaultBaseURL, Source: "default"},
	}))
	if len(hints) != 3 {
		t.Fatalf("len(hints) = %d, want 3", len(hints))
	}
	if got := hints[0].URL; got != "https://ai-lingma-example-cn-beijing.rdc.aliyuncs.com" {
		t.Fatalf("first hint = %q, want enterprise endpoint", got)
	}
}

func TestExtractBaseURLIgnoresLingmaOSSAssetHost(t *testing.T) {
	got := extractBaseURLFromText(`2026-05-06 endpoint config: https://ai-lingma-example-cn-beijing.rdc.aliyuncs.com
2026-05-06 Download asset from: https://lingma-ide.oss-rg-china-mainland.aliyuncs.com/lingma-extension/download?name=plugin.zip`)
	want := "https://ai-lingma-example-cn-beijing.rdc.aliyuncs.com"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNormalizeBaseURLRepairsMissingLeadingH(t *testing.T) {
	got := normalizeRemoteBaseURLHint(`ttps://ai-lingma-example-cn-beijing.rdc.aliyuncs.com`)
	want := "https://ai-lingma-example-cn-beijing.rdc.aliyuncs.com"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNormalizeBaseURLRejectsLingmaOSSAssetHost(t *testing.T) {
	if got := normalizeRemoteBaseURLHint(`https://lingma-ide.oss-rg-china-mainland.aliyuncs.com/lingma-extension/download`); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestNormalizeBaseURLRejectsUnsupportedScheme(t *testing.T) {
	if got := normalizeRemoteBaseURLHint(`ftp://ai-lingma-example-cn-beijing.rdc.aliyuncs.com`); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestNormalizeBaseURLAcceptsCustomEnterpriseHost(t *testing.T) {
	got := normalizeRemoteBaseURLHint(`https://lingma.asiainfo.com/`)
	want := "https://lingma.asiainfo.com"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNormalizeBaseURLAcceptsCustomHostFromTrustedCandidate(t *testing.T) {
	got := normalizeRemoteBaseURLHint(`https://ai.example-corp.internal/algo/api/v2/model/list`)
	want := "https://ai.example-corp.internal"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNormalizeBaseURLRejectsMarketplaceDownloadURL(t *testing.T) {
	if got := normalizeRemoteBaseURLHint(`https://marketplace.visualstudio.com/items?itemName=alibaba-cloud.tongyi-lingma`); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestSortBaseURLHintsPrefersCustomEnterpriseEndpoint(t *testing.T) {
	hints := sortBaseURLHints(uniqueBaseURLHints([]BaseURLHint{
		{URL: DefaultBaseURL, Source: "default"},
		{URL: "https://lingma.asiainfo.com", Source: "qodercn-app-config"},
	}))
	if got := hints[0].URL; got != "https://lingma.asiainfo.com" {
		t.Fatalf("first hint = %q, want custom enterprise endpoint", got)
	}
}

func TestResolveBaseURLCandidatesPreferCachedSuccess(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HOME", tempDir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tempDir, ".config"))
	t.Setenv("APPDATA", filepath.Join(tempDir, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(tempDir, "AppData", "Local"))
	t.Setenv("ProgramData", filepath.Join(tempDir, "ProgramData"))
	cacheSuccessfulBaseURL("https://lingma.asiainfo.com/algo/api/v2/model/list")

	hints := ResolveBaseURLCandidates()
	if len(hints) == 0 {
		t.Fatal("expected candidates")
	}
	if got := hints[0]; got.URL != "https://lingma.asiainfo.com" || got.Source != "last successful remote domain" {
		t.Fatalf("first hint = %+v, want cached successful domain", got)
	}
}

func TestModelListStatusErrorSuggestsManualRemoteBaseURLOn404(t *testing.T) {
	client := New(Config{BaseURL: "https://lingma-ide.oss-rg-china-mainland.aliyuncs.com"})
	err := client.modelListStatusError(404, `<Error><Code>NoSuchKey</Code></Error>`)
	if err == nil {
		t.Fatal("expected error")
	}
	text := err.Error()
	for _, want := range []string{
		"https://lingma-ide.oss-rg-china-mainland.aliyuncs.com",
		"远端 API 域名自动探测命中了错误地址",
		"https://lingma.alibabacloud.com",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("error %q missing %q", text, want)
		}
	}
}

func TestBuildBodyProjectsNativeTools(t *testing.T) {
	client := New(Config{})
	body, err := client.buildBody("req-1", ChatRequest{
		Model:  "kmodel",
		Prompt: "read file",
		Tools: []toolemulation.ToolDef{{
			Name:        "read_file",
			Description: "Read a local file",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{"type": "string"},
				},
				"required": []any{"file_path"},
			},
		}},
		ToolChoice: toolemulation.ToolChoice{Mode: "tool", Name: "read_file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", payload["tools"])
	}
	tool := tools[0].(map[string]any)
	fn := tool["function"].(map[string]any)
	if tool["type"] != "function" || fn["name"] != "read_file" {
		t.Fatalf("unexpected tool projection: %#v", tool)
	}
	choice := payload["tool_choice"].(map[string]any)
	choiceFn := choice["function"].(map[string]any)
	if choice["type"] != "function" || choiceFn["name"] != "read_file" {
		t.Fatalf("unexpected tool choice: %#v", payload["tool_choice"])
	}
}

func TestBuildBodyPreservesStructuredToolMessages(t *testing.T) {
	client := New(Config{})
	body, err := client.buildBody("req-1", ChatRequest{
		Model:  "kmodel",
		Prompt: "fallback prompt",
		Messages: []Message{
			{Role: "user", Content: "查看项目"},
			{Role: "assistant", ToolCalls: []toolemulation.ToolCall{{
				ID:        "call_1",
				Name:      "Bash",
				Arguments: map[string]any{"command": "pwd && ls -la"},
			}}},
			{Role: "tool", ToolCallID: "call_1", Content: "total 10"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	messages := payload["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("messages = %#v", messages)
	}
	assistant := messages[1].(map[string]any)
	calls := assistant["tool_calls"].([]any)
	call := calls[0].(map[string]any)
	fn := call["function"].(map[string]any)
	args := fn["arguments"].(string)
	if assistant["role"] != "assistant" || fn["name"] != "Bash" || !strings.Contains(args, "pwd") || !strings.Contains(args, "ls -la") {
		t.Fatalf("unexpected assistant message: %#v", assistant)
	}
	tool := messages[2].(map[string]any)
	if tool["role"] != "tool" || tool["tool_call_id"] != "call_1" || tool["content"] != "total 10" {
		t.Fatalf("unexpected tool message: %#v", tool)
	}
}

func TestBuildBodyProjectsRemoteImages(t *testing.T) {
	client := New(Config{})
	body, err := client.buildBody("req-1", ChatRequest{
		Model:  "kmodel",
		Prompt: "看图",
		Messages: []Message{{
			Role:    "user",
			Content: "看图",
			Images: []Image{{
				MediaType: "image/png",
				Data:      "iVBORw0KGgo=",
			}},
		}},
		Images: []Image{{
			MediaType: "image/png",
			Data:      "iVBORw0KGgo=",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	// Images ride in the message content parts, not the top-level image_urls
	// field (matching the QoderCN CLI); image_urls stays null.
	if payload["image_urls"] != nil {
		t.Fatalf("image_urls = %#v, want null", payload["image_urls"])
	}
	modelConfig := payload["model_config"].(map[string]any)
	if modelConfig["is_vl"] != true {
		t.Fatalf("model_config.is_vl = %#v, want true", modelConfig["is_vl"])
	}
	messages := payload["messages"].([]any)
	message := messages[0].(map[string]any)
	content := message["content"].([]any)
	if content[0].(map[string]any)["type"] != "text" {
		t.Fatalf("unexpected first content part: %#v", content[0])
	}
	imagePart := content[1].(map[string]any)
	if imagePart["type"] != "image_url" {
		t.Fatalf("unexpected image content part: %#v", imagePart)
	}
	url, _ := imagePart["image_url"].(map[string]any)["url"].(string)
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("unexpected image url: %q", url)
	}
}

func TestBuildBodyEnablesRemoteReasoningWhenRequested(t *testing.T) {
	client := New(Config{})
	body, err := client.buildBody("req-1", ChatRequest{
		Model:           "kmodel",
		Prompt:          "请先思考再回答",
		ReasoningEffort: "medium",
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	modelConfig := payload["model_config"].(map[string]any)
	if modelConfig["is_reasoning"] != true {
		t.Fatalf("model_config.is_reasoning = %#v, want true", modelConfig["is_reasoning"])
	}
}

func TestBuildGenerationParametersForwardsReasoningEffort(t *testing.T) {
	cases := []struct {
		name        string
		req         ChatRequest
		wantEnable  any    // true, false, or nil (key absent)
		wantEffort  string // "" means key absent
	}{
		{"explicit level passthrough", ChatRequest{Model: "gm51model", ReasoningEffort: "max"}, true, "max"},
		{"non-openai level passthrough", ChatRequest{Model: "gm51model", ReasoningEffort: "xhigh"}, true, "xhigh"},
		{"none disables thinking", ChatRequest{Model: "gm51model", ReasoningEffort: "none"}, false, "none"},
		{"empty on plain model omits keys", ChatRequest{Model: "gm51model"}, nil, ""},
		{"thinking model implies enable, no level", ChatRequest{Model: "qwen3-thinking"}, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := buildGenerationParameters(tc.req, 0.1)
			if got := params["enable_thinking"]; got != tc.wantEnable {
				t.Fatalf("enable_thinking = %#v, want %#v", got, tc.wantEnable)
			}
			got, _ := params["reasoning_effort"].(string)
			if got != tc.wantEffort {
				t.Fatalf("reasoning_effort = %q, want %q", got, tc.wantEffort)
			}
		})
	}
}

func TestParseSSEPayloadExtractsNativeToolCallFragments(t *testing.T) {
	payload := `{"body":"{\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"file_path\\\":\\\"/tmp/a.txt\\\"}\"}}]}}]}","statusCodeValue":200}`
	event, ok, err := parseSSEPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("event not parsed")
	}
	if len(event.ToolCalls) != 1 {
		t.Fatalf("tool calls = %#v", event.ToolCalls)
	}
	call := event.ToolCalls[0]
	if call.ID != "call_1" || call.Name != "read_file" || call.ArgumentsFragment != `{"file_path":"/tmp/a.txt"}` {
		t.Fatalf("unexpected call = %#v", call)
	}
}

func TestRemoteToolCallBufferMergesArgumentFragments(t *testing.T) {
	buffer := newRemoteToolCallBuffer()
	buffer.Add([]remoteToolCallFragment{{
		Index: 0,
		ID:    "call_1",
		Type:  "function",
		Name:  "read_file",
	}})
	buffer.Add([]remoteToolCallFragment{{Index: 0, ArgumentsFragment: `{"file_path":"/tmp`}})
	buffer.Add([]remoteToolCallFragment{{Index: 0, ArgumentsFragment: `/lingma-native`}})
	buffer.Add([]remoteToolCallFragment{{Index: 0, ArgumentsFragment: `-tool-test.txt"}`}})
	calls := buffer.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %#v", calls)
	}
	call := calls[0]
	if call.ID != "call_1" || call.Name != "read_file" || call.Arguments["file_path"] != "/tmp/lingma-native-tool-test.txt" {
		t.Fatalf("unexpected merged call = %#v", call)
	}
}

func TestExtractMachineIDFromTextMarkers(t *testing.T) {
	got := extractMachineIDFromText(`2026-05-06 info using machine id from file: abcdef1234567890abcdef`)
	if got != "abcdef1234567890abcdef" {
		t.Fatalf("machine id = %q", got)
	}
}

func TestExtractMachineIDFromJetBrainsLingmaGeneratedUUID(t *testing.T) {
	got := extractMachineIDFromText(`2026-04-14T10:18:23.823+0800	INFO	Generated uuid: 4d344c56-5436-432d-9658-506d4d344c2d`)
	if got != "4d344c56-5436-432d-9658-506d4d344c2d" {
		t.Fatalf("machine id = %q", got)
	}
}

func TestExtractMachineIDFromTextJSON(t *testing.T) {
	got := extractMachineIDFromText(`{"machineId":"windows-machine-id-1234567890","other":true}`)
	if got != "windows-machine-id-1234567890" {
		t.Fatalf("machine id = %q", got)
	}
}

func TestCandidateLingmaCacheDirsIncludesVSCodeSharedClientCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LINGMA_CACHE_DIR", "")
	dirs := candidateLingmaCacheDirs()
	want := filepath.Join(home, ".lingma", "vscode", "sharedClientCache")
	for _, dir := range dirs {
		if dir == want {
			return
		}
	}
	t.Fatalf("missing vscode shared client cache %q in %#v", want, dirs)
}

func TestCandidateLingmaCacheDirsIncludesVSCodeGlobalStorageCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LINGMA_CACHE_DIR", "")
	dirs := candidateLingmaCacheDirs()
	want := filepath.Join(home, ".config", "Code", "User", "globalStorage", "alibaba-cloud.tongyi-lingma")
	for _, dir := range dirs {
		if dir == want {
			return
		}
	}
	t.Fatalf("missing VS Code globalStorage cache %q in %#v", want, dirs)
}

func TestCandidateLingmaCacheDirsIncludesLinuxQoderCNSharedClientCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LINGMA_CACHE_DIR", "")
	dirs := candidateLingmaCacheDirs()
	want := filepath.Join(home, ".config", "QoderCN", "SharedClientCache")
	for _, dir := range dirs {
		if dir == want {
			return
		}
	}
	t.Fatalf("missing Linux QoderCN SharedClientCache %q in %#v", want, dirs)
}

func TestCandidateLingmaCacheDirsIncludesXDGQoderCNSharedClientCache(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg-config")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("LINGMA_CACHE_DIR", "")
	dirs := candidateLingmaCacheDirs()
	want := filepath.Join(xdg, "QoderCN", "SharedClientCache")
	for _, dir := range dirs {
		if dir == want {
			return
		}
	}
	t.Fatalf("missing XDG QoderCN SharedClientCache %q in %#v", want, dirs)
}

func TestCandidateLingmaCacheDirsIncludesXDGVSCodeGlobalStorageCache(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg-config")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("LINGMA_CACHE_DIR", "")
	dirs := candidateLingmaCacheDirs()
	want := filepath.Join(xdg, "Code", "User", "globalStorage", "alibaba-cloud.tongyi-lingma")
	for _, dir := range dirs {
		if dir == want {
			return
		}
	}
	t.Fatalf("missing XDG VS Code globalStorage cache %q in %#v", want, dirs)
}

func TestCandidateLingmaCacheDirsIncludesQoderCNSharedClientCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LINGMA_CACHE_DIR", "")
	dirs := candidateLingmaCacheDirs()
	want := filepath.Join(home, "Library", "Application Support", "QoderCN", "SharedClientCache")
	for _, dir := range dirs {
		if dir == want {
			return
		}
	}
	t.Fatalf("missing QoderCN shared client cache %q in %#v", want, dirs)
}

func TestCandidateLingmaCacheDirsIncludesDashedQoderCNSharedClient(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LINGMA_CACHE_DIR", "")
	dirs := candidateLingmaCacheDirs()
	want := filepath.Join(home, ".qoder-cn", "shared_client")
	for _, dir := range dirs {
		if dir == want {
			return
		}
	}
	t.Fatalf("missing dashed QoderCN shared_client cache %q in %#v", want, dirs)
}

func TestCredentialLoadErrorSummaryIsCompactByDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LINGMA_CACHE_DIR", "")
	t.Setenv("LINGMA_VERBOSE_CREDENTIAL_ERRORS", "")

	_, err := importLingmaCacheCredential()
	if err == nil {
		t.Fatal("expected error")
	}
	text := err.Error()
	if !strings.Contains(text, "未找到可用的 QoderCN/Lingma 登录缓存") {
		t.Fatalf("unexpected compact error: %q", text)
	}
	if strings.Contains(text, filepath.Join(home, ".qoder-cn", "shared_client")) {
		t.Fatalf("compact error should not include full candidate paths: %q", text)
	}
	if !strings.Contains(text, "LINGMA_VERBOSE_CREDENTIAL_ERRORS=1") {
		t.Fatalf("compact error should mention verbose switch: %q", text)
	}
}

func TestCredentialLoadErrorVerboseIncludesFullPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LINGMA_CACHE_DIR", "")
	t.Setenv("LINGMA_VERBOSE_CREDENTIAL_ERRORS", "1")

	_, err := importLingmaCacheCredential()
	if err == nil {
		t.Fatal("expected error")
	}
	text := err.Error()
	want := filepath.Join(home, ".qoder-cn", "shared_client")
	if !strings.Contains(text, want) {
		t.Fatalf("verbose error should include full candidate path %q: %q", want, text)
	}
}

func TestCandidateLingmaCacheDirsPrefersQoderCNBeforeLingma(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LINGMA_CACHE_DIR", "")
	dirs := candidateLingmaCacheDirs()
	qoder := filepath.Join(home, "Library", "Application Support", "QoderCN", "SharedClientCache")
	lingma := filepath.Join(home, "Library", "Application Support", "Lingma", "SharedClientCache")
	qoderIndex, lingmaIndex := -1, -1
	for i, dir := range dirs {
		switch dir {
		case qoder:
			qoderIndex = i
		case lingma:
			lingmaIndex = i
		}
	}
	if qoderIndex < 0 || lingmaIndex < 0 || qoderIndex > lingmaIndex {
		t.Fatalf("QoderCN cache should be preferred before Lingma cache, qoder=%d lingma=%d dirs=%#v", qoderIndex, lingmaIndex, dirs)
	}
}

func TestLoadMachineIDReadsVSCodeSharedClientCacheID(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "cache"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cache", "id"), []byte("abcdefghijklmnop1234"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := loadMachineID(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "abcdefghijklmnop1234" {
		t.Fatalf("machine id = %q", got)
	}
}

func TestLoadMachineIDReadsQoderCLIAuthIDFallback(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "cli", ".auth"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cli", ".auth", "id"), []byte("qoder-machine-id-1234567890"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := loadMachineID(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "qoder-machine-id-1234567890" {
		t.Fatalf("machine id = %q", got)
	}
}

func sseFrame(t *testing.T, inner map[string]any) string {
	t.Helper()
	body, err := json.Marshal(inner)
	if err != nil {
		t.Fatal(err)
	}
	outer, err := json.Marshal(map[string]any{
		"body":            string(body),
		"statusCodeValue": 200,
		"statusCode":      "OK",
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(outer)
}

func TestParseSSEPayloadReasoningDelta(t *testing.T) {
	ev, ok, err := parseSSEPayload(sseFrame(t, map[string]any{
		"object":  "chat.completion.chunk",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"reasoning_content": "The user"}}},
	}))
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if ev.Reasoning != "The user" || ev.Content != "" {
		t.Fatalf("reasoning=%q content=%q", ev.Reasoning, ev.Content)
	}
}

func TestParseSSEPayloadToolCallWithFinish(t *testing.T) {
	ev, ok, err := parseSSEPayload(sseFrame(t, map[string]any{
		"choices": []any{map[string]any{
			"index":         0,
			"finish_reason": "tool_calls",
			"delta": map[string]any{"tool_calls": []any{map[string]any{
				"index":    0,
				"id":       "call_abc",
				"type":     "function",
				"function": map[string]any{"name": "Bash", "arguments": `{"command":"echo hi"}`},
			}}},
		}},
	}))
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if ev.FinishReason != "tool_calls" {
		t.Fatalf("finish=%q", ev.FinishReason)
	}
	if len(ev.ToolCalls) != 1 || ev.ToolCalls[0].Name != "Bash" || ev.ToolCalls[0].ID != "call_abc" {
		t.Fatalf("toolcalls=%+v", ev.ToolCalls)
	}
}

func TestParseSSEPayloadUsage(t *testing.T) {
	ev, ok, err := parseSSEPayload(sseFrame(t, map[string]any{
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": ""}, "finish_reason": "stop"}},
		"usage": map[string]any{
			"prompt_tokens":             20095,
			"completion_tokens":         28,
			"total_tokens":              20123,
			"credits":                   0.5841,
			"original_credits":          0.5841,
			"billable":                  true,
			"prompt_tokens_details":     map[string]any{"cached_tokens": 20009},
			"completion_tokens_details": map[string]any{"reasoning_tokens": 11},
		},
	}))
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if ev.Usage == nil {
		t.Fatal("usage nil")
	}
	if ev.Usage.PromptTokens != 20095 || ev.Usage.CompletionTokens != 28 || ev.Usage.TotalTokens != 20123 {
		t.Fatalf("tokens=%+v", ev.Usage)
	}
	if ev.Usage.PromptTokensDetails.CachedTokens != 20009 {
		t.Fatalf("cached=%d", ev.Usage.PromptTokensDetails.CachedTokens)
	}
	if ev.Usage.CompletionTokensDetails.ReasoningTokens != 11 {
		t.Fatalf("reasoning=%d", ev.Usage.CompletionTokensDetails.ReasoningTokens)
	}
	if ev.FinishReason != "stop" {
		t.Fatalf("finish=%q", ev.FinishReason)
	}
}
