package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// reviewOf finds the one review of a work order.
func (r *laneRig) reviewOf(id string) workorders.WorkOrder {
	r.t.Helper()
	var found []workorders.WorkOrder
	err := r.store.Read(r.t.Context(), scope, func(tx ports.ReadTx) error {
		rows, err := tx.WorkOrders().List()
		for _, row := range rows {
			if row.Kind == workorders.KindReview && row.Reviews == id {
				found = append(found, row)
			}
		}
		return err
	})
	if err != nil || len(found) != 1 {
		r.t.Fatalf("reviews of %s: %+v, %v", id, found, err)
	}
	return found[0]
}

// judges is a review lane reporting that it completed, with a verdict on the criteria the rig's contract holds.
func (r *laneRig) judges(verdicts ...workorders.CriterionVerdict) fake.DriverStep {
	return fake.DriverStep{Do: func(spec ports.SessionSpec) {
		raw, err := json.Marshal(workorders.Report{Outcome: workorders.OutcomeCompleted, Verdicts: verdicts})
		if err != nil {
			r.t.Error(err)
			return
		}
		if _, err := r.journal.SaveReport(context.Background(), spec.Lane, raw); err != nil {
			r.t.Error(err)
		}
	}}
}

func (r *laneRig) state(id string) workorders.State {
	r.t.Helper()
	var state workorders.State
	err := r.store.Read(r.t.Context(), scope, func(tx ports.ReadTx) error {
		row, err := tx.WorkOrders().Get(id)
		state = row.State
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return state
}

// A change lane commits a file and completes. Its review is claimed, dispatched, and run by the same
// worker: the review lane stands in a worktree at the commit under review with no branch, reads the diff
// in its context folder, runs in a read-only session, and when it ends nothing is left of its worktree and
// no branch exists or moved for it (FR-REVIEW-1, FR-REVIEW-2).
func TestAReviewLaneReadsTheDiffAtTheReviewedCommitAndLeavesNoBranch(t *testing.T) {
	t.Parallel()
	base := newRig(t)
	worktrees := fake.NewWorktrees()
	start := worktrees.Grow("/repository", "main")
	base.worktrees, base.repository = worktrees, "repo-1"
	base.starts = &control.Starts{Bases: worktrees, Checkout: func(context.Context, string) (string, error) { return "/repository", nil }}
	r := newLaneRigFor(t, base)

	var head string
	commits := fake.DriverStep{Do: func(spec ports.SessionSpec) {
		worktrees.Point("/repository", "lane/lane-1", start)
		head = worktrees.Touch("/repository", "lane/lane-1", "retry.go")
		worktrees.MoveHead(spec.Dir, head)
	}}
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, commits, r.reports(workorders.OutcomeCompleted), completed(0.25)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r.state(r.id) != workorders.Acceptance || head == "" {
		t.Fatalf("the work reads %s after its lane, with the commit %q", r.state(r.id), head)
	}
	review := r.reviewOf(r.id)
	branchesBefore := worktrees.Branches("/repository")

	ctx := t.Context()
	leases := control.NewLeases(r.store)
	worker := r.subject.Auth().WorkerID
	if _, err := leases.Claim(ctx, scope, workorders.ClaimRequest{WorkOrder: review.ID, ClaimID: "claim-review", Worker: worker, TTLMs: 60_000}, member); err != nil {
		t.Fatal(err)
	}
	if _, err := leases.Dispatch(ctx, scope, review.ID, "review-1", member); err != nil {
		t.Fatal(err)
	}
	// The repository's profile bounds the review rounds, and the review's end carries the bound with its
	// verdicts, because the control plane reads no profile.
	r.config.Provisioner.Profiles.(*fake.ProfileSource).Put("/repository", "[review]\nmax_review_rounds = 2\n")
	r.driver.Play(fake.DriverScript{
		SessionID: "session-review", Model: "resolved-model",
		Turns: [][]fake.DriverStep{{{Bound: true}, r.judges(workorders.CriterionVerdict{
			Criterion: "c1", Verdict: workorders.VerdictPass, Reasons: "retry.go adds the budget", Citations: []string{"retry.go:1"},
		}), completed(0.1)}},
	})
	if err := r.subject.Converge(ctx); err != nil {
		t.Fatal(err)
	}

	created := worktrees.Created()
	if len(created) != 2 {
		t.Fatalf("the worker made %d worktrees: %+v", len(created), created)
	}
	spec := created[1]
	if !spec.Detached || spec.Base != head || spec.Branch != "" || spec.Repository != "/repository" || !strings.HasSuffix(spec.Path, "/review-1") {
		t.Errorf("the review's worktree was asked for as %+v, want detached at %s", spec, head)
	}
	diff, ok := worktrees.ContextFile(spec.Path, "review.diff")
	if !ok || !strings.Contains(diff, "retry.go") {
		t.Errorf("the diff reads %q", diff)
	}
	if files, ok := worktrees.ContextFile(spec.Path, "review-files.txt"); !ok || files != "retry.go\n" {
		t.Errorf("the files read %q", files)
	}

	sessions := r.driver.Specs()
	if len(sessions) != 2 {
		t.Fatalf("%d sessions started", len(sessions))
	}
	session := sessions[1]
	if !session.ReadOnly || session.Dir != spec.Path || sessions[0].ReadOnly {
		t.Errorf("the review's session reads %+v, and the first lane's read-only is %v", session, sessions[0].ReadOnly)
	}
	for _, want := range []string{head, start, lanes.ReviewDiffFile, lanes.ReviewFilesFile, "work order " + r.id} {
		if !strings.Contains(session.Brief, want) {
			t.Errorf("the brief leaves out %q:\n%s", want, session.Brief)
		}
	}

	if r.state(review.ID) != workorders.Done || r.state(r.id) != workorders.Acceptance {
		t.Errorf("the review reads %s and the work %s", r.state(review.ID), r.state(r.id))
	}
	// The verdict it reported is on record, bound to the commit it read and to its own lane.
	err := r.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		verdicts, err := tx.Evidence().Verdicts(r.id)
		if err != nil {
			return err
		}
		if len(verdicts) != 1 || verdicts[0].Criterion != "c1" || verdicts[0].Verdict != workorders.VerdictPass ||
			verdicts[0].Commit != head || verdicts[0].Lane != "review-1" || verdicts[0].Review != review.ID || verdicts[0].MaxRounds != 2 {
			t.Errorf("the verdicts on record read %+v, want one pass for c1 at %s from review-1, bounded at 2 rounds", verdicts, head)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The ledger asks the worker to take the worktree away, and the next pass does.
	if err := r.subject.Converge(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := worktrees.Inspect(ctx, spec.Path); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("the review's worktree is still there: %v", err)
	}
	if after := worktrees.Branches("/repository"); strings.Join(after, " ") != strings.Join(branchesBefore, " ") {
		t.Errorf("the repository had branches %v and has %v", branchesBefore, after)
	}
	if _, err := worktrees.Inspect(ctx, created[0].Path); err != nil {
		t.Errorf("the reviewed lane's worktree went with the review's: %v", err)
	}
}
