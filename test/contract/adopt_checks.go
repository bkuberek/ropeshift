package contract

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
)

// AdoptChecks hold the adopt session (ADR-0023): a planning work order that adopts an existing project keeps
// saying so, its lane is told to adopt and given the inventory, only planning work adopts, and a project that
// already has an approved plan plans its next work with a planning session rather than adopting again.
var AdoptChecks = []Check{
	{"AS1", "an adopt work order keeps saying so through a rebuild, and its lane is told to adopt and given the inventory", checkAnAdoptLaneIsToldItAdopts},
	{"AS2", "only planning work adopts", checkOnlyPlanningWorkAdopts},
	{"AS3", "adoption is refused for a project that already has an approved plan, and not before", checkAdoptionRefusedOnceAPlanIsApproved},
	{"AS4", "an adopt lane's branch asks for approval grouped into what exists, continues, is new, and is left out, with every file it changed", checkAnAdoptedPlanAsksGrouped},
	{"AS5", "an adoption whose branch cannot be read asks all the same, and says why it shows no change", checkAnAdoptedPlanWithNoBaseSaysWhy},
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

// adoptedFiles are what an adopt lane leaves on its branch: a plan that leaves a branch out, requirements of
// which the code meets one, an existing milestone with an existing shared contract, and a new milestone whose
// contract continues an in-flight branch beside fresh work.
func adoptedFiles() map[string]string {
	return map[string]string{
		"plan.md": "+++\nkind = \"plan\"\nid = \"" + adoptionPlan + "\"\ntitle = \"Adopted\"\n" +
			"documents = [\"requirements:shop\", \"milestone:m0\", \"milestone:m1\"]\n\n" +
			"[[left_out]]\nref = \"branch:spike/old-ui\"\nreason = \"An abandoned experiment\"\n+++\n\nHow.\n",
		"requirements.md": "+++\nkind = \"requirements\"\nid = \"shop\"\nplan = \"" + adoptionPlan + "\"\n\n" +
			"[[requirement]]\nid = \"SHOP-1\"\ntext = \"One client\"\nstatus = \"met\"\nevidence = [\"shop:client.go\", \"shop:client_test.go\"]\n\n" +
			"[[requirement]]\nid = \"SHOP-2\"\ntext = \"It retries\"\n+++\n\n# Requirements\n\nAs the team wrote them.\n",
		"m0.md":     existingMilestoneOf("m0", adoptionPlan, 0),
		"m1.md":     "+++\nkind = \"milestone\"\nid = \"m1\"\ntitle = \"Retries\"\nplan = \"" + adoptionPlan + "\"\npriority = 1\ndepends_on = [\"m0\"]\n+++\n\nRetries.\n",
		"client.md": existingContractOf("client", adoptionPlan, "m0"),
		"retry.md": "+++\nkind = \"contract\"\nid = \"retry\"\ntitle = \"Retry failed calls\"\nplan = \"" + adoptionPlan + "\"\nmilestone = \"m1\"\n" +
			"uses = [\"client\"]\n\n[[criteria]]\nid = \"c1\"\ntext = \"It retries\"\nrequirements = [\"SHOP-2\"]\n\n" +
			"[[work_orders]]\nid = \"retry-finish\"\ntitle = \"Finish the retry branch\"\nrepo = \".\"\nsize = \"small\"\ncontinues = \"feature/retry\"\n\n" +
			"[[work_orders]]\nid = \"retry-docs\"\ntitle = \"Document the retries\"\nrepo = \".\"\nsize = \"small\"\n+++\n\nRetry.\n",
	}
}

// adoptLaneEnds runs an adopt work order's lane from a claim that recorded where its branch began, and ends it
// with the branch at head.
func (w *startWorld) adoptLaneEnds(id, head string) error {
	request := w.r.Request(id, "a")
	grant, err := w.leases.Claim(w.r.Ctx, w.r.Scope, request, w.r.worker("a"))
	if err != nil {
		return err
	}
	ops := w.r.NewOps(w.s)
	lane := w.r.ID("lane")
	if _, err := w.leases.Dispatch(w.r.Ctx, w.r.Scope, id, lane, w.r.Actor); err != nil {
		return err
	}
	generation, err := ops.StartLane(w.r.Ctx, w.r.Scope, grant.Lease, lane, w.r.worker("a"))
	if err != nil {
		return err
	}
	holder := workorders.Holder{Lease: grant.Lease, Lane: lane, Generation: generation}
	return ops.EndLane(w.r.Ctx, w.r.Scope, holder, LaneFinished(head, LaneCheck(criterionOne, true)), w.r.worker("a"))
}

// pendingAdoption takes in the adopt lane's branch and reads what its request says of the adoption.
func (w *startWorld) pendingAdoption(intake *control.Intake) (authority.Approval, *planning.Adoption, error) {
	var asked []string
	if _, err := intake.TakeInPlans(w.r.Ctx, w.r.Scope, openFrom(acceptedSource(adoptedFiles()), &asked)); err != nil {
		return authority.Approval{}, nil, err
	}
	pending, err := w.r.theOpenPlanApprovalFor(w.s, "Adopted")
	if err != nil {
		return authority.Approval{}, nil, err
	}
	var detail planning.PlanDetail
	if err := json.Unmarshal([]byte(pending.Detail), &detail); err != nil {
		return authority.Approval{}, nil, err
	}
	return pending, detail.Adoption, nil
}

func checkAnAdoptedPlanAsksGrouped(r *Rig) error {
	s := r.Store()
	// A planning lane that does not adopt asks as it did, with nothing grouped.
	_, plain, err := r.aLanePlanAsksForApproval(s, laneFiles("The first draft."))
	if err != nil {
		return err
	}
	var detail planning.PlanDetail
	if err := json.Unmarshal([]byte(plain.Detail), &detail); err != nil || detail.Adoption != nil {
		return Violatedf("a plan that adopts nothing is grouped as %+v: %v", detail.Adoption, err)
	}
	w := r.startWorld(s)
	if err := w.repository(func(*control.RepositoryChange) {}); err != nil {
		return err
	}
	path := w.path(startRepository)
	base := w.git.Touch(path, "main", "README.md")
	id, err := w.adoptOrder(startRepository)
	if err != nil {
		return err
	}
	// The lane's branch begins where main stood, and changes the README and adds the plan's files.
	w.git.Point(path, "lane/adopt", base)
	w.git.Touch(path, "lane/adopt", "README.md")
	head := w.git.Touch(path, "lane/adopt", "plan.md")
	if err := w.adoptLaneEnds(id, head); err != nil {
		return err
	}
	pending, adoption, err := w.pendingAdoption(control.NewIntake(s).Checking(w.starts))
	if err != nil {
		return err
	}
	if pending.WorkOrder != id || adoption == nil {
		return Violatedf("the adoption's request is %+v with adoption %+v", pending, adoption)
	}
	want := planning.Adoption{
		Exists: []planning.ExistingRecord{
			{Ref: "requirement:SHOP-1", Title: "One client", Evidence: []string{"shop:client.go", "shop:client_test.go"}},
			{Ref: "milestone:m0", Title: "Milestone m0", Cites: []string{"contract:client"}},
			{Ref: "contract:client", Title: "The contract client"},
		},
		Continues: []planning.ContinuedWork{
			{Contract: "contract:retry", WorkOrder: "retry-finish", Title: "Finish the retry branch", Repo: ".", Branch: "feature/retry"},
		},
		New: []planning.NewRecord{
			{Ref: "milestone:m1", Title: "Retries"},
			{Ref: "contract:retry", Title: "Retry failed calls", WorkOrders: []string{"retry-docs"}},
		},
		LeftOut: []planning.LeftOut{{Ref: "branch:spike/old-ui", Reason: "An abandoned experiment"}},
		Changes: []planning.FileChange{{Status: planning.ChangeChanged, Path: "README.md"}, {Status: planning.ChangeAdded, Path: "plan.md"}},
	}
	if !reflect.DeepEqual(*adoption, want) {
		return Violatedf("the adoption reads\n%+v\nwant\n%+v", *adoption, want)
	}
	return nil
}

func checkAnAdoptedPlanWithNoBaseSaysWhy(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	if err := w.repository(func(*control.RepositoryChange) {}); err != nil {
		return err
	}
	path := w.path(startRepository)
	w.git.Grow(path, "main")
	id, err := w.adoptOrder(startRepository)
	if err != nil {
		return err
	}
	// The branch the lane names is a commit this repository does not hold.
	if err := w.adoptLaneEnds(id, strings.Repeat("f", 40)); err != nil {
		return err
	}
	_, adoption, err := w.pendingAdoption(control.NewIntake(s).Checking(w.starts))
	if err != nil {
		return err
	}
	if adoption == nil || len(adoption.Changes) != 0 || adoption.ChangesUnread == "" || len(adoption.Exists) != 3 {
		return Violatedf("an adoption whose branch git cannot read reads %+v", adoption)
	}
	return nil
}
