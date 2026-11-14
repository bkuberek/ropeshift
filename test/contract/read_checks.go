package contract

import (
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// ReadChecks hold what a lane reads in the other repositories of its project (ADR-0022, FR-REPO-9, FR-REPO-11):
// a planning lane reads every one at its base branch tip; a lane that changes code reads the repositories its
// dependencies target, at the commit each dependency merged, and those its contract lists in `reads`, at
// their base branch tips; a review lane reads what the lane it reviews read. Each is recorded on the claim
// when the lane starts and told to the worker, and never a repository the project removed or does not hold.
var ReadChecks = []Check{
	{"RD1", "a planning lane's claim records every other repository of its project at its base branch tip, by name", checkPlanningLaneReadsItsProject},
	{"RD2", "a repository whose base cannot be read is recorded with the reason, and the planning lane still starts", checkAnUnreadableRepositoryDoesNotHoldThePlan},
	{"RD3", "a lane that changes code with no dependency in another repository and nothing in reads records nothing to read", checkAChangeReadsNothing},
	{"RD4", "a worker is told what its lane reads, and the lane says so", checkWorkerIsToldWhatItsLaneReads},
	{"RD5", "a lane that changes code reads the repository its dependency targets at the commit the dependency merged, and its review reads the same", checkAChangeReadsWhatItsDependencyMerged},
	{"RD6", "a lane reads what its contract lists at base branch tips, never its own repository, and records a name the project does not hold with the reason", checkAChangeReadsWhatItsContractLists},
}

// planOrder records ready planning work in a repository.
func (w *startWorld) planOrder(repository string) (string, error) {
	ops := w.r.NewOps(w.s)
	made, err := ops.Record(w.r.Ctx, w.r.Scope, control.LedgerWork{
		Order: workorders.WorkOrder{
			ID: w.r.ID("wo"), Kind: workorders.KindPlan, Title: "Plan it", RepositoryID: repository,
			MaxAttempts: defaultMaxAttempts, Needs: map[string]string{"driver": "claude-code"},
		},
		Statement: "Plan the work.",
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
		return "", Violatedf("ready planning work reads %+v", result)
	}
	return made.ID, nil
}

// project makes the planning repository and the repositories a plan reads: api on main, web on a base a
// member named, and one the project removed.
func (w *startWorld) project() (api, web string, err error) {
	system := ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"}
	for id, directory := range map[string]string{startRepository: "plan", "repo-api": "api", "repo-web": "web", "repo-old": "old"} {
		if _, err := w.r.repositories(w.s).Detect(w.r.Ctx, w.r.Scope, control.DetectedRepository{ID: id, DefaultBranch: "main", Directory: directory}, system); err != nil {
			return "", "", err
		}
	}
	if _, err := w.r.change(w.s, "repo-web", control.RepositoryChange{Base: text("release/1")}, w.r.Actor); err != nil {
		return "", "", err
	}
	if _, err := w.r.repositories(w.s).Remove(w.r.Ctx, w.r.Scope, "repo-old", nil, w.r.Actor); err != nil {
		return "", "", err
	}
	w.git.Grow(w.path(startRepository), "main")
	w.git.Grow(w.path("repo-old"), "main")
	api = w.git.Grow(w.path("repo-api"), "main")
	w.git.Grow(w.path("repo-web"), "main")
	web = w.git.Grow(w.path("repo-web"), "release/1")
	return api, web, nil
}

func checkPlanningLaneReadsItsProject(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	api, web, err := w.project()
	if err != nil {
		return err
	}
	id, err := w.planOrder(startRepository)
	if err != nil {
		return err
	}
	claim, err := w.claim(id)
	if err != nil {
		return err
	}
	want := []ledger.RepositoryRead{
		{Repository: "repo-api", Name: "api", Ref: "main", Commit: api},
		{Repository: "repo-web", Name: "web", Ref: "release/1", Commit: web},
	}
	if !slices.Equal(claim.Reads, want) {
		return Violatedf("the planning lane reads %+v, want %+v", claim.Reads, want)
	}
	// What the lane reads stands at the commits recorded when it started, wherever the branches go next.
	w.git.Grow(w.path("repo-api"), "main")
	if again, err := w.claimOf(claim.ID); err != nil || !slices.Equal(again.Reads, want) {
		return Violatedf("the claim reads %+v later: %v", again.Reads, err)
	}
	return nil
}

func checkAnUnreadableRepositoryDoesNotHoldThePlan(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	if _, _, err := w.project(); err != nil {
		return err
	}
	// The member names a base for web that nobody has.
	if _, err := r.change(s, "repo-web", control.RepositoryChange{Base: text("release/9")}, r.Actor); err != nil {
		return err
	}
	id, err := w.planOrder(startRepository)
	if err != nil {
		return err
	}
	claim, err := w.claim(id)
	if err != nil {
		return err
	}
	if len(claim.Reads) != 2 || claim.Reads[0].Commit == "" || claim.Reads[1].Name != "web" || claim.Reads[1].Commit != "" ||
		!strings.Contains(claim.Reads[1].Detail, "release/9") {
		return Violatedf("the planning lane reads %+v", claim.Reads)
	}
	return nil
}

func checkAChangeReadsNothing(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	if _, _, err := w.project(); err != nil {
		return err
	}
	id, err := w.order(startRepository, "")
	if err != nil {
		return err
	}
	if claim, err := w.claim(id); err != nil || len(claim.Reads) != 0 {
		return Violatedf("a change reads %+v: %v", claim.Reads, err)
	}
	return nil
}

func checkWorkerIsToldWhatItsLaneReads(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	api, web, err := w.project()
	if err != nil {
		return err
	}
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
	id, err := w.planOrder(startRepository)
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
	want := []ledger.RepositoryRead{
		{Repository: "repo-api", Name: "api", Ref: "main", Commit: api},
		{Repository: "repo-web", Name: "web", Ref: "release/1", Commit: web},
	}
	desired, err := plane.Desired(r.Ctx, ledger.DesiredRequest{Auth: auth})
	if err != nil {
		return err
	}
	if len(desired.Leases) != 1 || !slices.Equal(desired.Leases[0].Reads, want) {
		return Violatedf("the worker is told %+v", desired.Leases)
	}
	view, err := plane.Lane(r.Ctx, lane)
	if err != nil || !slices.Equal(view.Reads, want) {
		return Violatedf("the lane reads %+v: %v", view.Reads, err)
	}
	return nil
}

// started claims ready work through the rig's starts, dispatches its lane, and starts it, as a worker would.
func (w *startWorld) started(id string) (workorders.Holder, workorders.Claim, error) {
	request := w.r.Request(id, "a")
	grant, err := w.leases.Claim(w.r.Ctx, w.r.Scope, request, w.r.worker("a"))
	if err != nil {
		return workorders.Holder{}, workorders.Claim{}, err
	}
	lane := w.r.ID("lane")
	if _, err := w.leases.Dispatch(w.r.Ctx, w.r.Scope, id, lane, w.r.Actor); err != nil {
		return workorders.Holder{}, workorders.Claim{}, err
	}
	generation, err := w.r.NewOps(w.s).StartLane(w.r.Ctx, w.r.Scope, grant.Lease, lane, w.r.worker("a"))
	if err != nil {
		return workorders.Holder{}, workorders.Claim{}, err
	}
	claim, err := w.claimOf(request.ClaimID)
	return workorders.Holder{Lease: grant.Lease, Lane: lane, Generation: generation}, claim, err
}

func checkAChangeReadsWhatItsDependencyMerged(r *Rig) error {
	// The dependency is merged by ropeshift in its own repository, which records the commit the merge made.
	f, err := newMergeRig(r, ledger.ReviewOnBranch, ledger.ReviewApproves, ledger.MergeCommit)
	if err != nil {
		return err
	}
	if _, err := f.judge(passes("c1", "ok"), passes("c2", "ok")); err != nil {
		return err
	}
	merge, err := f.oneMerge()
	if err != nil {
		return err
	}
	merged := r.ID("merged")
	if err := f.mergeCame(merge, ledger.MergeResult{Outcome: ledger.MergeMerged, MergeCommit: merged}); err != nil {
		return err
	}
	s, dependency := f.store, f.grant.WorkOrder
	if err := r.stateIs(s, dependency, workorders.Done); err != nil {
		return err
	}
	record, err := r.theRepository(s, f.repository)
	if err != nil {
		return err
	}
	w := r.startWorld(s)
	// The base moves on past the merge before the dependent work starts.
	w.git.Grow(w.path(f.repository), "main")
	w.git.Grow(w.path(startRepository), "main")
	id, err := w.order(startRepository, "", dependency)
	if err != nil {
		return err
	}
	holder, claim, err := w.started(id)
	if err != nil {
		return err
	}
	want := []ledger.RepositoryRead{{Repository: f.repository, Name: record.Name, Ref: "main", Commit: merged}}
	if !slices.Equal(claim.Reads, want) {
		return Violatedf("the lane reads %+v, want %+v", claim.Reads, want)
	}
	result := LaneFinished(w.git.Add(), LaneCheck(criterionOne, true))
	result.Branch = "lane/" + id
	if err := r.NewOps(s).EndLane(r.Ctx, r.Scope, holder, result, r.worker("a")); err != nil {
		return err
	}
	review, err := r.theReview(s, id)
	if err != nil {
		return err
	}
	// The base moves again, and the review still reads what the writer read.
	w.git.Grow(w.path(f.repository), "main")
	judged, err := w.claim(review.ID)
	if err != nil {
		return err
	}
	if !slices.Equal(judged.Reads, want) {
		return Violatedf("the review reads %+v, want what its lane read, %+v", judged.Reads, want)
	}
	return nil
}

func checkAChangeReadsWhatItsContractLists(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	_, web, err := w.project()
	if err != nil {
		return err
	}
	plan := w.git.Grow(w.path(startRepository), "main")
	contract := strings.Replace(ContractText("client", "client", "Build the client."), `size = "small"`, "size = \"small\"\nrepo = \"api\"", 1)
	contract = strings.Replace(contract, `requirements = ["PAY-12"]`, `requirements = ["PAY-12"]`+"\nreads = [\"web\", \"api\", \"old\", \"elsewhere\", \"plan\"]", 1)
	files := fake.NewWorkSource()
	files.Write("client.md", []byte(contract))
	if _, err := r.intake(s, files); err != nil {
		return err
	}
	order, err := r.orderByTitle(s, "The work order of client")
	if err != nil {
		return err
	}
	if result, err := r.NewOps(s).Pass(r.Ctx, r.Scope, order.ID, r.member()); err != nil || result.State != workorders.Ready {
		return Violatedf("the work reads %+v: %v", result, err)
	}
	claim, err := w.claim(order.ID)
	if err != nil {
		return err
	}
	// Its own repository is its worktree, and a removed repository or one of another project is no
	// repository of this project.
	if len(claim.Reads) != 4 {
		return Violatedf("the lane reads %+v", claim.Reads)
	}
	unheld := func(read ledger.RepositoryRead, name string) bool {
		return read.Name == name && read.Repository == "" && read.Commit == "" && strings.Contains(read.Detail, "no repository named "+name)
	}
	if !unheld(claim.Reads[0], "elsewhere") || !unheld(claim.Reads[1], "old") {
		return Violatedf("the lane reads names the project does not hold as %+v", claim.Reads[:2])
	}
	want := []ledger.RepositoryRead{
		{Repository: startRepository, Name: "plan", Ref: "main", Commit: plan},
		{Repository: "repo-web", Name: "web", Ref: "release/1", Commit: web},
	}
	if !slices.Equal(claim.Reads[2:], want) {
		return Violatedf("the lane reads %+v, want %+v", claim.Reads[2:], want)
	}
	return nil
}
