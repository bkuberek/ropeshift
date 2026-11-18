package adoption

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
)

// A name the ledger accepts can still hold what make or a shell reads as syntax: a variable, a comment, or a
// separator. The Makefile carries only a name made of letters, digits, and . _ -.
func TestAMakefileRefusesNamesTheLedgerAccepts(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"a$b", "a#b", "a:b", "a=b", "a'b", "a(b)", "a;b", "a&b", "a|b", "a`b", "é"} {
		if err := ledger.CheckRepositoryName(name); err != nil {
			t.Fatalf("the ledger refuses %q: %v", name, err)
		}
		if makeWord(name) {
			t.Errorf("the Makefile carries the name %q", name)
		}
	}
	for _, name := range []string{"api", "api-2", "my_api", "api.v2", "A1"} {
		if !makeWord(name) {
			t.Errorf("the Makefile refuses the name %q", name)
		}
	}
	for _, name := range []string{"", ".", "..", "-api", "a b", "a/b"} {
		if makeWord(name) {
			t.Errorf("the Makefile carries the name %q", name)
		}
	}
}

// A path is carried when each of its segments is non-empty, is neither . nor .., and is made of letters, digits, and
// . _ -. The ledger's name check refuses a slash and refuses . and .. only as a whole name, so a path is checked by
// segment here.
func TestAMakefileChecksEachSegmentOfAPath(t *testing.T) {
	t.Parallel()
	for _, rel := range []string{"repos/api", "repos/group/deep", "services/a.b/x_y-z", "repos/-x", ".hidden/x", "a", "repos/...x", ".github/x", "repos/api.git", "repos/.gitx", "repos/x.git/y"} {
		if !makePath(rel) {
			t.Errorf("the Makefile refuses the path %q", rel)
		}
	}
	for _, rel := range []string{
		"", ".", "..", "repos/../x", "../x", "repos/..", "repos//x", "/repos/x", "repos/x/", "./repos/x", "repos/./x",
		".git", ".git/modules/x", "repos/.git", "repos/.git/x", ".GIT/modules/x", ".Git", "repos/.gIt/x",
		"-x/api", "repos/a b", "repos/a$b", "repos/a#b", "repos/a:b", "repos/a=b", "repos\\x", "repos/é",
	} {
		if makePath(rel) {
			t.Errorf("the Makefile carries the path %q", rel)
		}
	}
}

// A segment named .git, in any case, is git's own folder: a clone registered at a path through it would be git's data,
// and on a file system that ignores case .GIT is the same folder. The Makefile leaves such a repository out and says so.
func TestAMakefileOmitsARepositoryWhosePathHoldsAGitFolder(t *testing.T) {
	t.Parallel()
	for _, rel := range []string{".git/modules/x", "repos/.GIT/x", "repos/.Git"} {
		carried, omitted := splitCarried([]planning.WorkspaceRepository{{Name: "api", Path: rel}})
		if len(carried) != 0 || len(omitted) != 1 || !strings.Contains(omitted[0].Reason, `".git"`) {
			t.Errorf("path %q: carried %v, omitted %v, want one omission naming a .git segment", rel, carried, omitted)
		}
	}
}
