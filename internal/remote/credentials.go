package remote

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Credential struct {
	CosyKey         string
	EncryptUserInfo string
	UserID          string
	MachineID       string
	Source          string
	TokenExpireTime int64
}

type CredentialPickPolicy string

const (
	CredentialPickAuto    CredentialPickPolicy = "auto"
	CredentialPickNewest  CredentialPickPolicy = "newest"
	CredentialPickLongest CredentialPickPolicy = "longest"
)

type CredentialInspection struct {
	Status             string  `json:"status"`
	CacheDir           string  `json:"cache_dir,omitempty"`
	Source             string  `json:"source,omitempty"`
	UserFileModifiedAt string  `json:"user_file_modified_at,omitempty"`
	TokenExpireAt      string  `json:"token_expire_at,omitempty"`
	DaysLeft           float64 `json:"days_left,omitempty"`
	TokenExpired       bool    `json:"token_expired"`
	UserID             string  `json:"user_id,omitempty"`
	MachineID          string  `json:"machine_id,omitempty"`
	Error              string  `json:"error,omitempty"`
}

type storedCredentialFile struct {
	Source          string `json:"source"`
	TokenExpireTime string `json:"token_expire_time"`
	Auth            struct {
		CosyKey         string `json:"cosy_key"`
		EncryptUserInfo string `json:"encrypt_user_info"`
		UserID          string `json:"user_id"`
		MachineID       string `json:"machine_id"`
	} `json:"auth"`
}

func LoadCredential(authFile string) (Credential, error) {
	if path := strings.TrimSpace(authFile); path != "" {
		return loadCredentialFile(expandHome(path))
	}
	return importLingmaCacheCredential()
}

func LoadCredentialByPolicy(authFile string, policy CredentialPickPolicy) (Credential, error) {
	if path := strings.TrimSpace(authFile); path != "" || policy == "" || policy == CredentialPickAuto {
		return LoadCredential(path)
	}

	var best credentialCandidate
	var attempts []credentialLoadAttempt
	for _, cacheDir := range candidateLingmaCacheDirs() {
		candidate, err := loadCredentialCandidate(cacheDir)
		if err != nil {
			attempts = append(attempts, credentialLoadAttempt{Path: cacheDir, Err: err})
			continue
		}
		if best.Cred.Source == "" || betterCredentialCandidate(candidate, best, policy) {
			best = candidate
		}
	}
	if best.Cred.Source != "" {
		return best.Cred, nil
	}
	if len(attempts) == 0 {
		return Credential{}, errors.New("no Lingma cache directory candidate was found")
	}
	return Credential{}, fmt.Errorf("load Lingma/QoderCN login cache: %s", summarizeCredentialLoadAttempts(attempts))
}

func SaveCredentialFile(cred Credential, path string) error {
	if err := validateCredential(cred); err != nil {
		return err
	}
	outputPath := expandHome(strings.TrimSpace(path))
	if outputPath == "" {
		return errors.New("remote credential output path is required")
	}
	var stored storedCredentialFile
	stored.Source = cred.Source
	if cred.TokenExpireTime > 0 {
		stored.TokenExpireTime = strconv.FormatInt(cred.TokenExpireTime, 10)
	}
	stored.Auth.CosyKey = cred.CosyKey
	stored.Auth.EncryptUserInfo = cred.EncryptUserInfo
	stored.Auth.UserID = cred.UserID
	stored.Auth.MachineID = cred.MachineID

	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0700); err != nil {
		return err
	}
	tmp := outputPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, outputPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func InspectCredentialCandidates() []CredentialInspection {
	inspections := make([]CredentialInspection, 0)
	for _, cacheDir := range candidateLingmaCacheDirs() {
		userPath, ok := firstExistingCredentialUserFile(cacheDir)
		if !ok {
			continue
		}
		userInfo, statErr := os.Stat(userPath)
		if statErr != nil {
			continue
		}
		inspection := CredentialInspection{
			Status:             "invalid",
			CacheDir:           cacheDir,
			Source:             userPath,
			UserFileModifiedAt: userInfo.ModTime().Format(time.RFC3339),
		}
		candidate, err := loadCredentialCandidate(cacheDir)
		if err != nil {
			inspection.Error = compactCredentialError(err)
			inspections = append(inspections, inspection)
			continue
		}
		inspection.Status = "ok"
		inspection.Source = candidate.Cred.Source
		inspection.UserID = maskIdentifier(candidate.Cred.UserID)
		inspection.MachineID = maskIdentifier(candidate.Cred.MachineID)
		inspection.TokenExpired = IsExpired(candidate.Cred, 0)
		if candidate.Cred.TokenExpireTime > 0 {
			expireAt := time.UnixMilli(candidate.Cred.TokenExpireTime)
			inspection.TokenExpireAt = expireAt.Format(time.RFC3339)
			inspection.DaysLeft = time.Until(expireAt).Hours() / 24
		}
		inspections = append(inspections, inspection)
	}
	return inspections
}

func loadCredentialFile(path string) (Credential, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Credential{}, fmt.Errorf("read remote auth file: %w", err)
	}
	var stored storedCredentialFile
	if err := json.Unmarshal(body, &stored); err != nil {
		return Credential{}, fmt.Errorf("parse remote auth file: %w", err)
	}
	cred := Credential{
		CosyKey:         stored.Auth.CosyKey,
		EncryptUserInfo: stored.Auth.EncryptUserInfo,
		UserID:          stored.Auth.UserID,
		MachineID:       stored.Auth.MachineID,
		Source:          valueOr(stored.Source, path),
		TokenExpireTime: parseExpire(stored.TokenExpireTime),
	}
	return cred, validateCredential(cred)
}

func importLingmaCacheCredential() (Credential, error) {
	var attempts []credentialLoadAttempt
	for _, lingmaDir := range candidateLingmaCacheDirs() {
		cred, err := importLingmaCacheCredentialFromDir(lingmaDir)
		if err == nil {
			return cred, nil
		}
		attempts = append(attempts, credentialLoadAttempt{Path: lingmaDir, Err: err})
	}
	if len(attempts) == 0 {
		return Credential{}, errors.New("no Lingma cache directory candidate was found")
	}
	return Credential{}, fmt.Errorf("load Lingma/QoderCN login cache: %s", summarizeCredentialLoadAttempts(attempts))
}

type credentialCandidate struct {
	Cred         Credential
	UserModified time.Time
}

func loadCredentialCandidate(cacheDir string) (credentialCandidate, error) {
	userPath, ok := firstExistingCredentialUserFile(cacheDir)
	if !ok {
		return credentialCandidate{}, os.ErrNotExist
	}
	info, err := os.Stat(userPath)
	if err != nil {
		return credentialCandidate{}, err
	}
	cred, err := importLingmaCacheCredentialFromDir(cacheDir)
	if err != nil {
		return credentialCandidate{}, err
	}
	return credentialCandidate{Cred: cred, UserModified: info.ModTime()}, nil
}

// candidateCredentialUserFiles lists the encrypted credential blob locations we
// support: the legacy IDE cache (cache/user) and the QoderCN CLI login (.auth/user).
func candidateCredentialUserFiles(lingmaDir string) []string {
	return uniquePathStrings([]string{
		filepath.Join(lingmaDir, "cache", "user"),
		filepath.Join(lingmaDir, ".auth", "user"),
	})
}

func firstExistingCredentialUserFile(lingmaDir string) (string, bool) {
	for _, path := range candidateCredentialUserFiles(lingmaDir) {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, true
		}
	}
	return "", false
}

func betterCredentialCandidate(candidate, best credentialCandidate, policy CredentialPickPolicy) bool {
	switch policy {
	case CredentialPickNewest:
		return candidate.UserModified.After(best.UserModified)
	case CredentialPickLongest:
		return candidate.Cred.TokenExpireTime > best.Cred.TokenExpireTime
	default:
		return false
	}
}

func importLingmaCacheCredentialFromDir(lingmaDir string) (Credential, error) {
	userPath, ok := firstExistingCredentialUserFile(lingmaDir)
	if !ok {
		userPath = filepath.Join(lingmaDir, "cache", "user")
	}
	encrypted, err := os.ReadFile(userPath)
	if err != nil {
		return Credential{}, fmt.Errorf("read %s: %w", userPath, err)
	}
	machineID, err := loadMachineID(lingmaDir)
	if err != nil {
		return Credential{}, err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encrypted)))
	if err != nil {
		return Credential{}, fmt.Errorf("decode %s: %w", userPath, err)
	}
	plaintext, err := decryptCacheUser(machineID, ciphertext)
	if err != nil {
		return Credential{}, err
	}
	var payload struct {
		Key             string `json:"key"`
		EncryptUserInfo string `json:"encrypt_user_info"`
		UserID          string `json:"uid"`
		ExpireTime      any    `json:"expire_time"`
	}
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return Credential{}, fmt.Errorf("parse %s: %w", userPath, err)
	}
	cred := Credential{
		CosyKey:         payload.Key,
		EncryptUserInfo: payload.EncryptUserInfo,
		UserID:          payload.UserID,
		MachineID:       machineID,
		Source:          userPath,
		TokenExpireTime: parseExpireAny(payload.ExpireTime),
	}
	return cred, validateCredential(cred)
}

func candidateLingmaCacheDirs() []string {
	if explicit := strings.TrimSpace(os.Getenv("LINGMA_CACHE_DIR")); explicit != "" {
		return []string{expandHome(explicit)}
	}

	var dirs []string
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		dirs = append(dirs,
			filepath.Join(home, ".qoder-cn"),
			filepath.Join(home, ".qoder-cn", "shared_client"),
			filepath.Join(home, ".qoder-cn", "vscode", "sharedClientCache"),
			filepath.Join(home, ".qodercn"),
			filepath.Join(home, ".qodercn", "vscode", "sharedClientCache"),
			filepath.Join(home, "Library", "Application Support", "QoderCN", "SharedClientCache"),
			filepath.Join(home, "Library", "Application Support", "Qoder", "SharedClientCache"),
			filepath.Join(home, ".config", "QoderCN"),
			filepath.Join(home, ".config", "QoderCN", "SharedClientCache"),
			filepath.Join(home, ".config", "Qoder", "SharedClientCache"),
			filepath.Join(home, ".local", "share", "QoderCN"),
			filepath.Join(home, ".lingma"),
			filepath.Join(home, ".lingma", "vscode", "sharedClientCache"),
			filepath.Join(home, "Library", "Application Support", "Lingma", "SharedClientCache"),
			filepath.Join(home, ".config", "Lingma"),
			filepath.Join(home, ".config", "Lingma", "SharedClientCache"),
			filepath.Join(home, ".local", "share", "Lingma"),
		)
		dirs = append(dirs, vscodeGlobalStorageCacheDirs(filepath.Join(home, ".config"))...)
	}
	for _, envName := range []string{"APPDATA", "LOCALAPPDATA", "ProgramData"} {
		if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
			dirs = append(dirs,
				filepath.Join(value, "QoderCN"),
				filepath.Join(value, "qodercn"),
				filepath.Join(value, "QoderCN", "SharedClientCache"),
				filepath.Join(value, "Qoder"),
				filepath.Join(value, "Qoder", "SharedClientCache"),
				filepath.Join(value, "Lingma"),
				filepath.Join(value, "lingma"),
				filepath.Join(value, "Lingma", "SharedClientCache"),
			)
		}
	}
	if value := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); value != "" {
		dirs = append(dirs, filepath.Join(value, "QoderCN"))
		dirs = append(dirs, filepath.Join(value, "QoderCN", "SharedClientCache"))
		dirs = append(dirs, filepath.Join(value, "Qoder", "SharedClientCache"))
		dirs = append(dirs, filepath.Join(value, "Lingma"))
		dirs = append(dirs, filepath.Join(value, "Lingma", "SharedClientCache"))
		dirs = append(dirs, vscodeGlobalStorageCacheDirs(value)...)
	}
	return uniquePathStrings(dirs)
}

func vscodeGlobalStorageCacheDirs(root string) []string {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}
	return []string{
		filepath.Join(root, "Code", "User", "globalStorage", "alibaba-cloud.tongyi-lingma"),
		filepath.Join(root, "Code - OSS", "User", "globalStorage", "alibaba-cloud.tongyi-lingma"),
		filepath.Join(root, "VSCodium", "User", "globalStorage", "alibaba-cloud.tongyi-lingma"),
		filepath.Join(root, "Cursor", "User", "globalStorage", "alibaba-cloud.tongyi-lingma"),
		filepath.Join(root, "Windsurf", "User", "globalStorage", "alibaba-cloud.tongyi-lingma"),
	}
}

type credentialLoadAttempt struct {
	Path string
	Err  error
}

func summarizeCredentialLoadAttempts(attempts []credentialLoadAttempt) string {
	if strings.TrimSpace(os.Getenv("LINGMA_VERBOSE_CREDENTIAL_ERRORS")) == "1" {
		details := make([]string, 0, len(attempts))
		for _, attempt := range attempts {
			details = append(details, fmt.Sprintf("%s: %v", attempt.Path, attempt.Err))
		}
		return strings.Join(details, "; ")
	}

	missingCount := 0
	invalidSamples := make([]string, 0, 3)
	for _, attempt := range attempts {
		if isMissingCredentialAttempt(attempt.Err) {
			missingCount++
			continue
		}
		if len(invalidSamples) < 3 {
			invalidSamples = append(invalidSamples, fmt.Sprintf("%s: %v", compactCredentialPath(attempt.Path), attempt.Err))
		}
	}

	parts := []string{
		fmt.Sprintf("未找到可用的 QoderCN/Lingma 登录缓存或缓存格式不兼容（已检查 %d 个候选位置）", len(attempts)),
	}
	if missingCount > 0 {
		parts = append(parts, fmt.Sprintf("%d 个位置不存在登录缓存", missingCount))
	}
	if len(invalidSamples) > 0 {
		parts = append(parts, "不兼容缓存示例："+strings.Join(invalidSamples, "; "))
	}
	parts = append(parts, "请确认 QoderCN/Lingma 已启动并登录；如需完整候选路径，请设置 LINGMA_VERBOSE_CREDENTIAL_ERRORS=1 后重试")
	return strings.Join(parts, "；")
}

func isMissingCredentialAttempt(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "cannot find the path specified") ||
		strings.Contains(text, "no such file or directory") ||
		strings.Contains(text, "system cannot find the file specified")
}

func compactCredentialPath(path string) string {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if cleaned == "" {
		return path
	}
	parts := strings.FieldsFunc(cleaned, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	if len(parts) <= 3 {
		return cleaned
	}
	return filepath.Join("...", filepath.Join(parts[len(parts)-3:]...))
}

func loadMachineID(lingmaDir string) (string, error) {
	for _, path := range candidateMachineIDFiles(lingmaDir) {
		if body, err := os.ReadFile(path); err == nil {
			if value := strings.TrimSpace(string(body)); value != "" {
				return value, nil
			}
		}
	}

	for _, path := range candidateMachineIDLogFiles(lingmaDir) {
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if value := extractMachineIDFromText(string(body)); value != "" {
			return value, nil
		}
	}

	return "", errors.New("remote credential requires cache/id, cli/.auth/id, or Lingma/QoderCN log machine id; checked cache/id, cli auth id, app logs, and IDE shared client logs")
}

func candidateMachineIDFiles(lingmaDir string) []string {
	return uniquePathStrings([]string{
		filepath.Join(lingmaDir, "cache", "id"),
		filepath.Join(lingmaDir, ".auth", "machine_id"),
		filepath.Join(lingmaDir, ".auth", "id"),
		filepath.Join(lingmaDir, "cli", ".auth", "id"),
	})
}

func candidateMachineIDLogFiles(lingmaDir string) []string {
	paths := []string{
		filepath.Join(lingmaDir, "logs", "lingma.log"),
		filepath.Join(lingmaDir, "logs", "Lingma.log"),
		filepath.Join(lingmaDir, "logs", "main.log"),
		filepath.Join(lingmaDir, "logs", "renderer.log"),
		filepath.Join(lingmaDir, "logs", "sharedprocess.log"),
	}
	paths = append(paths, recursiveLogFiles(filepath.Join(lingmaDir, "logs"), 24)...)

	if home, err := os.UserHomeDir(); err == nil {
		for _, root := range lingmaLogRoots(home) {
			paths = append(paths, recentLingmaAppLogs(root)...)
			paths = append(paths, recursiveLogFiles(root, 24)...)
		}
	}
	return uniquePathStrings(paths)
}

func recursiveLogFiles(root string, limit int) []string {
	type item struct {
		path    string
		modTime int64
	}
	items := make([]item, 0)
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		name := strings.ToLower(entry.Name())
		if !strings.HasSuffix(name, ".log") && !strings.Contains(name, "lingma") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		items = append(items, item{path: path, modTime: info.ModTime().UnixNano()})
		return nil
	})
	sort.Slice(items, func(i, j int) bool { return items[i].modTime > items[j].modTime })
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.path)
	}
	return out
}

func extractMachineIDFromText(text string) string {
	markers := []string{
		"using machine id from file:",
		"machine id:",
		"machine_id:",
		"machineId:",
		"machine-id:",
		"generated uuid:",
	}
	lowerText := strings.ToLower(text)
	for _, marker := range markers {
		index := strings.LastIndex(lowerText, strings.ToLower(marker))
		if index < 0 {
			continue
		}
		line := text[index+len(marker):]
		if newline := strings.IndexByte(line, '\n'); newline >= 0 {
			line = line[:newline]
		}
		if value := normalizeMachineID(line); value != "" {
			return value
		}
	}

	re := regexp.MustCompile(`(?i)"?(machine[_-]?id|machineId)"?\s*[:=]\s*"?([A-Za-z0-9._:-]{16,})"?`)
	matches := re.FindAllStringSubmatch(text, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		if len(matches[i]) >= 3 {
			if value := normalizeMachineID(matches[i][2]); value != "" {
				return value
			}
		}
	}
	return ""
}

func normalizeMachineID(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, ` "'<>),]}`)
	if idx := strings.IndexAny(value, " \t\r\n,;"); idx >= 0 {
		value = value[:idx]
	}
	value = strings.Trim(value, ` "'<>),]}`)
	if len(value) < aes.BlockSize {
		return ""
	}
	return value
}

func decryptCacheUser(machineID string, ciphertext []byte) ([]byte, error) {
	if len(machineID) < aes.BlockSize {
		return nil, errors.New("machine id too short for cache decryption")
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, errors.New("invalid cache/user ciphertext size")
	}
	key := []byte(machineID[:aes.BlockSize])
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, key).CryptBlocks(plaintext, ciphertext)
	return unpadPKCS7(plaintext)
}

func unpadPKCS7(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty plaintext")
	}
	padLen := int(data[len(data)-1])
	if padLen <= 0 || padLen > aes.BlockSize || padLen > len(data) {
		return nil, errors.New("invalid cache/user padding")
	}
	for _, b := range data[len(data)-padLen:] {
		if int(b) != padLen {
			return nil, errors.New("invalid cache/user padding bytes")
		}
	}
	return data[:len(data)-padLen], nil
}

func validateCredential(cred Credential) error {
	if strings.TrimSpace(cred.CosyKey) == "" {
		return errors.New("remote credential missing cosy_key")
	}
	if strings.TrimSpace(cred.EncryptUserInfo) == "" {
		return errors.New("remote credential missing encrypt_user_info")
	}
	if strings.TrimSpace(cred.UserID) == "" {
		return errors.New("remote credential missing user_id")
	}
	if strings.TrimSpace(cred.MachineID) == "" {
		return errors.New("remote credential missing machine_id")
	}
	return nil
}

func compactCredentialError(err error) string {
	if err == nil {
		return ""
	}
	text := strings.ReplaceAll(err.Error(), "\n", " ")
	if len(text) > 240 {
		return text[:240] + "..."
	}
	return text
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

func parseExpire(value string) int64 {
	parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return parsed
}

func parseExpireAny(value any) int64 {
	switch typed := value.(type) {
	case string:
		return parseExpire(typed)
	case float64:
		return int64(typed)
	case int64:
		return typed
	case int:
		return int64(typed)
	default:
		return 0
	}
}

func IsExpired(cred Credential, margin time.Duration) bool {
	return cred.TokenExpireTime > 0 && time.Now().Add(margin).UnixMilli() > cred.TokenExpireTime
}

func MachineOSHeader() string {
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return "arm64_darwin"
		}
		return "x86_64_darwin"
	case "windows":
		if runtime.GOARCH == "arm64" {
			return "arm64_windows"
		}
		return "x86_64_windows"
	case "linux":
		if runtime.GOARCH == "arm64" {
			return "arm64_linux"
		}
		return "x86_64_linux"
	default:
		return runtime.GOARCH + "_" + runtime.GOOS
	}
}

func uniquePathStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		cleaned := filepath.Clean(value)
		key := strings.ToLower(cleaned)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, cleaned)
	}
	return out
}
