package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"lingma-ipc-proxy/internal/feishu"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	updateChannel       = "feishu-stable"
	updateSchemaVersion = 1
	maxUpdateManifest   = 1 << 20
)

type OnlineUpdateStatus struct {
	Enabled       bool                    `json:"enabled"`
	Channel       string                  `json:"channel"`
	Current       string                  `json:"current"`
	Latest        string                  `json:"latest,omitempty"`
	Available     bool                    `json:"available"`
	Mandatory     bool                    `json:"mandatory"`
	State         string                  `json:"state"`
	ManifestURL   string                  `json:"manifestUrl,omitempty"`
	LastCheckedAt string                  `json:"lastCheckedAt,omitempty"`
	LastError     string                  `json:"lastError,omitempty"`
	ReleaseNotes  string                  `json:"releaseNotes,omitempty"`
	Asset         *OnlineUpdateAsset      `json:"asset,omitempty"`
	DownloadedTo  string                  `json:"downloadedTo,omitempty"`
	Progress      int                     `json:"progress,omitempty"`
	PromptPack    feishu.PromptPackStatus `json:"promptPack"`
}

type OnlineUpdateAsset struct {
	Platform  string `json:"platform"`
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
	Size      int64  `json:"size"`
	Kind      string `json:"kind"`
	Filename  string `json:"filename,omitempty"`
}

type updateManifest struct {
	SchemaVersion       int                          `json:"schemaVersion"`
	Channel             string                       `json:"channel"`
	Version             string                       `json:"version"`
	MinSupportedVersion string                       `json:"minSupportedVersion,omitempty"`
	Mandatory           bool                         `json:"mandatory,omitempty"`
	ReleaseNotes        string                       `json:"releaseNotes,omitempty"`
	Assets              map[string]OnlineUpdateAsset `json:"assets"`
}

var updaterState = struct {
	sync.Mutex
	status OnlineUpdateStatus
}{}

func initialOnlineUpdateStatus() OnlineUpdateStatus {
	return OnlineUpdateStatus{
		Enabled:     strings.TrimSpace(updateManifestURL) != "" && strings.TrimSpace(updatePublicKey) != "",
		Channel:     updateChannel,
		Current:     desktopAppVersion,
		State:       "idle",
		ManifestURL: strings.TrimSpace(updateManifestURL),
		PromptPack:  feishu.GetPromptPackStatus(),
	}
}

func (a *App) configureOnlineUpdates(dataDir string) {
	feishu.ConfigurePromptPack(feishu.PromptPackOptions{
		ManifestURL:     promptPackManifestURL,
		PublicKeyBase64: promptPackPublicKey,
		DataDir:         dataDir,
		AppVersion:      desktopAppVersion,
	})
	updaterState.Lock()
	updaterState.status = initialOnlineUpdateStatus()
	updaterState.Unlock()
}

func (a *App) startOnlineUpdateChecks() {
	go func() {
		time.Sleep(30 * time.Second)
		_, _ = a.CheckPromptPackUpdate()
		_, _ = a.CheckForUpdates()
		ticker := time.NewTicker(12 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			_, _ = a.CheckPromptPackUpdate()
			_, _ = a.CheckForUpdates()
		}
	}()
}

func (a *App) GetOnlineUpdateStatus() OnlineUpdateStatus {
	updaterState.Lock()
	defer updaterState.Unlock()
	if updaterState.status.Current == "" {
		updaterState.status = initialOnlineUpdateStatus()
	}
	updaterState.status.PromptPack = feishu.GetPromptPackStatus()
	return updaterState.status
}

func (a *App) CheckPromptPackUpdate() (feishu.PromptPackStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	status, err := feishu.CheckPromptPackUpdate(ctx)
	updaterState.Lock()
	if updaterState.status.Current == "" {
		updaterState.status = initialOnlineUpdateStatus()
	}
	updaterState.status.PromptPack = status
	updaterState.Unlock()
	runtime.EventsEmit(a.ctx, "updates:status", a.GetOnlineUpdateStatus())
	if err != nil {
		a.emitLogWithSource("updates", "warn", "Prompt Pack 更新检查失败："+err.Error())
	} else {
		a.emitLogWithSource("updates", "info", "Prompt Pack 已更新："+status.Version)
	}
	return status, err
}

func (a *App) CheckForUpdates() (OnlineUpdateStatus, error) {
	now := time.Now().Format(time.RFC3339)
	if strings.TrimSpace(updateManifestURL) == "" {
		status := a.updateStatus(func(s *OnlineUpdateStatus) {
			s.State = "disabled"
			s.LastCheckedAt = now
			s.LastError = "未配置更新 manifest URL"
		})
		return status, errors.New(status.LastError)
	}
	if strings.TrimSpace(updatePublicKey) == "" {
		status := a.updateStatus(func(s *OnlineUpdateStatus) {
			s.State = "disabled"
			s.LastCheckedAt = now
			s.LastError = "未配置更新签名公钥"
		})
		return status, errors.New(status.LastError)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	manifest, err := fetchUpdateManifest(ctx, updateManifestURL)
	if err != nil {
		status := a.updateStatus(func(s *OnlineUpdateStatus) {
			s.State = "error"
			s.LastCheckedAt = now
			s.LastError = err.Error()
		})
		return status, err
	}
	asset, ok := selectUpdateAsset(manifest)
	if !ok {
		err := fmt.Errorf("更新 manifest 未提供当前平台资产: %s", updatePlatformKey())
		status := a.updateStatus(func(s *OnlineUpdateStatus) {
			s.State = "error"
			s.LastCheckedAt = now
			s.LastError = err.Error()
		})
		return status, err
	}
	available := compareVersion(manifest.Version, desktopAppVersion) > 0
	status := a.updateStatus(func(s *OnlineUpdateStatus) {
		s.Enabled = true
		s.State = "checked"
		s.LastCheckedAt = now
		s.LastError = ""
		s.Latest = manifest.Version
		s.Available = available
		s.Mandatory = manifest.Mandatory || !minVersionSatisfied(desktopAppVersion, manifest.MinSupportedVersion)
		s.ReleaseNotes = manifest.ReleaseNotes
		if available {
			next := asset
			next.Platform = updatePlatformKey()
			s.Asset = &next
		} else {
			s.Asset = nil
		}
	})
	runtime.EventsEmit(a.ctx, "updates:status", status)
	return status, nil
}

func (a *App) DownloadAndInstallUpdate() (OnlineUpdateStatus, error) {
	status := a.GetOnlineUpdateStatus()
	if !status.Available || status.Asset == nil {
		next, err := a.CheckForUpdates()
		if err != nil {
			return next, err
		}
		status = next
	}
	if status.Asset == nil {
		return status, errors.New("当前没有可安装更新")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	downloaded, err := a.downloadUpdateAsset(ctx, *status.Asset)
	if err != nil {
		return a.updateStatus(func(s *OnlineUpdateStatus) {
			s.State = "error"
			s.LastError = err.Error()
		}), err
	}
	if err := revealDownloadedUpdate(downloaded); err != nil {
		return a.updateStatus(func(s *OnlineUpdateStatus) {
			s.State = "error"
			s.LastError = err.Error()
			s.DownloadedTo = downloaded
		}), err
	}
	next := a.updateStatus(func(s *OnlineUpdateStatus) {
		s.State = "downloaded"
		s.DownloadedTo = downloaded
		s.Progress = 100
	})
	runtime.EventsEmit(a.ctx, "updates:status", next)
	return next, nil
}

func (a *App) downloadUpdateAsset(ctx context.Context, asset OnlineUpdateAsset) (string, error) {
	if err := validateHTTPSURL(asset.URL); err != nil {
		return "", err
	}
	if strings.TrimSpace(asset.SHA256) == "" || strings.TrimSpace(asset.Signature) == "" {
		return "", errors.New("更新资产缺少 sha256/signature")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("下载更新失败: HTTP %d", resp.StatusCode)
	}
	dir, err := updateCacheDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	filename := strings.TrimSpace(asset.Filename)
	if filename == "" {
		filename = filepath.Base(req.URL.Path)
	}
	if filename == "." || filename == "/" || filename == "" {
		filename = "lingma-proxy-update.bin"
	}
	path := filepath.Join(dir, filepath.Base(filename))
	file, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha256.New()
	var written int64
	buf := make([]byte, 256*1024)
	total := asset.Size
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if _, err := file.Write(chunk); err != nil {
				return "", err
			}
			if _, err := hasher.Write(chunk); err != nil {
				return "", err
			}
			written += int64(n)
			if total > 0 {
				progress := int(float64(written) / float64(total) * 100)
				if progress > 99 {
					progress = 99
				}
				a.updateStatus(func(s *OnlineUpdateStatus) {
					s.State = "downloading"
					s.Progress = progress
				})
				runtime.EventsEmit(a.ctx, "updates:status", a.GetOnlineUpdateStatus())
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	got := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(got, strings.TrimSpace(asset.SHA256)) {
		return "", fmt.Errorf("更新资产 sha256 不匹配: got %s", got)
	}
	if err := verifySignature(asset.SHA256, asset.Signature, updatePublicKey); err != nil {
		return "", err
	}
	a.updateStatus(func(s *OnlineUpdateStatus) {
		s.State = "downloaded"
		s.Progress = 100
		s.DownloadedTo = path
	})
	return path, nil
}

func (a *App) updateStatus(mutator func(*OnlineUpdateStatus)) OnlineUpdateStatus {
	updaterState.Lock()
	defer updaterState.Unlock()
	if updaterState.status.Current == "" {
		updaterState.status = initialOnlineUpdateStatus()
	}
	mutator(&updaterState.status)
	updaterState.status.PromptPack = feishu.GetPromptPackStatus()
	return updaterState.status
}

func fetchUpdateManifest(ctx context.Context, manifestURL string) (updateManifest, error) {
	if err := validateHTTPSURL(manifestURL); err != nil {
		return updateManifest{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return updateManifest{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return updateManifest{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return updateManifest{}, fmt.Errorf("更新 manifest 下载失败: HTTP %d", resp.StatusCode)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, io.LimitReader(resp.Body, maxUpdateManifest+1)); err != nil {
		return updateManifest{}, err
	}
	if buf.Len() > maxUpdateManifest {
		return updateManifest{}, errors.New("更新 manifest 超过大小上限")
	}
	var manifest updateManifest
	if err := json.Unmarshal(buf.Bytes(), &manifest); err != nil {
		return updateManifest{}, err
	}
	if manifest.SchemaVersion != updateSchemaVersion {
		return updateManifest{}, fmt.Errorf("不支持的更新 manifest schemaVersion: %d", manifest.SchemaVersion)
	}
	if manifest.Channel != updateChannel {
		return updateManifest{}, fmt.Errorf("更新通道不匹配: %s", manifest.Channel)
	}
	if strings.TrimSpace(manifest.Version) == "" {
		return updateManifest{}, errors.New("更新 manifest 缺少 version")
	}
	return manifest, nil
}

func selectUpdateAsset(manifest updateManifest) (OnlineUpdateAsset, bool) {
	asset, ok := manifest.Assets[updatePlatformKey()]
	return asset, ok
}

func updatePlatformKey() string {
	return goruntime.GOOS + "-" + goruntime.GOARCH
}

func validateHTTPSURL(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("只允许 HTTPS URL: %s", rawURL)
	}
	return nil
}

func verifySignature(shaHex string, signatureBase64 string, publicKeyBase64 string) error {
	keyBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(publicKeyBase64))
	if err != nil || len(keyBytes) != ed25519.PublicKeySize {
		return errors.New("更新签名公钥无效")
	}
	sigBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signatureBase64))
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return errors.New("更新签名无效")
	}
	if !ed25519.Verify(ed25519.PublicKey(keyBytes), []byte(strings.TrimSpace(shaHex)), sigBytes) {
		return errors.New("更新签名校验失败")
	}
	return nil
}

func updateCacheDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(dir) == "" {
		dir, err = os.UserConfigDir()
	}
	if err != nil || strings.TrimSpace(dir) == "" {
		return "", errors.New("无法定位用户缓存目录")
	}
	return filepath.Join(dir, "lingma-proxy", "updates"), nil
}

func revealDownloadedUpdate(path string) error {
	switch goruntime.GOOS {
	case "darwin":
		return exec.Command("open", "-R", path).Start()
	case "windows":
		return exec.Command("explorer.exe", "/select,"+path).Start()
	default:
		return exec.Command("xdg-open", filepath.Dir(path)).Start()
	}
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
