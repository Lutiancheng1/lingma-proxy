package feishu

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveOfficialLarkSkillsKeepsNonLarkSkills(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "skills")
	if err := os.MkdirAll(filepath.Join(root, "lark-doc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "aihot"), 0o755); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(tmp, ".skill-lock.json")
	lock := map[string]any{
		"skills": map[string]any{
			"lark-doc": map[string]any{"skillPath": filepath.Join(root, "lark-doc", "SKILL.md")},
			"aihot":    map[string]any{"skillPath": filepath.Join(root, "aihot", "SKILL.md")},
		},
	}
	data, _ := json.Marshal(lock)
	if err := os.WriteFile(lockPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	oldRoots := globalSkillsRoots
	oldLocks := skillLockPaths
	globalSkillsRoots = []string{root}
	skillLockPaths = []string{lockPath}
	defer func() {
		globalSkillsRoots = oldRoots
		skillLockPaths = oldLocks
	}()

	removed, err := removeOfficialLarkSkills()
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed=%d, want 1", removed)
	}
	if _, err := os.Stat(filepath.Join(root, "lark-doc")); !os.IsNotExist(err) {
		t.Fatalf("lark skill should be removed, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "aihot")); err != nil {
		t.Fatalf("non-lark skill should remain: %v", err)
	}
	data, err = os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	var next map[string]any
	if err := json.Unmarshal(data, &next); err != nil {
		t.Fatal(err)
	}
	skills := next["skills"].(map[string]any)
	if _, ok := skills["lark-doc"]; ok {
		t.Fatalf("lark skill should be removed from lock: %s", data)
	}
	if _, ok := skills["aihot"]; !ok {
		t.Fatalf("non-lark skill should remain in lock: %s", data)
	}
}

func TestCleanupBridgeArtifactsEmitsProgress(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	oldRoots := globalSkillsRoots
	oldLocks := skillLockPaths
	oldUninstall := uninstallLarkCLIFunc
	oldShimDirs := npmShimDirsFunc
	globalSkillsRoots = []string{filepath.Join(tmp, "skills")}
	skillLockPaths = []string{filepath.Join(tmp, ".skill-lock.json")}
	uninstallLarkCLIFunc = func(context.Context) error { return nil }
	npmShimDirsFunc = func() []string { return []string{filepath.Join(tmp, "npm-bin")} }
	defer func() {
		globalSkillsRoots = oldRoots
		skillLockPaths = oldLocks
		uninstallLarkCLIFunc = oldUninstall
		npmShimDirsFunc = oldShimDirs
	}()

	var progress []string
	_, err := cleanupBridgeArtifacts(context.Background(), func(output, errText string) {
		if output != "" {
			progress = append(progress, output)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"正在卸载全局 @larksuite/cli...",
		"正在移除 lark-cli 启动文件...",
		"正在移除官方 lark-* Skills...",
		"正在清理 ~/.lark-cli 配置、授权和事件缓存...",
	} {
		found := false
		for _, got := range progress {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing progress %q in %#v", want, progress)
		}
	}
}

func TestRemoveLarkCLIStateUsesUserHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	dir := filepath.Join(tmp, ".lark-cli")
	if err := os.MkdirAll(filepath.Join(dir, "events"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"apps":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := removeLarkCLIState(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf(".lark-cli should be removed, err=%v", err)
	}
}

func TestBridgeSkillServiceClearAllRemovesImportedSkills(t *testing.T) {
	tmp := t.TempDir()
	store, err := newBridgeStore(tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc, err := NewBridgeSkillService(tmp, store)
	if err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(tmp, "feishu-skills", "demo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skill := BridgeSkill{ID: "demo", Name: "demo", Path: skillDir, Enabled: true}
	if err := store.UpsertSkill(context.Background(), skill); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}

	removed, err := svc.ClearAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed=%d, want 1", removed)
	}
	if skills := svc.List(false); len(skills) != 0 {
		t.Fatalf("skills remain in memory: %#v", skills)
	}
	if skills, err := store.LoadSkills(context.Background()); err != nil || len(skills) != 0 {
		t.Fatalf("skills remain in store: skills=%#v err=%v", skills, err)
	}
}
