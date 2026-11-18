package ghcli

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/doubles/ghstate"
)

const widgetsRepo = "github.com/acme/widgets"

// callsWith lists the calls whose arguments start with a prefix.
func callsWith(t *testing.T, gh string, prefix ...string) [][]string {
	t.Helper()
	var out [][]string
	for _, call := range doubles.FakeGHCalls(t, gh) {
		if len(call.Args) >= len(prefix) && slices.Equal(call.Args[:len(prefix)], prefix) {
			out = append(out, call.Args)
		}
	}
	return out
}

// The log of what failed in a check is read with `gh run view --log-failed`, narrowed to the check's job, and only its
// end is kept (https://cli.github.com/manual/gh_run_view).
func TestTheLogOfAFailedCheckIsReadFromItsRunAndJob(t *testing.T) {
	forge, repo, gh, _ := fixture(t)
	doubles.EditFakeGH(t, gh, func(s *ghstate.State) {
		s.Runs = []ghstate.Run{{ID: 77, Repo: widgetsRepo, Head: "abc", Status: "completed", Conclusion: "failure", Log: "unit\tstep\tone\nunit\tstep\ttwo FAIL\n"}}
	})
	check := ledger.CICheckRef{Name: "unit", URL: "https://github.com/acme/widgets/actions/runs/77/job/9"}
	got, err := forge.FailedLog(t.Context(), repo, check, 1<<20)
	if err != nil || got != "unit\tstep\tone\nunit\tstep\ttwo FAIL\n" {
		t.Fatalf("the log reads %q: %v", got, err)
	}
	want := []string{"run", "view", "77", "--repo", repoArg, "--log-failed", "--job", "9"}
	if calls := callsWith(t, gh, "run", "view"); len(calls) != 1 || !slices.Equal(calls[0], want) {
		t.Fatalf("the calls read %v, want %v", calls, want)
	}
	tail, err := forge.FailedLog(t.Context(), repo, check, 12)
	if err != nil || tail != "two FAIL\n" && !strings.HasSuffix(tail, "FAIL\n") || len(tail) > 12 {
		t.Fatalf("the last 12 bytes read %q: %v", tail, err)
	}
}

func TestACheckOfAnotherCIHasNoLogAndNoCallIsMade(t *testing.T) {
	forge, repo, gh, _ := fixture(t)
	got, err := forge.FailedLog(t.Context(), repo, ledger.CICheckRef{Name: "build", URL: "https://ci.example/builds/9"}, 1<<20)
	if err != nil || got != "" || len(callsWith(t, gh, "run")) != 0 {
		t.Fatalf("a check of another CI reads %q, calls %v: %v", got, doubles.FakeGHCalls(t, gh), err)
	}
}

func TestAMissingRunIsAnErrorAndNotAnEmptyLog(t *testing.T) {
	forge, repo, _, _ := fixture(t)
	_, err := forge.FailedLog(t.Context(), repo, ledger.CICheckRef{Name: "unit", URL: "https://github.com/acme/widgets/actions/runs/404"}, 1024)
	var failed *ghError
	if !errors.As(err, &failed) {
		t.Fatalf("a run the forge does not hold answers %v", err)
	}
}

// A rerun starts the failed jobs of the runs it names with the endpoint ADR-0037 cites, and a run still going is the
// forge's refusal, which is a result and not an error (https://docs.github.com/en/rest/actions/workflow-runs).
func TestARerunStartsTheFailedJobsOfTheRunsItNames(t *testing.T) {
	forge, repo, gh, _ := fixture(t)
	ref, head, _ := opened(t, forge, repo, gh)
	doubles.EditFakeGH(t, gh, func(s *ghstate.State) {
		s.Runs = []ghstate.Run{
			{ID: 1, Repo: widgetsRepo, Head: head, Status: "completed", Conclusion: "timed_out"},
			{ID: 2, Repo: widgetsRepo, Head: head, Status: "in_progress"},
		}
	})
	result, err := forge.RerunChecks(t.Context(), ref, head, []string{"1", "2"})
	if err != nil || !slices.Equal(result.Runs, []string{"1"}) || !strings.Contains(result.Refused, "run 2: HTTP 403") {
		t.Fatalf("the rerun came to %+v: %v", result, err)
	}
	want := [][]string{
		{"api", "--include", "--hostname", "github.com", "--method", "POST", "repos/acme/widgets/actions/runs/1/rerun-failed-jobs"},
		{"api", "--include", "--hostname", "github.com", "--method", "POST", "repos/acme/widgets/actions/runs/2/rerun-failed-jobs"},
	}
	if got := callsWith(t, gh, "api", "--include", "--hostname", "github.com", "--method"); !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Fatalf("the calls read %v, want %v", got, want)
	}
	// A repeat finds the run going, and starts nothing twice.
	again, err := forge.RerunChecks(t.Context(), ref, head, []string{"1"})
	if err != nil || len(again.Runs) != 0 || again.Refused == "" {
		t.Fatalf("a repeat came to %+v: %v", again, err)
	}
	if state, err := ghstate.Read(gh); err != nil || state.FindRun(1).Reruns != 1 {
		t.Fatalf("the forge restarted run 1 %d times: %v", state.FindRun(1).Reruns, err)
	}
}

// With no run named, the rerun finds the head's runs that finished without passing and starts those.
func TestARerunWithNoRunNamedStartsEveryRunThatDidNotPass(t *testing.T) {
	forge, repo, gh, _ := fixture(t)
	ref, head, _ := opened(t, forge, repo, gh)
	doubles.EditFakeGH(t, gh, func(s *ghstate.State) {
		s.Runs = []ghstate.Run{
			{ID: 1, Repo: widgetsRepo, Head: head, Status: "completed", Conclusion: "failure"},
			{ID: 2, Repo: widgetsRepo, Head: head, Status: "completed", Conclusion: "success"},
			{ID: 3, Repo: widgetsRepo, Head: head, Status: "completed", Conclusion: "cancelled"},
			{ID: 4, Repo: widgetsRepo, Head: head, Status: "completed", Conclusion: "action_required"},
			{ID: 5, Repo: widgetsRepo, Head: "elsewhere", Status: "completed", Conclusion: "failure"},
			{ID: 6, Repo: widgetsRepo, Head: head, Status: "in_progress"},
		}
	})
	result, err := forge.RerunChecks(t.Context(), ref, head, nil)
	if err != nil || !slices.Equal(result.Runs, []string{"1", "3"}) || result.Refused != "" {
		t.Fatalf("the rerun came to %+v: %v", result, err)
	}
	doubles.EditFakeGH(t, gh, func(s *ghstate.State) { s.Runs = nil })
	none, err := forge.RerunChecks(t.Context(), ref, head, nil)
	if err != nil || len(none.Runs) != 0 || none.Refused == "" {
		t.Fatalf("a head with no run that failed came to %+v: %v", none, err)
	}
}

func TestARateLimitedRerunIsAnErrorAndNotARefusal(t *testing.T) {
	forge, repo, gh, _ := fixture(t)
	ref, head, _ := opened(t, forge, repo, gh)
	doubles.EditFakeGH(t, gh, func(s *ghstate.State) { s.RateLimit = &ghstate.RateLimit{Secondary: true, RetryAfter: 90} })
	_, err := forge.RerunChecks(t.Context(), ref, head, []string{"1"})
	var limited *ports.RateLimitedError
	if !errors.Is(err, ports.ErrForgeRateLimited) || !errors.As(err, &limited) || limited.Rate.RetryAfter.Seconds() != 90 {
		t.Fatalf("a rerun over the limit answers %v", err)
	}
}

func TestARunNumberIsNeverPassedOnAsAFlag(t *testing.T) {
	forge, repo, gh, _ := fixture(t)
	ref, head, _ := opened(t, forge, repo, gh)
	_, err := forge.RerunChecks(t.Context(), ref, head, []string{"--method=DELETE"})
	if err == nil || len(callsWith(t, gh, "api", "--include", "--hostname", "github.com", "--method")) != 0 {
		t.Fatalf("a run named by a flag was run: %v", err)
	}
}

// The classification tells a failure a rerun is for from a new one by when the run ended, so a query over several pull
// requests reads the completion and the conclusion as a read of one does.
func TestABatchedReadKeepsWhenAndHowARunEnded(t *testing.T) {
	forge, repo, gh, _ := fixture(t)
	ref, _, _ := opened(t, forge, repo, gh)
	const done = "2026-01-02T09:07:30Z"
	doubles.EditFakeGH(t, gh, func(s *ghstate.State) {
		pr := s.Find(1)
		pr.Checks = []ghstate.Check{{Typename: "CheckRun", Name: "unit", Status: "COMPLETED", Conclusion: "TIMED_OUT", StartedAt: "2026-01-02T09:00:00Z", CompletedAt: done}}
		pr.Required = []string{"unit"}
	})
	read, err := forge.ReadPullRequests(t.Context(), repo, []int64{ref.Number})
	if err != nil || len(read.Pulls[ref.Number].Checks) != 1 {
		t.Fatalf("the batched read answers %+v: %v", read, err)
	}
	if check := read.Pulls[ref.Number].Checks[0]; check.CompletedAt != done || check.Conclusion != "timed_out" || check.Class() != ledger.CIClassInfrastructure {
		t.Errorf("a batched read reads the check as %+v, class %s", check, check.Class())
	}
}

// The base's tip answers which checks failed on it, from the check runs of its branch.
func TestTheBaseTipsFailedChecksAreRead(t *testing.T) {
	forge, repo, gh, _ := fixture(t)
	doubles.EditFakeGH(t, gh, func(s *ghstate.State) {
		s.Bases = []ghstate.BaseCheck{
			{Repo: widgetsRepo, Branch: "release/1.2", Commit: "tip-1", Name: "unit", Conclusion: "FAILURE"},
			{Repo: widgetsRepo, Branch: "release/1.2", Commit: "tip-1", Name: "lint", Conclusion: "SUCCESS"},
			{Repo: widgetsRepo, Branch: "release/1.2", Commit: "tip-1", Name: "e2e", Conclusion: "TIMED_OUT"},
		}
	})
	read, err := forge.BaseChecks(t.Context(), repo, "release/1.2")
	if err != nil || read.Commit != "tip-1" || !slices.Equal(read.Failed, []string{"unit"}) {
		t.Fatalf("the base reads %+v: %v", read, err)
	}
	want := []string{"api", "--include", "--hostname", "github.com", "repos/acme/widgets/commits/release%2F1.2/check-runs?per_page=100"}
	if calls := callsWith(t, gh, "api"); len(calls) != 1 || !slices.Equal(calls[0], want) {
		t.Fatalf("the calls read %v, want %v", calls, want)
	}
}
