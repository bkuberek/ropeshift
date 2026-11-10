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

func TestANewRepositoryLeavesEveryMergeToThePerson(t *testing.T) {
	r := NewRepository("r")
	if r.MergeAuthority != PersonMerges {
		t.Errorf("a new record reads merge authority %q", r.MergeAuthority)
	}
	if !r.MergeAuthority.PersonDecides() {
		t.Error("the default level does not leave the decision to the person")
	}
}

func TestOnlyReviewApprovesLeavesAcceptanceToTheReview(t *testing.T) {
	for authority, decides := range map[MergeAuthority]bool{PersonMerges: true, PersonApproves: true, ReviewApproves: false} {
		if got := authority.PersonDecides(); got != decides {
			t.Errorf("%s: the person decides is %v, want %v", authority, got, decides)
		}
	}
}

func TestARepositoryRefusesAMergeAuthorityNobodyOffers(t *testing.T) {
	r := NewRepository("r")
	r.MergeAuthority = "whatever"
	if err := r.Validate(); !errors.Is(err, ErrInvalidRepository) {
		t.Errorf("a level nobody offers: %v", err)
	}
	for _, level := range []MergeAuthority{PersonMerges, PersonApproves, ReviewApproves} {
		r.MergeAuthority = level
		if err := r.Validate(); err != nil {
			t.Errorf("%s: %v", level, err)
		}
	}
}

// A record made before the level existed reads as the default, because the empty value is not a level.
func TestARecordWithNoLevelReadsAsTheDefault(t *testing.T) {
	r := Repository{ID: "r", MergeMethod: MergeCommit, StartFrom: StartFromMerged}
	if r.Level() != PersonMerges {
		t.Errorf("an empty level reads %q", r.Level())
	}
}
