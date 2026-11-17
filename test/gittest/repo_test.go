package gittest

import (
	"strings"
	"testing"
)

// Pairs made in one test, in the same second, share no commit with each other, so a repository of one pair is
// never the repository of another by its first commit.
func TestPairsMadeTogetherShareNoFirstCommit(t *testing.T) {
	seen := map[string]bool{}
	for range 4 {
		higher, lower := NewUnrelatedPair(t)
		for _, root := range []string{higher.Roots("HEAD")[0], lower.Roots("HEAD")[0]} {
			if seen[root] {
				t.Fatalf("the first commit %s was made twice", root)
			}
			seen[root] = true
		}
	}
}

// A history whose first commit sorts below a limit is made whatever the limit is, however many leading zeros it
// has, and the repository holds one valid commit.
func TestNewWithRootBelowReachesALowLimit(t *testing.T) {
	for _, limit := range []string{
		"ffffffffffffffffffffffffffffffffffffffff",
		"00" + strings.Repeat("f", 38),
		"0000" + strings.Repeat("f", 36),
	} {
		repo := NewWithRootBelow(t, "ffffffffffffffffffffffffffffffffffffffff", limit)
		roots := repo.Roots("HEAD")
		if len(roots) != 1 || roots[0] >= limit {
			t.Errorf("the first commit is %v, want one below %s", roots, limit)
		}
		if out := repo.Git("fsck", "--strict"); strings.Contains(out, "error") {
			t.Errorf("the repository is not sound: %s", out)
		}
		if got := repo.Git("rev-list", "--count", "HEAD"); got != "1" {
			t.Errorf("the repository holds %s commits, want 1", got)
		}
	}
}
