package feishu

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBridgeSkillImportFolder(t *testing.T) {
	dataDir := t.TempDir()
	source := filepath.Join(t.TempDir(), "demo-skill")
	if err := os.MkdirAll(filepath.Join(source, "scripts"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: demo-skill\ndescription: Use when testing imports\nversion: 1.0.0\nwhen_to_use: import tests\n---\n# Demo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "scripts", "run.sh"), []byte("echo ok\n"), 0755); err != nil {
		t.Fatal(err)
	}
	store, err := newBridgeStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc, err := NewBridgeSkillService(dataDir, store)
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.ImportPath(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Imported) != 1 {
		t.Fatalf("expected one import, got %#v", result)
	}
	skill := result.Imported[0]
	if skill.Name != "demo-skill" || !skill.Enabled || len(skill.Scripts) != 1 {
		t.Fatalf("unexpected skill: %#v", skill)
	}
	body, found, err := svc.SkillBody(skill.ID)
	if err != nil || found.ID != skill.ID || body == "" {
		t.Fatalf("skill body failed: body=%q found=%#v err=%v", body, found, err)
	}
}

func TestBridgeSkillZipRejectsTraversal(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "bad.zip")
	file, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(file)
	w, err := zw.Create("../escape/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("bad"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	svc, err := NewBridgeSkillService(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ImportPath(context.Background(), zipPath); err == nil {
		t.Fatalf("expected traversal zip to fail")
	}
}

func TestBridgeSkillZipImportsMultipleSkills(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "skills.zip")
	file, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(file)
	for _, name := range []string{"aihot", "writer"} {
		w, err := zw.Create(name + "/SKILL.md")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("---\nname: " + name + "\ndescription: " + name + " skill\n---\n# " + name + "\n"))
		w, err = zw.Create(name + "/scripts/run.sh")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("echo " + name + "\n"))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	svc, err := NewBridgeSkillService(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.ImportPath(context.Background(), zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Imported) != 2 {
		t.Fatalf("expected two skills from one zip, got %#v", result.Imported)
	}
	if _, ok := svc.Find("aihot"); !ok {
		t.Fatalf("aihot not imported")
	}
	if _, ok := svc.Find("writer"); !ok {
		t.Fatalf("writer not imported")
	}
}

func TestBridgeSkillZipRejectsLargeFiles(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "large.zip")
	file, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(file)
	if _, err := zw.Create("large/SKILL.md"); err != nil {
		t.Fatal(err)
	}
	header := &zip.FileHeader{Name: "large/assets/big.bin", Method: zip.Deflate}
	header.SetMode(0644)
	w, err := zw.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(strings.Repeat("x", 10*1024*1024+1))); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	svc, err := NewBridgeSkillService(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ImportPath(context.Background(), zipPath); err == nil || !strings.Contains(err.Error(), "超过 10MB") {
		t.Fatalf("expected large zip file to fail, got %v", err)
	}
}

func TestExplicitSkillMentionApprovesOneTurnScript(t *testing.T) {
	manager := NewManager(ManagerOptions{DataDir: t.TempDir()})
	source := filepath.Join(t.TempDir(), "aihot")
	if err := os.MkdirAll(filepath.Join(source, "scripts"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: aihot\ndescription: AI news\n---\n# AI Hot\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "scripts", "daily.sh"), []byte("echo today-ai-news\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ImportSkillPath(context.Background(), source); err != nil {
		t.Fatal(err)
	}

	manager.setTurnSkillScriptApprovals("oc_test", "使用 aihot 查一下今天 AI 圈有什么")
	result := manager.executeBridgeSkillTool(context.Background(), "oc_test", "call_1", "skill_run_script", map[string]any{
		"skill":  "aihot",
		"script": "daily.sh",
	})
	if result.IsError || result.Output != "today-ai-news" {
		t.Fatalf("expected approved script execution, got %#v", result)
	}

	manager.clearTurnSkillScriptApprovals("oc_test")
	result = manager.executeBridgeSkillTool(context.Background(), "oc_test", "call_2", "skill_run_script", map[string]any{
		"skill":  "aihot",
		"script": "daily.sh",
	})
	if !result.IsError || !strings.Contains(result.Output, "/skill-run aihot daily.sh confirm") {
		t.Fatalf("expected confirmation after approval cleared, got %#v", result)
	}
}

func TestSkillRunScriptPassesArgs(t *testing.T) {
	manager := NewManager(ManagerOptions{DataDir: t.TempDir()})
	source := filepath.Join(t.TempDir(), "arg-skill")
	if err := os.MkdirAll(filepath.Join(source, "scripts"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: arg-skill\ndescription: Args\n---\n# Args\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "scripts", "run.sh"), []byte("echo \"$1:$2\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ImportSkillPath(context.Background(), source); err != nil {
		t.Fatal(err)
	}

	manager.setTurnSkillScriptApprovals("oc_test", "使用 arg-skill 执行脚本")
	result := manager.executeBridgeSkillTool(context.Background(), "oc_test", "call_1", "skill_run_script", map[string]any{
		"skill":  "arg-skill",
		"script": "run.sh",
		"args":   []any{"hello", "world"},
	})
	if result.IsError || result.Output != "hello:world" {
		t.Fatalf("expected script args to pass through, got %#v", result)
	}
}

func TestSkillHTTPRequestRejectsUnsupportedMethod(t *testing.T) {
	manager := NewManager(ManagerOptions{DataDir: t.TempDir()})
	source := filepath.Join(t.TempDir(), "api-skill")
	if err := os.MkdirAll(source, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: api-skill\ndescription: API skill\n---\n# API\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ImportSkillPath(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	result := manager.executeBridgeSkillTool(context.Background(), "oc_test", "call_1", "skill_http_request", map[string]any{
		"skill":  "api-skill",
		"method": "TRACE",
		"url":    "https://example.invalid/items",
	})
	if !result.IsError || !strings.Contains(result.Output, "unsupported HTTP method") {
		t.Fatalf("expected unsupported method error, got %#v", result)
	}
}

func TestSkillHTTPRequestBlocksLocalhost(t *testing.T) {
	manager := NewManager(ManagerOptions{DataDir: t.TempDir()})
	source := filepath.Join(t.TempDir(), "api-skill")
	if err := os.MkdirAll(source, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: api-skill\ndescription: API skill\n---\n# API\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ImportSkillPath(context.Background(), source); err != nil {
		t.Fatal(err)
	}

	result := manager.executeBridgeSkillTool(context.Background(), "oc_test", "call_1", "skill_http_request", map[string]any{
		"skill": "api-skill",
		"url":   "http://127.0.0.1:8080/items",
	})
	if !result.IsError || !strings.Contains(result.Output, "内网或本机地址") {
		t.Fatalf("expected localhost request to be blocked, got %#v", result)
	}
}
