package ledger

import (
	"errors"
	"slices"
	"strings"
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
	if r.MergeMethod != MergeCommit || r.StartFrom != StartFromMerged || r.ReviewTarget != ReviewOnBranch || r.MergeAuthority != PersonMerges {
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

// A repository is named by its clone's directory, then by the last part of its remote's path when the
// directory's name is taken (ADR-0022), and a name both give that is taken gets a number after it.
func TestARepositoryIsNamedByItsDirectoryThenItsRemote(t *testing.T) {
	taken := func(names ...string) func(string) bool {
		return func(name string) bool { return slices.Contains(names, name) }
	}
	for _, tt := range []struct {
		name, directory, remote, id string
		taken                       func(string) bool
		want                        string
	}{
		{"the directory", "api", "git@github.com:acme/payments-api.git", "abc", taken(), "api"},
		{"the remote when the directory's name is taken", "api", "git@github.com:acme/payments-api.git", "abc", taken("api"), "payments-api"},
		{"the remote with no directory", "", "https://example.invalid/acme/web/", "abc", taken(), "web"},
		{"a number when both are taken", "api", "https://example.invalid/acme/api.git", "abc", taken("api"), "api-2"},
		{"the next number", "api", "", "abc", taken("api", "api-2"), "api-3"},
		{"a directory no command could name", "my api", "https://example.invalid/acme/app.git", "abc", taken(), "app"},
		{"the identity when nothing else names it", "", "", "0123456789abcdef0123", taken(), "0123456789ab"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := NameRepository(tt.directory, tt.remote, tt.id, tt.taken); got != tt.want {
				t.Errorf("named %q, want %q", got, tt.want)
			}
		})
	}
}

func TestARepositoryNameIsOneACommandCanType(t *testing.T) {
	for _, name := range []string{"api", "payments-api", "web.v2", "API_2"} {
		if err := CheckRepositoryName(name); err != nil {
			t.Errorf("%q: %v", name, err)
		}
	}
	for _, name := range []string{"", ".", "..", "-rf", "a b", "a/b", "a\\b", "tab\there", strings.Repeat("a", 101)} {
		if err := CheckRepositoryName(name); !errors.Is(err, ErrInvalidRepository) {
			t.Errorf("%q: %v", name, err)
		}
	}
	r := NewRepository("r")
	r.Name = "a b"
	if err := r.Validate(); !errors.Is(err, ErrInvalidRepository) {
		t.Errorf("a record with a name no command can type: %v", err)
	}
}
