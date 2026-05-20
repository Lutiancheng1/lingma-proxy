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
	images, ok := payload["image_urls"].([]any)
	if !ok || len(images) != 1 {
		t.Fatalf("image_urls = %#v", payload["image_urls"])
	}
	image, ok := images[0].(string)
	if !ok || !strings.HasPrefix(image, "data:image/png;base64,") {
		t.Fatalf("unexpected image projection: %#v", images[0])
	}
	modelConfig := payload["model_config"].(map[string]any)
	if modelConfig["is_vl"] != true {
		t.Fatalf("model_config.is_vl = %#v, want true", modelConfig["is_vl"])
	}
	messages := payload["messages"].([]any)
	message := messages[0].(map[string]any)
	content := message["content"].([]any)
	if content[0].(map[string]any)["type"] != "text" || content[1].(map[string]any)["type"] != "image_url" {
		t.Fatalf("unexpected message content: %#v", content)
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
