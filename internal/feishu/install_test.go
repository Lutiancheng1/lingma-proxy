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
