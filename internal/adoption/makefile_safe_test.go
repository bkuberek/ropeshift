package adoption

import (
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
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
	for _, rel := range []string{"repos/api", "repos/group/deep", "services/a.b/x_y-z", "repos/-x", ".hidden/x", "a", "repos/...x"} {
		if !makePath(rel) {
			t.Errorf("the Makefile refuses the path %q", rel)
		}
	}
	for _, rel := range []string{
		"", ".", "..", "repos/../x", "../x", "repos/..", "repos//x", "/repos/x", "repos/x/", "./repos/x", "repos/./x",
		"-x/api", "repos/a b", "repos/a$b", "repos/a#b", "repos/a:b", "repos/a=b", "repos\\x", "repos/é",
	} {
		if makePath(rel) {
			t.Errorf("the Makefile carries the path %q", rel)
		}
	}
}
