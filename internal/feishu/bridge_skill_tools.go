package feishu

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var (
	skillHTTPURLPattern      = regexp.MustCompile("https?://[^\\s<>\"'`)]+")
	skillHTTPEndpointPattern = regexp.MustCompile("`(/[^`\\s]+)`")
)

func (m *Manager) executeBridgeSkillTool(ctx context.Context, conversationKey, toolCallID, toolName string, args map[string]any) ToolExecutionResult {
	if m.skillService == nil {
		return ToolExecutionResult{Output: "[error] Skill 服务未初始化", IsError: true}
	}
	switch toolName {
	case "skill_list":
		skills := m.skillService.List(true)
		if len(skills) == 0 {
			return ToolExecutionResult{Output: "当前没有启用的用户导入 Skill。"}
		}
		lines := []string{"已启用 Skills："}
		for _, skill := range skills {
			lines = append(lines, fmt.Sprintf("- %s（%s）：%s", skill.Name, skill.ID, summarizeText(skill.Description, 180)))
		}
		return ToolExecutionResult{Output: strings.Join(lines, "\n")}
	case "skill_search":
		query := strings.ToLower(strings.TrimSpace(stringArg(args, "query")))
		if query == "" {
			return ToolExecutionResult{Output: "[error] query 不能为空", IsError: true}
		}
		var lines []string
		for _, skill := range m.skillService.List(true) {
			haystack := strings.ToLower(strings.Join([]string{skill.Name, skill.Description, skill.WhenToUse}, "\n"))
			if strings.Contains(haystack, query) {
				lines = append(lines, fmt.Sprintf("- %s（%s）：%s", skill.Name, skill.ID, summarizeText(skill.Description, 220)))
			}
		}
		if len(lines) == 0 {
			return ToolExecutionResult{Output: "没有找到匹配的 Skill。"}
		}
		return ToolExecutionResult{Output: "匹配 Skills：\n" + strings.Join(lines, "\n")}
	case "skill_view":
		fullBody, skill, err := m.skillService.SkillBody(stringArg(args, "name"))
		if err != nil {
			return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
		}
		if m.store != nil {
			_ = m.store.SaveSkillInvocation(ctx, conversationKey, skill.ID, skill.Name, toolCallID)
		}
		m.markTurnSkillViewed(conversationKey, skill)
		chunked := chunkAndAnnotateSkillBody(fullBody, args)
		return ToolExecutionResult{Output: renderSkillViewOutput(skill, chunked)}
	case "skill_run_script":
		skillName := stringArg(args, "skill")
		script := filepath.Base(stringArg(args, "script"))
		if skillName == "" || script == "" {
			return ToolExecutionResult{Output: "[error] skill 和 script 不能为空", IsError: true}
		}
		skill, ok := m.skillService.Find(skillName)
		if !ok {
			return ToolExecutionResult{Output: "[error] skill not found: " + skillName, IsError: true}
		}
		if !m.isTurnSkillViewed(conversationKey, skillName) {
			return ToolExecutionResult{Output: fmt.Sprintf("[error] 使用 Skill `%s` 执行脚本前必须先调用 skill_view 阅读 SKILL.md。下一步：调用 skill_view({\"name\":\"%s\"})，根据其中 Scripts 列表选择脚本后再执行。", skill.Name, skill.ID), IsError: true}
		}
		if len(skill.Scripts) == 0 {
			return ToolExecutionResult{Output: "[error] 该 Skill 没有 scripts/ 目录或脚本。请先用 skill_view 阅读 SKILL.md；如果文档要求 curl/HTTP API，请改用 skill_http_request。", IsError: true}
		}
		if m.isTurnSkillScriptApproved(conversationKey, skillName) {
			output, err := m.runSkillScriptCommand(ctx, skillName, script, stringListArg(args, "args"))
			if err != nil {
				return ToolExecutionResult{Output: "[error] " + err.Error() + "\n\n输出：\n" + output, IsError: true}
			}
			return ToolExecutionResult{Output: output}
		}
		return ToolExecutionResult{
			Output:  fmt.Sprintf("执行 Skill 脚本需要用户显式确认。请用户发送：\n/skill-run %s %s confirm\n\n确认前不要声称脚本已执行。", skillName, script),
			IsError: true,
		}
	case "skill_http_get":
		return m.executeSkillHTTPRequest(ctx, conversationKey, args)
	case "skill_http_request":
		return m.executeSkillHTTPRequest(ctx, conversationKey, args)
	default:
		return ToolExecutionResult{Output: "[error] unknown skill tool: " + toolName, IsError: true}
	}
}

func (m *Manager) executeSkillHTTPRequest(ctx context.Context, conversationKey string, args map[string]any) ToolExecutionResult {
	if m.skillService == nil {
		return ToolExecutionResult{Output: "[error] Skill 服务未初始化", IsError: true}
	}
	skillName := stringArg(args, "skill")
	if skillName == "" {
		return ToolExecutionResult{Output: "[error] skill 不能为空", IsError: true}
	}
	skill, ok := m.skillService.Find(skillName)
	if !ok {
		return ToolExecutionResult{Output: "[error] skill not found: " + skillName, IsError: true}
	}
	if !m.isTurnSkillViewed(conversationKey, skillName) {
		return ToolExecutionResult{Output: fmt.Sprintf("[error] 使用 Skill `%s` 发起 HTTP 请求前必须先调用 skill_view 阅读 SKILL.md。下一步：调用 skill_view({\"name\":\"%s\"})，再使用文档里的 Base URL / endpoint；不要自造域名或路径。", skill.Name, skill.ID), IsError: true}
	}
	docBody, _, err := m.skillService.SkillBody(skillName)
	if err != nil {
		return ToolExecutionResult{Output: "[error] 读取 Skill 文档失败：" + err.Error(), IsError: true}
	}
	rawURL := strings.TrimSpace(stringArg(args, "url"))
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed == nil || parsed.Host == "" {
		return ToolExecutionResult{Output: "[error] URL 无效", IsError: true}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ToolExecutionResult{Output: "[error] 只支持 http/https", IsError: true}
	}
	if err := validateSkillHTTPURLFromDocument(skill, docBody, parsed); err != nil {
		return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
	}
	method := strings.ToUpper(strings.TrimSpace(stringArg(args, "method")))
	if method == "" {
		method = http.MethodGet
	}
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return ToolExecutionResult{Output: "[error] unsupported HTTP method: " + method, IsError: true}
	}
	if err := validateSkillHTTPHost(ctx, parsed.Hostname()); err != nil {
		return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
	}
	cfg := m.Config().Context
	timeoutSeconds := cfg.SkillHTTPTimeout
	if timeoutSeconds <= 0 {
		timeoutSeconds = defaultSkillHTTPTimeout
	}
	maxBytes := cfg.SkillHTTPMaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultSkillHTTPMaxBytes
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	var requestBody io.Reader
	if value := stringArg(args, "body"); value != "" {
		requestBody = bytes.NewReader([]byte(value))
	}
	req, err := http.NewRequestWithContext(runCtx, method, parsed.String(), requestBody)
	if err != nil {
		return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; FeishuBridgeSkill/1.0)")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if headers, ok := args["headers"].(map[string]any); ok {
		for key, value := range headers {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			req.Header.Set(key, fmt.Sprint(value))
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)+1))
	if err != nil {
		return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
	}
	text := string(data)
	if len(data) > maxBytes {
		text = text[:maxBytes] + "\n... [truncated]"
	}
	prefix := fmt.Sprintf("HTTP %d %s\nURL: %s\n\n", resp.StatusCode, resp.Status, parsed.String())
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ToolExecutionResult{Output: prefix + text, IsError: true}
	}
	if looksLikeHTMLResponse(resp, text) {
		return ToolExecutionResult{Output: prefix + "[error] Skill HTTP 返回了 HTML 页面，不像 API 数据。下一步：重新调用 skill_view 阅读 SKILL.md，并改用文档里的 Base URL / endpoint；不要把站点域名改成 API 子域，也不要访问网页页面。", IsError: true}
	}
	return ToolExecutionResult{Output: prefix + text}
}

const (
	defaultSkillViewChunkChars = 6000
	maxSkillViewChunkChars     = 12000
	minSkillViewChunkChars     = 1000
)

type skillChunkResult struct {
	text    string
	total   int
	offset  int
	end     int
	hasMore bool
}

func chunkAndAnnotateSkillBody(body string, args map[string]any) skillChunkResult {
	runes := []rune(body)
	total := len(runes)
	offset := intFromAny(args["offset"])
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	chunkSize := intFromAny(args["chunk_size"])
	if chunkSize <= 0 {
		chunkSize = defaultSkillViewChunkChars
	}
	if chunkSize < minSkillViewChunkChars {
		chunkSize = minSkillViewChunkChars
	}
	if chunkSize > maxSkillViewChunkChars {
		chunkSize = maxSkillViewChunkChars
	}
	end := offset + chunkSize
	if end > total {
		end = total
	}
	return skillChunkResult{
		text:    string(runes[offset:end]),
		total:   total,
		offset:  offset,
		end:     end,
		hasMore: end < total,
	}
}

func renderSkillViewOutput(skill BridgeSkill, chunk skillChunkResult) string {
	scripts := "无"
	if len(skill.Scripts) > 0 {
		scripts = strings.Join(skill.Scripts, ", ")
	}
	body := chunk.text
	urls := extractSkillDocumentURLs(body)
	endpoints := extractSkillDocumentEndpoints(body)
	baseURLs := uniqueSkillStrings(urls)
	if len(baseURLs) > 8 {
		baseURLs = baseURLs[:8]
	}
	if len(endpoints) > 12 {
		endpoints = endpoints[:12]
	}
	requiredHeaders := "未发现"
	if strings.Contains(strings.ToLower(body), "user-agent") {
		requiredHeaders = "文档提到 User-Agent，请按 SKILL.md 要求设置。"
	}
	if strings.TrimSpace(body) == "" {
		body = "[empty SKILL.md]"
	}
	out := fmt.Sprintf(
		"Skill: %s\nID: %s\nPath: %s\nScripts: %s\nAllowed tools: %s\nDisable model invocation: %t\n\n结构化摘要：\n- Base URL: %s\n- 推荐端点/路径: %s\n- 必须请求头: %s\n- 执行约束: 后续 skill_http_request 的 URL 必须来自本 SKILL.md；不要自造域名、路径或把站点域名改成 API 子域。执行脚本前必须确认 scripts 列表中实际存在该脚本。\n\nSKILL.md：\n%s",
		skill.Name,
		skill.ID,
		skill.Path,
		scripts,
		joinOrNone(skill.AllowedTools),
		skill.DisableModelInvocation,
		joinOrNone(baseURLs),
		joinOrNone(endpoints),
		requiredHeaders,
		body,
	)
	if chunk.hasMore {
		out += fmt.Sprintf(
			"\n\nbridge_reading:\n"+
				"  kind: skill_body_chunk\n"+
				"  offset: %d\n"+
				"  end_offset: %d\n"+
				"  next_offset: %d\n"+
				"  chunk_chars: %d\n"+
				"  total_chars: %d\n"+
				"  has_more: true\n"+
				"  instruction: 如果用户要求完整阅读该 Skill，必须继续调用 skill_view 并把 offset 设为 next_offset，直到 has_more=false。禁止基于当前分块推断未读取内容。",
			chunk.offset, chunk.end, chunk.end, chunk.end-chunk.offset, chunk.total,
		)
	}
	return out
}

func validateSkillHTTPURLFromDocument(skill BridgeSkill, body string, parsed *url.URL) error {
	if parsed == nil {
		return fmt.Errorf("URL 无效")
	}
	docURLs := extractSkillDocumentURLs(body)
	if len(docURLs) == 0 {
		return fmt.Errorf("Skill `%s` 的 SKILL.md 没有可追溯的 http/https URL。下一步：先 skill_view 检查文档；如果确实需要访问未列出的 URL，请让用户明确确认。", skill.Name)
	}
	requestHost := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	requestPath := normalizedURLPath(parsed.Path)
	hostMatched := false
	var documentedPrefixes []string
	for _, raw := range docURLs {
		docURL, err := url.Parse(raw)
		if err != nil || docURL == nil || docURL.Host == "" {
			continue
		}
		if !strings.EqualFold(strings.TrimSuffix(docURL.Hostname(), "."), requestHost) {
			continue
		}
		hostMatched = true
		if prefix := normalizedDocumentPathPrefix(docURL.Path); prefix != "" && prefix != "/" {
			documentedPrefixes = append(documentedPrefixes, prefix)
		}
	}
	if !hostMatched {
		return fmt.Errorf("URL host `%s` 不在 Skill `%s` 的 SKILL.md URL allowlist 中。下一步：重新 skill_view，并使用文档里的 Base URL；不要自造域名或切换 API 子域。", requestHost, skill.Name)
	}
	endpoints := extractSkillDocumentEndpoints(body)
	for _, endpoint := range endpoints {
		if prefix := normalizedDocumentPathPrefix(endpoint); prefix != "" && prefix != "/" {
			documentedPrefixes = append(documentedPrefixes, prefix)
		}
	}
	documentedPrefixes = uniqueSkillStrings(documentedPrefixes)
	if len(documentedPrefixes) == 0 {
		return nil
	}
	for _, prefix := range documentedPrefixes {
		if pathHasPrefix(requestPath, prefix) {
			return nil
		}
	}
	return fmt.Errorf("URL path `%s` 不在 Skill `%s` 的 SKILL.md 推荐端点中。下一步：重新 skill_view，然后使用文档里的 endpoint，例如：%s。", requestPath, skill.Name, strings.Join(documentedPrefixes[:minInt(len(documentedPrefixes), 4)], ", "))
}

func extractSkillDocumentURLs(body string) []string {
	matches := skillHTTPURLPattern.FindAllString(body, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		cleaned := strings.Trim(match, " \t\r\n.,;，。；、")
		if parsed, err := url.Parse(cleaned); err == nil && parsed != nil && parsed.Host != "" {
			out = append(out, cleaned)
		}
	}
	return uniqueSkillStrings(out)
}

func extractSkillDocumentEndpoints(body string) []string {
	matches := skillHTTPEndpointPattern.FindAllStringSubmatch(body, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		path := strings.TrimSpace(match[1])
		if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/v1/") || strings.HasPrefix(path, "/v2/") {
			out = append(out, path)
		}
	}
	return uniqueSkillStrings(out)
}

func normalizedURLPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func normalizedDocumentPathPrefix(path string) string {
	path = normalizedURLPath(path)
	if idx := strings.Index(path, "{"); idx >= 0 {
		path = path[:idx]
	}
	path = strings.TrimRight(path, "/")
	if path == "" {
		return "/"
	}
	return path
}

func pathHasPrefix(path string, prefix string) bool {
	path = normalizedURLPath(path)
	prefix = normalizedDocumentPathPrefix(prefix)
	return path == prefix || strings.HasPrefix(path, strings.TrimRight(prefix, "/")+"/")
}

func looksLikeHTMLResponse(resp *http.Response, text string) bool {
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	trimmed := strings.ToLower(strings.TrimSpace(text))
	return strings.Contains(contentType, "text/html") ||
		strings.HasPrefix(trimmed, "<!doctype html") ||
		strings.HasPrefix(trimmed, "<html") ||
		strings.Contains(trimmed, "<body") ||
		strings.Contains(trimmed, "subdomain is not configured") ||
		strings.Contains(trimmed, "login")
}

func uniqueSkillStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func joinOrNone(values []string) string {
	if len(values) == 0 {
		return "未发现"
	}
	return strings.Join(values, ", ")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func validateSkillHTTPHost(ctx context.Context, hostname string) error {
	hostname = strings.TrimSpace(strings.TrimSuffix(hostname, "."))
	if hostname == "" {
		return fmt.Errorf("URL host 不能为空")
	}
	lower := strings.ToLower(hostname)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return fmt.Errorf("Skill HTTP 默认不允许访问 localhost")
	}
	if ip := net.ParseIP(hostname); ip != nil {
		if isBlockedSkillHTTPIP(ip) {
			return fmt.Errorf("Skill HTTP 默认不允许访问内网或本机地址: %s", hostname)
		}
		return nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(lookupCtx, hostname)
	if err != nil {
		return fmt.Errorf("解析 Skill HTTP host 失败: %w", err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("解析 Skill HTTP host 为空")
	}
	for _, addr := range addrs {
		if isBlockedSkillHTTPIP(addr.IP) {
			return fmt.Errorf("Skill HTTP 默认不允许访问解析到内网或本机地址的 host: %s", hostname)
		}
	}
	return nil
}

func isBlockedSkillHTTPIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 169 && ip4[1] == 254 {
			return true
		}
		return false
	}
	return false
}

func (m *Manager) runSkillScriptCommand(ctx context.Context, skillName, scriptName string, args []string) (string, error) {
	if m.skillService == nil {
		return "", fmt.Errorf("Skill 服务未初始化")
	}
	skill, ok := m.skillService.Find(skillName)
	if !ok {
		return "", fmt.Errorf("skill not found: %s", skillName)
	}
	if !skill.Enabled {
		return "", fmt.Errorf("skill is disabled: %s", skill.Name)
	}
	scriptName = filepath.Base(strings.TrimSpace(scriptName))
	if scriptName == "" {
		return "", fmt.Errorf("script is required")
	}
	scriptPath := filepath.Join(skill.Path, "scripts", scriptName)
	if !isPathInside(filepath.Join(skill.Path, "scripts"), scriptPath) {
		return "", fmt.Errorf("invalid script path")
	}
	realScriptsRoot, err := filepath.EvalSymlinks(filepath.Join(skill.Path, "scripts"))
	if err != nil {
		return "", err
	}
	realScript, err := filepath.EvalSymlinks(scriptPath)
	if err != nil {
		return "", err
	}
	if !isPathInside(realScriptsRoot, realScript) {
		return "", fmt.Errorf("script symlink escapes scripts directory")
	}
	runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmdArgs := append([]string{realScript}, args...)
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(runCtx, "cmd", append([]string{"/C"}, cmdArgs...)...)
	} else {
		cmd = exec.CommandContext(runCtx, "sh", cmdArgs...)
	}
	cmd.Dir = skill.Path
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(decodeCommandOutput(output))
	if runCtx.Err() == context.DeadlineExceeded {
		return text, fmt.Errorf("script timed out")
	}
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, err
	}
	if text == "" {
		text = "[no output]"
	}
	return text, nil
}

func (m *Manager) setTurnSkillScriptApprovals(conversationKey string, text string) {
	if m.skillService == nil || strings.TrimSpace(conversationKey) == "" {
		return
	}
	textLower := strings.ToLower(strings.TrimSpace(text))
	if textLower == "" || !looksLikeExplicitSkillUse(textLower) {
		return
	}
	approved := map[string]struct{}{}
	for _, skill := range m.skillService.List(true) {
		if skillMentionedInText(textLower, skill.Name) || skillMentionedInText(textLower, skill.ID) {
			approved[strings.ToLower(skill.Name)] = struct{}{}
			approved[strings.ToLower(skill.ID)] = struct{}{}
		}
	}
	if len(approved) == 0 {
		return
	}
	m.mu.Lock()
	if m.skillApprovals == nil {
		m.skillApprovals = map[string]map[string]struct{}{}
	}
	m.skillApprovals[conversationKey] = approved
	m.mu.Unlock()
}

func (m *Manager) clearTurnSkillState(conversationKey string) {
	if strings.TrimSpace(conversationKey) == "" {
		return
	}
	m.mu.Lock()
	delete(m.skillApprovals, conversationKey)
	delete(m.skillViews, conversationKey)
	m.mu.Unlock()
}

func (m *Manager) clearTurnSkillScriptApprovals(conversationKey string) {
	m.clearTurnSkillState(conversationKey)
}

func (m *Manager) markTurnSkillViewed(conversationKey string, skill BridgeSkill) {
	if strings.TrimSpace(conversationKey) == "" {
		return
	}
	m.mu.Lock()
	if m.skillViews == nil {
		m.skillViews = map[string]map[string]struct{}{}
	}
	viewed := m.skillViews[conversationKey]
	if viewed == nil {
		viewed = map[string]struct{}{}
		m.skillViews[conversationKey] = viewed
	}
	viewed[strings.ToLower(skill.Name)] = struct{}{}
	viewed[strings.ToLower(skill.ID)] = struct{}{}
	m.mu.Unlock()
}

func (m *Manager) isTurnSkillViewed(conversationKey string, skillName string) bool {
	key := strings.ToLower(strings.TrimSpace(skillName))
	if strings.TrimSpace(conversationKey) == "" || key == "" {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	viewed := m.skillViews[conversationKey]
	if len(viewed) == 0 {
		return false
	}
	if _, ok := viewed[key]; ok {
		return true
	}
	if m.skillService != nil {
		if skill, ok := m.skillService.Find(skillName); ok {
			_, byName := viewed[strings.ToLower(skill.Name)]
			_, byID := viewed[strings.ToLower(skill.ID)]
			return byName || byID
		}
	}
	return false
}

func (m *Manager) isTurnSkillScriptApproved(conversationKey string, skillName string) bool {
	key := strings.ToLower(strings.TrimSpace(skillName))
	if key == "" {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	approved := m.skillApprovals[conversationKey]
	if len(approved) == 0 {
		return false
	}
	if _, ok := approved[key]; ok {
		return true
	}
	if m.skillService != nil {
		if skill, ok := m.skillService.Find(skillName); ok {
			_, byName := approved[strings.ToLower(skill.Name)]
			_, byID := approved[strings.ToLower(skill.ID)]
			return byName || byID
		}
	}
	return false
}

func looksLikeExplicitSkillUse(textLower string) bool {
	for _, cue := range []string{"使用", "用", "调用", "运行", "执行", "查", "查询", "获取", "看一下", "帮我"} {
		if strings.Contains(textLower, strings.ToLower(cue)) {
			return true
		}
	}
	return false
}

func skillMentionedInText(textLower string, value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value != "" && strings.Contains(textLower, value)
}
