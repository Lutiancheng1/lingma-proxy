package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// feishuSkillManifestURL is the official well-known endpoint that lists every
// skill the feishu CLI expects. Keeping the required set in sync with this
// manifest prevents the onboarding step from reporting "缺失或未完整安装" once
// the feishu side adds a new skill.
const feishuSkillManifestURL = "https://open.feishu.cn/.well-known/skills/index.json"

// fallbackRequiredSkillNames is a snapshot of the manifest taken on
// 2026-05-23. It is only consulted when the live manifest cannot be fetched
// (e.g. the user is offline). Add new entries here whenever the live manifest
// expands so cached/offline runs stay accurate.
var fallbackRequiredSkillNames = []string{
	"lark-approval",
	"lark-apps",
	"lark-attendance",
	"lark-base",
	"lark-calendar",
	"lark-contact",
	"lark-doc",
	"lark-drive",
	"lark-event",
	"lark-im",
	"lark-mail",
	"lark-markdown",
	"lark-minutes",
	"lark-okr",
	"lark-openapi-explorer",
	"lark-shared",
	"lark-sheets",
	"lark-skill-maker",
	"lark-slides",
	"lark-task",
	"lark-vc",
	"lark-vc-agent",
	"lark-whiteboard",
	"lark-wiki",
	"lark-workflow-meeting-summary",
	"lark-workflow-standup-report",
}

type skillManifestEntry struct {
	Name string `json:"name"`
}

type skillManifest struct {
	Skills []skillManifestEntry `json:"skills"`
}

// requiredSkillNamesFn is overridable in tests so we can avoid hitting the
// network from unit tests.
var requiredSkillNamesFn = resolveRequiredSkillNames

var (
	manifestCacheMu      sync.Mutex
	manifestCacheNames   []string
	manifestCacheFetched time.Time
)

const manifestCacheTTL = 6 * time.Hour

func resolveRequiredSkillNames(ctx context.Context) []string {
	manifestCacheMu.Lock()
	if len(manifestCacheNames) > 0 && time.Since(manifestCacheFetched) < manifestCacheTTL {
		cached := append([]string(nil), manifestCacheNames...)
		manifestCacheMu.Unlock()
		return cached
	}
	manifestCacheMu.Unlock()

	names, err := fetchFeishuSkillManifest(ctx)
	if err != nil || len(names) == 0 {
		return append([]string(nil), fallbackRequiredSkillNames...)
	}

	manifestCacheMu.Lock()
	manifestCacheNames = append([]string(nil), names...)
	manifestCacheFetched = time.Now()
	manifestCacheMu.Unlock()
	return names
}

func fetchFeishuSkillManifest(ctx context.Context) ([]string, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, feishuSkillManifestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("manifest http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return nil, err
	}
	var manifest skillManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(manifest.Skills))
	names := make([]string, 0, len(manifest.Skills))
	for _, entry := range manifest.Skills {
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// discoverSkillsTimeout is kept as a fallback ceiling for the optional npx
// probe used in tests. Production discovery scans the filesystem directly
// (~/.agents/skills/<name>/SKILL.md) and never shells out to npx — the npx
// path measured at ~19s on a real machine, blowing past any reasonable poll
// interval and starving the npm cache lock.
const discoverSkillsTimeout = 12 * time.Second

// globalSkillsRoots are the directories where the `skills` tool installs
// global skills as fallback when the lock file is unavailable. The first
// hit wins for each skill name.
var globalSkillsRoots = []string{
	"~/.agents/skills",
	"~/.hermes/skills",
}

// skillLockPaths are candidate locations for the `skills` tool's lockfile.
// The lockfile is the authoritative record of what `skills add -g` has
// installed and survives even if the user moves the agent directories
// around. We check it first; FS scan is the fallback.
var skillLockPaths = []string{
	"~/.agents/.skill-lock.json",
	"~/.hermes/.skill-lock.json",
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}

type skillLockEntry struct {
	SkillPath string `json:"skillPath"`
}

type skillLockFile struct {
	Skills map[string]skillLockEntry `json:"skills"`
}

// readInstalledSkillsFromLock returns the set of skills the `skills` tool
// believes are installed globally. Returns nil if no lock file is present
// or readable — callers fall back to the FS scan.
func readInstalledSkillsFromLock() map[string]struct{} {
	for _, lockPath := range skillLockPaths {
		data, err := os.ReadFile(expandHome(lockPath))
		if err != nil {
			continue
		}
		var lock skillLockFile
		if err := json.Unmarshal(data, &lock); err != nil {
			continue
		}
		if len(lock.Skills) == 0 {
			continue
		}
		out := make(map[string]struct{}, len(lock.Skills))
		for name := range lock.Skills {
			out[name] = struct{}{}
		}
		return out
	}
	return nil
}

// findSkillOnDisk returns the first global skill directory containing
// SKILL.md for the given name, or "" if none.
func findSkillOnDisk(name string) string {
	for _, root := range globalSkillsRoots {
		candidate := filepath.Join(expandHome(root), name)
		info, err := os.Stat(candidate)
		if err != nil || !info.IsDir() {
			continue
		}
		skillFile := filepath.Join(candidate, "SKILL.md")
		fi, err := os.Stat(skillFile)
		if err != nil || fi.IsDir() {
			continue
		}
		realPath, evalErr := filepath.EvalSymlinks(candidate)
		if evalErr != nil {
			realPath = candidate
		}
		return realPath
	}
	return ""
}

func discoverSkills(ctx context.Context) ([]SkillStatus, error) {
	required := requiredSkillNamesFn(ctx)
	lockSet := readInstalledSkillsFromLock()
	statuses := make([]SkillStatus, 0, len(required))
	for _, name := range required {
		status := SkillStatus{Name: name}
		diskPath := findSkillOnDisk(name)
		_, inLock := lockSet[name]

		switch {
		case inLock && diskPath != "":
			status.Found = true
			status.Path = diskPath
		case inLock && diskPath == "":
			// Lock says installed but SKILL.md missing — broken install.
			status.Message = "SKILL.md 缺失（lock 记录已安装）"
		case !inLock && diskPath != "":
			// Disk has it but lock doesn't list it — accept it (user may
			// have installed via a different agent dir). Treat as Found
			// since the runtime cares about the file, not the lock.
			status.Found = true
			status.Path = diskPath
		default:
			status.Message = "未在 skill-lock 与全局 skills 目录中发现"
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func skillsReady(skills []SkillStatus) bool {
	if len(skills) == 0 {
		return false
	}
	for _, skill := range skills {
		if !skill.Found {
			return false
		}
	}
	return true
}

func missingSkillNames(skills []SkillStatus, limit int) []string {
	if limit <= 0 {
		limit = len(skills)
	}
	missing := make([]string, 0)
	for _, skill := range skills {
		if skill.Found {
			continue
		}
		if len(missing) >= limit {
			remaining := 0
			for _, rest := range skills {
				if !rest.Found {
					remaining++
				}
			}
			missing = append(missing, fmt.Sprintf("等另外 %d 个", remaining-len(missing)))
			break
		}
		missing = append(missing, skill.Name)
	}
	return missing
}
