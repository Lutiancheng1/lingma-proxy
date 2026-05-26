package feishu

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFallbackRequiredSkillNamesContainsCoreLark(t *testing.T) {
	required := map[string]bool{
		"lark-shared":   false,
		"lark-base":     false,
		"lark-im":       false,
		"lark-doc":      false,
		"lark-calendar": false,
		"lark-task":     false,
		"lark-wiki":     false,
		"lark-sheets":   false,
	}
	for _, name := range fallbackRequiredSkillNames {
		if _, ok := required[name]; ok {
			required[name] = true
		}
	}
	for name, found := range required {
		if !found {
			t.Fatalf("fallbackRequiredSkillNames missing core skill %q", name)
		}
	}
}

func TestSkillsReadyOverrideForFallbackSnapshot(t *testing.T) {
	// Custom requiredSkillNamesFn must be honored so unit tests don't reach
	// the real feishu manifest endpoint and so callers can pin a stable list.
	original := requiredSkillNamesFn
	t.Cleanup(func() { requiredSkillNamesFn = original })

	requiredSkillNamesFn = func(context.Context) []string {
		return []string{"lark-im"}
	}

	statuses := []SkillStatus{{Name: "lark-im", Found: true}}
	if !skillsReady(statuses) {
		t.Fatal("skillsReady should return true when the only required skill is found")
	}

	statuses = []SkillStatus{{Name: "lark-im", Found: false}}
	if skillsReady(statuses) {
		t.Fatal("skillsReady should return false when a required skill is missing")
	}
}

func TestClearSkillManifestCache(t *testing.T) {
	manifestCacheMu.Lock()
	manifestCacheNames = []string{"lark-im"}
	manifestCacheFetched = time.Now()
	manifestCacheMu.Unlock()

	clearSkillManifestCache()

	manifestCacheMu.Lock()
	defer manifestCacheMu.Unlock()
	if len(manifestCacheNames) != 0 || !manifestCacheFetched.IsZero() {
		t.Fatalf("manifest cache should be cleared: names=%#v fetched=%s", manifestCacheNames, manifestCacheFetched)
	}
}

func TestMissingSkillNamesLimitsOutput(t *testing.T) {
	statuses := []SkillStatus{
		{Name: "lark-im", Found: false},
		{Name: "lark-doc", Found: true},
		{Name: "lark-base", Found: false},
		{Name: "lark-calendar", Found: false},
	}
	got := missingSkillNames(statuses, 2)
	want := []string{"lark-im", "lark-base", "等另外 1 个"}
	if len(got) != len(want) {
		t.Fatalf("missingSkillNames len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("missingSkillNames[%d] = %q, want %q; all=%#v", i, got[i], want[i], got)
		}
	}
}

// withSkillTestEnv redirects skillLockPaths and globalSkillsRoots into a
// temporary directory and restores them after the test.
func withSkillTestEnv(t *testing.T) (lockPath, agentsRoot string) {
	t.Helper()
	tmp := t.TempDir()
	lockPath = filepath.Join(tmp, ".skill-lock.json")
	agentsRoot = filepath.Join(tmp, "skills")
	if err := os.MkdirAll(agentsRoot, 0o755); err != nil {
		t.Fatalf("mkdir agents: %v", err)
	}

	origLock := skillLockPaths
	origRoots := globalSkillsRoots
	origReq := requiredSkillNamesFn
	t.Cleanup(func() {
		skillLockPaths = origLock
		globalSkillsRoots = origRoots
		requiredSkillNamesFn = origReq
	})
	skillLockPaths = []string{lockPath}
	globalSkillsRoots = []string{agentsRoot}
	requiredSkillNamesFn = func(context.Context) []string {
		return []string{"lark-im", "lark-base", "lark-doc"}
	}
	return lockPath, agentsRoot
}

func writeSkillOnDisk(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", name, err)
	}
	skillFile := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(skillFile, []byte("# "+name+"\n"), 0o644); err != nil {
		t.Fatalf("write SKILL.md %s: %v", name, err)
	}
	return dir
}

func writeLock(t *testing.T, lockPath string, names ...string) {
	t.Helper()
	body := `{"skills":{`
	for i, n := range names {
		if i > 0 {
			body += ","
		}
		body += `"` + n + `":{"skillPath":".claude/skills/` + n + `/SKILL.md"}`
	}
	body += `}}`
	if err := os.WriteFile(lockPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write lock: %v", err)
	}
}

func statusByName(statuses []SkillStatus, name string) SkillStatus {
	for _, s := range statuses {
		if s.Name == name {
			return s
		}
	}
	return SkillStatus{}
}

func TestDiscoverSkillsLockAndDiskAgree(t *testing.T) {
	lockPath, agentsRoot := withSkillTestEnv(t)
	writeSkillOnDisk(t, agentsRoot, "lark-im")
	writeSkillOnDisk(t, agentsRoot, "lark-base")
	writeSkillOnDisk(t, agentsRoot, "lark-doc")
	writeLock(t, lockPath, "lark-im", "lark-base", "lark-doc")

	statuses, err := discoverSkills(context.Background())
	if err != nil {
		t.Fatalf("discoverSkills: %v", err)
	}
	for _, name := range []string{"lark-im", "lark-base", "lark-doc"} {
		s := statusByName(statuses, name)
		if !s.Found {
			t.Fatalf("%s should be found, got %+v", name, s)
		}
	}
	if !skillsReady(statuses) {
		t.Fatal("skillsReady should be true")
	}
}

func TestDiscoverSkillsDiskOnlyStillFound(t *testing.T) {
	// Lock file absent (e.g. user installed via a different agent dir).
	// FS scan should still pick the skill up.
	_, agentsRoot := withSkillTestEnv(t)
	writeSkillOnDisk(t, agentsRoot, "lark-im")
	writeSkillOnDisk(t, agentsRoot, "lark-base")
	writeSkillOnDisk(t, agentsRoot, "lark-doc")
	// no writeLock call

	statuses, err := discoverSkills(context.Background())
	if err != nil {
		t.Fatalf("discoverSkills: %v", err)
	}
	if !skillsReady(statuses) {
		t.Fatalf("expected skillsReady=true with disk-only install, got %+v", statuses)
	}
}

func TestRenderLarkSkillViewReadsOfficialSkill(t *testing.T) {
	_, agentsRoot := withSkillTestEnv(t)
	dir := writeSkillOnDisk(t, agentsRoot, "lark-sheets")
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: lark-sheets\n---\n# Sheets\n\nUse `lark-cli sheets +info` before `lark-cli sheets +read`.\n"), 0o644); err != nil {
		t.Fatalf("write skill body: %v", err)
	}
	got, err := renderLarkSkillView("sheets")
	if err != nil {
		t.Fatalf("renderLarkSkillView: %v", err)
	}
	if !strings.Contains(got, "lark-sheets") || !strings.Contains(got, "lark-cli sheets +info") {
		t.Fatalf("unexpected guide: %s", got)
	}
}

func TestBuildRelevantLarkSkillContextInjectsMatchedSkill(t *testing.T) {
	_, agentsRoot := withSkillTestEnv(t)
	dir := writeSkillOnDisk(t, agentsRoot, "lark-sheets")
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# Sheets\n\nUse `lark-cli sheets +info`.\n"), 0o644); err != nil {
		t.Fatalf("write skill body: %v", err)
	}
	statuses := []SkillStatus{{Name: "lark-sheets", Found: true, Path: dir}}
	got := buildRelevantLarkSkillContext(statuses, "读取这个电子表格")
	if !strings.Contains(got, "lark-cli sheets +info") {
		t.Fatalf("expected sheets skill context, got: %s", got)
	}
}

func TestDiscoverSkillsLockSaysInstalledButFileMissing(t *testing.T) {
	lockPath, _ := withSkillTestEnv(t)
	// Lock claims all three installed; nothing on disk.
	writeLock(t, lockPath, "lark-im", "lark-base", "lark-doc")

	statuses, err := discoverSkills(context.Background())
	if err != nil {
		t.Fatalf("discoverSkills: %v", err)
	}
	if skillsReady(statuses) {
		t.Fatal("skillsReady should be false when lock entries lack on-disk files")
	}
	for _, s := range statuses {
		if s.Found {
			t.Fatalf("%s wrongly Found despite missing SKILL.md", s.Name)
		}
		if s.Message == "" {
			t.Fatalf("%s should have explanatory message", s.Name)
		}
	}
}

func TestDiscoverSkillsPartialMissingReportsGap(t *testing.T) {
	lockPath, agentsRoot := withSkillTestEnv(t)
	writeSkillOnDisk(t, agentsRoot, "lark-im")
	writeSkillOnDisk(t, agentsRoot, "lark-base")
	// lark-doc absent both on lock and on disk
	writeLock(t, lockPath, "lark-im", "lark-base")

	statuses, err := discoverSkills(context.Background())
	if err != nil {
		t.Fatalf("discoverSkills: %v", err)
	}
	if skillsReady(statuses) {
		t.Fatal("skillsReady should be false when one required skill is absent")
	}
	doc := statusByName(statuses, "lark-doc")
	if doc.Found {
		t.Fatal("lark-doc should not be Found")
	}
}
