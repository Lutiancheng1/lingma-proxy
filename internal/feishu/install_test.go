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
