package lanes

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
)

func lookupIn(env map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := env[name]
		return value, ok
	}
}

func TestBuildEnvironment(t *testing.T) {
	parent := map[string]string{
		"HOME": "/home/someone", "PATH": "/usr/bin", "TERM": "xterm", "LANG": "C.UTF-8", "USER": "someone", "LOGNAME": "someone",
		"CLAUDECODE": "1", "CLAUDE_CODE_CHILD_SESSION": "1", "CLAUDE_CODE_SESSION_ID": "s", "CLAUDE_CODE_ENTRYPOINT": "cli",
		"ANTHROPIC_API_KEY": "k", "ANTHROPIC_BASE_URL": "https://example.invalid", "AWS_SECRET_ACCESS_KEY": "a", "GITHUB_TOKEN": "g",
		"SSH_AUTH_SOCK": "/s", "TMUX": "/t", "TMUX_PANE": "%1", "ROPESHIFT_SOCKET": "/parent", "NODE_OPTIONS": "--x", "OLDPWD": "/o",
		"PWD": "/p", "GIT_DIR": "/g", "BASH_ENV": "/b", "ENV": "/e", "LD_PRELOAD": "/l", "DYLD_INSERT_LIBRARIES": "/d",
	}
	tests := []struct {
		name    string
		spec    EnvironmentSpec
		want    []string
		refused []string
	}{
		{
			name: "only the allowlist passes",
			spec: EnvironmentSpec{},
			want: []string{"HOME=/home/someone", "LANG=C.UTF-8", "LOGNAME=someone", "PATH=/usr/bin", "TERM=xterm", "USER=someone"},
			refused: []string{
				"CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_ENTRYPOINT", "ANTHROPIC_API_KEY",
				"ANTHROPIC_BASE_URL", "AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN", "SSH_AUTH_SOCK", "TMUX", "TMUX_PANE", "ROPESHIFT_SOCKET", authority.EnvMemberToken, authority.EnvDelegateToken,
				"NODE_OPTIONS", "OLDPWD", "PWD", "GIT_DIR", "BASH_ENV", "ENV", "LD_PRELOAD", "DYLD_INSERT_LIBRARIES",
			},
		},
		{
			name:    "a matching reference passes one variable",
			spec:    EnvironmentSpec{SecretRefs: []string{"env:ANTHROPIC_API_KEY"}},
			want:    []string{"ANTHROPIC_API_KEY=k", "HOME=/home/someone", "LANG=C.UTF-8", "LOGNAME=someone", "PATH=/usr/bin", "TERM=xterm", "USER=someone"},
			refused: []string{"ANTHROPIC_BASE_URL", "GITHUB_TOKEN"},
		},
		{
			name:    "a keychain reference binds in the driver and passes nothing",
			spec:    EnvironmentSpec{SecretRefs: []string{"keychain:ropeshift-key"}},
			want:    []string{"HOME=/home/someone", "LANG=C.UTF-8", "LOGNAME=someone", "PATH=/usr/bin", "TERM=xterm", "USER=someone"},
			refused: []string{"ANTHROPIC_API_KEY"},
		},
		{
			name: "ropeshift's tags reach the lane",
			spec: EnvironmentSpec{Tags: map[string]string{"ROPESHIFT_LANE": "lane-1"}},
			want: []string{"HOME=/home/someone", "LANG=C.UTF-8", "LOGNAME=someone", "PATH=/usr/bin", "ROPESHIFT_LANE=lane-1", "TERM=xterm", "USER=someone"},
		},
		{
			name: "a tag replaces what a parent set",
			spec: EnvironmentSpec{Tags: map[string]string{"ROPESHIFT_SOCKET": "/mine"}},
			want: []string{"HOME=/home/someone", "LANG=C.UTF-8", "LOGNAME=someone", "PATH=/usr/bin", "ROPESHIFT_SOCKET=/mine", "TERM=xterm", "USER=someone"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := tt.spec
			spec.Lookup = lookupIn(parent)
			got, err := BuildEnvironment(spec)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %v\nwant %v", got, tt.want)
			}
			for _, name := range tt.refused {
				for _, entry := range got {
					if strings.HasPrefix(entry, name+"=") {
						t.Errorf("%s reached the lane", name)
					}
				}
			}
		})
	}
}

func TestBuildEnvironmentRefuses(t *testing.T) {
	parent := map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "t", "CLAUDECODE": "1"}
	tests := []struct {
		name string
		spec EnvironmentSpec
		want error
	}{
		{"an OAuth token", EnvironmentSpec{SecretRefs: []string{"env:CLAUDE_CODE_OAUTH_TOKEN"}}, ErrSessionVariable},
		{"a child session marker", EnvironmentSpec{SecretRefs: []string{"env:CLAUDECODE"}}, ErrSessionVariable},
		{"a variable that is not set", EnvironmentSpec{SecretRefs: []string{"env:ANTHROPIC_API_KEY"}}, ErrMissingVariable},
		{"a reference with no name", EnvironmentSpec{SecretRefs: []string{"env:"}}, ErrBadReference},
		{"a reference with no form", EnvironmentSpec{SecretRefs: []string{"ANTHROPIC_API_KEY"}}, ErrBadReference},
		{"a tag with no prefix", EnvironmentSpec{Tags: map[string]string{"LANE": "x"}}, ErrBadTag},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := tt.spec
			spec.Lookup = lookupIn(parent)
			if _, err := BuildEnvironment(spec); !errors.Is(err, tt.want) {
				t.Errorf("got %v, want %v", err, tt.want)
			}
		})
	}
}

func TestCheckSpawn(t *testing.T) {
	tests := []struct {
		name   string
		binary string
		want   error
	}{
		{"an absolute binary", "/opt/claude/bin/claude", nil},
		{"a name PATH would resolve", "claude", ErrRelativePath},
		{"a relative path", "./claude", ErrRelativePath},
		{"a login shell", "/bin/zsh", ErrShellSpawn},
		{"sh", "/bin/sh", ErrShellSpawn},
		{"bash", "/usr/local/bin/bash", ErrShellSpawn},
		{"fish", "/opt/homebrew/bin/fish", ErrShellSpawn},
		{"login", "/usr/bin/login", ErrShellSpawn},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := CheckSpawn(tt.binary); !errors.Is(err, tt.want) {
				t.Errorf("got %v, want %v", err, tt.want)
			}
		})
	}
}

// TestAMemberSessionCannotBeNamedToALane closes the two ways around the allowlist: a profile
// that names the variable, and a tag that sets it (D26).
func TestAMemberSessionCannotBeNamedToALane(t *testing.T) {
	lookup := func(name string) (string, bool) { return "held-by-a-shell", name == authority.EnvMemberToken }
	if _, err := BuildEnvironment(EnvironmentSpec{Lookup: lookup, SecretRefs: []string{"env:" + authority.EnvMemberToken}}); !errors.Is(err, ErrSessionVariable) {
		t.Fatalf("a reference: got %v, want ErrSessionVariable", err)
	}
	spec := EnvironmentSpec{Lookup: lookup, Tags: map[string]string{authority.EnvMemberToken: "x"}}
	if _, err := BuildEnvironment(spec); !errors.Is(err, ErrSessionVariable) {
		t.Fatalf("a tag: got %v, want ErrSessionVariable", err)
	}
}

// TestADelegateCredentialCannotBeNamedToALane holds a delegate credential to what holds a member's
// session: no profile names it to a lane, and no tag sets it (ADR-0024).
func TestADelegateCredentialCannotBeNamedToALane(t *testing.T) {
	lookup := func(name string) (string, bool) { return "held-by-an-agent", name == authority.EnvDelegateToken }
	if _, err := BuildEnvironment(EnvironmentSpec{Lookup: lookup, SecretRefs: []string{"env:" + authority.EnvDelegateToken}}); !errors.Is(err, ErrSessionVariable) {
		t.Fatalf("a reference: got %v, want ErrSessionVariable", err)
	}
	spec := EnvironmentSpec{Lookup: lookup, Tags: map[string]string{authority.EnvDelegateToken: "x"}}
	if _, err := BuildEnvironment(spec); !errors.Is(err, ErrSessionVariable) {
		t.Fatalf("a tag: got %v, want ErrSessionVariable", err)
	}
}

// TestALaneGetsNoForgeCredential is ADR-0020's guardrail: no profile names a forge token to a lane, and
// gh in a lane reads its configuration from a directory ropeshift gives it, not the person's.
func TestALaneGetsNoForgeCredential(t *testing.T) {
	parent := map[string]string{
		"PATH": "/usr/bin", "GH_TOKEN": "gh", "GITHUB_TOKEN": "g", "GH_ENTERPRISE_TOKEN": "e", "GITHUB_ENTERPRISE_TOKEN": "f",
		"GH_CONFIG_DIR": "/home/someone/.config/gh",
	}
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GH_CONFIG_DIR"} {
		_, err := BuildEnvironment(EnvironmentSpec{Lookup: lookupIn(parent), SecretRefs: []string{"env:" + name}})
		if !errors.Is(err, ErrForgeCredential) {
			t.Errorf("a reference to %s: got %v, want ErrForgeCredential", name, err)
		}
	}
	got, err := BuildEnvironment(EnvironmentSpec{Lookup: lookupIn(parent), ForgeConfigDir: "/state/lanes/lane-1/gh"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"GH_CONFIG_DIR=/state/lanes/lane-1/gh", "PATH=/usr/bin"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
