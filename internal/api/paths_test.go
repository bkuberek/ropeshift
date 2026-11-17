package api

import (
	"strconv"
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

func TestResolvePathsWorktreeDir(t *testing.T) {
	const home = "/home/someone"
	tests := []struct {
		name string
		goos string
		env  map[string]string
		want string
	}{
		{"macOS beside the socket folder", "darwin", nil, home + "/.ropeshift/worktrees"},
		{"macOS ignores the XDG variable", "darwin", map[string]string{"XDG_DATA_HOME": "/data"}, home + "/.ropeshift/worktrees"},
		{"Linux under XDG_DATA_HOME", "linux", map[string]string{"XDG_DATA_HOME": "/data"}, "/data/ropeshift/worktrees"},
		{"Linux with XDG_DATA_HOME unset", "linux", nil, home + "/.local/share/ropeshift/worktrees"},
		{"the worktree variable replaces it", "linux", map[string]string{"XDG_DATA_HOME": "/data", EnvWorktreeDir: "/w"}, "/w"},
		{"the worktree variable replaces it on macOS", "darwin", map[string]string{EnvWorktreeDir: "/w"}, "/w"},
		{"a state directory moves it under the state directory", "linux", map[string]string{EnvStateDir: "/s", "XDG_DATA_HOME": "/data"}, "/s/worktrees"},
		{"a state directory moves it on macOS", "darwin", map[string]string{EnvStateDir: "/s"}, "/s/worktrees"},
		{"the worktree variable wins over the state directory", "linux", map[string]string{EnvStateDir: "/s", EnvWorktreeDir: "/w"}, "/w"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolvePaths(tt.goos, func(key string) string { return tt.env[key] }, home)
			if err != nil {
				t.Fatal(err)
			}
			if got.WorktreeDir != tt.want {
				t.Errorf("WorktreeDir = %q, want %q", got.WorktreeDir, tt.want)
			}
		})
	}
}

// The XDG specification says a relative XDG_DATA_HOME is invalid and ignored, so the default stands. A relative
// ROPESHIFT_WORKTREE_DIR is the person's own choice and a wrong one, which would put lanes beside whatever
// directory the daemon started in, so the installation refuses to start and names the variable.
func TestARelativeWorktreeDirectoryIsIgnoredOrRefused(t *testing.T) {
	const home = "/home/someone"
	got, err := ResolvePaths("linux", func(key string) string { return map[string]string{"XDG_DATA_HOME": "data"}[key] }, home)
	if want := home + "/.local/share/ropeshift/worktrees"; err != nil || got.WorktreeDir != want {
		t.Errorf("a relative XDG_DATA_HOME gave %q (%v), want %q", got.WorktreeDir, err, want)
	}
	_, err = ResolvePaths("linux", func(key string) string { return map[string]string{EnvWorktreeDir: "lanes"}[key] }, home)
	if err == nil || !strings.Contains(err.Error(), EnvWorktreeDir) || !strings.Contains(err.Error(), "absolute") {
		t.Errorf("a relative %s: %v", EnvWorktreeDir, err)
	}
	// The state directory moves the worktrees only when it is itself an absolute path to move them under.
	if got, err := ResolvePaths("linux", func(key string) string { return map[string]string{EnvStateDir: "/s", EnvWorktreeDir: ""}[key] }, home); err != nil || got.WorktreeDir != "/s/worktrees" {
		t.Errorf("an unset %s: %q (%v)", EnvWorktreeDir, got.WorktreeDir, err)
	}
}

// A test or a second installation sets ROPESHIFT_STATE_DIR, and nothing it starts may name the person's own
// worktree directory.
func TestAnExplicitStateDirectoryNeverNamesTheHomeDefault(t *testing.T) {
	const home = "/home/someone"
	for _, goos := range []string{"linux", "darwin"} {
		for _, data := range []string{"", "/data"} {
			env := map[string]string{EnvStateDir: "/tmp/state", "XDG_DATA_HOME": data}
			got, err := ResolvePaths(goos, func(key string) string { return env[key] }, home)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(got.WorktreeDir, home) || (data != "" && strings.HasPrefix(got.WorktreeDir, data)) {
				t.Errorf("%s with XDG_DATA_HOME=%q put the worktrees at %q, outside the state directory", goos, data, got.WorktreeDir)
			}
		}
	}
}

func TestResolvePathsRefuses(t *testing.T) {
	if _, err := ResolvePaths("plan9", func(string) string { return "" }, "/home/someone"); err == nil {
		t.Error("an unknown system resolved")
	}
	long := "/" + strings.Repeat("a", MaxSocketPath)
	_, err := ResolvePaths("darwin", func(string) string { return "" }, long)
	if err == nil {
		t.Fatal("a socket path over the limit resolved")
	}
	assertSocketAdvice(t, err, "control.sock")
}

func TestSocketPathTooLongNamesTheLimitThePathAndTheFix(t *testing.T) {
	err := SocketPathTooLong("/very/long/path.sock")
	assertSocketAdvice(t, err, "/very/long/path.sock")
}

func assertSocketAdvice(t *testing.T, err error, path string) {
	t.Helper()
	for _, want := range []string{strconv.Itoa(MaxSocketPath), path, EnvRunDir, "shorter directory"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message %q does not say %q", err, want)
		}
	}
}
