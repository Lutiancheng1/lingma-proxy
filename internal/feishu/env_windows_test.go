//go:build windows

package feishu

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvedPATHIncludesWindowsNodeDefaults(t *testing.T) {
	t.Setenv("PATH", `C:\Windows\System32`)
	t.Setenv("Path", `C:\Windows\System32`)
	t.Setenv("ProgramFiles", `C:\Program Files`)
	t.Setenv("ProgramFiles(x86)", `C:\Program Files (x86)`)
	t.Setenv("LOCALAPPDATA", `C:\Users\tester\AppData\Local`)
	t.Setenv("APPDATA", `C:\Users\tester\AppData\Roaming`)

	got := resolvedPATH()
	for _, want := range []string{
		filepath.Join(`C:\Program Files`, "nodejs"),
		filepath.Join(`C:\Program Files (x86)`, "nodejs"),
		filepath.Join(`C:\Users\tester\AppData\Local`, "Programs", "nodejs"),
		filepath.Join(`C:\Users\tester\AppData\Roaming`, "npm"),
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("resolved PATH %q missing %q", got, want)
		}
	}
}
