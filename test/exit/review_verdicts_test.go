package exit

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/contract"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/gittest"
)

const (
	failedReason = "The retry is built but never logged: work.txt has no log line."
	passedReason = "work.txt now logs each retry."
)

// reviewSays is a review lane that reports one verdict on the contract's one criterion, and ends.
func reviewSays(verdict, reasons string, citations ...string) string {
	cited, _ := json.Marshal(citations)
	return `{
  "source": "a review lane that judges the one criterion",
  "turns": [[
    {"init": true},
    {"mcp": "lane_report", "arguments": {"outcome": "completed", "verdicts": [{"criterion": "c1", "verdict": "` + verdict + `", "reasons": "` + reasons + `", "citations": ` + string(cited) + `}]}},
    {"emit": {"type": "result", "subtype": "success", "is_error": false, "total_cost_usd": 0.02, "usage": {"input_tokens": 3, "output_tokens": 5}}}
  ]]
}`
}

// playing replaces the lane the install's CLI double plays, for the next lane that starts.
func (in *install) playing(fixture string) {
	in.t.Helper()
	dir, _, _ := strings.Cut(in.path, string(os.PathListSeparator))
	if err := os.WriteFile(filepath.Join(dir, doubles.FixtureFile), []byte(fixture), 0o600); err != nil {
		in.t.Fatal(err)
	}
}

// ledgerEvents reads what the ledger holds so far. The stream does not end by itself, so the read stops
// after a moment, with every event it had by then.
func (in *install) ledgerEvents() []api.StreamEvent {
	in.t.Helper()
	ctx, cancel := context.WithTimeout(in.t.Context(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binaries.cli, "events", "--json")
	cmd.Env = in.env()
	if in.session != "" {
		cmd.Env = append(cmd.Env, authority.EnvMemberToken+"="+in.session)
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	_ = cmd.Run()
	var out []api.StreamEvent
	for line := range strings.Lines(stdout.String()) {
		var event api.StreamEvent
		if json.Unmarshal([]byte(line), &event) == nil && event.Type != "" {
			out = append(out, event)
		}
	}
	return out
}

// eventsOf lists the events of one type about one subject.
func (in *install) eventsOf(eventType, subject string) []api.StreamEvent {
	in.t.Helper()
	var out []api.StreamEvent
	for _, event := range in.ledgerEvents() {
		if event.Type == eventType && event.SubjectID == subject {
			out = append(out, event)
		}
	}
	return out
}

// openReviewOf is the review of a work order that has not ended, waiting for a lane.
func (in *install) openReviewOf(order string) workorders.WorkOrder {
	in.t.Helper()
	var found workorders.WorkOrder
	in.waitFor("a review of "+order, func() bool {
		var list api.WorkOrderList
		if err := json.Unmarshal([]byte(in.must("work", "list", "--json")), &list); err != nil {
			in.t.Fatal(err)
		}
		for _, row := range list.WorkOrders {
			if row.Kind == workorders.KindReview && row.Reviews == order && row.State == workorders.Ready {
				found = row
			}
		}
		return found.ID != ""
	})
	return found
}

// judge runs the open review of a work order on a lane of its own, as any claimed work runs.
func (in *install) judge(order, claim, lane, fixture string) workorders.WorkOrder {
	in.t.Helper()
	review := in.openReviewOf(order)
	in.playing(fixture)
	in.must("work", "claim", review.ID, "--worker", in.workers()[0].ID, "--claim-id", claim)
	in.must("lane", "start", review.ID, "--lane", lane)
	in.waitFor("the review to be done", func() bool { return in.show(review.ID).State == workorders.Done })
	return review
}

// TestAReviewsVerdictsSettleTheWorkAtTheMergeAuthority is FR-REVIEW-4 to FR-REVIEW-8 and FR-MERGE-3 on the
// real binary with real git. A change lane commits and completes, and its review fails the one criterion:
// the work goes back by rule, and the next attempt's first prompt carries what the review found. That
// attempt completes, a new review passes it, and at person_merges, the default, the work waits in Needs you
// with the verdict. Raised to review_approves, a second piece of work whose review passes every criterion is
// accepted by the review itself, the audit says so, and so is the first, which waited for a member.
func TestAReviewsVerdictsSettleTheWorkAtTheMergeAuthority(t *testing.T) {
	in := newInstall(t)
	in.aLaneThatCommits()
	repo := gittest.New(t)
	repo.Commit(".ropeshift/profile.toml", "[lifecycle]\nmax_restarts = 0\n")
	repo.Commit("first.md", contract.ContractText("first", "First", "Build the first."))
	repo.Commit("second.md", contract.ContractText("second", "Second", "Build the second."))

	d := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--source", repo.Dir)
	t.Cleanup(d.stop)
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool { return len(in.workers()) == 1 })
	ids := in.workOrders(2)
	first, second := ids["The work order of first"], ids["The work order of second"]
	in.must("work", "review", first)
	in.must("work", "review", second)

	// The first attempt completes, and its review fails the one criterion.
	in.startLane(first, "claim-1", "lane-1")
	in.waitFor("the first attempt's outcome", func() bool { return in.show(first).State == workorders.Acceptance })
	failed := in.judge(first, "claim-2", "review-1", reviewSays("fail", failedReason, "work.txt:1", "FR-LOG-2"))
	in.waitFor("the work to go back", func() bool { return in.show(first).State == workorders.Ready })
	if state := in.show(failed.ID).State; state != workorders.Done {
		t.Errorf("the review that found a fault reads %s", state)
	}
	if sent := in.eventsOf("work_order.rework", first); len(sent) != 1 || sent[0].ActorKind != "system" || sent[0].ActorID != "review" {
		t.Errorf("the work was not sent back once, by the review's rule: %+v", sent)
	}

	// The next attempt is told what the review found, in its first prompt.
	in.playing(commitsAFile)
	in.startLane(first, "claim-3", "lane-2")
	in.waitFor("the second attempt's outcome", func() bool {
		view := in.acceptance(first)
		return view.State == workorders.Acceptance && len(view.Reviews) == 2
	})
	prompts := in.firstPrompts(t)
	for _, want := range []string{"What the review found. Review " + failed.ID, failedReason, "FR-LOG-2", "work.txt:1", "Address each finding"} {
		if !strings.Contains(prompts, want) {
			t.Errorf("the second attempt was not told %q:\n%s", want, prompts)
		}
	}
	if strings.Count(prompts, "What the review found. Review") != 1 {
		t.Errorf("the finding reached lanes it was not meant for:\n%s", prompts)
	}

	// A new review passes it. At person_merges the work waits for a member, with the verdict beside it.
	in.judge(first, "claim-4", "review-2", reviewSays("pass", passedReason, "work.txt:2"))
	view := in.acceptance(first)
	if view.State != workorders.Acceptance || view.Authority != ledger.PersonMerges || view.Rounds != 2 || len(view.Verdicts) != 1 {
		t.Fatalf("the work reads %+v", view)
	}
	if got := view.Verdicts[0]; got.Verdict != workorders.VerdictPass || got.Reasons != passedReason || got.Review == failed.ID {
		t.Errorf("the verdict that counts reads %+v", got)
	}
	var needs api.NeedsYou
	if err := json.Unmarshal([]byte(in.must("view", "needs-you")), &needs); err != nil {
		t.Fatal(err)
	}
	var item *api.WorkNeed
	for _, entry := range needs.Items {
		if entry.Work != nil && entry.Work.Reason == api.ReasonAcceptance && entry.Work.WorkOrder != nil && entry.Work.WorkOrder.ID == first {
			item = entry.Work
		}
	}
	if item == nil || item.Standing == nil || len(item.Standing.Verdicts) != 1 || item.Standing.Verdicts[0].Verdict != workorders.VerdictPass {
		t.Fatalf("Needs you does not hold the work with its verdict: %+v", item)
	}
	if text := in.must("work", "show", first); !strings.Contains(text, "verdict\tc1\tpass\t"+passedReason) {
		t.Errorf("work show leaves out the verdict:\n%s", text)
	}

	// Raised to review_approves, work whose review passes every criterion needs no member.
	var repositories api.RepositoryList
	if err := json.Unmarshal([]byte(in.must("repository", "list", "--json")), &repositories); err != nil || len(repositories.Repositories) != 1 {
		t.Fatalf("repositories %+v: %v", repositories, err)
	}
	in.must("repository", "set", repositories.Repositories[0].ID, "--merge-authority", "review_approves")
	in.playing(commitsAFile)
	in.startLane(second, "claim-5", "lane-3")
	in.waitFor("the second work's outcome", func() bool { return in.show(second).State == workorders.Acceptance })
	approved := in.judge(second, "claim-6", "review-3", reviewSays("pass", passedReason, "work.txt:1"))
	in.waitFor("the review to accept the work", func() bool { return in.show(second).State == workorders.Done })
	if accepted := in.eventsOf("work_order.acceptance_passed", second); len(accepted) != 1 || accepted[0].ActorKind != "system" || accepted[0].ActorID != "review" {
		t.Errorf("the second work was not accepted once, by the review's rule: %+v", accepted)
	}
	if state := in.show(approved.ID).State; state != workorders.Done {
		t.Errorf("the review that approved reads %s", state)
	}
	// The tick reads the level whenever it settles waiting work, so the first work, which waited for a member
	// with every criterion passed, is accepted by the same rule now that the member has raised the level.
	in.waitFor("the review to accept what already waited", func() bool { return in.show(first).State == workorders.Done })
}
