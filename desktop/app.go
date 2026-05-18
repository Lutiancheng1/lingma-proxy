package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"lingma-ipc-proxy/internal/httpapi"
	"lingma-ipc-proxy/internal/lingmaipc"
	"lingma-ipc-proxy/internal/remote"
	"lingma-ipc-proxy/internal/service"

	"github.com/google/uuid"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	desktopAppVersion          = "1.5.2"
	feedbackPayloadCharLimit   = 16000
	feedbackStringFieldLimit   = 4096
	feedbackDefaultRangePreset = "30m"
	feedbackDesktopFolderName  = "Lingma Proxy Feedback"
	proxyWarmupTimeout         = 30 * time.Second
	appStateFlushInterval      = 3 * time.Second
	appStatePersistRequestMax  = 300
	appStatePersistLogMax      = 1000
	appStatePersistBodyLimit   = 12000
	listSummaryMessageLimit    = 240
)

// App struct
// RequestRecord stores a single HTTP request summary
type RequestRecord struct {
	ID           string `json:"id,omitempty"`
	CreatedAt    string `json:"createdAt,omitempty"`
	Time         string `json:"time"`
	Method       string `json:"method"`
	Path         string `json:"path"`
	Model        string `json:"model,omitempty"`
	StatusCode   int    `json:"statusCode"`
	Duration     string `json:"duration"`
	Size         string `json:"size,omitempty"`
	HasReqBody   bool   `json:"hasReqBody,omitempty"`
	HasRespBody  bool   `json:"hasRespBody,omitempty"`
	InputTokens  int    `json:"inputTokens,omitempty"`
	OutputTokens int    `json:"outputTokens,omitempty"`
	TotalTokens  int    `json:"totalTokens,omitempty"`
	ReqBody      string `json:"reqBody,omitempty"`
	RespBody     string `json:"respBody,omitempty"`
}

type AppLog struct {
	CreatedAt string `json:"createdAt,omitempty"`
	Time      string `json:"time"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

type TokenStats struct {
	TotalRequests   int            `json:"totalRequests"`
	SuccessRequests int            `json:"successRequests"`
	InputTokens     int            `json:"inputTokens"`
	OutputTokens    int            `json:"outputTokens"`
	TotalTokens     int            `json:"totalTokens"`
	ByModel         map[string]int `json:"byModel,omitempty"`
	LastModel       string         `json:"lastModel,omitempty"`
	LastUpdated     string         `json:"lastUpdated,omitempty"`
}

type App struct {
	ctx context.Context

	mu        sync.RWMutex
	cfg       service.Config
	server    *httpapi.Server
	running   bool
	quitting  bool
	addr      string
	startedAt time.Time
	quitHint  time.Time
	models    []ModelInfo
	requests  []RequestRecord
	logs      []AppLog
	stats     TokenStats

	stateFlushTimer *time.Timer
	stateFlushAt    time.Time
}

// ModelInfo represents a model returned by /v1/models
type ModelInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ProxyStatus represents the current proxy status
type ProxyStatus struct {
	Running   bool   `json:"running"`
	Addr      string `json:"addr"`
	Backend   string `json:"backend"`
	Models    int    `json:"models"`
	Model     string `json:"model,omitempty"`
	StartedAt string `json:"startedAt,omitempty"`
}

// DetectionInfo exposes non-sensitive resolved connection details for the UI.
type DetectionInfo struct {
	ListenURL               string `json:"listenUrl"`
	Backend                 string `json:"backend"`
	BackendLabel            string `json:"backendLabel"`
	IPCSuccess              bool   `json:"ipcSuccess"`
	IPCTransport            string `json:"ipcTransport,omitempty"`
	IPCEndpoint             string `json:"ipcEndpoint,omitempty"`
	IPCError                string `json:"ipcError,omitempty"`
	RemoteBaseURL           string `json:"remoteBaseUrl"`
	RemoteBaseURLSource     string `json:"remoteBaseUrlSource,omitempty"`
	RemoteCredentialSuccess bool   `json:"remoteCredentialSuccess"`
	RemoteCredentialSource  string `json:"remoteCredentialSource,omitempty"`
	RemoteUserID            string `json:"remoteUserId,omitempty"`
	RemoteMachineID         string `json:"remoteMachineId,omitempty"`
	RemoteTokenExpireAt     string `json:"remoteTokenExpireAt,omitempty"`
	RemoteTokenExpired      bool   `json:"remoteTokenExpired"`
	RemoteCredentialError   string `json:"remoteCredentialError,omitempty"`
}

type FeedbackExportOptions struct {
	RangePreset          string `json:"rangePreset"`
	StartAt              string `json:"startAt,omitempty"`
	EndAt                string `json:"endAt,omitempty"`
	IncludeAppLogs       bool   `json:"includeAppLogs"`
	IncludeRequests      bool   `json:"includeRequests"`
	IncludeConfigSummary bool   `json:"includeConfigSummary"`
	IncludeEnvironment   bool   `json:"includeEnvironment"`
	IncludeDetectionInfo bool   `json:"includeDetectionInfo"`
	IssueDescription     string `json:"issueDescription,omitempty"`
	SavePath             string `json:"savePath,omitempty"`
}

type FeedbackExportResult struct {
	ZipPath      string `json:"zipPath"`
	ZipFilename  string `json:"zipFilename"`
	SaveDir      string `json:"saveDir"`
	ShareText    string `json:"shareText"`
	ExportedAt   string `json:"exportedAt"`
	AppLogCount  int    `json:"appLogCount"`
	RequestCount int    `json:"requestCount"`
}

type feedbackZipEntry struct {
	name string
	body []byte
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.cfg = defaultConfig()
	if err := a.loadAppState(); err != nil {
		runtime.LogWarningf(a.ctx, "failed to load app state: %v", err)
	}

	// Auto-save default config on first run so users can find/edit it later
	if err := a.saveConfig(a.cfg); err != nil {
		runtime.LogWarningf(a.ctx, "failed to save default config: %v", err)
	}

	// Auto-start proxy so the app is usable immediately
	go func() {
		if err := a.StartProxy(); err != nil {
			a.emitLog("error", fmt.Sprintf("Auto-start failed: %v. %s", err, transportFallbackHint()))
		} else {
			a.emitLog("info", "Proxy auto-started")
		}
	}()
}

// onDomReady is called when the frontend DOM is ready
func (a *App) onDomReady(ctx context.Context) {
	a.ctx = ctx
}

// onSecondInstanceLaunch is called when user clicks the dock icon while app is already running.
// We show the window so the user can interact with it again.
func (a *App) onSecondInstanceLaunch(secondInstanceData options.SecondInstanceData) {
	a.ShowWindow()
}

// beforeClose hides the window by default so the proxy can keep running.
// QuitApp sets quitting=true before allowing the process to exit.
func (a *App) beforeClose(ctx context.Context) bool {
	a.mu.Lock()
	if a.quitting {
		a.mu.Unlock()
		return true
	}

	now := time.Now()
	if !a.quitHint.IsZero() && now.Sub(a.quitHint) <= 2*time.Second {
		a.mu.Unlock()
		go a.forceQuit()
		return true
	}
	a.quitHint = now
	a.mu.Unlock()

	message := "再按一次退出快捷键将停止代理并退出应用"
	a.emitLog("warn", message)
	runtime.EventsEmit(a.ctx, "quit:confirm", message)
	return true
}

// ShowWindow shows the main window
func (a *App) ShowWindow() {
	runtime.Show(a.ctx)
	runtime.WindowShow(a.ctx)
	runtime.WindowUnminimise(a.ctx)
}

// HideWindow hides the main window
func (a *App) HideWindow() {
	runtime.Hide(a.ctx)
}

// MinimizeWindow minimises the main window.
func (a *App) MinimizeWindow() {
	runtime.WindowMinimise(a.ctx)
}

func (a *App) beginQuit() {
	go a.forceQuit()
}

// QuitApp fully quits the application
func (a *App) QuitApp() {
	a.beginQuit()
}

// ForceQuitApp stops the proxy and exits the desktop process immediately.
func (a *App) ForceQuitApp() {
	a.beginQuit()
}

// RequestQuitShortcut requires two shortcut presses to avoid accidental exits.
func (a *App) RequestQuitShortcut() {
	now := time.Now()
	a.mu.Lock()
	shouldQuit := !a.quitHint.IsZero() && now.Sub(a.quitHint) <= 2*time.Second
	a.quitHint = now
	a.mu.Unlock()

	if shouldQuit {
		go a.forceQuit()
		return
	}

	message := "再按一次退出快捷键将停止代理并退出应用"
	a.emitLog("warn", message)
	runtime.EventsEmit(a.ctx, "quit:confirm", message)
}

func (a *App) forceQuit() {
	a.mu.Lock()
	if a.quitting {
		a.mu.Unlock()
		return
	}
	a.quitting = true
	a.flushAppStateLocked()
	a.mu.Unlock()

	a.emitLog("info", "正在停止代理并退出应用")

	done := make(chan struct{})
	go func() {
		if err := a.StopProxy(); err != nil {
			runtime.LogWarningf(a.ctx, "stop proxy before exit failed: %v", err)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(1200 * time.Millisecond):
		runtime.LogWarning(a.ctx, "force quit continuing before proxy shutdown completed")
	}
	os.Exit(0)
}

func (a *App) emitLog(level string, message string) {
	entry := AppLog{
		CreatedAt: time.Now().Format(time.RFC3339),
		Time:      time.Now().Format("15:04:05"),
		Level:     level,
		Message:   message,
	}
	a.mu.Lock()
	a.logs = append(a.logs, entry)
	if len(a.logs) > 2000 {
		a.logs = a.logs[len(a.logs)-2000:]
	}
	a.saveAppStateLocked()
	a.mu.Unlock()
	runtime.EventsEmit(a.ctx, "log", summarizeLogEntry(entry))
}

// GetStatus returns the current proxy status
func (a *App) GetStatus() ProxyStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()
	startedAt := ""
	if !a.startedAt.IsZero() {
		startedAt = a.startedAt.Format(time.RFC3339)
	}
	return ProxyStatus{
		Running:   a.running,
		Addr:      a.addr,
		Backend:   string(a.cfg.Backend),
		Models:    len(a.models),
		Model:     a.cfg.Model,
		StartedAt: startedAt,
	}
}

// GetConfig returns the current configuration.
// Timeout is returned in seconds for frontend convenience.
func (a *App) GetConfig() service.Config {
	a.mu.RLock()
	cfg := a.cfg
	a.mu.RUnlock()
	cfg.Timeout = cfg.Timeout / time.Second
	cfg.WarmupTimeout = cfg.WarmupTimeout / time.Second
	return cfg
}

// GetDetectionInfo returns resolved IPC/remote details without exposing tokens.
func (a *App) GetDetectionInfo() DetectionInfo {
	a.mu.RLock()
	cfg := a.cfg
	addr := a.addr
	a.mu.RUnlock()

	if strings.TrimSpace(addr) == "" {
		addr = fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	}
	baseURL := remote.ResolveBaseURLWithSource(cfg.RemoteBaseURL)
	info := DetectionInfo{
		ListenURL:           "http://" + addr,
		Backend:             string(cfg.Backend),
		BackendLabel:        backendLabel(cfg.Backend),
		RemoteBaseURL:       baseURL.URL,
		RemoteBaseURLSource: baseURL.Source,
	}

	if opts, err := lingmaipc.ResolveDialOptions(cfg.Transport, cfg.Pipe, cfg.WebSocketURL); err == nil {
		info.IPCSuccess = true
		info.IPCTransport = string(opts.Transport)
		switch opts.Transport {
		case lingmaipc.TransportPipe:
			info.IPCEndpoint = opts.PipePath
		case lingmaipc.TransportWebSocket:
			info.IPCEndpoint = opts.WebSocketURL
		}
	} else {
		info.IPCError = err.Error()
	}

	if cred, err := remote.LoadCredential(cfg.RemoteAuthFile); err == nil {
		info.RemoteCredentialSuccess = true
		info.RemoteCredentialSource = cred.Source
		info.RemoteUserID = maskIdentifier(cred.UserID)
		info.RemoteMachineID = maskIdentifier(cred.MachineID)
		info.RemoteTokenExpired = remote.IsExpired(cred, 0)
		if cred.TokenExpireTime > 0 {
			info.RemoteTokenExpireAt = time.UnixMilli(cred.TokenExpireTime).Format(time.RFC3339)
		}
	} else {
		info.RemoteCredentialError = err.Error()
	}

	return info
}

// UpdateConfig updates the configuration, saves to file, and restarts the proxy if running.
// Frontend sends Timeout in seconds; we convert to time.Duration.
func (a *App) UpdateConfig(cfg service.Config) error {
	// Convert seconds -> Duration if frontend sent a small value
	if cfg.Timeout > 0 && cfg.Timeout < time.Second {
		cfg.Timeout = cfg.Timeout * time.Second
	}
	if cfg.WarmupTimeout > 0 && cfg.WarmupTimeout < time.Second {
		cfg.WarmupTimeout = cfg.WarmupTimeout * time.Second
	}
	if cfg.WarmupTimeout <= 0 {
		cfg.WarmupTimeout = proxyWarmupTimeout
	}

	a.mu.Lock()
	wasRunning := a.running
	a.cfg = cfg
	a.mu.Unlock()

	if err := a.saveConfig(cfg); err != nil {
		runtime.LogWarningf(a.ctx, "failed to save config: %v", err)
		a.emitLog("warn", fmt.Sprintf("Config updated but failed to save: %v", err))
	} else {
		a.emitLog("info", "Config saved to file")
	}

	if wasRunning {
		if err := a.StopProxy(); err != nil {
			return fmt.Errorf("stop failed: %w", err)
		}
		return a.StartProxy()
	}
	return nil
}

func (a *App) saveConfig(cfg service.Config) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".config", "lingma-proxy")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	timeoutSec := int(cfg.Timeout.Seconds())
	fileCfg := map[string]any{
		"host":                    cfg.Host,
		"port":                    cfg.Port,
		"backend":                 string(cfg.Backend),
		"transport":               string(cfg.Transport),
		"pipe":                    cfg.Pipe,
		"websocket_url":           cfg.WebSocketURL,
		"remote_base_url":         cfg.RemoteBaseURL,
		"remote_auth_file":        cfg.RemoteAuthFile,
		"remote_version":          cfg.RemoteVersion,
		"cwd":                     cfg.Cwd,
		"current_file_path":       cfg.CurrentFilePath,
		"mode":                    cfg.Mode,
		"model":                   cfg.Model,
		"shell_type":              cfg.ShellType,
		"session_mode":            string(cfg.SessionMode),
		"timeout":                 timeoutSec,
		"warmup_timeout":          int(cfg.WarmupTimeout.Seconds()),
		"remote_fallback_enabled": cfg.RemoteFallbackEnabled,
		"remote_fallback_models":  cfg.RemoteFallbackModels,
	}

	data, err := json.MarshalIndent(fileCfg, "", "  ")
	if err != nil {
		return err
	}

	path := filepath.Join(dir, "config.json")
	return os.WriteFile(path, data, 0644)
}

// StartProxy starts the lingma-ipc-proxy HTTP server
func (a *App) StartProxy() error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("proxy already running")
	}

	addr := fmt.Sprintf("%s:%d", a.cfg.Host, a.cfg.Port)
	cfg := a.cfg
	a.mu.Unlock()

	svc := service.New(cfg)
	server := httpapi.NewServer(addr, svc)
	server.OnRequest = func(method, path string, statusCode int, duration time.Duration, reqBody, respBody string) {
		inputTokens, outputTokens := extractTokenUsage(respBody)
		model := extractRequestModel(reqBody)
		record := RequestRecord{
			ID:           uuid.NewString(),
			CreatedAt:    time.Now().Format(time.RFC3339),
			Time:         time.Now().Format("15:04:05"),
			Method:       method,
			Path:         path,
			Model:        model,
			StatusCode:   statusCode,
			Duration:     duration.Round(time.Millisecond).String(),
			Size:         formatPayloadSize(len(reqBody) + len(respBody)),
			HasReqBody:   strings.TrimSpace(reqBody) != "",
			HasRespBody:  strings.TrimSpace(respBody) != "",
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			TotalTokens:  inputTokens + outputTokens,
			ReqBody:      reqBody,
			RespBody:     respBody,
		}
		a.mu.Lock()
		a.requests = append(a.requests, record)
		if len(a.requests) > 2000 {
			a.requests = a.requests[len(a.requests)-2000:]
		}
		a.accumulateTokenStatsLocked(record)
		a.saveAppStateLocked()
		a.mu.Unlock()
		runtime.EventsEmit(a.ctx, "requests:updated")
		runtime.EventsEmit(a.ctx, "usage:updated", a.GetTokenStats())
	}

	// Check if the port is available before claiming we're running
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("port %s is already in use: %w", addr, err)
	}
	ln.Close()

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			runtime.LogErrorf(a.ctx, "server error: %v", err)
			a.emitLog("error", fmt.Sprintf("Server error: %v", err))
			a.mu.Lock()
			a.running = false
			a.addr = ""
			a.startedAt = time.Time{}
			a.mu.Unlock()
		}
	}()

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("proxy already running")
	}
	a.server = server
	a.addr = addr
	a.running = true
	a.startedAt = time.Now()
	a.mu.Unlock()

	msg := fmt.Sprintf("Proxy started on http://%s", addr)
	runtime.LogInfof(a.ctx, msg)
	a.emitLog("info", msg)

	// Warm up backend and fetch models in background so the HTTP endpoint
	// becomes available immediately after the desktop app launches/restarts.
	go a.fetchModels(addr)
	go func() {
		warmupCtx, cancel := context.WithTimeout(context.Background(), effectiveWarmupTimeout(cfg))
		defer cancel()
		if err := svc.Warmup(warmupCtx); err != nil {
			runtime.LogWarningf(a.ctx, "warmup failed: %v", err)
			a.emitLog("warn", fmt.Sprintf("%s warmup failed: %v. %s", backendLabel(cfg.Backend), err, warmupFallbackHint(cfg.Backend)))
			return
		}
		runtime.LogInfof(a.ctx, "%s warmup completed", backendLabel(cfg.Backend))
		a.emitLog("info", fmt.Sprintf("%s warmup completed", backendLabel(cfg.Backend)))
	}()

	return nil
}

func (a *App) GetLogs() []AppLog {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]AppLog, len(a.logs))
	copy(out, a.logs)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// GetLogSummaries returns recent logs with truncated messages for lightweight list rendering.
func (a *App) GetLogSummaries() []AppLog {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]AppLog, len(a.logs))
	for i, entry := range a.logs {
		cloned := entry
		cloned.Message = trimListMessage(entry.Message)
		out[i] = cloned
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (a *App) GetLogDetail(createdAt string) (AppLog, error) {
	target := strings.TrimSpace(createdAt)
	if target == "" {
		return AppLog{}, fmt.Errorf("log createdAt is required")
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	for i := len(a.logs) - 1; i >= 0; i-- {
		if a.logs[i].CreatedAt == target {
			return a.logs[i], nil
		}
	}
	return AppLog{}, fmt.Errorf("log not found")
}

func (a *App) ClearLogs() {
	a.mu.Lock()
	a.logs = nil
	a.saveAppStateLocked()
	a.mu.Unlock()
	runtime.EventsEmit(a.ctx, "logs:updated", a.GetLogSummaries())
}

func (a *App) ChooseFeedbackExportPath() (string, error) {
	defaultPath, err := defaultFeedbackExportPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(defaultPath), 0755); err != nil {
		return "", err
	}
	return runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:                "保存反馈日志包",
		DefaultDirectory:     filepath.Dir(defaultPath),
		DefaultFilename:      filepath.Base(defaultPath),
		CanCreateDirectories: true,
		Filters: []runtime.FileFilter{
			{
				DisplayName: "Zip 压缩包 (*.zip)",
				Pattern:     "*.zip",
			},
		},
	})
}

func (a *App) ExportFeedbackBundle(options FeedbackExportOptions) (FeedbackExportResult, error) {
	result := FeedbackExportResult{}

	savePath := strings.TrimSpace(options.SavePath)
	if savePath == "" {
		var err error
		savePath, err = a.ChooseFeedbackExportPath()
		if err != nil {
			return result, err
		}
	}
	if savePath == "" {
		return result, nil
	}
	if !strings.HasSuffix(strings.ToLower(savePath), ".zip") {
		savePath += ".zip"
	}

	startAt, endAt, err := resolveFeedbackRange(options)
	if err != nil {
		return result, err
	}

	a.mu.RLock()
	logs := cloneLogs(a.logs)
	requests := cloneRequests(a.requests)
	stats := a.stats
	cfg := a.cfg
	status := ProxyStatus{
		Running:   a.running,
		Addr:      a.addr,
		Backend:   string(a.cfg.Backend),
		Models:    len(a.models),
		Model:     a.cfg.Model,
		StartedAt: a.startedAt.Format(time.RFC3339),
	}
	a.mu.RUnlock()

	filteredLogs := filterLogsByRange(logs, startAt, endAt)
	filteredRequests := filterRequestsByRange(requests, startAt, endAt)
	sanitizedLogs := sanitizeLogs(filteredLogs)
	sanitizedRequests := sanitizeRequests(filteredRequests)
	manifest := buildFeedbackManifest(options, startAt, endAt, sanitizedLogs, sanitizedRequests)

	entries := make([]feedbackZipEntry, 0, 7)
	entries = append(entries, feedbackZipEntry{name: "manifest.json", body: mustJSON(manifest)})
	if options.IncludeAppLogs {
		entries = append(entries, feedbackZipEntry{name: "app-logs.json", body: mustJSON(sanitizedLogs)})
	}
	if options.IncludeRequests {
		entries = append(entries, feedbackZipEntry{name: "request-logs.json", body: mustJSON(sanitizedRequests)})
	}
	if options.IncludeConfigSummary {
		entries = append(entries, feedbackZipEntry{name: "config-summary.json", body: mustJSON(buildConfigSummary(cfg, stats, status))})
	}
	if options.IncludeEnvironment {
		entries = append(entries, feedbackZipEntry{name: "environment.json", body: mustJSON(buildEnvironmentSummary(cfg))})
	}
	if options.IncludeDetectionInfo {
		entries = append(entries, feedbackZipEntry{name: "detection-info.json", body: mustJSON(a.GetDetectionInfo())})
	}
	note := strings.TrimSpace(options.IssueDescription)
	if note != "" {
		entries = append(entries, feedbackZipEntry{name: "user-note.txt", body: []byte(note + "\n")})
	}

	if err := os.MkdirAll(filepath.Dir(savePath), 0755); err != nil {
		return result, err
	}
	if err := writeFeedbackZip(savePath, entries); err != nil {
		return result, err
	}

	exportedAt := time.Now()
	shareText := buildFeedbackShareText(options, cfg, status, startAt, endAt, filepath.Base(savePath))
	result = FeedbackExportResult{
		ZipPath:      savePath,
		ZipFilename:  filepath.Base(savePath),
		SaveDir:      filepath.Dir(savePath),
		ShareText:    shareText,
		ExportedAt:   exportedAt.Format("2006/01/02 15:04:05"),
		AppLogCount:  len(sanitizedLogs),
		RequestCount: len(sanitizedRequests),
	}
	a.emitLog("info", fmt.Sprintf("反馈日志包已导出：%s", savePath))
	return result, nil
}

func (a *App) OpenPathInFileManager(path string) error {
	target := strings.TrimSpace(path)
	if target == "" {
		return fmt.Errorf("path is required")
	}
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	openPath := target
	if !info.IsDir() {
		openPath = filepath.Dir(target)
	}

	var cmd *exec.Cmd
	switch goruntime.GOOS {
	case "darwin":
		cmd = exec.Command("open", openPath)
	case "windows":
		cmd = exec.Command("explorer", filepath.Clean(openPath))
	default:
		cmd = exec.Command("xdg-open", openPath)
	}
	return cmd.Start()
}

// StopProxy stops the proxy server
func (a *App) StopProxy() error {
	a.mu.Lock()
	if !a.running || a.server == nil {
		a.mu.Unlock()
		return nil
	}

	server := a.server
	a.server = nil
	a.running = false
	a.addr = ""
	a.startedAt = time.Time{}
	a.models = nil
	a.mu.Unlock()

	runtime.EventsEmit(a.ctx, "status:updated", a.GetStatus())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		a.emitLog("warn", fmt.Sprintf("Proxy stop forced after graceful shutdown timeout: %v", err))
		return err
	}

	runtime.LogInfo(a.ctx, "proxy stopped")
	a.emitLog("info", "Proxy stopped")
	return nil
}

// GetModels returns the cached model list
func (a *App) GetModels() []ModelInfo {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.models
}

// GetRequests returns recent HTTP request records
func (a *App) GetRequests() []RequestRecord {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]RequestRecord, len(a.requests))
	copy(out, a.requests)
	// reverse so newest first
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// GetRequestSummaries returns recent request records without large request/response bodies.
func (a *App) GetRequestSummaries() []RequestRecord {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]RequestRecord, len(a.requests))
	for i, record := range a.requests {
		cloned := record
		cloned.ReqBody = ""
		cloned.RespBody = ""
		out[i] = cloned
	}
	// reverse so newest first
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (a *App) GetRequestDetail(idOrCreatedAt string) (RequestRecord, error) {
	target := strings.TrimSpace(idOrCreatedAt)
	if target == "" {
		return RequestRecord{}, fmt.Errorf("request id is required")
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	for i := len(a.requests) - 1; i >= 0; i-- {
		if a.requests[i].ID == target || a.requests[i].CreatedAt == target {
			return a.requests[i], nil
		}
	}
	return RequestRecord{}, fmt.Errorf("request not found")
}

// ClearRequests clears the request history
func (a *App) ClearRequests() {
	a.mu.Lock()
	a.requests = nil
	a.saveAppStateLocked()
	a.mu.Unlock()
	runtime.EventsEmit(a.ctx, "requests:updated")
	a.emitLog("info", "Request history cleared")
}

func (a *App) GetTokenStats() TokenStats {
	a.mu.RLock()
	defer a.mu.RUnlock()
	stats := a.stats
	if stats.ByModel != nil {
		stats.ByModel = cloneIntMap(stats.ByModel)
	}
	return stats
}

// RefreshModels probes the running proxy for the latest model list.
func (a *App) RefreshModels() ([]ModelInfo, error) {
	a.mu.RLock()
	running := a.running
	addr := a.addr
	a.mu.RUnlock()

	if !running || addr == "" {
		return nil, fmt.Errorf("proxy is not running")
	}

	models, err := a.fetchModels(addr)
	if err != nil {
		return nil, err
	}
	return models, nil
}

func (a *App) SelectModel(modelID string) (ProxyStatus, error) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return a.GetStatus(), fmt.Errorf("model id is required")
	}

	a.mu.Lock()
	found := len(a.models) == 0
	for _, model := range a.models {
		if model.ID == modelID {
			found = true
			break
		}
	}
	if !found {
		a.mu.Unlock()
		return a.GetStatus(), fmt.Errorf("model %q is not in the detected model list", modelID)
	}
	a.cfg.Model = modelID
	cfg := a.cfg
	server := a.server
	a.mu.Unlock()

	if server != nil {
		server.SetDefaultModel(modelID)
	}
	if err := a.saveConfig(cfg); err != nil {
		a.emitLog("warn", fmt.Sprintf("Model switched but config save failed: %v", err))
	}
	a.emitLog("info", fmt.Sprintf("已切换默认模型：%s", modelID))
	return a.GetStatus(), nil
}

func (a *App) fetchModels(addr string) ([]ModelInfo, error) {
	url := fmt.Sprintf("http://%s/v1/models", addr)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		runtime.LogWarningf(a.ctx, "fetch models failed: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		runtime.LogWarningf(a.ctx, "decode models failed: %v", err)
		return nil, err
	}

	models := make([]ModelInfo, 0, len(result.Data))
	for _, m := range result.Data {
		models = append(models, ModelInfo{ID: m.ID, Name: m.Name})
	}

	a.mu.Lock()
	a.models = models
	a.mu.Unlock()

	runtime.EventsEmit(a.ctx, "models:updated", models)
	if len(models) > 0 {
		a.emitLog("info", fmt.Sprintf("Loaded %d models", len(models)))
	}
	return models, nil
}

func extractRequestModel(reqBody string) string {
	if strings.TrimSpace(reqBody) == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(reqBody), &payload); err != nil {
		return ""
	}
	if model, ok := payload["model"].(string); ok {
		return strings.TrimSpace(model)
	}
	return ""
}

func formatPayloadSize(bytes int) string {
	if bytes <= 0 {
		return "-"
	}
	const kb = 1024
	const mb = 1024 * kb
	if bytes >= mb {
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	}
	if bytes >= kb {
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(kb))
	}
	return fmt.Sprintf("%d B", bytes)
}

type appStateFile struct {
	Requests []RequestRecord `json:"requests"`
	Logs     []AppLog        `json:"logs"`
	Stats    TokenStats      `json:"stats"`
}

func (a *App) loadAppState() error {
	path, err := appStatePath()
	if err != nil {
		return err
	}
	info, statErr := os.Stat(path)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var state appStateFile
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	migrated := false
	if statErr == nil {
		if backfillRequestCreatedAt(state.Requests, info.ModTime()) {
			migrated = true
		}
		if backfillRequestIDs(state.Requests) {
			migrated = true
		}
		if backfillLogCreatedAt(state.Logs, info.ModTime()) {
			migrated = true
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = state.Requests
	a.logs = state.Logs
	a.stats = state.Stats
	if a.stats.ByModel == nil {
		a.stats.ByModel = map[string]int{}
	}
	a.reconcileTokenStatsLocked()
	if migrated {
		a.flushAppStateLocked()
	}
	return nil
}

func (a *App) saveAppStateLocked() {
	a.stateFlushAt = time.Now().Add(appStateFlushInterval)
	if a.stateFlushTimer != nil {
		a.stateFlushTimer.Reset(appStateFlushInterval)
		return
	}
	a.stateFlushTimer = time.AfterFunc(appStateFlushInterval, func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if time.Now().Before(a.stateFlushAt) {
			return
		}
		a.flushAppStateLocked()
	})
}

func (a *App) flushAppStateLocked() {
	if a.stateFlushTimer != nil {
		a.stateFlushTimer.Stop()
		a.stateFlushTimer = nil
	}
	a.stateFlushAt = time.Time{}
	path, err := appStatePath()
	if err != nil {
		runtime.LogWarningf(a.ctx, "resolve app state path failed: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		runtime.LogWarningf(a.ctx, "create app state dir failed: %v", err)
		return
	}
	state := appStateFile{
		Requests: trimPersistedRequests(a.requests),
		Logs:     trimPersistedLogs(a.logs),
		Stats:    a.stats,
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		runtime.LogWarningf(a.ctx, "marshal app state failed: %v", err)
		return
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		runtime.LogWarningf(a.ctx, "write app state failed: %v", err)
	}
}

func trimPersistedRequests(records []RequestRecord) []RequestRecord {
	if len(records) == 0 {
		return nil
	}
	start := 0
	if len(records) > appStatePersistRequestMax {
		start = len(records) - appStatePersistRequestMax
	}
	out := make([]RequestRecord, 0, len(records)-start)
	for _, record := range records[start:] {
		cloned := record
		cloned.ReqBody = trimStatePayload(record.ReqBody)
		cloned.RespBody = trimStatePayload(record.RespBody)
		out = append(out, cloned)
	}
	return out
}

func trimPersistedLogs(records []AppLog) []AppLog {
	if len(records) == 0 {
		return nil
	}
	start := 0
	if len(records) > appStatePersistLogMax {
		start = len(records) - appStatePersistLogMax
	}
	out := make([]AppLog, len(records)-start)
	copy(out, records[start:])
	return out
}

func trimStatePayload(text string) string {
	if len(text) <= appStatePersistBodyLimit {
		return text
	}
	return text[:appStatePersistBodyLimit] + "\n... [truncated for desktop state cache]"
}

func trimListMessage(text string) string {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) <= listSummaryMessageLimit {
		return trimmed
	}
	return trimmed[:listSummaryMessageLimit] + "..."
}

func summarizeLogEntry(entry AppLog) AppLog {
	cloned := entry
	cloned.Message = trimListMessage(entry.Message)
	return cloned
}

func appStatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "lingma-ipc-proxy", "app-state.json"), nil
}

func (a *App) accumulateTokenStatsLocked(record RequestRecord) {
	a.stats.TotalRequests++
	if record.StatusCode >= 200 && record.StatusCode < 300 {
		a.stats.SuccessRequests++
	}
	a.stats.InputTokens += record.InputTokens
	a.stats.OutputTokens += record.OutputTokens
	a.stats.TotalTokens += record.TotalTokens
	if a.stats.ByModel == nil {
		a.stats.ByModel = map[string]int{}
	}
	model := strings.TrimSpace(record.Model)
	if model == "" {
		model = "-"
	}
	if record.TotalTokens > 0 {
		a.stats.ByModel[model] += record.TotalTokens
		if isUsageBearingRequest(record.Path) && model != "-" {
			a.stats.LastModel = model
		}
	}
	a.stats.LastUpdated = time.Now().Format(time.RFC3339)
}

func (a *App) reconcileTokenStatsLocked() {
	if a.stats.ByModel == nil {
		a.stats.ByModel = map[string]int{}
	}
	a.stats.LastModel = ""
	for i := len(a.requests) - 1; i >= 0; i-- {
		record := a.requests[i]
		model := strings.TrimSpace(record.Model)
		if model == "" || record.TotalTokens <= 0 || !isUsageBearingRequest(record.Path) {
			continue
		}
		a.stats.LastModel = model
		break
	}
}

func isUsageBearingRequest(path string) bool {
	switch strings.TrimSpace(path) {
	case "/v1/messages", "/v1/chat/completions", "/v1/completions", "/v1/responses":
		return true
	default:
		return false
	}
}

func cloneIntMap(src map[string]int) map[string]int {
	out := make(map[string]int, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func extractTokenUsage(respBody string) (int, int) {
	if strings.TrimSpace(respBody) == "" {
		return 0, 0
	}
	input, output := extractUsageFromJSON(respBody)
	if input != 0 || output != 0 {
		return input, output
	}
	for _, line := range strings.Split(respBody, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		in, out := extractUsageFromJSON(payload)
		if in > 0 {
			input = in
		}
		if out > 0 {
			output = out
		}
	}
	return input, output
}

func extractUsageFromJSON(raw string) (int, int) {
	var payload any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return 0, 0
	}
	input, output, ok := findUsageTokens(payload)
	if !ok {
		return 0, 0
	}
	return input, output
}

func findUsageTokens(value any) (int, int, bool) {
	switch typed := value.(type) {
	case map[string]any:
		if usage, ok := typed["usage"].(map[string]any); ok {
			return usageTokenPair(usage)
		}
		if input, output, ok := usageTokenPair(typed); ok {
			return input, output, true
		}
		for _, child := range typed {
			if input, output, ok := findUsageTokens(child); ok {
				return input, output, true
			}
		}
	case []any:
		for _, child := range typed {
			if input, output, ok := findUsageTokens(child); ok {
				return input, output, true
			}
		}
	}
	return 0, 0, false
}

func usageTokenPair(value map[string]any) (int, int, bool) {
	input := intFromAny(value["input_tokens"]) + intFromAny(value["prompt_tokens"])
	output := intFromAny(value["output_tokens"]) + intFromAny(value["completion_tokens"])
	if input > 0 || output > 0 {
		return input, output, true
	}
	if intFromAny(value["total_tokens"]) > 0 {
		return input, output, true
	}
	return 0, 0, false
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		n, _ := typed.Int64()
		return int(n)
	default:
		return 0
	}
}

func cloneLogs(src []AppLog) []AppLog {
	out := make([]AppLog, len(src))
	copy(out, src)
	return out
}

func cloneRequests(src []RequestRecord) []RequestRecord {
	out := make([]RequestRecord, len(src))
	copy(out, src)
	return out
}

func defaultFeedbackExportPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	now := time.Now().Format("2006-01-02-15-04-05")
	return filepath.Join(home, "Desktop", feedbackDesktopFolderName, fmt.Sprintf("LingmaProxy-feedback-%s.zip", now)), nil
}

func resolveFeedbackRange(options FeedbackExportOptions) (time.Time, time.Time, error) {
	now := time.Now()
	endAt := now
	preset := strings.TrimSpace(options.RangePreset)
	if preset == "" {
		preset = feedbackDefaultRangePreset
	}
	switch preset {
	case "30m":
		return now.Add(-30 * time.Minute), endAt, nil
	case "2h":
		return now.Add(-2 * time.Hour), endAt, nil
	case "24h":
		return now.Add(-24 * time.Hour), endAt, nil
	case "7d":
		return now.Add(-7 * 24 * time.Hour), endAt, nil
	case "all":
		return time.Time{}, endAt, nil
	case "custom":
		startAt, err := parseFeedbackTime(options.StartAt)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid start time: %w", err)
		}
		customEndAt, err := parseFeedbackTime(options.EndAt)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid end time: %w", err)
		}
		if customEndAt.Before(startAt) {
			return time.Time{}, time.Time{}, errors.New("end time must be after start time")
		}
		return startAt, customEndAt, nil
	default:
		return now.Add(-30 * time.Minute), endAt, nil
	}
}

func parseFeedbackTime(value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, errors.New("time is required")
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04",
		"2006-01-02 15:04",
	}
	for _, layout := range layouts {
		if ts, err := time.ParseInLocation(layout, trimmed, time.Local); err == nil {
			return ts, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported time format: %s", trimmed)
}

func filterLogsByRange(logs []AppLog, startAt, endAt time.Time) []AppLog {
	if startAt.IsZero() {
		return logs
	}
	filtered := make([]AppLog, 0, len(logs))
	for _, entry := range logs {
		ts, ok := parseRecordTimestamp(entry.CreatedAt)
		if !ok {
			continue
		}
		if !ts.Before(startAt) && !ts.After(endAt) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func filterRequestsByRange(requests []RequestRecord, startAt, endAt time.Time) []RequestRecord {
	if startAt.IsZero() {
		return requests
	}
	filtered := make([]RequestRecord, 0, len(requests))
	for _, entry := range requests {
		ts, ok := parseRecordTimestamp(entry.CreatedAt)
		if !ok {
			continue
		}
		if !ts.Before(startAt) && !ts.After(endAt) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func parseRecordTimestamp(value string) (time.Time, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}

func backfillRequestCreatedAt(records []RequestRecord, anchor time.Time) bool {
	return backfillCreatedAt(anchor, len(records), func(i int) string {
		return records[i].CreatedAt
	}, func(i int) string {
		return records[i].Time
	}, func(i int, value string) {
		records[i].CreatedAt = value
	})
}

func backfillRequestIDs(records []RequestRecord) bool {
	if len(records) == 0 {
		return false
	}
	mutated := false
	for i := range records {
		if strings.TrimSpace(records[i].ID) != "" {
			continue
		}
		records[i].ID = uuid.NewString()
		mutated = true
	}
	return mutated
}

func backfillLogCreatedAt(records []AppLog, anchor time.Time) bool {
	return backfillCreatedAt(anchor, len(records), func(i int) string {
		return records[i].CreatedAt
	}, func(i int) string {
		return records[i].Time
	}, func(i int, value string) {
		records[i].CreatedAt = value
	})
}

func backfillCreatedAt(anchor time.Time, length int, getCreatedAt func(int) string, getTime func(int) string, setCreatedAt func(int, string)) bool {
	if length == 0 {
		return false
	}
	currentDay := time.Date(anchor.Year(), anchor.Month(), anchor.Day(), 0, 0, 0, 0, time.Local)
	var newerTOD time.Duration
	haveNewerTOD := false
	mutated := false

	for i := length - 1; i >= 0; i-- {
		if ts, ok := parseRecordTimestamp(getCreatedAt(i)); ok {
			currentDay = time.Date(ts.Year(), ts.Month(), ts.Day(), 0, 0, 0, 0, time.Local)
			newerTOD = time.Duration(ts.Hour())*time.Hour + time.Duration(ts.Minute())*time.Minute + time.Duration(ts.Second())*time.Second
			haveNewerTOD = true
			continue
		}
		tod, ok := parseClockTime(getTime(i))
		if !ok {
			continue
		}
		if haveNewerTOD && tod > newerTOD {
			currentDay = currentDay.AddDate(0, 0, -1)
		}
		createdAt := currentDay.Add(tod).Format(time.RFC3339)
		setCreatedAt(i, createdAt)
		mutated = true
		newerTOD = tod
		haveNewerTOD = true
	}

	return mutated
}

func parseClockTime(value string) (time.Duration, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, false
	}
	parsed, err := time.ParseInLocation("15:04:05", trimmed, time.Local)
	if err != nil {
		return 0, false
	}
	return time.Duration(parsed.Hour())*time.Hour + time.Duration(parsed.Minute())*time.Minute + time.Duration(parsed.Second())*time.Second, true
}

func sanitizeLogs(logs []AppLog) []AppLog {
	out := make([]AppLog, 0, len(logs))
	for _, entry := range logs {
		out = append(out, AppLog{
			CreatedAt: entry.CreatedAt,
			Time:      entry.Time,
			Level:     entry.Level,
			Message:   redactPlainText(entry.Message),
		})
	}
	return out
}

func sanitizeRequests(requests []RequestRecord) []RequestRecord {
	out := make([]RequestRecord, 0, len(requests))
	for _, record := range requests {
		out = append(out, RequestRecord{
			ID:           record.ID,
			CreatedAt:    record.CreatedAt,
			Time:         record.Time,
			Method:       record.Method,
			Path:         record.Path,
			Model:        record.Model,
			StatusCode:   record.StatusCode,
			Duration:     record.Duration,
			Size:         record.Size,
			HasReqBody:   record.HasReqBody,
			HasRespBody:  record.HasRespBody,
			InputTokens:  record.InputTokens,
			OutputTokens: record.OutputTokens,
			TotalTokens:  record.TotalTokens,
			ReqBody:      redactAndLimitPayload(record.ReqBody),
			RespBody:     redactAndLimitPayload(record.RespBody),
		})
	}
	return out
}

func buildFeedbackManifest(options FeedbackExportOptions, startAt, endAt time.Time, logs []AppLog, requests []RequestRecord) map[string]any {
	rangeLabel := strings.TrimSpace(options.RangePreset)
	if rangeLabel == "" {
		rangeLabel = feedbackDefaultRangePreset
	}
	manifest := map[string]any{
		"appVersion":       desktopAppVersion,
		"exportedAt":       time.Now().Format(time.RFC3339),
		"rangePreset":      rangeLabel,
		"appLogCount":      len(logs),
		"requestCount":     len(requests),
		"redacted":         true,
		"requestBodyLimit": feedbackPayloadCharLimit,
	}
	if !startAt.IsZero() {
		manifest["startAt"] = startAt.Format(time.RFC3339)
		manifest["endAt"] = endAt.Format(time.RFC3339)
	}
	return manifest
}

func buildConfigSummary(cfg service.Config, stats TokenStats, status ProxyStatus) map[string]any {
	summary := map[string]any{
		"listenUrl":             fmt.Sprintf("http://%s:%d", cfg.Host, cfg.Port),
		"backend":               string(cfg.Backend),
		"transport":             string(cfg.Transport),
		"remoteBaseURL":         strings.TrimSpace(cfg.RemoteBaseURL),
		"remoteVersion":         strings.TrimSpace(cfg.RemoteVersion),
		"cwd":                   cfg.Cwd,
		"currentFilePath":       cfg.CurrentFilePath,
		"mode":                  cfg.Mode,
		"model":                 cfg.Model,
		"shellType":             cfg.ShellType,
		"sessionMode":           string(cfg.SessionMode),
		"timeoutSeconds":        int(cfg.Timeout.Seconds()),
		"warmupTimeoutSeconds":  int(effectiveWarmupTimeout(cfg).Seconds()),
		"remoteFallbackEnabled": cfg.RemoteFallbackEnabled,
		"remoteFallbackModels":  cleanConfigStrings(cfg.RemoteFallbackModels),
		"statusRunning":         status.Running,
		"statusStartedAt":       status.StartedAt,
		"tokenTotalRequests":    stats.TotalRequests,
		"tokenSuccessRequests":  stats.SuccessRequests,
		"tokenInputTokens":      stats.InputTokens,
		"tokenOutputTokens":     stats.OutputTokens,
		"tokenTotalTokens":      stats.TotalTokens,
		"tokenLastModel":        stats.LastModel,
		"tokenLastUpdated":      stats.LastUpdated,
	}
	if strings.TrimSpace(cfg.WebSocketURL) != "" {
		summary["websocketUrl"] = cfg.WebSocketURL
	}
	if strings.TrimSpace(cfg.Pipe) != "" {
		summary["pipe"] = cfg.Pipe
	}
	if strings.TrimSpace(cfg.RemoteAuthFile) != "" {
		summary["remoteAuthFile"] = cfg.RemoteAuthFile
	}
	return summary
}

func buildEnvironmentSummary(cfg service.Config) map[string]any {
	hostname, _ := os.Hostname()
	return map[string]any{
		"appName":         "Lingma Proxy",
		"appVersion":      desktopAppVersion,
		"os":              goruntime.GOOS,
		"arch":            goruntime.GOARCH,
		"backend":         string(cfg.Backend),
		"model":           cfg.Model,
		"timezone":        time.Now().Location().String(),
		"exportLocalTime": time.Now().Format(time.RFC3339),
		"hostnameMasked":  maskIdentifier(hostname),
	}
}

func buildFeedbackShareText(options FeedbackExportOptions, cfg service.Config, status ProxyStatus, startAt, endAt time.Time, filename string) string {
	lines := []string{
		"反馈说明：",
		strings.TrimSpace(options.IssueDescription),
		"",
		"应用版本：",
		fmt.Sprintf("Lingma Proxy %s", desktopAppVersion),
		"",
		"系统环境：",
		fmt.Sprintf("%s / %s", titleOS(goruntime.GOOS), goruntime.GOARCH),
		fmt.Sprintf("backend: %s", status.Backend),
		fmt.Sprintf("model: %s", cfg.Model),
		"",
		"日志范围：",
		feedbackRangeLabel(options.RangePreset, startAt, endAt),
		"",
		"反馈包文件：",
		filename,
		"",
		"说明：",
		"反馈包默认已脱敏，不包含明文登录态缓存与完整无限长请求体。可将该压缩包提交到 GitHub Issue，或直接发送给维护者。",
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func feedbackRangeLabel(preset string, startAt, endAt time.Time) string {
	switch strings.TrimSpace(preset) {
	case "30m":
		return "最近 30 分钟"
	case "2h":
		return "最近 2 小时"
	case "24h":
		return "最近 24 小时"
	case "7d":
		return "最近 7 天"
	case "all":
		return "全部"
	case "custom":
		return fmt.Sprintf("%s - %s", startAt.Format("2006/01/02 15:04"), endAt.Format("2006/01/02 15:04"))
	default:
		return "最近 30 分钟"
	}
}

func titleOS(osName string) string {
	switch osName {
	case "darwin":
		return "macOS"
	case "windows":
		return "Windows"
	default:
		return osName
	}
}

func writeFeedbackZip(path string, entries []feedbackZipEntry) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := zip.NewWriter(file)
	for _, entry := range entries {
		w, err := writer.Create(entry.name)
		if err != nil {
			_ = writer.Close()
			return err
		}
		if _, err := w.Write(entry.body); err != nil {
			_ = writer.Close()
			return err
		}
	}
	return writer.Close()
}

func mustJSON(value any) []byte {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return []byte("{}")
	}
	return data
}

func redactAndLimitPayload(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if payload, ok := trySanitizeJSON(trimmed); ok {
		return limitString(payload, feedbackPayloadCharLimit)
	}
	return limitString(redactPlainText(trimmed), feedbackPayloadCharLimit)
}

func trySanitizeJSON(raw string) (string, bool) {
	var payload any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "", false
	}
	sanitized := sanitizeValue("", payload)
	data, err := json.MarshalIndent(sanitized, "", "  ")
	if err != nil {
		return "", false
	}
	return string(data), true
}

func sanitizeValue(key string, value any) any {
	if isSensitiveFieldName(key) {
		return "[REDACTED]"
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for childKey, childValue := range typed {
			out[childKey] = sanitizeValue(childKey, childValue)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = sanitizeValue(key, child)
		}
		return out
	case string:
		return sanitizeStringValue(key, typed)
	default:
		return value
	}
}

func sanitizeStringValue(key, value string) string {
	if isSensitiveFieldName(key) {
		return "[REDACTED]"
	}
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "data:image/") {
		return "[REDACTED_IMAGE_DATA_URL]"
	}
	if looksLikeBase64Blob(trimmed) {
		return "[REDACTED_BINARY_DATA]"
	}
	return limitString(redactPlainText(value), feedbackStringFieldLimit)
}

func isSensitiveFieldName(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(key, "-", "_")))
	switch normalized {
	case "authorization", "cookie", "set_cookie", "x_api_key", "api_key", "access_token", "refresh_token", "token", "password", "secret", "machine_id", "machineid", "user_id", "userid":
		return true
	default:
		return strings.HasSuffix(normalized, "_token") || strings.HasSuffix(normalized, "_secret") || strings.HasSuffix(normalized, "_cookie")
	}
}

var (
	bearerPattern    = regexp.MustCompile(`(?i)(authorization["']?\s*[:=]\s*["']?bearer\s+)[^"'\s,}]+`)
	tokenPairPattern = regexp.MustCompile(`(?i)\b(access_token|refresh_token|token|api_key|cookie|set-cookie|password|secret)\b["']?\s*[:=]\s*["']?([^"'\n\r,}]+)`)
	machineIDPattern = regexp.MustCompile(`(?i)\b(machineid|machine_id|userid|user_id)\b["']?\s*[:=]\s*["']?([^"'\n\r,}]+)`)
)

func redactPlainText(value string) string {
	result := value
	result = bearerPattern.ReplaceAllString(result, "${1}[REDACTED]")
	result = tokenPairPattern.ReplaceAllString(result, `$1="[REDACTED]"`)
	result = machineIDPattern.ReplaceAllString(result, `$1="[REDACTED]"`)
	result = strings.ReplaceAll(result, "\u0000", "")
	return result
}

func looksLikeBase64Blob(value string) bool {
	if len(value) < 2048 {
		return false
	}
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '+' || r == '/' || r == '=' || r == '\n' || r == '\r' {
			continue
		}
		return false
	}
	return true
}

func limitString(value string, limit int) string {
	if limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + fmt.Sprintf("\n... [TRUNCATED %d chars]", len(runes)-limit)
}

func effectiveWarmupTimeout(cfg service.Config) time.Duration {
	if cfg.WarmupTimeout <= 0 {
		return proxyWarmupTimeout
	}
	return cfg.WarmupTimeout
}

func defaultConfig() service.Config {
	cfg := service.Config{
		Host:                  "127.0.0.1",
		Port:                  8095,
		Backend:               service.BackendRemote,
		Transport:             lingmaipc.TransportAuto,
		Cwd:                   defaultCwd(),
		Mode:                  "agent",
		Model:                 "kmodel",
		ShellType:             defaultShellType(),
		SessionMode:           service.SessionModeAuto,
		Timeout:               0,
		WarmupTimeout:         proxyWarmupTimeout,
		RemoteFallbackEnabled: true,
		RemoteFallbackModels:  service.DefaultRemoteFallbackModels(),
	}

	// Try to load config file from multiple locations
	configPaths := configSearchPaths()
	for _, configPath := range configPaths {
		if info, err := os.Stat(configPath); err == nil && !info.IsDir() {
			if data, err := os.ReadFile(configPath); err == nil {
				var fileCfg struct {
					Host                  string   `json:"host"`
					Port                  int      `json:"port"`
					Backend               string   `json:"backend"`
					Transport             string   `json:"transport"`
					Pipe                  string   `json:"pipe"`
					WebSocketURL          string   `json:"websocket_url"`
					RemoteBaseURL         string   `json:"remote_base_url"`
					RemoteAuthFile        string   `json:"remote_auth_file"`
					RemoteVersion         string   `json:"remote_version"`
					Cwd                   string   `json:"cwd"`
					CurrentFilePath       string   `json:"current_file_path"`
					Mode                  string   `json:"mode"`
					Model                 string   `json:"model"`
					ShellType             string   `json:"shell_type"`
					SessionMode           string   `json:"session_mode"`
					TimeoutSeconds        int      `json:"timeout"`
					WarmupTimeoutSeconds  int      `json:"warmup_timeout"`
					RemoteFallbackEnabled *bool    `json:"remote_fallback_enabled"`
					RemoteFallbackModels  []string `json:"remote_fallback_models"`
				}
				if err := json.Unmarshal(data, &fileCfg); err == nil {
					if fileCfg.Host != "" {
						cfg.Host = fileCfg.Host
					}
					if fileCfg.Port > 0 {
						cfg.Port = fileCfg.Port
					}
					if fileCfg.Backend != "" {
						cfg.Backend = service.BackendMode(fileCfg.Backend)
					}
					if fileCfg.Transport != "" {
						if t, err := lingmaipc.ParseTransport(fileCfg.Transport); err == nil {
							cfg.Transport = t
						}
					}
					if fileCfg.Pipe != "" {
						cfg.Pipe = fileCfg.Pipe
					}
					if fileCfg.WebSocketURL != "" {
						cfg.WebSocketURL = fileCfg.WebSocketURL
					}
					if fileCfg.RemoteBaseURL != "" {
						cfg.RemoteBaseURL = fileCfg.RemoteBaseURL
					}
					if fileCfg.RemoteAuthFile != "" {
						cfg.RemoteAuthFile = fileCfg.RemoteAuthFile
					}
					if fileCfg.RemoteVersion != "" {
						cfg.RemoteVersion = fileCfg.RemoteVersion
					}
					if fileCfg.Cwd != "" {
						cfg.Cwd = fileCfg.Cwd
					}
					if fileCfg.CurrentFilePath != "" {
						cfg.CurrentFilePath = fileCfg.CurrentFilePath
					}
					if fileCfg.Mode != "" {
						cfg.Mode = fileCfg.Mode
					}
					if fileCfg.Model != "" {
						cfg.Model = fileCfg.Model
					}
					if fileCfg.ShellType != "" {
						cfg.ShellType = fileCfg.ShellType
					}
					if fileCfg.SessionMode != "" {
						cfg.SessionMode = service.SessionMode(fileCfg.SessionMode)
					}
					if fileCfg.TimeoutSeconds >= 0 {
						cfg.Timeout = time.Duration(fileCfg.TimeoutSeconds) * time.Second
					}
					if fileCfg.WarmupTimeoutSeconds > 0 {
						cfg.WarmupTimeout = time.Duration(fileCfg.WarmupTimeoutSeconds) * time.Second
					}
					if fileCfg.RemoteFallbackEnabled != nil {
						cfg.RemoteFallbackEnabled = *fileCfg.RemoteFallbackEnabled
					}
					if len(fileCfg.RemoteFallbackModels) > 0 {
						cfg.RemoteFallbackModels = cleanConfigStrings(fileCfg.RemoteFallbackModels)
					}
				}
				break // loaded successfully
			}
		}
	}

	return cfg
}

func backendLabel(backend service.BackendMode) string {
	switch backend {
	case service.BackendRemote:
		return "远端 API"
	default:
		return "IPC 插件"
	}
}

func maskIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= 8 {
		return string(runes[:1]) + "***"
	}
	return string(runes[:4]) + "..." + string(runes[len(runes)-4:])
}

func cleanConfigStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		item := strings.TrimSpace(value)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func configSearchPaths() []string {
	var paths []string
	// 1. Executable directory (for dev / portable mode)
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(exe), "lingma-proxy.json"))
		paths = append(paths, filepath.Join(filepath.Dir(exe), "lingma-ipc-proxy.json"))
	}
	// 2. Current working directory
	if wd, err := os.Getwd(); err == nil {
		paths = append(paths, filepath.Join(wd, "lingma-proxy.json"))
		paths = append(paths, filepath.Join(wd, "lingma-ipc-proxy.json"))
	}
	// 3. User home directory
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, "lingma-proxy.json"))
		paths = append(paths, filepath.Join(home, "lingma-ipc-proxy.json"))
		paths = append(paths, filepath.Join(home, ".config", "lingma-proxy", "config.json"))
		paths = append(paths, filepath.Join(home, ".config", "lingma-ipc-proxy", "config.json"))
	}
	return paths
}

func defaultCwd() string {
	// Use the user's home directory as the default working directory
	// so it works out-of-the-box regardless of where the app is launched.
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

func defaultShellType() string {
	if goruntime.GOOS == "windows" {
		return "powershell"
	}
	return "zsh"
}

func transportFallbackHint() string {
	return "请确认 Lingma 插件已启动并登录；如果自动探测失败，请到设置页手动填写：远端 API 官方默认域名 https://lingma.alibabacloud.com，企业版请填写你的专属域名；macOS WebSocket 示例 ws://127.0.0.1:36510/，Windows Named Pipe 示例 \\\\.\\pipe\\lingma-xxxx，或 Windows WebSocket 示例 ws://127.0.0.1:36510/。"
}

func warmupFallbackHint(backend service.BackendMode) string {
	if backend == service.BackendRemote {
		return "请检查设置页“当前解析结果”里的远端域名是否为官方或企业专属 API 域名；如果出现 OSS/静态资源域名或模型列表 404，请手动填写远端 API 官方默认域名 https://lingma.alibabacloud.com，企业版请填写你的专属域名，并确认登录态未过期。"
	}
	return "请确认 Lingma 插件已启动并登录；如果自动探测失败，请到设置页手动填写：macOS WebSocket 示例 ws://127.0.0.1:36510/，Windows Named Pipe 示例 \\\\.\\pipe\\lingma-xxxx，或 Windows WebSocket 示例 ws://127.0.0.1:36510/。"
}
