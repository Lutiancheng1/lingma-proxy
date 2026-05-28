//go:build windows

package feishu

import (
	"context"
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
		filepath.Join(`C:\Users\tester\AppData\Roaming`, "lingma-proxy", "npm-global"),
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
	_, args, cmdLine, ok := windowsShellCommand(`"C:\Users\47157\AppData\Roaming\nvm\v20.19.6\lark-cli.cmd"`, []string{"auth", "login", "--recommend"})
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
	if strings.Contains(cmdLine, `\"`) {
		t.Fatalf("raw cmd line must not contain backslash-escaped executable quotes: %q", cmdLine)
	}
	if !strings.Contains(cmdLine, `call "C:\Users\47157\AppData\Roaming\nvm\v20.19.6\lark-cli.cmd"`) {
		t.Fatalf("raw cmd line missing callable quoted executable path: %q", cmdLine)
	}
}

func TestCommandContextWithEnvUsesRawCmdLineForProgramFilesCmd(t *testing.T) {
	cmd := commandContextWithEnv(context.Background(), `"C:\Program Files\nodejs\lark-cli.cmd"`, "auth", "login", "--recommend")
	if !strings.EqualFold(filepath.Base(cmd.Path), "cmd.exe") {
		t.Fatalf("expected cmd.exe wrapper, got path=%q args=%#v", cmd.Path, cmd.Args)
	}
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CmdLine == "" {
		t.Fatalf("expected raw cmd line for cmd.exe wrapper, got %#v", cmd.SysProcAttr)
	}
	if strings.Contains(cmd.SysProcAttr.CmdLine, `\"`) {
		t.Fatalf("raw cmd line must not contain Go/backslash escaped quotes: %q", cmd.SysProcAttr.CmdLine)
	}
	if !strings.Contains(cmd.SysProcAttr.CmdLine, `call "C:\Program Files\nodejs\lark-cli.cmd"`) {
		t.Fatalf("raw cmd line missing Program Files lark-cli path: %q", cmd.SysProcAttr.CmdLine)
	}
}
