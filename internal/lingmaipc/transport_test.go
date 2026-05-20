package lingmaipc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveSharedClientInfoFromJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".info.json")
	content := `{"websocketPort":36510,"pid":14060,"ipcServerPath":"\\\\.\\pipe\\lingma-bf0f32","isDev":false}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write shared info json: %v", err)
	}

	info, err := resolveSharedClientInfoFromPaths([]string{path})
	if err != nil {
		t.Fatalf("resolve shared info json: %v", err)
	}
	if info.WebSocketPort != 36510 {
		t.Fatalf("unexpected websocket port: %d", info.WebSocketPort)
	}
	if info.PID != 14060 {
		t.Fatalf("unexpected pid: %d", info.PID)
	}
	if info.IPCServerPath != `\\.\pipe\lingma-bf0f32` {
		t.Fatalf("unexpected pipe path: %q", info.IPCServerPath)
	}
}

func TestResolveSharedClientInfoFromLegacyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".info")
	content := "36510\n14060\n\\\\.\\pipe\\lingma-bf0f32\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write shared info legacy: %v", err)
	}

	info, err := resolveSharedClientInfoFromPaths([]string{path})
	if err != nil {
		t.Fatalf("resolve shared info legacy: %v", err)
	}
	if info.WebSocketPort != 36510 {
		t.Fatalf("unexpected websocket port: %d", info.WebSocketPort)
	}
	if info.PID != 14060 {
		t.Fatalf("unexpected pid: %d", info.PID)
	}
	if info.IPCServerPath != `\\.\pipe\lingma-bf0f32` {
		t.Fatalf("unexpected pipe path: %q", info.IPCServerPath)
	}
}

func TestNormalizeWebSocketURLAddsRootPath(t *testing.T) {
	got, err := normalizeWebSocketURL("ws://127.0.0.1:36510")
	if err != nil {
		t.Fatalf("normalize websocket url: %v", err)
	}
	if got != "ws://127.0.0.1:36510/" {
		t.Fatalf("unexpected normalized websocket url: %q", got)
	}
}

func TestDefaultSharedClientInfoPathsIncludeQoderCN(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LINGMA_SHARED_CLIENT_INFO", "")

	paths := defaultSharedClientInfoPaths()
	wantPart := filepath.Join("QoderCN", "SharedClientCache", ".info.json")
	for _, path := range paths {
		if strings.Contains(path, wantPart) {
			return
		}
	}
	t.Fatalf("missing QoderCN shared client info path containing %q in %#v", wantPart, paths)
}

func TestDefaultSharedClientInfoPathsPreferQoderCNBeforeLingma(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LINGMA_SHARED_CLIENT_INFO", "")

	paths := defaultSharedClientInfoPaths()
	qoderPart := filepath.Join("QoderCN", "SharedClientCache", ".info.json")
	lingmaPart := filepath.Join("Lingma", "SharedClientCache", ".info.json")
	qoderIndex, lingmaIndex := -1, -1
	for i, path := range paths {
		switch {
		case strings.Contains(path, qoderPart):
			if qoderIndex < 0 {
				qoderIndex = i
			}
		case strings.Contains(path, lingmaPart):
			if lingmaIndex < 0 {
				lingmaIndex = i
			}
		}
	}
	if qoderIndex < 0 || lingmaIndex < 0 || qoderIndex > lingmaIndex {
		t.Fatalf("QoderCN shared info should be preferred before Lingma, qoder=%d lingma=%d paths=%#v", qoderIndex, lingmaIndex, paths)
	}
}

func TestNewestExistingPathPrefersNewestSocket(t *testing.T) {
	dir := t.TempDir()
	older := filepath.Join(dir, "lingma.sock")
	newer := filepath.Join(dir, "qodercn.sock")
	if err := os.WriteFile(older, []byte("older"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newer, []byte("newer"), 0o644); err != nil {
		t.Fatal(err)
	}
	newTime := time.Now()
	oldTime := newTime.Add(-time.Hour)
	if err := os.Chtimes(older, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newer, newTime, newTime); err != nil {
		t.Fatal(err)
	}
	if got := newestExistingPath([]string{older, newer}); got != newer {
		t.Fatalf("newestExistingPath = %q, want %q", got, newer)
	}
}
