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
	"runtime"
	"strings"
	"time"
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
		body, skill, err := m.skillService.SkillBody(stringArg(args, "name"))
		if err != nil {
			return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
		}
		if m.store != nil {
			_ = m.store.SaveSkillInvocation(ctx, conversationKey, skill.ID, skill.Name, toolCallID)
		}
		if len(body) > 12000 {
			body = body[:12000] + "\n... [truncated]"
		}
		scripts := "无"
		if len(skill.Scripts) > 0 {
			scripts = strings.Join(skill.Scripts, ", ")
		}
		return ToolExecutionResult{Output: fmt.Sprintf("Skill: %s\nPath: %s\nScripts: %s\n\n%s", skill.Name, skill.Path, scripts, body)}
	case "skill_run_script":
		skillName := stringArg(args, "skill")
		script := filepath.Base(stringArg(args, "script"))
		if skillName == "" || script == "" {
			return ToolExecutionResult{Output: "[error] skill 和 script 不能为空", IsError: true}
		}
		if skill, ok := m.skillService.Find(skillName); ok && len(skill.Scripts) == 0 {
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
		return m.executeSkillHTTPRequest(ctx, args)
	case "skill_http_request":
		return m.executeSkillHTTPRequest(ctx, args)
	default:
		return ToolExecutionResult{Output: "[error] unknown skill tool: " + toolName, IsError: true}
	}
}

func (m *Manager) executeSkillHTTPRequest(ctx context.Context, args map[string]any) ToolExecutionResult {
	if m.skillService == nil {
		return ToolExecutionResult{Output: "[error] Skill 服务未初始化", IsError: true}
	}
	skillName := stringArg(args, "skill")
	if skillName == "" {
		return ToolExecutionResult{Output: "[error] skill 不能为空", IsError: true}
	}
	if _, ok := m.skillService.Find(skillName); !ok {
		return ToolExecutionResult{Output: "[error] skill not found: " + skillName, IsError: true}
	}
	rawURL := strings.TrimSpace(stringArg(args, "url"))
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed == nil || parsed.Host == "" {
		return ToolExecutionResult{Output: "[error] URL 无效", IsError: true}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ToolExecutionResult{Output: "[error] 只支持 http/https", IsError: true}
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
	var body io.Reader
	if value := stringArg(args, "body"); value != "" {
		body = bytes.NewReader([]byte(value))
	}
	req, err := http.NewRequestWithContext(runCtx, method, parsed.String(), body)
	if err != nil {
		return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; FeishuBridgeSkill/1.0)")
	if body != nil {
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
	return ToolExecutionResult{Output: prefix + text}
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
	text := strings.TrimSpace(string(output))
	if len(text) > 6000 {
		text = text[:6000] + "\n... [truncated]"
	}
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

func (m *Manager) clearTurnSkillScriptApprovals(conversationKey string) {
	if strings.TrimSpace(conversationKey) == "" {
		return
	}
	m.mu.Lock()
	delete(m.skillApprovals, conversationKey)
	m.mu.Unlock()
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
