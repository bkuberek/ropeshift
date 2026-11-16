package version

import (
	"strings"
	"testing"
)

func TestCompare(t *testing.T) {
	tests := []struct {
		name  string
		a, b  string
		order int
		ok    bool
	}{
		{"the same release", "v0.1.0", "v0.1.0", 0, true},
		{"the same pre-release", "v0.1.0-alpha.16", "v0.1.0-alpha.16", 0, true},
		{"a later patch", "v0.1.1", "v0.1.0", 1, true},
		{"an earlier minor", "v0.1.9", "v0.2.0", -1, true},
		{"numbers, not text, order the core", "v0.10.0", "v0.9.0", 1, true},
		{"a release is later than its pre-release", "v0.1.0", "v0.1.0-rc.1", 1, true},
		{"a pre-release is earlier than its release", "v0.1.0-rc.1", "v0.1.0", -1, true},
		{"numeric identifiers compare as numbers", "v0.1.0-alpha.9", "v0.1.0-alpha.16", -1, true},
		{"a later name", "v0.1.0-beta.1", "v0.1.0-alpha.16", 1, true},
		{"a number is earlier than a name", "v0.1.0-1", "v0.1.0-alpha", -1, true},
		{"more identifiers are later", "v0.1.0-alpha.1.1", "v0.1.0-alpha.1", 1, true},
		{"a development build is no release", Development, "v0.1.0", 0, false},
		{"nothing is no release", "v0.1.0", "", 0, false},
		{"a pseudo-version is no release", "v0.0.0-20260101000000-0123456789ab", "v0.1.0", 0, false},
		{"a dirty tree is no release", "v0.1.0+dirty", "v0.1.0", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order, ok := Compare(tt.a, tt.b)
			if ok != tt.ok || order != tt.order {
				t.Errorf("Compare(%q, %q) = %d, %v; want %d, %v", tt.a, tt.b, order, ok, tt.order, tt.ok)
			}
		})
	}
}

func TestWarning(t *testing.T) {
	const cellar = "/opt/homebrew/Cellar/ropeshift/0.1.0-alpha.17/bin/ropeshift"
	tests := []struct {
		name      string
		running   string
		installed Installed
		want      []string
	}{
		{"the same version", "v0.1.0-alpha.16", Installed{Path: cellar, Version: "v0.1.0-alpha.16"}, nil},
		{"an installed build that is newer", "v0.1.0-alpha.16", Installed{Path: cellar, Version: "v0.1.0-alpha.17"},
			[]string{"v0.1.0-alpha.17 is installed", "still running v0.1.0-alpha.16", "brew services restart ropeshift"}},
		{"an installed build that is older", "v0.1.0-alpha.17", Installed{Path: cellar, Version: "v0.1.0-alpha.16"}, nil},
		{"an installed build that could not be read", "v0.1.0-alpha.16", Installed{Path: cellar}, nil},
		{"a daemon that reports no version", "", Installed{Path: cellar, Version: "v0.1.0-alpha.17"},
			[]string{"v0.1.0-alpha.17 is installed", "still running an older build", "brew services restart ropeshift"}},
		{"a daemon that reports no version and an installed development build", "", Installed{Path: cellar, Version: Development}, nil},
		{"a development daemon", Development, Installed{Path: cellar, Version: "v0.1.0"}, nil},
		{"a build outside Homebrew", "v0.1.0-alpha.16", Installed{Path: "/usr/local/bin/ropeshift", Version: "v0.1.0"},
			[]string{"v0.1.0 is installed", "Restart the ropeshift service"}},
		{"the opt link of an Intel Mac", "v0.1.0-alpha.16", Installed{Path: "/usr/local/opt/ropeshift/bin/ropeshiftd", Version: "v0.1.0"},
			[]string{"brew services restart ropeshift"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Warning(tt.running, tt.installed)
			if len(tt.want) == 0 {
				if got != "" {
					t.Errorf("warned %q, want nothing", got)
				}
				return
			}
			for _, part := range tt.want {
				if !strings.Contains(got, part) {
					t.Errorf("warning %q lacks %q", got, part)
				}
			}
		})
	}
	if got := Warning("v0.1.0", Installed{Path: "/usr/local/bin/ropeshift", Version: "v0.2.0"}); strings.Contains(got, "brew") {
		t.Errorf("a build outside Homebrew was told to use brew: %q", got)
	}
}
