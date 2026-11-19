package server_test

import (
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// aReviewsWorld is the detailed world with a repository whose changes land through pull requests at
// person_approves, and three changes in review: one the review could not judge, which waits for a member's
// acceptance; one whose review passed, whose pull request still has a check running and whose merge waits for the
// member's approval; and one whose merge the member approved and the worker has not reported.
func aReviewsWorld(t *testing.T) *apitest.Daemon {
	t.Helper()
	clock := apitest.NewClock()
	knowledge := fake.NewKnowledgeSource()
	apitest.Documents(knowledge)
	d := apitest.StartWith(t, apitest.Options{Clock: clock.Dialect, Knowledge: []ports.KnowledgeSource{knowledge}})
	w := apitest.NewWorld(t, d)
	project := apitest.Factory(w, clock)
	apitest.Populate(w, clock)
	apitest.Detail(w, clock)
	w.At = clock.Now

	w.Repository("repository-1", ledger.PersonApproves)
	target, binding := ledger.ReviewOnPullRequest, "github.com/acme/app"
	if _, err := d.Client.SetRepository(t.Context(), "repository-1", api.RepositoryChange{ReviewTarget: &target, ForgeBinding: &binding}); err != nil {
		t.Fatal(err)
	}

	shipped := func(id, title, head string, number int64, checks ...ledger.PullRequestCheck) {
		clock.Advance(time.Minute)
		lane := w.Running(id, title)
		clock.Advance(5 * time.Minute)
		lane.Finish(workorders.LaneResult{
			Report: workorders.Report{
				Outcome: workorders.OutcomeCompleted,
				Checks:  []workorders.CheckResult{{Name: "go test", Passed: true, Criterion: "c1"}},
			},
			Branch: "lane/lane-" + id, Head: head,
		})
		err := d.Store.Write(t.Context(), apitest.Scope, "test.pull_request", func(tx ports.WriteTx) error {
			if err := tx.Forge().SavePullRequest(ledger.WorkPullRequest{
				PullRequest: ledger.PullRequest{
					Number: number, URL: "https://forge.example/acme/app/pull/" + id, State: ledger.PullRequestOpen, Base: "main", Branch: "lane/lane-" + id,
					HeadCommit: head, Mergeable: "MERGEABLE", Checks: checks,
				},
				WorkOrder: id, Repository: "repository-1", Worker: apitest.WorldWorker,
			}); err != nil {
				return err
			}
			_, err := tx.Append(ledger.EventDraft{Type: "test.pull_request", Actor: ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"}, Subject: ledger.Subject{Kind: "work_order", ID: id}})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		clock.Advance(3 * time.Minute)
		w.Reviewed(id, workorders.CriterionVerdict{
			Criterion: "c1", Verdict: workorders.VerdictPass, Reasons: "The budget stops the retries after the third.", Citations: []string{"retry.go:21"},
		})
	}
	shipped("wo-ruled", "Trim the retry log", "cab3210000000000000000000000000000000000", 11,
		ledger.PullRequestCheck{Name: "CI result", State: ledger.CheckPassed, Required: true}, ledger.PullRequestCheck{Name: "e2e", State: ledger.CheckPending, Required: true})
	shipped("wo-merging", "Add the retry budget", "fed9870000000000000000000000000000000000", 12,
		ledger.PullRequestCheck{Name: "CI result", State: ledger.CheckPassed, Required: true}, ledger.PullRequestCheck{Name: "lint", State: ledger.CheckPassed})
	clock.Advance(time.Minute)
	if _, err := d.Client.Accept(t.Context(), "wo-merging"); err != nil {
		t.Fatal(err)
	}

	project.Change()
	return d
}

// TestGoldenReviews writes what the Reviews and Overview views read of work in review: the work that waits for a
// member, with its verdicts, its checks, its pull request, and the acceptance and merge requests a member decides,
// from a real ledger.
func TestGoldenReviews(t *testing.T) {
	t.Parallel()
	d := aReviewsWorld(t)
	for _, name := range []string{api.ViewNeedsYou, api.ViewCompleted} {
		holdsGolden(t, "reviews-"+name, steady(pretty(t, readAny(t, d, name))))
	}
	var accept, merge bool
	for _, item := range readView[api.NeedsYou](t, d, api.ViewNeedsYou, "").Items {
		accept = accept || (item.Work != nil && item.Work.WorkOrder != nil && item.Work.WorkOrder.ID == "wo-accept" && item.Work.Reason == api.ReasonAcceptance)
		merge = merge || (item.Approval != nil && item.Approval.Kind == authority.KindMerge && item.Approval.WorkOrder == "wo-ruled")
	}
	if !accept || !merge {
		t.Errorf("the world holds an acceptance request %v and a merge request %v for the work in review", accept, merge)
	}
	states := map[string]*api.CompletedWork{}
	completed := readView[api.Completed](t, d, api.ViewCompleted, "")
	for i, item := range completed.Items {
		states[item.WorkOrder.ID] = &completed.Items[i]
	}
	for id, want := range map[string]workorders.State{"wo-accept": workorders.Acceptance, "wo-ruled": workorders.Integrating, "wo-merging": workorders.Integrating} {
		if states[id] == nil || states[id].WorkOrder.State != want {
			t.Errorf("%s reads %+v, want %s", id, states[id], want)
		}
	}
	for _, id := range []string{"wo-ruled", "wo-merging"} {
		if states[id] == nil || states[id].Standing.PullRequest == nil || len(states[id].Standing.PullRequest.Checks) == 0 {
			t.Errorf("%s has no pull request with checks: %+v", id, states[id])
		}
	}
}
