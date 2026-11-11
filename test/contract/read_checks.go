package contract

import (
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// ReadChecks hold what a planning lane reads in the other repositories of its project (ADR-0022, FR-REPO-9):
// each one at its base branch tip, recorded on the claim when the lane starts and told to the worker, never
// a repository the project removed, and nothing for a lane that only writes.
var ReadChecks = []Check{
	{"RD1", "a planning lane's claim records every other repository of its project at its base branch tip, by name", checkPlanningLaneReadsItsProject},
	{"RD2", "a repository whose base cannot be read is recorded with the reason, and the planning lane still starts", checkAnUnreadableRepositoryDoesNotHoldThePlan},
	{"RD3", "a lane that changes code records nothing to read", checkAChangeReadsNothing},
	{"RD4", "a worker is told what its lane reads, and the lane says so", checkWorkerIsToldWhatItsLaneReads},
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
