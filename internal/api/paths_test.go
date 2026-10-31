package api

import (
	"strings"
	"testing"
)

func TestResolvePaths(t *testing.T) {
	const home = "/home/someone"
	tests := []struct {
		name  string
		goos  string
		env   map[string]string
		state string
		run   string
	}{
		{"macOS", "darwin", nil, home + "/Library/Application Support/ropeshift", home + "/.ropeshift/run"},
		{
			"Linux with the XDG variables", "linux",
			map[string]string{"XDG_STATE_HOME": "/state", "XDG_RUNTIME_DIR": "/run/user/1000"},
			"/state/ropeshift", "/run/user/1000/ropeshift",
		},
		{"Linux with neither", "linux", nil, home + "/.local/state/ropeshift", home + "/.ropeshift/run"},
		{
			"both moved by the environment", "darwin",
			map[string]string{EnvStateDir: "/s", EnvRunDir: "/r"},
			"/s", "/r",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolvePaths(tt.goos, func(key string) string { return tt.env[key] }, home)
			if err != nil {
				t.Fatal(err)
			}
			if got.StateDir != tt.state || got.RunDir != tt.run {
				t.Errorf("got %+v", got)
			}
		})
	}
}

func TestResolvePathsRefuses(t *testing.T) {
	if _, err := ResolvePaths("plan9", func(string) string { return "" }, "/home/someone"); err == nil {
		t.Error("an unknown system resolved")
	}
	long := "/" + strings.Repeat("a", MaxSocketPath)
	if _, err := ResolvePaths("darwin", func(string) string { return "" }, long); err == nil {
		t.Error("a socket path over the limit resolved")
	}
}
