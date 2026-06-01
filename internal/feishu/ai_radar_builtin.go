package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	aiRadarDefaultBaseURL = "https://aihot.virxact.com"
	aiRadarPageLimit      = 10
)

var aiRadarBaseURL = aiRadarDefaultBaseURL

type aiRadarState struct {
	LastSuccessAt string `json:"last_success_at,omitempty"`
	LastRunAt     string `json:"last_run_at,omitempty"`
}

type aiRadarItem struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	SourceURL   string `json:"sourceUrl"`
	Source      string `json:"source"`
	SourceName  string `json:"sourceName"`
	Summary     string `json:"summary"`
	Category    string `json:"category"`
	PublishedAt string `json:"publishedAt"`
}

type aiRadarItemsResponse struct {
	Items      []aiRadarItem `json:"items"`
	HasNext    bool          `json:"hasNext"`
	NextCursor string        `json:"nextCursor"`
}

func (m *Manager) executeNativeAIRadarDailyTask(ctx context.Context, payload builtinSchedulePayload, meta LogMeta) (string, error) {
	workspace, err := m.aiRadarWorkspace(payload)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return "", fmt.Errorf("创建 AI Radar workspace 失败：%w", err)
	}
	statePath := strings.TrimSpace(payload.State)
	if statePath == "" {
		statePath = filepath.Join(workspace, "state.json")
	} else {
		statePath = expandUserPath(statePath)
		if !filepath.IsAbs(statePath) {
			statePath = filepath.Join(workspace, statePath)
		}
		statePath = filepath.Clean(statePath)
	}

	now := time.Now().UTC()
	state := readAIRadarState(statePath)
	start := now.Add(-24 * time.Hour)
	if state.LastSuccessAt != "" {
		if parsed, err := time.Parse(time.RFC3339, state.LastSuccessAt); err == nil && parsed.Before(now) {
			start = parsed.UTC()
		}
	}
	items, err := fetchAIRadarSelectedItems(ctx, aiRadarBaseURL, start, now)
	if err != nil {
		return "", err
	}
	state.LastRunAt = now.Format(time.RFC3339)
	state.LastSuccessAt = now.Format(time.RFC3339)
	if err := writeAIRadarState(statePath, state); err != nil {
		return "", err
	}
	m.logf("info", fmt.Sprintf("Feishu Agent AI Radar native daily: items=%d workspace=%s", len(items), workspace), meta)
	return renderAIRadarDailyMarkdown(items, start, now, statePath), nil
}

func executeAIHotLookupTool(parent context.Context, args map[string]any) ToolExecutionResult {
	locName := strings.TrimSpace(stringArg(args, "timezone"))
	if locName == "" {
		locName = defaultScheduleTimezone
	}
	loc, err := time.LoadLocation(locName)
	if err != nil {
		return ToolExecutionResult{Output: "[error] timezone 无效：" + locName, IsError: true}
	}
	now := time.Now().UTC()
	localNow := now.In(loc)
	rangeMode := strings.ToLower(strings.TrimSpace(stringArg(args, "range")))
	if rangeMode == "" {
		rangeMode = "today"
	}
	var start time.Time
	switch rangeMode {
	case "today":
		start = time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc).UTC()
	case "last_24h":
		start = now.Add(-24 * time.Hour)
	default:
		return ToolExecutionResult{Output: "[error] range 只支持 today 或 last_24h", IsError: true}
	}
	limit := intArg(args, "limit")
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	items, err := fetchAIRadarSelectedItems(parent, aiRadarBaseURL, start, now)
	if err != nil {
		return ToolExecutionResult{Output: "[error] " + err.Error(), IsError: true}
	}
	category := strings.TrimSpace(stringArg(args, "category"))
	items = filterAIHotItems(items, category)
	total := len(items)
	if len(items) > limit {
		items = items[:limit]
	}
	output := renderAIHotLookupJSON(items, total, start, now, locName, category)
	return ToolExecutionResult{Output: output}
}

func (m *Manager) aiRadarWorkspace(payload builtinSchedulePayload) (string, error) {
	if strings.TrimSpace(payload.State) != "" && filepath.IsAbs(expandUserPath(payload.State)) {
		return filepath.Dir(expandUserPath(payload.State)), nil
	}
	cfg := m.Config()
	base := strings.TrimSpace(cfg.SafeFiles.WorkspaceDir)
	if base == "" {
		base = DefaultSafeFilesConfig().WorkspaceDir
	}
	base = expandUserPath(base)
	if !filepath.IsAbs(base) {
		return "", fmt.Errorf("Feishu Agent workspace 不是绝对路径：%s", base)
	}
	return filepath.Join(filepath.Clean(base), "ai-radar"), nil
}

func readAIRadarState(path string) aiRadarState {
	data, err := os.ReadFile(path)
	if err != nil {
		return aiRadarState{}
	}
	var state aiRadarState
	_ = json.Unmarshal(data, &state)
	return state
}

func writeAIRadarState(path string, state aiRadarState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func fetchAIRadarSelectedItems(ctx context.Context, baseURL string, start time.Time, end time.Time) ([]aiRadarItem, error) {
	if !start.Before(end) {
		return nil, fmt.Errorf("AI Radar 时间窗口无效：%s >= %s", start.Format(time.RFC3339), end.Format(time.RFC3339))
	}
	client := &http.Client{Timeout: 25 * time.Second}
	var out []aiRadarItem
	seen := map[string]struct{}{}
	cursor := ""
	for page := 0; page < aiRadarPageLimit; page++ {
		endpoint, err := url.Parse(strings.TrimRight(baseURL, "/") + "/api/public/items")
		if err != nil {
			return nil, err
		}
		query := endpoint.Query()
		query.Set("mode", "selected")
		query.Set("since", formatAIRadarTime(start))
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		endpoint.RawQuery = query.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("AI Radar 抓取失败：%w", err)
		}
		var payload aiRadarItemsResponse
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("AI Radar 响应解析失败：%w", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("AI Radar API 返回 HTTP %d", resp.StatusCode)
		}
		for _, item := range payload.Items {
			published, ok := parseAIRadarPublishedAt(item.PublishedAt)
			if !ok || published.Before(start) || !published.Before(end) {
				continue
			}
			key := strings.TrimSpace(item.ID)
			if key == "" {
				key = strings.TrimSpace(firstNonEmpty(item.URL, item.SourceURL, item.Title))
			}
			if key != "" {
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
			}
			out = append(out, item)
		}
		if !payload.HasNext || strings.TrimSpace(payload.NextCursor) == "" {
			break
		}
		cursor = payload.NextCursor
	}
	sort.SliceStable(out, func(i, j int) bool {
		left, _ := parseAIRadarPublishedAt(out[i].PublishedAt)
		right, _ := parseAIRadarPublishedAt(out[j].PublishedAt)
		return left.After(right)
	})
	return out, nil
}

func renderAIRadarDailyMarkdown(items []aiRadarItem, start time.Time, end time.Time, statePath string) string {
	lines := []string{
		"AI Radar 日报",
		"",
		fmt.Sprintf("统计窗口：%s 至 %s", start.In(time.Local).Format("2006-01-02 15:04"), end.In(time.Local).Format("2006-01-02 15:04")),
		fmt.Sprintf("共 %d 条精选 AI 信号。", len(items)),
	}
	if len(items) == 0 {
		lines = append(lines, "", "本窗口内暂无新的 AI HOT selected 信号。")
	} else {
		sections := map[string][]aiRadarItem{}
		var order []string
		for _, item := range items {
			section := aiRadarSection(item.Category)
			if _, ok := sections[section]; !ok {
				order = append(order, section)
			}
			sections[section] = append(sections[section], item)
		}
		for _, section := range order {
			lines = append(lines, "", "## "+section)
			for _, item := range sections[section] {
				link := firstNonEmpty(item.URL, item.SourceURL)
				title := strings.TrimSpace(item.Title)
				if title == "" {
					title = link
				}
				if link != "" {
					title = fmt.Sprintf("[%s](%s)", title, link)
				}
				summary := strings.TrimSpace(item.Summary)
				if summary != "" {
					lines = append(lines, "- "+title+"： "+summarizeText(summary, 180))
				} else {
					lines = append(lines, "- "+title)
				}
			}
		}
	}
	lines = append(lines, "", "状态文件："+statePath)
	return strings.Join(lines, "\n")
}

func renderAIHotLookupJSON(items []aiRadarItem, total int, start time.Time, end time.Time, timezone string, category string) string {
	type outputItem struct {
		Title       string `json:"title"`
		URL         string `json:"url,omitempty"`
		Summary     string `json:"summary,omitempty"`
		Category    string `json:"category,omitempty"`
		Section     string `json:"section"`
		Source      string `json:"source,omitempty"`
		PublishedAt string `json:"published_at,omitempty"`
	}
	outItems := make([]outputItem, 0, len(items))
	for _, item := range items {
		outItems = append(outItems, outputItem{
			Title:       strings.TrimSpace(item.Title),
			URL:         firstNonEmpty(item.URL, item.SourceURL),
			Summary:     strings.TrimSpace(item.Summary),
			Category:    strings.TrimSpace(item.Category),
			Section:     aiRadarSection(item.Category),
			Source:      firstNonEmpty(item.Source, item.SourceName),
			PublishedAt: strings.TrimSpace(item.PublishedAt),
		})
	}
	payload := map[string]any{
		"ok":           true,
		"kind":         "aihot_lookup",
		"source":       "AI HOT selected",
		"timezone":     timezone,
		"window_start": start.Format(time.RFC3339),
		"window_end":   end.Format(time.RFC3339),
		"total":        total,
		"returned":     len(outItems),
		"category":     category,
		"markdown":     renderAIHotLookupMarkdown(items, total, start, end),
		"items":        outItems,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Sprint(payload)
	}
	return string(data)
}

func renderAIHotLookupMarkdown(items []aiRadarItem, total int, start time.Time, end time.Time) string {
	lines := []string{
		"AI HOT 精选",
		fmt.Sprintf("统计窗口：%s 至 %s", start.In(time.Local).Format("2006-01-02 15:04"), end.In(time.Local).Format("2006-01-02 15:04")),
		fmt.Sprintf("共匹配 %d 条，返回 %d 条。", total, len(items)),
	}
	if len(items) == 0 {
		return strings.Join(append(lines, "暂无匹配的 AI HOT selected 信号。"), "\n")
	}
	sections := map[string][]aiRadarItem{}
	var order []string
	for _, item := range items {
		section := aiRadarSection(item.Category)
		if _, ok := sections[section]; !ok {
			order = append(order, section)
		}
		sections[section] = append(sections[section], item)
	}
	for _, section := range order {
		lines = append(lines, "", "## "+section)
		for _, item := range sections[section] {
			title := strings.TrimSpace(item.Title)
			link := firstNonEmpty(item.URL, item.SourceURL)
			if title == "" {
				title = link
			}
			if link != "" {
				title = fmt.Sprintf("[%s](%s)", title, link)
			}
			summary := strings.TrimSpace(item.Summary)
			if summary != "" {
				lines = append(lines, "- "+title+"： "+summarizeText(summary, 180))
			} else {
				lines = append(lines, "- "+title)
			}
		}
	}
	return strings.Join(lines, "\n")
}

func filterAIHotItems(items []aiRadarItem, category string) []aiRadarItem {
	category = strings.TrimSpace(category)
	if category == "" {
		return items
	}
	out := make([]aiRadarItem, 0, len(items))
	for _, item := range items {
		section := aiRadarSection(item.Category)
		haystack := strings.Join([]string{item.Category, section, item.Title, item.Summary}, "\n")
		if strings.Contains(strings.ToLower(haystack), strings.ToLower(category)) {
			out = append(out, item)
		}
	}
	return out
}

func aiRadarSection(category string) string {
	category = strings.TrimSpace(category)
	if category == "" {
		return "其他"
	}
	aliases := map[string]string{
		"ai-models":   "模型发布/更新",
		"ai-products": "产品发布/更新",
		"industry":    "行业动态",
		"paper":       "前沿论文",
		"tip":         "技巧与观点",
		"模型":          "模型发布/更新",
		"产品":          "产品发布/更新",
		"工具":          "产品发布/更新",
		"行业":          "行业动态",
		"资讯":          "行业动态",
		"论文":          "前沿论文",
		"研究":          "前沿论文",
		"观点":          "技巧与观点",
		"技巧":          "技巧与观点",
	}
	for key, section := range aliases {
		if strings.Contains(category, key) {
			return section
		}
	}
	return category
}

func parseAIRadarPublishedAt(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	layouts := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func formatAIRadarTime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000Z")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func expandUserPath(path string) string {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}
