package deploy

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lingma-ipc-proxy/internal/remote"
	"lingma-ipc-proxy/internal/service"
)

type ServerBundleOptions struct {
	AuthFile      string
	OutputPath    string
	PickPolicy    remote.CredentialPickPolicy
	BaseURL       string
	ProxyURL      string
	RemoteVersion string
	Host          string
	Port          int
	Model         string
}

type ServerBundleResult struct {
	Path          string `json:"path"`
	Filename      string `json:"filename"`
	SaveDir       string `json:"saveDir"`
	CredentialSrc string `json:"credentialSource"`
	TokenExpireAt string `json:"tokenExpireAt,omitempty"`
	TokenExpired  bool   `json:"tokenExpired"`
	UserID        string `json:"userId,omitempty"`
	MachineID     string `json:"machineId,omitempty"`
}

type zipEntry struct {
	name string
	body []byte
	mode os.FileMode
}

func WriteServerBundle(options ServerBundleOptions) (ServerBundleResult, error) {
	result := ServerBundleResult{}
	outputPath := expandHome(strings.TrimSpace(options.OutputPath))
	if outputPath == "" {
		return result, fmt.Errorf("output path is required")
	}
	if !strings.HasSuffix(strings.ToLower(outputPath), ".zip") {
		outputPath += ".zip"
	}
	if options.Port <= 0 {
		options.Port = 8095
	}
	if strings.TrimSpace(options.Host) == "" {
		options.Host = "0.0.0.0"
	}
	if strings.TrimSpace(options.Model) == "" {
		options.Model = "kmodel"
	}

	cred, err := remote.LoadCredentialByPolicy(options.AuthFile, options.PickPolicy)
	if err != nil {
		return result, err
	}
	credentialJSON, err := marshalCredential(cred)
	if err != nil {
		return result, err
	}

	configJSON, err := marshalJSON(map[string]any{
		"host":                    options.Host,
		"port":                    options.Port,
		"backend":                 string(service.BackendRemote),
		"remote_base_url":         strings.TrimSpace(options.BaseURL),
		"remote_auth_file":        "/credentials.json",
		"remote_proxy_url":        strings.TrimSpace(options.ProxyURL),
		"remote_version":          strings.TrimSpace(options.RemoteVersion),
		"model":                   options.Model,
		"session_mode":            string(service.SessionModeAuto),
		"timeout":                 0,
		"remote_fallback_enabled": true,
		"remote_fallback_models":  service.DefaultRemoteFallbackModels(),
	})
	if err != nil {
		return result, err
	}

	entries := []zipEntry{
		{name: "credentials.json", body: credentialJSON, mode: 0600},
		{name: "lingma-proxy.json", body: configJSON, mode: 0644},
		{name: "docker-compose.yml", body: []byte(dockerComposeYAML(options.Port)), mode: 0644},
		{name: "README.txt", body: []byte(bundleReadme(options.Port)), mode: 0644},
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return result, err
	}
	if err := writeZip(outputPath, entries); err != nil {
		return result, err
	}

	result = ServerBundleResult{
		Path:          outputPath,
		Filename:      filepath.Base(outputPath),
		SaveDir:       filepath.Dir(outputPath),
		CredentialSrc: cred.Source,
		TokenExpired:  remote.IsExpired(cred, 0),
		UserID:        maskIdentifier(cred.UserID),
		MachineID:     maskIdentifier(cred.MachineID),
	}
	if cred.TokenExpireTime > 0 {
		result.TokenExpireAt = time.UnixMilli(cred.TokenExpireTime).Format(time.RFC3339)
	}
	return result, nil
}

func WriteCredentialFile(authFile, outputPath string, policy remote.CredentialPickPolicy) (ServerBundleResult, error) {
	result := ServerBundleResult{}
	cred, err := remote.LoadCredentialByPolicy(authFile, policy)
	if err != nil {
		return result, err
	}
	path := expandHome(strings.TrimSpace(outputPath))
	if path == "" {
		return result, fmt.Errorf("output path is required")
	}
	if err := remote.SaveCredentialFile(cred, path); err != nil {
		return result, err
	}
	result = ServerBundleResult{
		Path:          path,
		Filename:      filepath.Base(path),
		SaveDir:       filepath.Dir(path),
		CredentialSrc: cred.Source,
		TokenExpired:  remote.IsExpired(cred, 0),
		UserID:        maskIdentifier(cred.UserID),
		MachineID:     maskIdentifier(cred.MachineID),
	}
	if cred.TokenExpireTime > 0 {
		result.TokenExpireAt = time.UnixMilli(cred.TokenExpireTime).Format(time.RFC3339)
	}
	return result, nil
}

func marshalCredential(cred remote.Credential) ([]byte, error) {
	tmpDir, err := os.MkdirTemp("", "lingma-credential-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)
	path := filepath.Join(tmpDir, "credentials.json")
	if err := remote.SaveCredentialFile(cred, path); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func marshalJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func dockerComposeYAML(port int) string {
	return fmt.Sprintf(`services:
  lingma-proxy:
    image: ghcr.io/lutiancheng1/lingma-proxy:latest
    restart: unless-stopped
    ports:
      - "%d:8095"
    volumes:
      - ./credentials.json:/credentials.json:ro
      - ./lingma-proxy.json:/lingma-proxy.json:ro
    command:
      - --config
      - /lingma-proxy.json
`, port)
}

func bundleReadme(port int) string {
	return fmt.Sprintf(`Lingma Proxy server deployment bundle

This bundle contains a portable credentials.json exported from this machine.
Keep it private. Do not commit it to Git or upload it to a public location.

Capability boundary:

  This bundle only includes Remote API credentials and proxy config. It does
  not include QoderCN / Lingma, IDE plugins, or a local IPC runtime. On a server
  without QoderCN / Lingma running, text APIs, model listing, and Remote API
  tool calls can work with credentials.json, but IPC plugin mode and image
  fallback are unavailable or limited.

Docker quick start:

  unzip lingma-proxy-server-bundle.zip
  docker compose up -d

Direct CLI start without Docker:

  lingma-proxy --config ./lingma-proxy.json

API endpoint after startup:

  http://127.0.0.1:%d/v1/chat/completions

If you copy the files to your own server, keep credentials.json next to
lingma-proxy.json and docker-compose.yml. The login token can expire, so export
a fresh bundle when the server starts returning authentication errors.
`, port)
}

func writeZip(path string, entries []zipEntry) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := zip.NewWriter(file)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetMode(entry.mode)
		w, err := writer.CreateHeader(header)
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

func expandHome(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == "~" || !strings.HasPrefix(path, "~/") {
		if path == "~" {
			if home, err := os.UserHomeDir(); err == nil {
				return home
			}
		}
		return path
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return path
}

func maskIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return strings.Repeat("*", len(value))
	}
	return value[:3] + strings.Repeat("*", len(value)-6) + value[len(value)-3:]
}
