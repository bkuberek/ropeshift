package contract

import (
	"errors"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// AdoptChecks hold the adopt session (ADR-0023): a planning work order that adopts an existing project keeps
// saying so, its lane is told to adopt and given the inventory, only planning work adopts, and a project that
// already has an approved plan plans its next work with a planning session rather than adopting again.
var AdoptChecks = []Check{
	{"AS1", "an adopt work order keeps saying so through a rebuild, and its lane is told to adopt and given the inventory", checkAnAdoptLaneIsToldItAdopts},
	{"AS2", "only planning work adopts", checkOnlyPlanningWorkAdopts},
	{"AS3", "adoption is refused for a project that already has an approved plan, and not before", checkAdoptionRefusedOnceAPlanIsApproved},
}

// adoptOrder records ready planning work that adopts the project, in a repository.
func (w *startWorld) adoptOrder(repository string) (string, error) {
	ops := w.r.NewOps(w.s)
	made, err := ops.Record(w.r.Ctx, w.r.Scope, control.LedgerWork{
		Order: workorders.WorkOrder{
			ID: w.r.ID("adopt"), Kind: workorders.KindPlan, Title: "Adopt the project", RepositoryID: repository, Adopt: true,
			MaxAttempts: defaultMaxAttempts, Needs: map[string]string{"driver": "claude-code"},
		},
		Statement: "Onboard the project.",
		Criteria:  criteriaOf(criterionOne),
	}, w.r.Actor)
	if err != nil {
		return "", err
	}
	if _, err := ops.Pass(w.r.Ctx, w.r.Scope, made.ID, w.r.Actor); err != nil {
		return "", err
	}
	return made.ID, nil
}

func checkAnAdoptLaneIsToldItAdopts(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	if _, _, err := w.project(); err != nil {
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
	adopt, err := w.adoptOrder(startRepository)
	if err != nil {
		return err
	}
	plan, err := w.planOrder(startRepository)
	if err != nil {
		return err
	}
	lanes := map[string]string{}
	for _, id := range []string{adopt, plan} {
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
		lanes[id] = lane
	}
	desired, err := plane.Desired(r.Ctx, ledger.DesiredRequest{Auth: auth})
	if err != nil {
		return err
	}
	told := map[string]ledger.DesiredLease{}
	for _, lease := range desired.Leases {
		told[lease.WorkOrder] = lease
	}
	if lease := told[adopt]; lease.Lane != lanes[adopt] || !lease.Adopt || !lease.Inventory || lease.Kind != string(workorders.KindPlan) || len(lease.Reads) != 2 {
		return Violatedf("the adopt lane is told %+v", lease)
	}
	if lease := told[plan]; lease.Lane != lanes[plan] || lease.Adopt || lease.Inventory {
		return Violatedf("a planning lane that does not adopt is told %+v", lease)
	}
	if err := r.NewOps(s).Rebuild(r.Ctx, r.Scope, r.Actor); err != nil {
		return err
	}
	if row, err := r.WorkOrder(s, adopt); err != nil || !row.Adopt || row.Kind != workorders.KindPlan {
		return Violatedf("after a rebuild the adopt work order reads %+v: %v", row, err)
	}
	return r.CheckProjection(s)
}

func checkOnlyPlanningWorkAdopts(r *Rig) error {
	s := r.Store()
	_, err := r.NewOps(s).Record(r.Ctx, r.Scope, control.LedgerWork{
		Order:    workorders.WorkOrder{ID: r.ID("wo"), Kind: workorders.KindChange, Title: "Change it", Adopt: true, MaxAttempts: 1},
		Criteria: criteriaOf(criterionOne),
	}, r.member())
	if !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("a change that adopts was recorded: %v", err)
	}
	return nil
}

func checkAdoptionRefusedOnceAPlanIsApproved(r *Rig) error {
	s := r.Store()
	adopt := func() error {
		_, err := r.NewOps(s).Record(r.Ctx, r.Scope, control.LedgerWork{
			Order:    workorders.WorkOrder{ID: r.ID("adopt"), Kind: workorders.KindPlan, Title: "Adopt the project", Adopt: true, MaxAttempts: 1},
			Criteria: criteriaOf(criterionOne),
		}, r.member())
		return err
	}
	_, pending, err := r.aLanePlanAsksForApproval(s, laneFiles("The first draft."))
	if err != nil {
		return err
	}
	// A plan that only waits for approval is not a plan the project has.
	if err := adopt(); err != nil {
		return Violatedf("adoption with a plan that waits was refused: %v", err)
	}
	if _, err := r.approve(s, pending, true, theMember); err != nil {
		return err
	}
	if err := adopt(); !errors.Is(err, workorders.ErrAlreadyPlanned) {
		return Violatedf("adoption of a project with an approved plan: %v", err)
	}
	// Planning its next work is still open to it.
	if _, err := r.NewOps(s).Record(r.Ctx, r.Scope, control.LedgerWork{
		Order:    workorders.WorkOrder{ID: r.ID("plan"), Kind: workorders.KindPlan, Title: "Plan the next work", MaxAttempts: 1},
		Criteria: criteriaOf(criterionOne),
	}, r.member()); err != nil {
		return Violatedf("a planning session for a planned project was refused: %v", err)
	}
	return nil
}
