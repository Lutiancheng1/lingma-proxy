package feishu

import "testing"

func TestNodeVersionSupported(t *testing.T) {
	cases := []struct {
		version string
		want    bool
	}{
		{"v16.20.2", false},
		{"v20.11.1", false},
		{"v20.12.0", true},
		{"v22.0.0", true},
	}
	for _, tc := range cases {
		major, minor := parseNodeVersion(tc.version)
		if got := nodeVersionSupported(major, minor); got != tc.want {
			t.Fatalf("nodeVersionSupported(%q) = %v, want %v", tc.version, got, tc.want)
		}
	}
}

func TestSkillsInstallCommandsIncludePinnedFallbacks(t *testing.T) {
	commands := skillsInstallCommands()
	if len(commands) < 3 {
		t.Fatalf("skillsInstallCommands length = %d, want pinned fallbacks", len(commands))
	}
	if got := commands[0]; len(got) < 3 || got[0] != "npx" || got[1] != "-y" || got[2] != "skills@1.5.6" {
		t.Fatalf("first skills install command = %#v, want pinned compatible skills command", got)
	}
	foundLatestFallback := false
	for _, arg := range commands[len(commands)-1] {
		if arg == "skills" {
			foundLatestFallback = true
		}
	}
	if !foundLatestFallback {
		t.Fatalf("skillsInstallCommands = %#v, want latest skills fallback last", commands)
	}
}

func TestSkillsInstallCommandsUseOfficialEndpoint(t *testing.T) {
	commands := skillsInstallCommands()
	hasOfficial := false
	for _, argv := range commands {
		joined := ""
		for _, a := range argv {
			joined += " " + a
		}
		if containsString(argv, "https://open.feishu.cn") && containsString(argv, "--skill") {
			hasOfficial = true
			break
		}
		_ = joined
	}
	if !hasOfficial {
		t.Fatalf("expected at least one command targeting https://open.feishu.cn with --skill flag, got %#v", commands)
	}
}

func containsString(slice []string, target string) bool {
	for _, s := range slice {
		if s == target {
			return true
		}
	}
	return false
}

func TestSelectPreferredCompatibleNodeVersionPrefersNode22(t *testing.T) {
	output := `
    16.20.2
  * 24.16.0 (Currently using 64-bit executable)
    22.11.0
    20.18.0
`
	if got := selectPreferredCompatibleNodeVersion(output); got != "22.11.0" {
		t.Fatalf("selectPreferredCompatibleNodeVersion() = %q, want 22.11.0", got)
	}
}

func TestSelectPreferredCompatibleNodeVersionFallsBackToNode20(t *testing.T) {
	output := `
    24.16.0
    20.18.0
`
	if got := selectPreferredCompatibleNodeVersion(output); got != "20.18.0" {
		t.Fatalf("selectPreferredCompatibleNodeVersion() = %q, want 20.18.0", got)
	}
}

func TestSelectPreferredCompatibleNodeVersionRejectsOldVersions(t *testing.T) {
	output := `
    16.20.2
    20.11.1
`
	if got := selectPreferredCompatibleNodeVersion(output); got != "" {
		t.Fatalf("selectPreferredCompatibleNodeVersion() = %q, want empty", got)
	}
}
