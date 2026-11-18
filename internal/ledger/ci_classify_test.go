package ledger

import "testing"

// failedRun is a required run that finished and did not pass, with the conclusion the forge gave it.
func failedRun(conclusion string) PullRequestCheck {
	return PullRequestCheck{Name: "unit", Required: true, State: CheckFailed, Status: CIRunCompleted, Conclusion: conclusion}
}

// ADR-0037 fixes the class of each conclusion, and no setting moves one.
func TestEveryConclusionHasItsClass(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		conclusion string
		want       CICheckClass
	}{
		{"failure", CIClassTestFailure},
		{"cancelled", CIClassInfrastructure},
		{"timed_out", CIClassInfrastructure},
		{"startup_failure", CIClassInfrastructure},
		{"stale", CIClassInfrastructure},
		{"action_required", CIClassActionRequired},
		// A word the design does not name starts no agent: it is infrastructure, which is rerun a bounded number of times
		// and then reaches a person.
		{"a_conclusion_github_adds_later", CIClassInfrastructure},
		// The words of the forge are lower case, and a read in another case is the same word.
		{"TIMED_OUT", CIClassInfrastructure},
		// A commit status, which has no conclusion, fails as a failure of the code always did.
		{"", CIClassTestFailure},
	} {
		if got := failedRun(tt.conclusion).Class(); got != tt.want {
			t.Errorf("a failed check that concluded %q is %s, want %s", tt.conclusion, got, tt.want)
		}
	}
}

func TestACheckThatPassedOrIsRunningHasNoFailureClass(t *testing.T) {
	t.Parallel()
	for _, conclusion := range []string{"success", "neutral", "skipped"} {
		check := PullRequestCheck{Name: "unit", Required: true, State: CheckPassed, Status: CIRunCompleted, Conclusion: conclusion}
		if got := check.Class(); got != CIClassPass {
			t.Errorf("a check that concluded %q is %s, want %s", conclusion, got, CIClassPass)
		}
	}
	if got := (PullRequestCheck{State: CheckPending}).Class(); got != CIClassPending {
		t.Errorf("a check still running is %s", got)
	}
	// A check the forge failed with a conclusion that reads as a pass says something unexpected, which a person reads.
	if got := failedRun("success").Class(); got != CIClassActionRequired {
		t.Errorf("a failed check that concluded success is %s", got)
	}
}

func TestAFailureTheBaseSharesIsARedBaseAndNothingElseIs(t *testing.T) {
	t.Parallel()
	check := failedRun("failure")
	check.RedOnBase = "abc123"
	if got := check.Class(); got != CIClassBaseRed {
		t.Errorf("a failure the base's tip shares is %s", got)
	}
	// Only a failure of the code is excused by the base: a runner that was lost is lost whatever the base does.
	lost := failedRun("timed_out")
	lost.RedOnBase = "abc123"
	if got := lost.Class(); got != CIClassInfrastructure {
		t.Errorf("a timed out run on a red base is %s", got)
	}
}

func TestAnActionsRunIsReadFromTheAddressOfItsOwnRepositoryOnly(t *testing.T) {
	t.Parallel()
	binding := ForgeBinding{Host: "github.com", Owner: "acme", Repo: "widgets"}
	for _, tt := range []struct {
		address, run, job string
		ok                bool
	}{
		{"https://github.com/acme/widgets/actions/runs/123", "123", "", true},
		{"https://github.com/acme/widgets/actions/runs/123/job/456", "123", "456", true},
		{"https://GitHub.com/Acme/Widgets/actions/runs/123/job/456?pr=7#step:3:1", "123", "456", true},
		{"http://github.com/acme/widgets/actions/runs/123", "", "", false},
		{"https://github.com/acme/other/actions/runs/123", "", "", false},
		{"https://example.com/acme/widgets/actions/runs/123", "", "", false},
		{"https://github.com/acme/widgets/actions/runs/12x", "", "", false},
		{"https://github.com/acme/widgets/actions/runs/123/attempts/2", "", "", false},
		{"https://github.com/acme/widgets/actions/runs/123/job/", "", "", false},
		{"https://ci.example/builds/9", "", "", false},
		{"", "", "", false},
	} {
		run, job, ok := ActionsRun(tt.address, binding)
		if run != tt.run || job != tt.job || ok != tt.ok {
			t.Errorf("%q reads run %q job %q ok %v, want %q %q %v", tt.address, run, job, ok, tt.run, tt.job, tt.ok)
		}
	}
}

func TestAFailureBeforeTheLastRerunIsTheRerunsAndNotANewOne(t *testing.T) {
	t.Parallel()
	check := failedRun("timed_out")
	check.CompletedAt = "2026-01-02T09:00:00Z"
	const rerunAt = 1_767_344_400_000 + 5_000 // 2026-01-02T09:00:05Z
	if check.CompletedAfter(rerunAt) {
		t.Errorf("a run that finished before the rerun was asked for reads as after it")
	}
	if !check.CompletedAfter(rerunAt - 10_000) {
		t.Errorf("a run that finished after the rerun was asked for reads as before it")
	}
	if !check.CompletedAfter(0) {
		t.Errorf("with no rerun asked for, every finish counts")
	}
	check.CompletedAt = "not a time"
	if check.CompletedAfter(rerunAt) {
		t.Errorf("a finish nobody can read counts as a new failure after a rerun")
	}
}
