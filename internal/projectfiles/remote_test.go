package projectfiles_test

import (
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/projectfiles"
)

func checkRemote(remote string) error {
	return projectfiles.Check(projectfiles.Workspace, []projectfiles.CodeRepository{{Name: "orchid-api", Remote: remote}})
}

// A remote is one line a manifest holds as a TOML string and a Makefile hands to a shell, so Check accepts
// the URL and scp-like forms git clones from and nothing that could end the string or the line.
func TestAcceptedRemoteForms(t *testing.T) {
	for _, remote := range []string{
		"https://github.com/orchid/orchid-api.git",
		"https://example.invalid:8443/orchid/orchid-api",
		"ssh://git@github.com/orchid/orchid-api.git",
		"ssh://git@example.invalid:2222/orchid/orchid-api.git",
		"git://example.invalid/orchid/orchid-api.git",
		"file:///srv/git/orchid-api.git",
		"git@github.com:orchid/orchid-api.git",
		"example.invalid:orchid/orchid-api.git",
		"https://example.invalid/a%20b.git",
	} {
		if err := checkRemote(remote); err != nil {
			t.Errorf("remote %q: %v", remote, err)
		}
	}
}

func TestRefusedRemotes(t *testing.T) {
	cases := []struct {
		name   string
		remote string
	}{
		{"a control character", "https://example.invalid/a\x01b.git"},
		{"a newline", "https://example.invalid/a\nb.git"},
		{"a carriage return", "https://example.invalid/a\rb.git"},
		{"a tab", "https://example.invalid/a\tb.git"},
		{"a space", "https://example.invalid/a b.git"},
		{"a leading space", " https://example.invalid/a.git"},
		{"a double quote", `https://example.invalid/a".git`},
		{"a single quote", "https://example.invalid/a'.git"},
		{"a backslash", `https://example.invalid/a\b.git`},
		{"a dollar sign", "https://example.invalid/$HOME.git"},
		{"a command substitution", "https://example.invalid/$(touch pwned).git"},
		{"a backtick", "https://example.invalid/`touch pwned`.git"},
		{"a semicolon", "https://example.invalid/a;touch pwned"},
		{"an ampersand", "https://example.invalid/a&b"},
		{"a pipe", "https://example.invalid/a|b"},
		{"a hash", "https://example.invalid/a#b"},
		{"a glob character", "https://example.invalid/a*.git"},
		{"a leading dash", "-oProxyCommand=x"},
		{"a scp-like leading dash", "-x@example.invalid:a.git"},
		{"a bare word", "orchid-api"},
		{"a local path", "/srv/git/orchid-api.git"},
		{"a relative path", "../orchid-api"},
		{"a scheme git does not clone from", "ftp://example.invalid/a.git"},
		{"a URL with no host", "https:///a.git"},
		{"a URL with nothing after the scheme", "https://"},
		{"a scp-like remote with no path", "git@example.invalid:"},
		{"a scp-like remote with no host", "git@:a.git"},
		{"a remote helper that runs a command", "ext::foo"},
		{"a remote helper on a file descriptor", "fd::0"},
	}
	for _, remote := range []string{"ext::foo", "fd::0"} {
		if err := checkRemote(remote); err == nil || !strings.Contains(err.Error(), "remote helper") {
			t.Errorf("remote %q: %v, want the rule that a remote names no remote helper", remote, err)
		}
	}
	for _, remote := range []string{"/srv/git/orchid-api.git", "../orchid-api", "orchid-api"} {
		if err := checkRemote(remote); err == nil || !strings.Contains(err.Error(), "for a local repository use a file:// URL") {
			t.Errorf("remote %q: %v, want the way to name a local repository", remote, err)
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkRemote(tc.remote)
			if !errors.Is(err, projectfiles.ErrInvalid) || !strings.Contains(err.Error(), "remote") || !strings.Contains(err.Error(), "orchid-api") {
				t.Errorf("remote %q: %v, want %v naming the repository and its remote", tc.remote, err, projectfiles.ErrInvalid)
			}
		})
	}
}

// A credential in a URL would reach the installation, the reports, and the committed manifest. An https or http URL
// holds no user, because a token as the user is the common case, and no URL holds a password. The refusal does not
// repeat what it refused.
func TestARemoteWithACredentialIsRefusedWithoutEchoingIt(t *testing.T) {
	for _, remote := range []string{
		"https://user:s3cret@example.invalid/orchid/orchid-api.git",
		"https://:s3cret@example.invalid/orchid/orchid-api.git",
		"ssh://git:s3cret@example.invalid/orchid/orchid-api.git",
		"git://user:s3cret@example.invalid:9418/orchid-api.git",
		"https://user:s3cret@example.invalid/a b.git",
		"https://us er:s3cret@example.invalid/a.git",
		"https://s3cret@example.invalid/orchid/orchid-api.git",
		"https://s3cret:@example.invalid/orchid/orchid-api.git",
		"https://s3cret@example.invalid:8443/orchid/orchid-api.git",
		"http://s3cret@example.invalid/orchid/orchid-api.git",
		"https://s3 cret@example.invalid/a.git",
	} {
		for name, err := range map[string]error{"CheckRemote": projectfiles.CheckRemote(remote), "Check": checkRemote(remote)} {
			if err == nil || !strings.Contains(err.Error(), "no user or password") {
				t.Errorf("%s %q: %v, want the rule that a remote holds no credential", name, remote, err)
				continue
			}
			if strings.Contains(err.Error(), "s3cret") {
				t.Errorf("%s %q: the refusal echoes the credential: %v", name, remote, err)
			}
		}
	}
	for _, remote := range []string{
		"ssh://git@example.invalid/orchid/orchid-api.git",
		"ssh://git@example.invalid:2222/orchid/orchid-api.git",
		"git://git@example.invalid/orchid/orchid-api.git",
		"https://example.invalid:8443/orchid/a:b.git",
		"https://example.invalid/orchid/a@b.git",
		"git@example.invalid:orchid/orchid-api.git",
	} {
		if err := projectfiles.CheckRemote(remote); err != nil {
			t.Errorf("remote %q: %v", remote, err)
		}
	}
}
