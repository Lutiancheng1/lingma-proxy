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

func TestResolvedPATHIncludesWindowsNPMConfigPrefix(t *testing.T) {
	t.Setenv("PATH", `C:\Windows\System32`)
	t.Setenv("Path", `C:\Windows\System32`)
	t.Setenv("npm_config_prefix", `D:\node.js\node_global`)
	resetResolvedCommandEnv()

	got := resolvedPATH()
	want := `D:\node.js\node_global`
	if !strings.Contains(got, want) {
		t.Fatalf("resolved PATH %q missing npm_config_prefix %q", got, want)
	}
}

func TestWindowsShellCommandUnquotesCmdExecutable(t *testing.T) {
	_, args, ok := windowsShellCommand(`"C:\Users\47157\AppData\Roaming\nvm\v20.19.6\lark-cli.cmd"`, []string{"auth", "login", "--recommend"})
	if !ok {
		t.Fatal("expected .cmd executable to be wrapped by cmd.exe")
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, `\"`) {
		t.Fatalf("cmd wrapper must not pass backslash-escaped quotes to cmd.exe: %q", joined)
	}
	if strings.Contains(joined, `""C:\Users`) {
		t.Fatalf("cmd wrapper should remove pre-existing executable quotes before quoting: %q", joined)
	}
	if !strings.Contains(joined, `"C:\Users\47157\AppData\Roaming\nvm\v20.19.6\lark-cli.cmd"`) {
		t.Fatalf("cmd wrapper missing quoted executable path: %q", joined)
	}
}
