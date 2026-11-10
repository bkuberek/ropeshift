package contract

import (
	"context"
	"errors"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// StartChecks hold where a lane begins: from the base the work order or its repository names, fetched
// first, and never from the checkout's HEAD. Work that depends on other work in the same repository
// starts from that work once it is merged, or from the accepted commit when the repository says so, and
// until then it waits with a reason a person can read (FR-BASE-1 to FR-BASE-4).
var StartChecks = []Check{
	{"SB1", "a claim starts its lane from the tip of the repository's base branch, and records the ref and the commit", checkClaimRecordsTheBaseTip},
	{"SB2", "a member's base for the repository, and a work order's own, decide the branch a lane starts from", checkBasesOverride},
	{"SB3", "a base that cannot be read holds the claim with a reason, once, and the next claim after it can be read starts", checkUnreadableBaseHoldsTheClaim},
	{"SB4", "work that depends on unmerged work waits for the merge, naming the dependency, and starts from the base once it is in", checkDependentWaitsForTheMerge},
	{"SB5", "starting from accepted work takes the dependency that holds the others, and waits when none does", checkAcceptedStartsFromTheDependency},
	{"SB6", "a dependency in another repository, or one that left no commit, does not move the start", checkOtherDependenciesDoNotMoveTheStart},
	{"SB7", "a tick clears a hold the merge resolved, and says nothing more while it stands", checkTickClearsResolvedHolds},
	{"SB8", "a woken lane keeps the base it started from, whatever the branch holds now", checkWokenLaneKeepsItsBase},
	{"SB10", "a worker is told the base its lane starts from, and a client reads it on the lane", checkWorkerAndLaneViewCarryTheBase},
	{"SB9", "a hold replays from the log, and a granted claim clears it", checkHoldReplays},
}

const startRepository = "repo-b"

// startWorld is a control plane that resolves starts over a fake repository.
type startWorld struct {
	r      *Rig
	s      ports.Store
	git    *fake.Worktrees
	starts *control.Starts
	leases *control.Leases
}

func (r *Rig) startWorld(s ports.Store) *startWorld {
	git := fake.NewWorktrees()
	starts := &control.Starts{Worktrees: git, Checkout: func(_ context.Context, repository string) (string, error) {
		return "/work/" + repository, nil
	}}
	return &startWorld{r: r, s: s, git: git, starts: starts, leases: control.NewLeases(s).Starting(starts)}
}

func (w *startWorld) path(repository string) string { return "/work/" + repository }

func (w *startWorld) repository(settings func(*control.RepositoryChange)) error {
	if _, err := w.r.detect(w.s, startRepository, "", "main"); err != nil {
		return err
	}
	var change control.RepositoryChange
	settings(&change)
	_, err := w.r.change(w.s, startRepository, change, w.r.Actor)
	return err
}

// order records ready work in a repository, that depends on other work when it names some.
func (w *startWorld) order(repository, base string, dependsOn ...string) (string, error) {
	ops := w.r.NewOps(w.s)
	made, err := ops.Record(w.r.Ctx, w.r.Scope, control.LedgerWork{
		Order: workorders.WorkOrder{
			ID: w.r.ID("wo"), Kind: workorders.KindChange, Title: "Build on it", RepositoryID: repository, Base: base,
			MaxAttempts: defaultMaxAttempts, Needs: map[string]string{"driver": "claude-code"},
		},
		Statement: "Do the work.",
		Criteria:  criteriaOf(criterionOne),
		DependsOn: dependsOn,
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

// accepted records work in a repository that a lane finished at a commit and a member accepted.
func (w *startWorld) accepted(repository, head string, dependsOn ...string) (string, error) {
	id, err := w.order(repository, "", dependsOn...)
	if err != nil {
		return "", err
	}
	holder, err := w.r.Running(w.s, id, "a")
	if err != nil {
		return "", err
	}
	result := LaneFinished(head, LaneCheck(criterionOne, true))
	if head == "" {
		result = laneResult(workorders.OutcomeCompleted, "")
	}
	result.Branch = "lane/" + id
	if head == "" {
		result.Branch = ""
	}
	if err := w.r.NewOps(w.s).EndLane(w.r.Ctx, w.r.Scope, holder, result, w.r.worker("a")); err != nil {
		return "", err
	}
	if state, err := w.r.NewOps(w.s).Accept(w.r.Ctx, w.r.Scope, id, w.r.Actor); err != nil || state != workorders.Done {
		return "", Violatedf("accepting %s left it in %s: %v", id, state, err)
	}
	return id, nil
}

func (w *startWorld) claim(id string) (workorders.Claim, error) {
	request := w.r.Request(id, "a")
	if _, err := w.leases.Claim(w.r.Ctx, w.r.Scope, request, w.r.worker("a")); err != nil {
		return workorders.Claim{}, err
	}
	return w.claimOf(request.ClaimID)
}

func (w *startWorld) claimOf(id string) (workorders.Claim, error) {
	var claim workorders.Claim
	err := w.s.Read(w.r.Ctx, w.r.Scope, func(tx ports.ReadTx) error {
		var err error
		claim, err = tx.Claims().Find(id)
		return err
	})
	return claim, err
}

// holdOf returns the hold a refused claim named.
func holdOf(err error) (workorders.StartHold, bool) {
	var hold workorders.StartHold
	return hold, errors.As(err, &hold)
}

func (w *startWorld) refused(id string, reason workorders.HoldReason) (workorders.StartHold, error) {
	request := w.r.Request(id, "a")
	_, err := w.leases.Claim(w.r.Ctx, w.r.Scope, request, w.r.worker("a"))
	hold, ok := holdOf(err)
	switch {
	case !ok:
		return hold, Violatedf("claiming work that must wait returned %v", err)
	case !errors.Is(err, workorders.ErrIneligible):
		return hold, Violatedf("the refusal is not a refusal by policy: %v", err)
	case hold.Reason != reason:
		return hold, Violatedf("the hold reads %+v, want %s", hold, reason)
	}
	if _, err := w.claimOf(request.ClaimID); !errors.Is(err, ports.ErrNotFound) {
		return hold, Violatedf("a refused claim left a record: %v", err)
	}
	row, err := w.r.WorkOrder(w.s, id)
	if err != nil {
		return hold, err
	}
	if row.State != workorders.Ready || row.StartHeld == nil || *row.StartHeld != hold {
		return hold, Violatedf("the work order reads %s with hold %+v, want ready with %+v", row.State, row.StartHeld, hold)
	}
	return hold, nil
}

func (w *startWorld) eventsOf(id, kind string) (int, error) {
	var count int
	err := w.s.Read(w.r.Ctx, w.r.Scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: workorders.SubjectKind, ID: id})
		for _, e := range events {
			if e.Type == kind {
				count++
			}
		}
		return err
	})
	return count, err
}

func criteriaOf(ids ...string) []planning.Criterion {
	var out []planning.Criterion
	for _, id := range ids {
		out = append(out, planning.Criterion{ID: id, Text: "It holds: " + id})
	}
	return out
}

func checkClaimRecordsTheBaseTip(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	tip := w.git.Grow(w.path(startRepository), "main")
	first, err := w.order(startRepository, "")
	if err != nil {
		return err
	}
	claim, err := w.claim(first)
	if err != nil {
		return err
	}
	if claim.BaseRef != "main" || claim.BaseCommit != tip {
		return Violatedf("the claim reads base %s at %s, want main at %s", claim.BaseRef, claim.BaseCommit, tip)
	}
	// The base moves, and the next lane starts from where it is now.
	moved := w.git.Grow(w.path(startRepository), "main")
	second, err := w.order(startRepository, "")
	if err != nil {
		return err
	}
	if claim, err = w.claim(second); err != nil || claim.BaseCommit != moved {
		return Violatedf("after the base moved the claim reads %s: %v", claim.BaseCommit, err)
	}
	// The first lane's claim kept what it started from.
	if again, err := w.claimOf(claim.ID); err != nil || again.BaseCommit != moved {
		return Violatedf("the claim changed: %+v: %v", again, err)
	}
	return nil
}

func checkBasesOverride(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	path := w.path(startRepository)
	w.git.Grow(path, "main")
	release := w.git.Grow(path, "release/1.2")
	hotfix := w.git.Grow(path, "hotfix")
	if err := w.repository(func(c *control.RepositoryChange) { c.Base = text("release/1.2") }); err != nil {
		return err
	}
	plain, err := w.order(startRepository, "")
	if err != nil {
		return err
	}
	if claim, err := w.claim(plain); err != nil || claim.BaseRef != "release/1.2" || claim.BaseCommit != release {
		return Violatedf("work that names no base reads %+v: %v", claim, err)
	}
	named, err := w.order(startRepository, "hotfix")
	if err != nil {
		return err
	}
	if claim, err := w.claim(named); err != nil || claim.BaseRef != "hotfix" || claim.BaseCommit != hotfix {
		return Violatedf("work that names its base reads %+v: %v", claim, err)
	}
	return nil
}

func checkUnreadableBaseHoldsTheClaim(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	path := w.path(startRepository)
	tip := w.git.Grow(path, "main")
	w.git.UseRemote("origin")
	w.git.FailFetch(errors.New("no route to the remote"))
	id, err := w.order(startRepository, "")
	if err != nil {
		return err
	}
	for range 3 {
		hold, err := w.refused(id, workorders.HoldBaseUnavailable)
		if err != nil {
			return err
		}
		if !strings.Contains(hold.Detail, "no route to the remote") {
			return Violatedf("the reason hides why: %q", hold.Detail)
		}
	}
	if held, err := w.eventsOf(id, workorders.EventStartHeld); err != nil || held != 1 {
		return Violatedf("three refusals for one reason recorded %d events: %v", held, err)
	}
	// A different reason is a new fact.
	missing, err := w.order(startRepository, "no-such-branch")
	if err != nil {
		return err
	}
	w.git.FailFetch(nil)
	if _, err := w.refused(missing, workorders.HoldBaseUnavailable); err != nil {
		return err
	}
	// The remote answers, and the same work starts from what it holds, with the hold gone.
	claim, err := w.claim(id)
	if err != nil {
		return err
	}
	if claim.BaseRef != "origin/main" || claim.BaseCommit != tip {
		return Violatedf("the claim reads base %s at %s", claim.BaseRef, claim.BaseCommit)
	}
	row, err := r.WorkOrder(s, id)
	if err != nil || row.StartHeld != nil || row.State != workorders.Claimed {
		return Violatedf("a granted claim left %s with hold %+v: %v", row.State, row.StartHeld, err)
	}
	return r.CheckProjection(s)
}

func checkDependentWaitsForTheMerge(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	path := w.path(startRepository)
	tip := w.git.Grow(path, "main")
	work := w.git.Add()
	dependency, err := w.accepted(startRepository, work)
	if err != nil {
		return err
	}
	id, err := w.order(startRepository, "", dependency)
	if err != nil {
		return err
	}
	hold, err := w.refused(id, workorders.HoldWaitingForMerge)
	if err != nil {
		return err
	}
	if hold.Dependency != dependency {
		return Violatedf("the hold names %q, want the dependency %s", hold.Dependency, dependency)
	}
	merged := w.git.Add(tip, work)
	w.git.Point(path, "main", merged)
	claim, err := w.claim(id)
	if err != nil {
		return err
	}
	if claim.BaseRef != "main" || claim.BaseCommit != merged {
		return Violatedf("after the merge the claim reads base %s at %s, want main at %s", claim.BaseRef, claim.BaseCommit, merged)
	}
	return nil
}

func checkAcceptedStartsFromTheDependency(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	path := w.path(startRepository)
	w.git.Grow(path, "main")
	accepted := ledger.StartFromAccepted
	if err := w.repository(func(c *control.RepositoryChange) { c.StartFrom = &accepted }); err != nil {
		return err
	}
	first := w.git.Add()
	second := w.git.Add(first)
	one, err := w.accepted(startRepository, first)
	if err != nil {
		return err
	}
	// One dependency: its commit.
	solo, err := w.order(startRepository, "", one)
	if err != nil {
		return err
	}
	if claim, err := w.claim(solo); err != nil || claim.BaseCommit != first || claim.BaseRef != "lane/"+one {
		return Violatedf("work on one accepted dependency reads %+v: %v", claim, err)
	}
	// Two that build on each other: the one that holds the other.
	two, err := w.accepted(startRepository, second, one)
	if err != nil {
		return err
	}
	both, err := w.order(startRepository, "", one, two)
	if err != nil {
		return err
	}
	if claim, err := w.claim(both); err != nil || claim.BaseCommit != second || claim.BaseRef != "lane/"+two {
		return Violatedf("work on a chain reads %+v: %v", claim, err)
	}
	// Two that diverged: none holds the other.
	left, right := w.git.Add(), w.git.Add()
	a, err := w.accepted(startRepository, left)
	if err != nil {
		return err
	}
	b, err := w.accepted(startRepository, right)
	if err != nil {
		return err
	}
	apart, err := w.order(startRepository, "", a, b)
	if err != nil {
		return err
	}
	hold, err := w.refused(apart, workorders.HoldDependenciesDiverge)
	if err != nil {
		return err
	}
	if !strings.Contains(hold.Detail, a) || !strings.Contains(hold.Detail, b) {
		return Violatedf("the reason names neither dependency: %q", hold.Detail)
	}
	return nil
}

func checkOtherDependenciesDoNotMoveTheStart(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	path := w.path(startRepository)
	tip := w.git.Grow(path, "main")
	// A commit the repository does not hold stands for work in another repository, which this one
	// cannot test for.
	elsewhere, err := w.accepted("repo-c", "elsewhere-commit")
	if err != nil {
		return err
	}
	nothing, err := w.accepted(startRepository, "")
	if err != nil {
		return err
	}
	id, err := w.order(startRepository, "", elsewhere, nothing)
	if err != nil {
		return err
	}
	claim, err := w.claim(id)
	if err != nil || claim.BaseCommit != tip || claim.BaseRef != "main" {
		return Violatedf("work whose dependencies do not count reads %+v: %v", claim, err)
	}
	return nil
}

func checkTickClearsResolvedHolds(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	path := w.path(startRepository)
	tip := w.git.Grow(path, "main")
	work := w.git.Add()
	dependency, err := w.accepted(startRepository, work)
	if err != nil {
		return err
	}
	id, err := w.order(startRepository, "", dependency)
	if err != nil {
		return err
	}
	tick := ledger.Actor{Kind: ledger.ActorSystem, ID: "intake-tick"}
	// The tick tells a person what a ready work order waits for before anyone claims it.
	if changed, err := w.leases.RefreshStarts(r.Ctx, r.Scope, tick); err != nil || !slices.Equal(changed, []string{id}) {
		return Violatedf("the first tick changed %v: %v", changed, err)
	}
	row, err := r.WorkOrder(s, id)
	if err != nil || row.StartHeld == nil || row.StartHeld.Reason != workorders.HoldWaitingForMerge || row.StartHeld.Dependency != dependency {
		return Violatedf("the tick left %+v: %v", row.StartHeld, err)
	}
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if changed, err := w.leases.RefreshStarts(r.Ctx, r.Scope, tick); err != nil || len(changed) != 0 {
		return Violatedf("a tick that found the same reason changed %v: %v", changed, err)
	}
	if after, err := r.Revision(s, r.Scope); err != nil || after != before {
		return Violatedf("a tick that found the same reason moved the ledger from %d to %d: %v", before, after, err)
	}
	w.git.Point(path, "main", w.git.Add(tip, work))
	if changed, err := w.leases.RefreshStarts(r.Ctx, r.Scope, tick); err != nil || !slices.Equal(changed, []string{id}) {
		return Violatedf("the tick after the merge changed %v: %v", changed, err)
	}
	row, err = r.WorkOrder(s, id)
	if err != nil || row.StartHeld != nil || row.State != workorders.Ready {
		return Violatedf("after the merge the tick left %s with %+v: %v", row.State, row.StartHeld, err)
	}
	if resolved, err := w.eventsOf(id, workorders.EventStartResolved); err != nil || resolved != 1 {
		return Violatedf("the tick recorded %d resolutions: %v", resolved, err)
	}
	return r.CheckProjection(s)
}

func checkWokenLaneKeepsItsBase(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	path := w.path(startRepository)
	tip := w.git.Grow(path, "main")
	id, err := w.order(startRepository, "")
	if err != nil {
		return err
	}
	request := r.Request(id, "a")
	grant, err := w.leases.Claim(r.Ctx, r.Scope, request, r.worker("a"))
	if err != nil {
		return err
	}
	lane := r.ID("lane")
	if _, err := w.leases.Dispatch(r.Ctx, r.Scope, id, lane, r.Actor); err != nil {
		return err
	}
	ops := r.NewOps(s)
	generation, err := ops.StartLane(r.Ctx, r.Scope, grant.Lease, lane, r.Actor)
	if err != nil {
		return err
	}
	holder := workorders.Holder{Lease: grant.Lease, Lane: lane, Generation: generation}
	wake := r.ID("wake")
	if err := ops.BeginParking(r.Ctx, r.Scope, holder, parking.Draft{ID: wake, Expr: `{"human_resume":true}`}, r.worker("a")); err != nil {
		return err
	}
	if _, err := ops.CommitParked(r.Ctx, r.Scope, holder, PushedCommit(wake), r.worker("a")); err != nil {
		return err
	}
	if _, err := ops.ResumeLane(r.Ctx, r.Scope, lane, r.Actor); err != nil {
		return err
	}
	w.git.Grow(path, "main")
	again, err := w.claim(id)
	if err != nil {
		return err
	}
	if again.Lane != lane || again.BaseRef != "main" || again.BaseCommit != tip {
		return Violatedf("a claim on woken work reads lane %s with base %s at %s, want %s at %s", again.Lane, again.BaseRef, again.BaseCommit, lane, tip)
	}
	return nil
}

func checkHoldReplays(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	path := w.path(startRepository)
	w.git.Grow(path, "main")
	work := w.git.Add()
	dependency, err := w.accepted(startRepository, work)
	if err != nil {
		return err
	}
	id, err := w.order(startRepository, "", dependency)
	if err != nil {
		return err
	}
	if _, err := w.refused(id, workorders.HoldWaitingForMerge); err != nil {
		return err
	}
	if err := r.CheckProjection(s); err != nil {
		return err
	}
	if err := r.NewOps(s).Rebuild(r.Ctx, r.Scope, r.Actor); err != nil {
		return err
	}
	row, err := r.WorkOrder(s, id)
	if err != nil || row.StartHeld == nil || row.StartHeld.Reason != workorders.HoldWaitingForMerge {
		return Violatedf("a rebuild left the hold at %+v: %v", row.StartHeld, err)
	}
	return nil
}

func checkWorkerAndLaneViewCarryTheBase(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	path := w.path(startRepository)
	tip := w.git.Grow(path, "main")
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
	id, err := w.order(startRepository, "")
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
	w.git.Grow(path, "main")
	desired, err := plane.Desired(r.Ctx, ledger.DesiredRequest{Auth: auth})
	if err != nil {
		return err
	}
	if len(desired.Leases) != 1 || desired.Leases[0].Lane != lane || desired.Leases[0].BaseRef != "main" || desired.Leases[0].BaseCommit != tip {
		return Violatedf("the worker is told %+v", desired.Leases)
	}
	view, err := plane.Lane(r.Ctx, lane)
	if err != nil || view.BaseRef != "main" || view.BaseCommit != tip {
		return Violatedf("the lane reads base %s at %s: %v", view.BaseRef, view.BaseCommit, err)
	}
	return nil
}
