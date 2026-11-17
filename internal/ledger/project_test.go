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
		{"a display name too long to show", func(p *Project) { p.DisplayName = strings.Repeat("a", 101) }},
		{"a display name with a control character", func(p *Project) { p.DisplayName = "Acme\x1b[2J" }},
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

// A display name is what a list or a heading shows whole: at most 100 characters, counted as characters and not
// bytes, with no control character and no space at either end. An empty one is valid, because a project may have
// none and a rename given none keeps the one it has.
func TestADisplayNameIsShortTrimmedTextWithNoControlCharacter(t *testing.T) {
	zwj, zwnj := string(rune(0x200d)), string(rune(0x200c))
	for _, tt := range []struct {
		name        string
		displayName string
		ok          bool
	}{
		{"empty", "", true},
		{"words with capitals", "Harbor docs, 2026", true},
		{"100 characters of two bytes each", strings.Repeat("é", 100), true},
		{"101 characters", strings.Repeat("a", 101), false},
		{"a tab", "Harbor\tdocs", false},
		{"a line break", "Harbor\ndocs", false},
		{"an escape sequence", "Harbor\x1b[31m", false},
		{"a delete", "Harbor\x7f", false},
		{"space before", " Harbor", false},
		{"space after", "Harbor ", false},
		{"bytes that are no UTF-8", "Harbor\xff", false},
		{"a right-to-left override", "Harbor" + string(rune(0x202e)) + "docs", false},
		{"a left-to-right embedding", "Harbor" + string(rune(0x202a)) + "docs", false},
		{"a bidi isolate", "Harbor" + string(rune(0x2066)) + "docs", false},
		{"a pop directional isolate", "Harbor" + string(rune(0x2069)) + "docs", false},
		{"a zero-width space", "Harbor" + string(rune(0x200b)) + "docs", false},
		{"a byte order mark", string(rune(0xFEFF)) + "Harbor", false},
		{"a line separator", "Harbor" + string(rune(0x2028)) + "docs", false},
		{"a paragraph separator", "Harbor" + string(rune(0x2029)) + "docs", false},
		{"letters from other scripts", "Ünïcödé 日本語 العربية", true},
		{"a Persian name with a non-joiner", "می" + zwnj + "خواهم", true},
		{"a profession emoji with a joiner", "\U0001F469" + zwj + "\U0001F4BB Team", true},
		{"a flag emoji with a variation selector and a joiner", "\U0001F3F3️" + zwj + "\U0001F308", true},
		{"a skin tone modifier before a joiner", "\U0001F469\U0001F3FD" + zwj + "\U0001F4BB", true},
		{"a joiner at the start", zwj + "Harbor", false},
		{"a joiner at the end", "Harbor" + zwj, false},
		{"a non-joiner at the start", zwnj + "Harbor", false},
		{"a non-joiner at the end", "Harbor" + zwnj, false},
		{"a doubled joiner", "a" + zwj + zwj + "b", false},
		{"a non-joiner then a joiner", "a" + zwnj + zwj + "b", false},
		{"a joiner after a space", "a " + zwj + "b", false},
		{"a joiner before a space", "a" + zwj + " b", false},
		{"a joiner next to a zero-width space", "a" + zwj + string(rune(0x200b)) + "b", false},
		{"a zero-width space next to a non-joiner", "a" + string(rune(0x200b)) + zwnj + "b", false},
		{"a joiner next to a bidi override", "a" + zwj + string(rune(0x202e)) + "b", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDisplayName(tt.displayName)
			if tt.ok && err != nil {
				t.Errorf("%q refused: %v", tt.displayName, err)
			}
			if !tt.ok && !errors.Is(err, ErrInvalidProject) {
				t.Errorf("%q: %v", tt.displayName, err)
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
