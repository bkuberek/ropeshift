package ledger

import (
	"errors"
	"strings"
	"testing"
)

func TestAProjectNameIsALowercaseHyphenatedSlug(t *testing.T) {
	for _, tt := range []struct {
		name string
		ok   bool
	}{
		{"default", true},
		{"acme-app", true},
		{"app2", true},
		{"2026-plans", true},
		{"", false},
		{"Acme", false},
		{"acme_app", false},
		{"acme app", false},
		{"-acme", false},
		{"acme-", false},
		{"acme--app", false},
		{"acme/app", false},
		{strings.Repeat("a", 63), true},
		{strings.Repeat("a", 64), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProjectName(tt.name)
			if tt.ok && err != nil {
				t.Errorf("%q refused: %v", tt.name, err)
			}
			if !tt.ok && !errors.Is(err, ErrInvalidProject) {
				t.Errorf("%q: %v", tt.name, err)
			}
		})
	}
}

func TestAProjectNeedsAnIDANameAKnownStateAndADatabase(t *testing.T) {
	valid := Project{ID: "p1", Name: "acme", State: ProjectActive, Database: "projects/p1.db"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("a whole project refused: %v", err)
	}
	for _, tt := range []struct {
		name   string
		change func(*Project)
	}{
		{"no id", func(p *Project) { p.ID = "" }},
		{"a name that is no slug", func(p *Project) { p.Name = "Acme App" }},
		{"a state nobody knows", func(p *Project) { p.State = "paused" }},
		{"no database", func(p *Project) { p.Database = "" }},
		{"the installation's own scope", func(p *Project) { p.ID = InstallationScope }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := valid
			tt.change(&p)
			if err := p.Validate(); err == nil {
				t.Errorf("%+v was accepted", p)
			}
		})
	}
}

func TestARepositoryIsIdentifiedByItsRemoteHostAndPath(t *testing.T) {
	for _, tt := range []struct {
		name, remote, id, want string
	}{
		{"https", "https://github.com/acme/app.git", "app", "github.com/acme/app"},
		{"https with no suffix", "https://github.com/acme/app", "app", "github.com/acme/app"},
		{"credentials and a port", "https://user:secret@GitHub.com:443/acme/app.git", "app", "github.com/acme/app"},
		{"ssh url", "ssh://git@github.com:22/acme/app.git", "app", "github.com/acme/app"},
		{"scp form", "git@github.com:acme/app.git", "app", "github.com/acme/app"},
		{"scp form with no user", "github.com:acme/app", "app", "github.com/acme/app"},
		{"a trailing slash", "https://gitlab.example.com/group/sub/app.git/", "app", "gitlab.example.com/group/sub/app"},
		{"a local remote path", "/srv/git/app.git", "app", "/srv/git/app"},
		{"a file url", "file:///srv/git/app.git", "app", "/srv/git/app"},
		{"no remote", "", "/home/me/src/app", "/home/me/src/app"},
		{"no remote and spaces", "  ", "/home/me/src/app", "/home/me/src/app"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := RepositoryIdentity(tt.remote, tt.id); got != tt.want {
				t.Errorf("RepositoryIdentity(%q, %q) = %q, want %q", tt.remote, tt.id, got, tt.want)
			}
		})
	}
}

func TestTheSameRepositoryHasOneIdentityOverEveryProtocol(t *testing.T) {
	remotes := []string{
		"https://github.com/acme/app.git",
		"ssh://git@github.com/acme/app.git",
		"git@github.com:acme/app.git",
		"https://token@github.com/acme/app",
	}
	want := RepositoryIdentity(remotes[0], "")
	for _, remote := range remotes[1:] {
		if got := RepositoryIdentity(remote, ""); got != want {
			t.Errorf("%s reads %q and %s reads %q", remotes[0], want, remote, got)
		}
	}
}
