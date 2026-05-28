package feishu

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	promptPackChannel       = "feishu-stable"
	promptPackSchemaVersion = 1
	maxPromptPackBytes      = 1 << 20
)

var promptRuleOrder = []string{
	"base",
	"feishu_cli_rules",
	"tool_error_recovery",
	"long_document_rules",
	"permission_rules",
	"skill_rules",
}

//go:embed prompt_rules/*.md
var embeddedPromptRules embed.FS

type PromptPackOptions struct {
	ManifestURL     string
	PublicKeyBase64 string
	DataDir         string
	AppVersion      string
}

type PromptPackStatus struct {
	Enabled       bool   `json:"enabled"`
	Channel       string `json:"channel"`
	Version       string `json:"version"`
	Source        string `json:"source"`
	ManifestURL   string `json:"manifestUrl,omitempty"`
	LastCheckedAt string `json:"lastCheckedAt,omitempty"`
	LastAppliedAt string `json:"lastAppliedAt,omitempty"`
	LastError     string `json:"lastError,omitempty"`
	Notes         string `json:"notes,omitempty"`
}

type promptPackManifest struct {
	SchemaVersion int    `json:"schemaVersion"`
	Channel       string `json:"channel"`
	Version       string `json:"version"`
	MinAppVersion string `json:"minAppVersion,omitempty"`
	URL           string `json:"url"`
	SHA256        string `json:"sha256"`
	Signature     string `json:"signature"`
	PublishedAt   string `json:"publishedAt,omitempty"`
	Notes         string `json:"notes,omitempty"`
}

type promptPackFile struct {
	SchemaVersion int               `json:"schemaVersion"`
	Channel       string            `json:"channel"`
	Version       string            `json:"version"`
	MinAppVersion string            `json:"minAppVersion,omitempty"`
	Modules       map[string]string `json:"modules"`
	ModuleOrder   []string          `json:"moduleOrder,omitempty"`
	PublishedAt   string            `json:"publishedAt,omitempty"`
	Notes         string            `json:"notes,omitempty"`
}

type promptPackManager struct {
	mu          sync.RWMutex
	options     PromptPackOptions
	status      PromptPackStatus
	active      *promptPackFile
	embedded    *promptPackFile
	httpClient  *http.Client
	initialized bool
}

var globalPromptPack = &promptPackManager{
	httpClient: &http.Client{Timeout: 20 * time.Second},
}

func ConfigurePromptPack(opts PromptPackOptions) {
	globalPromptPack.configure(opts)
}

func GetPromptPackStatus() PromptPackStatus {
	return globalPromptPack.Status()
}

func CheckPromptPackUpdate(ctx context.Context) (PromptPackStatus, error) {
	return globalPromptPack.Check(ctx)
}

func activePromptRulesText() string {
	return globalPromptPack.Render()
}

func (m *promptPackManager) configure(opts PromptPackOptions) {
	m.mu.Lock()
	defer m.mu.Unlock()
	opts.ManifestURL = strings.TrimSpace(opts.ManifestURL)
	opts.PublicKeyBase64 = strings.TrimSpace(opts.PublicKeyBase64)
	opts.DataDir = strings.TrimSpace(opts.DataDir)
	opts.AppVersion = strings.TrimSpace(opts.AppVersion)
	m.options = opts
	m.embedded = embeddedPromptPack()
	m.active = nil
	m.status = PromptPackStatus{
		Enabled:     opts.ManifestURL != "" && opts.PublicKeyBase64 != "",
		Channel:     promptPackChannel,
		Version:     m.embedded.Version,
		Source:      "embedded",
		ManifestURL: opts.ManifestURL,
	}
	if cached, err := m.loadCacheLocked(); err == nil && cached != nil {
		m.active = cached
		m.status.Version = cached.Version
		m.status.Source = "cache"
		m.status.Notes = cached.Notes
	}
	m.initialized = true
}

func (m *promptPackManager) Status() PromptPackStatus {
	m.ensureInitialized()
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

func (m *promptPackManager) Render() string {
	m.ensureInitialized()
	m.mu.RLock()
	defer m.mu.RUnlock()
	pack := m.embedded
	if m.active != nil {
		pack = m.active
	}
	return renderPromptPack(pack)
}

func (m *promptPackManager) Check(ctx context.Context) (PromptPackStatus, error) {
	m.ensureInitialized()
	m.mu.RLock()
	opts := m.options
	m.mu.RUnlock()
	now := time.Now().Format(time.RFC3339)
	if strings.TrimSpace(opts.ManifestURL) == "" {
		return m.setPromptPackError(now, "未配置 Prompt Pack manifest URL"), errors.New("prompt pack manifest URL is not configured")
	}
	if strings.TrimSpace(opts.PublicKeyBase64) == "" {
		return m.setPromptPackError(now, "未配置 Prompt Pack 签名公钥"), errors.New("prompt pack public key is not configured")
	}
	manifest, err := m.fetchManifest(ctx, opts.ManifestURL)
	if err != nil {
		return m.setPromptPackError(now, err.Error()), err
	}
	packBytes, err := m.fetchPack(ctx, manifest.URL)
	if err != nil {
		return m.setPromptPackError(now, err.Error()), err
	}
	if err := verifyPromptPackDownload(packBytes, manifest, opts.PublicKeyBase64); err != nil {
		return m.setPromptPackError(now, err.Error()), err
	}
	var pack promptPackFile
	if err := json.Unmarshal(packBytes, &pack); err != nil {
		return m.setPromptPackError(now, "Prompt Pack JSON 解析失败："+err.Error()), err
	}
	if err := validatePromptPack(pack, opts.AppVersion); err != nil {
		return m.setPromptPackError(now, err.Error()), err
	}
	if err := m.saveCache(packBytes); err != nil {
		return m.setPromptPackError(now, "Prompt Pack 缓存写入失败："+err.Error()), err
	}
	applied := time.Now().Format(time.RFC3339)
	m.mu.Lock()
	m.active = &pack
	m.status = PromptPackStatus{
		Enabled:       true,
		Channel:       pack.Channel,
		Version:       pack.Version,
		Source:        "remote",
		ManifestURL:   opts.ManifestURL,
		LastCheckedAt: now,
		LastAppliedAt: applied,
		Notes:         firstNonEmptyString(pack.Notes, manifest.Notes),
	}
	status := m.status
	m.mu.Unlock()
	return status, nil
}

func (m *promptPackManager) ensureInitialized() {
	m.mu.RLock()
	initialized := m.initialized
	m.mu.RUnlock()
	if initialized {
		return
	}
	m.configure(PromptPackOptions{})
}

func (m *promptPackManager) setPromptPackError(checkedAt string, message string) PromptPackStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.LastCheckedAt = checkedAt
	m.status.LastError = message
	return m.status
}

func (m *promptPackManager) fetchManifest(ctx context.Context, manifestURL string) (promptPackManifest, error) {
	data, err := fetchSmallHTTPSJSON(ctx, m.httpClient, manifestURL, maxPromptPackBytes)
	if err != nil {
		return promptPackManifest{}, err
	}
	var manifest promptPackManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return promptPackManifest{}, fmt.Errorf("Prompt Pack manifest 解析失败: %w", err)
	}
	if manifest.SchemaVersion != promptPackSchemaVersion {
		return promptPackManifest{}, fmt.Errorf("不支持的 Prompt Pack manifest schemaVersion: %d", manifest.SchemaVersion)
	}
	if manifest.Channel != promptPackChannel {
		return promptPackManifest{}, fmt.Errorf("Prompt Pack 通道不匹配: %s", manifest.Channel)
	}
	if strings.TrimSpace(manifest.URL) == "" || strings.TrimSpace(manifest.SHA256) == "" || strings.TrimSpace(manifest.Signature) == "" {
		return promptPackManifest{}, errors.New("Prompt Pack manifest 缺少 url/sha256/signature")
	}
	return manifest, nil
}

func (m *promptPackManager) fetchPack(ctx context.Context, packURL string) ([]byte, error) {
	return fetchSmallHTTPSJSON(ctx, m.httpClient, packURL, maxPromptPackBytes)
}

func fetchSmallHTTPSJSON(ctx context.Context, client *http.Client, rawURL string, limit int64) ([]byte, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, fmt.Errorf("只允许 HTTPS URL: %s", rawURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, io.LimitReader(resp.Body, limit+1)); err != nil {
		return nil, err
	}
	if int64(buf.Len()) > limit {
		return nil, fmt.Errorf("下载内容超过上限 %d bytes", limit)
	}
	return buf.Bytes(), nil
}

func verifyPromptPackDownload(data []byte, manifest promptPackManifest, publicKeyBase64 string) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, strings.TrimSpace(manifest.SHA256)) {
		return fmt.Errorf("Prompt Pack sha256 不匹配: got %s", got)
	}
	keyBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(publicKeyBase64))
	if err != nil || len(keyBytes) != ed25519.PublicKeySize {
		return errors.New("Prompt Pack 签名公钥无效")
	}
	sigBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(manifest.Signature))
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return errors.New("Prompt Pack 签名无效")
	}
	if !ed25519.Verify(ed25519.PublicKey(keyBytes), []byte(manifest.SHA256), sigBytes) {
		return errors.New("Prompt Pack 签名校验失败")
	}
	return nil
}

func validatePromptPack(pack promptPackFile, appVersion string) error {
	if pack.SchemaVersion != promptPackSchemaVersion {
		return fmt.Errorf("不支持的 Prompt Pack schemaVersion: %d", pack.SchemaVersion)
	}
	if pack.Channel != promptPackChannel {
		return fmt.Errorf("Prompt Pack 通道不匹配: %s", pack.Channel)
	}
	if strings.TrimSpace(pack.Version) == "" {
		return errors.New("Prompt Pack 缺少 version")
	}
	if !minVersionSatisfied(appVersion, pack.MinAppVersion) {
		return fmt.Errorf("Prompt Pack 需要 App >= %s", pack.MinAppVersion)
	}
	for _, name := range promptRuleOrder {
		if strings.TrimSpace(pack.Modules[name]) == "" {
			return fmt.Errorf("Prompt Pack 缺少模块: %s", name)
		}
	}
	for name := range pack.Modules {
		if !isAllowedPromptModule(name) {
			return fmt.Errorf("Prompt Pack 包含未允许模块: %s", name)
		}
	}
	return nil
}

func isAllowedPromptModule(name string) bool {
	for _, allowed := range promptRuleOrder {
		if name == allowed {
			return true
		}
	}
	return false
}

func embeddedPromptPack() *promptPackFile {
	modules := map[string]string{}
	for _, name := range promptRuleOrder {
		data, err := embeddedPromptRules.ReadFile("prompt_rules/" + name + ".md")
		if err != nil {
			continue
		}
		modules[name] = strings.TrimSpace(string(data))
	}
	return &promptPackFile{
		SchemaVersion: promptPackSchemaVersion,
		Channel:       promptPackChannel,
		Version:       "embedded",
		Modules:       modules,
		ModuleOrder:   append([]string(nil), promptRuleOrder...),
	}
}

func renderPromptPack(pack *promptPackFile) string {
	if pack == nil {
		return strings.TrimSpace(baseSystemPrompt)
	}
	order := pack.ModuleOrder
	if len(order) == 0 {
		order = promptRuleOrder
	}
	parts := make([]string, 0, len(order))
	for _, name := range order {
		if !isAllowedPromptModule(name) {
			continue
		}
		text := strings.TrimSpace(pack.Modules[name])
		if text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return strings.TrimSpace(baseSystemPrompt)
	}
	return strings.Join(parts, "\n\n")
}

func (m *promptPackManager) cachePath() string {
	if strings.TrimSpace(m.options.DataDir) == "" {
		return ""
	}
	return filepath.Join(m.options.DataDir, "prompt-pack-cache.json")
}

func (m *promptPackManager) loadCacheLocked() (*promptPackFile, error) {
	path := m.cachePath()
	if path == "" {
		return nil, os.ErrNotExist
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var pack promptPackFile
	if err := json.Unmarshal(data, &pack); err != nil {
		return nil, err
	}
	if err := validatePromptPack(pack, m.options.AppVersion); err != nil {
		return nil, err
	}
	return &pack, nil
}

func (m *promptPackManager) saveCache(data []byte) error {
	path := m.cachePath()
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func minVersionSatisfied(current, min string) bool {
	current = strings.TrimPrefix(strings.TrimSpace(current), "v")
	min = strings.TrimPrefix(strings.TrimSpace(min), "v")
	if min == "" || current == "" {
		return true
	}
	return compareVersion(current, min) >= 0
}

func compareVersion(a, b string) int {
	ap := parseVersionParts(a)
	bp := parseVersionParts(b)
	for i := 0; i < len(ap) || i < len(bp); i++ {
		av, bv := 0, 0
		if i < len(ap) {
			av = ap[i]
		}
		if i < len(bp) {
			bv = bp[i]
		}
		if av > bv {
			return 1
		}
		if av < bv {
			return -1
		}
	}
	return 0
}

func parseVersionParts(version string) []int {
	fields := strings.FieldsFunc(version, func(r rune) bool {
		return r == '.' || r == '-' || r == '_'
	})
	parts := make([]int, 0, len(fields))
	for _, field := range fields {
		n := 0
		for _, r := range field {
			if r < '0' || r > '9' {
				break
			}
			n = n*10 + int(r-'0')
		}
		parts = append(parts, n)
	}
	return parts
}
