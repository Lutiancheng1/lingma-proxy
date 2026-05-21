package feishu

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRemoveString(t *testing.T) {
	got := removeString([]string{"a", "b", "a", "c"}, "a")
	want := []string{"b", "c"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
}

func TestPlaceSelectedNodeDirKeepsEarlierNonNodePath(t *testing.T) {
	root := t.TempDir()
	fakeCLI := filepath.Join(root, "fake-cli")
	oldNode := filepath.Join(root, "old-node")
	other := filepath.Join(root, "other")
	newNode := filepath.Join(root, "new-node")
	for _, dir := range []string{fakeCLI, oldNode, other, newNode} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	nodeName := "node"
	if runtime.GOOS == "windows" {
		nodeName = "node.exe"
	}
	if err := os.WriteFile(filepath.Join(oldNode, nodeName), []byte(""), 0755); err != nil {
		t.Fatal(err)
	}
	got := placeSelectedNodeDir([]string{fakeCLI, oldNode, other}, newNode)
	want := []string{fakeCLI, newNode, oldNode, other}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
}
