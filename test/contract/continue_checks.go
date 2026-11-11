package contract

import (
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
)

// ContinueChecks hold a work order that finishes an in-flight branch (ADR-0023, FR-ADOPT-7): its lane starts
// from the branch's tip on a branch of its own, its base commit is where the branch left the work order's
// base, the branch itself never moves, and a branch that is gone holds the work with a reason.
var ContinueChecks = []Check{
	{"CB1", "work that continues a branch starts from its tip and records the branch's merge base with the base as its base commit", checkContinuedWorkStartsFromTheBranch},
	{"CB2", "work that continues a branch nobody holds waits with a reason, and starts once the branch is there", checkAContinuedBranchThatIsGoneHoldsTheWork},
	{"CB3", "intake carries continues to the work order, and reports a work order whose branch its repository does not hold", checkIntakeReportsAMissingContinuedBranch},
	{"CB4", "a worker is told the branch its lane continues and the tip its branch begins at, and the lane says so", checkWorkerIsToldTheContinuedBranch},
}

// continuing records ready work that finishes a branch.
func (w *startWorld) continuing(repository, branch string) (string, error) {
	ops := w.r.NewOps(w.s)
	made, err := ops.Record(w.r.Ctx, w.r.Scope, control.LedgerWork{
		Order: workorders.WorkOrder{
			ID: w.r.ID("wo"), Kind: workorders.KindChange, Title: "Finish it", RepositoryID: repository, Continues: branch,
			MaxAttempts: defaultMaxAttempts, Needs: map[string]string{"driver": "claude-code"},
		},
		Statement: "Finish the branch.",
		Criteria:  criteriaOf(criterionOne),
	}, w.r.Actor)
	if err != nil {
		return "", err
	}
	result, err := ops.Pass(w.r.Ctx, w.r.Scope, made.ID, w.r.Actor)
	if err != nil {
		return "", err
	}
	if result.State != workorders.Ready {
		return "", Violatedf("ready work reads %+v", result)
	}
	return made.ID, nil
}

// diverged grows a branch off main's first commit and then main past it, and returns where they parted
// and each tip.
func (w *startWorld) diverged(branch string) (fork, tip, base string) {
	path := w.path(startRepository)
	fork = w.git.Grow(path, "main")
	w.git.Point(path, branch, fork)
	tip = w.git.Grow(path, branch)
	base = w.git.Grow(path, "main")
	return fork, tip, base
}

func checkContinuedWorkStartsFromTheBranch(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	fork, tip, _ := w.diverged("feature/retry")
	id, err := w.continuing(startRepository, "feature/retry")
	if err != nil {
		return err
	}
	if row, err := r.WorkOrder(s, id); err != nil || row.Continues != "feature/retry" {
		return Violatedf("the work order reads %+v: %v", row, err)
	}
	claim, err := w.claim(id)
	if err != nil {
		return err
	}
	if claim.ContinuesRef != "feature/retry" || claim.ContinuesCommit != tip || claim.BaseRef != "main" || claim.BaseCommit != fork {
		return Violatedf("the claim continues %s at %s from base %s at %s; want feature/retry at %s from main at %s",
			claim.ContinuesRef, claim.ContinuesCommit, claim.BaseRef, claim.BaseCommit, tip, fork)
	}
	if !slices.Contains(w.git.Branches(w.path(startRepository)), "feature/retry") {
		return Violatedf("the continued branch went")
	}
	// With a remote, the branch the remote holds is the one continued.
	w.git.UseRemote("origin")
	again, err := w.continuing(startRepository, "feature/retry")
	if err != nil {
		return err
	}
	if claim, err := w.claim(again); err != nil || claim.ContinuesRef != "origin/feature/retry" || claim.ContinuesCommit != tip || claim.BaseRef != "origin/main" {
		return Violatedf("with a remote the claim reads %+v: %v", claim, err)
	}
	return nil
}

func checkAContinuedBranchThatIsGoneHoldsTheWork(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	path := w.path(startRepository)
	main := w.git.Grow(path, "main")
	id, err := w.continuing(startRepository, "feature/gone")
	if err != nil {
		return err
	}
	hold, err := w.refused(id, workorders.HoldContinuedBranchMissing)
	if err != nil {
		return err
	}
	if !strings.Contains(hold.Detail, "feature/gone") {
		return Violatedf("the reason does not name the branch: %+v", hold)
	}
	if _, err := w.leases.RefreshStarts(r.Ctx, r.Scope, r.Actor); err != nil {
		return err
	}
	if row, err := r.WorkOrder(s, id); err != nil || row.StartHeld == nil || row.StartHeld.Reason != workorders.HoldContinuedBranchMissing {
		return Violatedf("a tick while the branch is gone left %+v: %v", row.StartHeld, err)
	}
	w.git.Point(path, "feature/gone", main)
	w.git.Grow(path, "feature/gone")
	if _, err := w.leases.RefreshStarts(r.Ctx, r.Scope, r.Actor); err != nil {
		return err
	}
	if row, err := r.WorkOrder(s, id); err != nil || row.StartHeld != nil {
		return Violatedf("once the branch is there the work reads %+v: %v", row.StartHeld, err)
	}
	if _, err := w.claim(id); err != nil {
		return err
	}
	return nil
}

func checkIntakeReportsAMissingContinuedBranch(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	// The fake names the repository holding its files "memory".
	held := w.path("memory")
	w.git.Grow(held, "main")
	w.git.Grow(held, "feature/retry")
	continues := func(id, branch string) string {
		return strings.Replace(ContractText(id, id, "Finish it."), `size = "small"`, "size = \"small\"\ncontinues = \""+branch+"\"", 1)
	}
	source := fake.NewWorkSource()
	source.Write("kept.md", []byte(continues("kept", "feature/retry")))
	source.Write("gone.md", []byte(continues("gone", "feature/gone")))
	report, err := control.NewIntake(s).Checking(w.starts).Poll(r.Ctx, r.Scope, source)
	if err != nil {
		return err
	}
	if err := problemsMention(report.Problems, "gone.md", "gone-order", "feature/gone"); err != nil {
		return err
	}
	if slices.ContainsFunc(report.Problems, func(p planning.SourceProblem) bool { return p.Path == "kept.md" }) {
		return Violatedf("a branch the repository holds is a problem: %+v", report.Problems)
	}
	for title, branch := range map[string]string{"The work order of kept": "feature/retry", "The work order of gone": "feature/gone"} {
		if order, err := r.orderByTitle(s, title); err != nil || order.Continues != branch {
			return Violatedf("%s continues %q, want %q: %v", title, order.Continues, branch, err)
		}
	}
	if err := r.CheckProjection(s); err != nil {
		return err
	}
	if err := r.NewOps(s).Rebuild(r.Ctx, r.Scope, r.Actor); err != nil {
		return err
	}
	if order, err := r.orderByTitle(s, "The work order of kept"); err != nil || order.Continues != "feature/retry" {
		return Violatedf("a rebuild left %+v: %v", order, err)
	}
	return nil
}

func checkWorkerIsToldTheContinuedBranch(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	fork, tip, _ := w.diverged("feature/retry")
	plane := control.NewWorkers(s, r.Scope, control.WorkerOptions{HeartbeatMs: statusHeartbeatMs, LeaseTTLMs: TTLMs, Starts: w.starts})
	token, err := plane.CreateJoinToken(r.Ctx, ledger.Actor{Kind: ledger.ActorMember, ID: "member"})
	if err != nil {
		return err
	}
	worker := r.ID("worker")
	credential, err := plane.Register(r.Ctx, ledger.Registration{JoinToken: token, WorkerID: worker, Name: "w"})
	if err != nil {
		return err
	}
	auth := ledger.WorkerAuth{WorkerID: worker, Credential: credential.Credential}
	if _, err := plane.OpenSession(r.Ctx, ledger.SessionRequest{Auth: auth, Protocol: ledger.ProtocolVersion, Manifest: UsableManifest}); err != nil {
		return err
	}
	id, err := w.continuing(startRepository, "feature/retry")
	if err != nil {
		return err
	}
	grant, err := plane.Claim(r.Ctx, auth, workorders.ClaimRequest{WorkOrder: id, ClaimID: r.ID("claim"), TTLMs: TTLMs})
	if err != nil {
		return err
	}
	lane := r.ID("lane")
	if _, err := w.leases.Dispatch(r.Ctx, r.Scope, id, lane, r.Actor); err != nil {
		return err
	}
	if _, err := r.NewOps(s).StartLane(r.Ctx, r.Scope, grant.Lease, lane, r.Actor); err != nil {
		return err
	}
	desired, err := plane.Desired(r.Ctx, ledger.DesiredRequest{Auth: auth})
	if err != nil {
		return err
	}
	if len(desired.Leases) != 1 {
		return Violatedf("the worker is told %+v", desired.Leases)
	}
	lease := desired.Leases[0]
	if lease.Continues != "feature/retry" || lease.ContinuesRef != "feature/retry" || lease.ContinuesCommit != tip || lease.BaseCommit != fork {
		return Violatedf("the worker is told to continue %q (%s at %s) from base %s", lease.Continues, lease.ContinuesRef, lease.ContinuesCommit, lease.BaseCommit)
	}
	view, err := plane.Lane(r.Ctx, lane)
	if err != nil || view.ContinuesRef != "feature/retry" || view.ContinuesCommit != tip || view.BaseCommit != fork {
		return Violatedf("the lane reads %+v: %v", view, err)
	}
	return nil
}
