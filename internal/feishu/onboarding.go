package feishu

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var urlPattern = regexp.MustCompile(`https?://[^\s]+`)

type cliConfigFile struct {
	Apps []struct {
		AppID     string `json:"appId"`
		AppSecret struct {
			Source string `json:"source"`
			ID     string `json:"id"`
		} `json:"appSecret"`
		Brand string `json:"brand"`
		Lang  string `json:"lang"`
	} `json:"apps"`
}

func larkConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lark-cli", "config.json"), nil
}

func readCLIConfigStatus() ConfigStatus {
	path, err := larkConfigPath()
	if err != nil {
		return ConfigStatus{Message: err.Error()}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ConfigStatus{Path: path, Message: "未检测到飞书 CLI 配置"}
	}
	var cfg cliConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ConfigStatus{Path: path, Message: "飞书 CLI 配置解析失败"}
	}
	if len(cfg.Apps) == 0 {
		return ConfigStatus{Path: path, Message: "飞书 CLI 尚未完成应用初始化"}
	}
	app := cfg.Apps[0]
	return ConfigStatus{
		Configured: true,
		AppID:      strings.TrimSpace(app.AppID),
		Brand:      strings.TrimSpace(app.Brand),
		Path:       path,
	}
}

func readAuthStatus(ctx context.Context) AuthStatus {
	cmd := commandContextWithEnv(ctx, "lark-cli", "auth", "status", "--verify")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return AuthStatus{Message: strings.TrimSpace(string(output))}
	}
	var payload struct {
		AppID            string `json:"appId"`
		Brand            string `json:"brand"`
		Identity         string `json:"identity"`
		UserName         string `json:"userName"`
		UserOpenID       string `json:"userOpenId"`
		ExpiresAt        string `json:"expiresAt"`
		RefreshExpiresAt string `json:"refreshExpiresAt"`
		TokenStatus      string `json:"tokenStatus"`
		Verified         bool   `json:"verified"`
	}
	if err := json.Unmarshal(output, &payload); err != nil {
		return AuthStatus{Message: "飞书 CLI 授权状态解析失败"}
	}
	return AuthStatus{
		Authorized:       payload.Verified,
		Verified:         payload.Verified,
		Identity:         payload.Identity,
		UserName:         payload.UserName,
		UserOpenID:       payload.UserOpenID,
		ExpiresAt:        payload.ExpiresAt,
		RefreshExpiresAt: payload.RefreshExpiresAt,
		TokenStatus:      payload.TokenStatus,
	}
}

func commandShell(goos string) (string, []string) {
	switch goos {
	case "windows":
		return "cmd", []string{"/C"}
	default:
		return "", nil
	}
}

func storeSecret(serviceName, appID, secret string) error {
	switch runtime.GOOS {
	case "darwin":
		cmd := commandWithEnv("security", "add-generic-password", "-U", "-a", appID, "-s", serviceName, "-w", secret)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("store secret in keychain failed: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return nil
	case "windows":
		target := serviceName + ":" + appID
		cmd := commandWithEnv("cmdkey", "/generic:"+target, "/user:"+appID, "/pass:"+secret)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("store secret in credential manager failed: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return nil
	default:
		return nil
	}
}

func runStreamingCommand(ctx context.Context, cmd *exec.Cmd, onLine func(string), onURL func(string)) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	scan := func(scanner *bufio.Scanner) {
		for scanner.Scan() {
			line := scanner.Text()
			if onLine != nil {
				onLine(line)
			}
			if onURL != nil {
				if match := urlPattern.FindString(line); match != "" {
					onURL(strings.TrimRight(match, ".,)"))
				}
			}
		}
	}
	go scan(bufio.NewScanner(stdout))
	go scan(bufio.NewScanner(stderr))
	return cmd.Wait()
}

func formatOutputLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return ""
	}
	return trimmed
}

func timestampNow() string {
	return time.Now().Format(time.RFC3339)
}
