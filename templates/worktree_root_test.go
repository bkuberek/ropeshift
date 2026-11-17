package templates_test

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/profiles"
	"ropeshift.com/ropeshift/templates"
)

// The user profile sets the worktree root it is given, whatever characters the path holds, and comments the
// root out when it is given none, so the built-in root holds.
func TestTheUserProfileSetsTheWorktreeRootItIsGiven(t *testing.T) {
	for _, root := range []string{
		"/srv/lanes/{project}/{repo}",
		"/srv/my lanes/{repo}",
		`/srv/a"b\c/{project}/{repo}`,
		"/srv/été/{repo}",
		"/srv/tab\there/{repo}",
	} {
		text := render(t, "user/profile.toml", templates.Data{WorktreeRoot: root})
		profile, err := profiles.Resolve(text)
		if err != nil {
			t.Fatalf("the profile for the root %q does not resolve: %v\n%s", root, err, text)
		}
		if profile.Worktree.Root != root {
			t.Errorf("the profile for the root %q resolves to %q", root, profile.Worktree.Root)
		}
	}
}

func TestTheUserProfileCommentsOutTheRootWhenItIsGivenNone(t *testing.T) {
	for _, data := range datasets {
		text := render(t, "user/profile.toml", data.data)
		profile, err := profiles.Resolve(text)
		if err != nil {
			t.Fatalf("%s: %v", data.name, err)
		}
		if profile.Worktree.Root != "" {
			t.Errorf("%s: the profile sets the root %q", data.name, profile.Worktree.Root)
		}
		if !strings.Contains(text, "\n# root = \"/path/to/lanes/{project}/{repo}\"\n") {
			t.Errorf("%s: the profile does not show the root commented out:\n%s", data.name, text)
		}
	}
}
