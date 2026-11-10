package ledger

import (
	"errors"
	"testing"
)

func TestARepositoryFollowsItsDefaultBranchUntilAMemberNamesABase(t *testing.T) {
	r := Repository{ID: "r", DefaultBranch: "main"}
	if got := r.Base(); got != "main" {
		t.Errorf("a record with no base reads %q", got)
	}
	r.BaseBranch = "release/1.2"
	if got := r.Base(); got != "release/1.2" {
		t.Errorf("a record with a base reads %q", got)
	}
}

func TestANewRepositoryHoldsTheSafestDefaults(t *testing.T) {
	r := NewRepository("r")
	if r.MergeMethod != MergeCommit || r.StartFrom != StartFromMerged || r.Integration {
		t.Errorf("defaults: %+v", r)
	}
}

func TestStartingFromAcceptedWorkNeedsAMethodThatKeepsCommits(t *testing.T) {
	for _, method := range []MergeMethod{MergeSquash, MergeRebase} {
		r := NewRepository("r")
		r.MergeMethod, r.StartFrom = method, StartFromAccepted
		if err := r.Validate(); !errors.Is(err, ErrRewritesCommits) {
			t.Errorf("%s with accepted: %v", method, err)
		}
	}
	r := NewRepository("r")
	r.StartFrom = StartFromAccepted
	if err := r.Validate(); err != nil {
		t.Errorf("merge with accepted: %v", err)
	}
	r.StartFrom, r.MergeMethod = StartFromMerged, MergeSquash
	if err := r.Validate(); err != nil {
		t.Errorf("squash with merged: %v", err)
	}
}

func TestARepositoryRefusesWhatIsNotAMethodOrAStart(t *testing.T) {
	r := NewRepository("r")
	r.MergeMethod = "fast-forward"
	if err := r.Validate(); !errors.Is(err, ErrInvalidRepository) {
		t.Errorf("a method nobody offers: %v", err)
	}
	r = NewRepository("r")
	r.StartFrom = "whenever"
	if err := r.Validate(); !errors.Is(err, ErrInvalidRepository) {
		t.Errorf("a start nobody offers: %v", err)
	}
}

func TestABranchNameIsAGitRefName(t *testing.T) {
	good := []string{"main", "release/1.2", "feature/a-b_c.d", "v2"}
	bad := []string{"", "-rf", "a b", "a..b", "/a", "a/", "a//b", "a.lock", "a~1", "a^", "a:b", "a?b", "a*b", "a[b", "a\\b", "@", "a@{b", ".hidden", "a/.hidden", "a."}
	for _, name := range good {
		if err := CheckBranch(name); err != nil {
			t.Errorf("%q: %v", name, err)
		}
	}
	for _, name := range bad {
		if err := CheckBranch(name); !errors.Is(err, ErrInvalidRepository) {
			t.Errorf("%q: %v", name, err)
		}
	}
}
