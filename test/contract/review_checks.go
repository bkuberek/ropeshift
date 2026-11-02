package contract

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// PlanLaneChecks are what the ledger promises about a lane that plans and a lane that reviews
// (FR-PLAN-2, FR-PLAN-4, D17).
var PlanLaneChecks = []Check{
	{"PL1", "only a member starts a review, and only of a plan", checkOnlyAMemberStartsAReview},
	{"PL2", "a review pins each contract's newest version and stands ready", checkReviewPinsItsVersions},
	{"PL3", "a retry of a review returns the first", checkReviewRetry},
	{"PL4", "a plan with no work has nothing to review", checkNothingToReview},
	{"PL5", "a blocking question on a plan holds all of its work", checkPlanQuestionHolds},
	{"PL6", "what a review asks reaches the ladder when it ends", checkReviewQuestionsWait},
	{"PL7", "a review lane that parked holds nothing back", checkParkedReviewHoldsNothing},
	{"PL8", "accepting a review passes the versions it read", checkAcceptedReviewPasses},
	{"PL9", "a version a review did not read stays unreviewed", checkReviewCoversItsVersions},
	{"PL10", "an orchestrator's acceptance passes no review", checkOrchestratorPassesNoReview},
	{"PL11", "an accepted plan reaches the ledger at its commit", checkPlanTakenIn},
	{"PL12", "a plan nobody accepted reaches nothing", checkUnacceptedPlanStays},
	{"PL13", "a commit is read once", checkPlanTakenInOnce},
	{"PL14", "a file intake cannot read is reported, and stops nothing", checkPlanProblemIsReported},
	{"PL15", "reading an accepted plan moves no cursor", checkPlanMovesNoCursor},
	{"PL16", "a commit that cannot be opened is read again later", checkPlanSourceFailure},
}

const (
	planFile     = "plan.md"
	reviewedPlan = "a-plan"
	plannedOrder = "The work order of alpha"
	acceptedHead = "accepted-head"
)

func plannedContract(statement string) []byte {
	return []byte(strings.Replace(ContractText("alpha", "Alpha", statement), `title = "Alpha"`, "title = \"Alpha\"\nplan = \""+reviewedPlan+"\"", 1))
}

// APlan puts one plan with one contract and one work order in the ledger, and returns the plan's ID.
func (r *Rig) APlan(s ports.Store) (string, *fake.WorkSource, error) {
	source := fake.NewWorkSource()
	source.Write(planFile, []byte(PlanText(reviewedPlan, "The plan", "")))
	source.Write(intakeFile, plannedContract("One."))
	if _, err := r.intake(s, source); err != nil {
		return "", nil, err
	}
	var plan string
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		plans, err := tx.Planning().Plans()
		if err != nil {
			return err
		}
		for _, row := range plans {
			if row.Title == "The plan" {
				plan = row.ID
			}
		}
		return nil
	})
	if err == nil && plan == "" {
		err = Violatedf("the plan did not reach the ledger")
	}
	return plan, source, err
}

func (r *Rig) reviewOf(s ports.Store, plan string) (workorders.WorkOrder, error) {
	return control.NewWorkOrders(s).ReviewPlan(r.Ctx, r.Scope, r.ID("review"), plan, r.member())
}

// reviewRunning runs a review lane on a registered worker, which is what a lane's `ask` needs.
func (r *Rig) reviewRunning() (*statusRig, string, error) {
	store := r.Store()
	var plan string
	s, err := newStatusRigOn(r, store, func(s ports.Store) (string, error) {
		var err error
		if plan, _, err = r.APlan(s); err != nil {
			return "", err
		}
		review, err := r.reviewOf(s, plan)
		return review.ID, err
	})
	return s, plan, err
}

func (s *statusRig) askAs(text string, blocking bool) (questions.AskResult, error) {
	return s.plane.Ask(s.r.Ctx, questions.LaneAsk{
		Auth: s.auth, Epoch: s.epoch, WorkOrder: s.grant.WorkOrder, Fence: s.grant.Fence,
		Ask: questions.Ask{ID: s.r.ID("q"), Text: text, Blocking: blocking, Lane: s.lane, Generation: 1},
	})
}

func (s *statusRig) digestQuestions() ([]string, error) {
	digest, err := s.plane.WakeDigest(s.r.Ctx, ledger.WakeRequest{Auth: s.auth, Epoch: s.epoch})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, item := range digest.Items {
		if item.Kind == ledger.WakeQuestion {
			out = append(out, item.ID)
		}
	}
	return out, nil
}

func (s *statusRig) reviewEnds() error {
	raw, err := json.Marshal(LaneFinished(acceptedHead, LaneCheck(reviewCriterion, true)))
	if err != nil {
		return err
	}
	return accepted(s.upload(ledger.UploadLaneEnded, raw))
}

const reviewCriterion = "r1"

func checkOnlyAMemberStartsAReview(r *Rig) error {
	s := r.Store()
	plan, _, err := r.APlan(s)
	if err != nil {
		return err
	}
	for _, agent := range theAgents {
		if _, err := control.NewWorkOrders(s).ReviewPlan(r.Ctx, r.Scope, r.ID("review"), plan, agent); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("%s started a review: %v", agent.Kind, err)
		}
	}
	draft := control.LedgerWork{Order: workorders.WorkOrder{ID: r.ID("wo"), Kind: workorders.KindContractReview, Title: "A review of nothing"}}
	if _, err := r.NewOps(s).Record(r.Ctx, r.Scope, draft, r.member()); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("a review that names no plan: %v", err)
	}
	if _, err := r.reviewOf(s, "no-such-plan"); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("a review of a plan nobody holds: %v", err)
	}
	return nil
}

func checkReviewPinsItsVersions(r *Rig) error {
	s := r.Store()
	plan, source, err := r.APlan(s)
	if err != nil {
		return err
	}
	source.Write(intakeFile, plannedContract("Two."))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	review, err := r.reviewOf(s, plan)
	if err != nil {
		return err
	}
	if review.State != workorders.Ready || review.Kind != workorders.KindContractReview || review.ReviewsPlan != plan {
		return Violatedf("a review reads %+v", review)
	}
	if len(review.ReviewPins) != 1 || !strings.HasSuffix(review.ReviewPins[0], "@2") {
		return Violatedf("a review pinned %v", review.ReviewPins)
	}
	return r.CheckProjection(s)
}

func checkReviewRetry(r *Rig) error {
	s := r.Store()
	plan, _, err := r.APlan(s)
	if err != nil {
		return err
	}
	id := r.ID("review")
	first, err := control.NewWorkOrders(s).ReviewPlan(r.Ctx, r.Scope, id, plan, r.member())
	if err != nil {
		return err
	}
	again, err := control.NewWorkOrders(s).ReviewPlan(r.Ctx, r.Scope, id, plan, r.member())
	if err != nil {
		return err
	}
	if diff := workorders.Diff(first, again); len(diff) != 0 {
		return Violatedf("a retry differs on %v", diff)
	}
	return nil
}

func checkNothingToReview(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write(planFile, []byte(PlanText(reviewedPlan, "The plan", "")))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	var plans []planning.Plan
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		plans, err = tx.Planning().Plans()
		return err
	})
	if err != nil || len(plans) != 1 {
		return Violatedf("plans %+v, %v", plans, err)
	}
	if _, err := r.reviewOf(s, plans[0].ID); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("a review of a plan with no work: %v", err)
	}
	return nil
}

func checkPlanQuestionHolds(r *Rig) error {
	s, plan, err := r.reviewRunning()
	if err != nil {
		return err
	}
	order, err := r.orderByTitle(s.store, plannedOrder)
	if err != nil {
		return err
	}
	if _, err := r.NewOps(s.store).Pass(r.Ctx, r.Scope, order.ID, r.member()); err != nil {
		return err
	}
	if row, err := r.WorkOrder(s.store, order.ID); err != nil || row.State != workorders.Ready {
		return Violatedf("the work before a question: %+v, %v", row, err)
	}
	asked, err := s.askAs("Which budget applies?", true)
	if err != nil {
		return err
	}
	row, err := r.WorkOrder(s.store, order.ID)
	if err != nil {
		return err
	}
	if row.State != workorders.ContractReview {
		return Violatedf("a blocking question left the plan's work at %s", row.State)
	}
	var question questions.Question
	err = s.store.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		question, err = tx.Questions().Question(asked.QuestionID)
		return err
	})
	if err != nil {
		return err
	}
	if question.SubjectKind != planning.SubjectPlan || question.SubjectID != plan {
		return Violatedf("a review's question is about %s %s", question.SubjectKind, question.SubjectID)
	}
	moved, err := r.NewOps(s.store).Promote(r.Ctx, r.Scope, r.Actor)
	if err != nil {
		return err
	}
	if len(moved) != 0 {
		return Violatedf("work moved past an open blocking question: %v", moved)
	}
	return r.CheckProjection(s.store)
}

func checkReviewQuestionsWait(r *Rig) error {
	s, _, err := r.reviewRunning()
	if err != nil {
		return err
	}
	if _, err := s.askAs("Which budget applies?", true); err != nil {
		return err
	}
	if _, err := s.askAs("Does a retry keep its key?", false); err != nil {
		return err
	}
	waiting, err := s.digestQuestions()
	if err != nil {
		return err
	}
	if len(waiting) != 0 {
		return Violatedf("a wake while the review reads: %v", waiting)
	}
	if err := s.reviewEnds(); err != nil {
		return err
	}
	if waiting, err = s.digestQuestions(); err != nil {
		return err
	}
	if len(waiting) != 2 {
		return Violatedf("what the review asked after its end: %v", waiting)
	}
	return nil
}

func checkParkedReviewHoldsNothing(r *Rig) error {
	s, _, err := r.reviewRunning()
	if err != nil {
		return err
	}
	if _, err := s.askAs("Which budget applies?", true); err != nil {
		return err
	}
	wake := r.ID("wake")
	if err := accepted(s.parkingUpload(wake, "true")); err != nil {
		return err
	}
	if err := accepted(s.parkedUpload(wake, true)); err != nil {
		return err
	}
	waiting, err := s.digestQuestions()
	if err != nil {
		return err
	}
	if len(waiting) != 1 {
		return Violatedf("a parked review holds what it is parked on: %v", waiting)
	}
	return nil
}

// reviewAccepted runs a review to its end with one blocking question open, and says how the plan's
// work stands.
func (r *Rig) reviewAccepted(by ledger.Actor, between func(*statusRig) error) (*statusRig, workorders.WorkOrder, error) {
	s, _, err := r.reviewRunning()
	if err != nil {
		return nil, workorders.WorkOrder{}, err
	}
	if between != nil {
		if err := between(s); err != nil {
			return nil, workorders.WorkOrder{}, err
		}
	}
	if err := s.reviewEnds(); err != nil {
		return nil, workorders.WorkOrder{}, err
	}
	if _, err := control.NewAcceptances(s.store).Accept(r.Ctx, r.Scope, s.grant.WorkOrder, by); err != nil {
		return nil, workorders.WorkOrder{}, err
	}
	order, err := r.orderByTitle(s.store, plannedOrder)
	return s, order, err
}

func checkAcceptedReviewPasses(r *Rig) error {
	var asked questions.AskResult
	s, order, err := r.reviewAccepted(r.member(), func(s *statusRig) error {
		var err error
		asked, err = s.askAs("Which budget applies?", true)
		return err
	})
	if err != nil {
		return err
	}
	if order.State != workorders.ContractReview || order.ReviewedBy == "" {
		return Violatedf("after a member accepted the review, with a question open: %+v", order)
	}
	if _, err := r.NewOps(s.store).Answer(r.Ctx, r.Scope, asked.QuestionID, questions.Answer{DecisionID: r.ID("d"), Statement: "Two."}, r.member()); err != nil {
		return err
	}
	moved, err := r.NewOps(s.store).Promote(r.Ctx, r.Scope, r.Actor)
	if err != nil {
		return err
	}
	if !slices.Contains(moved, order.ID) {
		return Violatedf("the work stayed behind a closed question: %v", moved)
	}
	return r.CheckProjection(s.store)
}

func checkReviewCoversItsVersions(r *Rig) error {
	var source *fake.WorkSource
	store := r.Store()
	s, err := newStatusRigOn(r, store, func(s ports.Store) (string, error) {
		plan, written, err := r.APlan(s)
		if err != nil {
			return "", err
		}
		source = written
		review, err := r.reviewOf(s, plan)
		return review.ID, err
	})
	if err != nil {
		return err
	}
	source.Write(intakeFile, plannedContract("Two."))
	if _, err := r.intake(store, source); err != nil {
		return err
	}
	if err := s.reviewEnds(); err != nil {
		return err
	}
	if _, err := control.NewAcceptances(store).Accept(r.Ctx, r.Scope, s.grant.WorkOrder, r.member()); err != nil {
		return err
	}
	order, err := r.orderByTitle(store, plannedOrder)
	if err != nil {
		return err
	}
	if order.ContractVersion != 2 || order.ReviewedBy != "" || order.State != workorders.ContractReview {
		return Violatedf("a review of version 1 passed %+v", order)
	}
	return nil
}

func checkOrchestratorPassesNoReview(r *Rig) error {
	_, order, err := r.reviewAccepted(aWorkerAsOrch, nil)
	if err != nil {
		return err
	}
	if order.ReviewedBy != "" || order.State != workorders.ContractReview {
		return Violatedf("an orchestrator's acceptance passed a review: %+v", order)
	}
	return nil
}

// acceptedSource is what a lane left: one contract, at one commit.
func acceptedSource(files map[string]string) *fake.WorkSource {
	source := fake.NewWorkSource()
	for name, text := range files {
		source.Write(name, []byte(text))
	}
	return source
}

// PlanLaneDone is a `plan` work order a member accepted, whose files nothing has read.
func (r *Rig) PlanLaneDone(s ports.Store) (string, error) {
	id, err := r.planLane(s)
	if err != nil {
		return "", err
	}
	_, err = r.NewOps(s).Accept(r.Ctx, r.Scope, id, r.member())
	return id, err
}

// planLane is a `plan` work order whose lane ended at acceptedHead.
func (r *Rig) planLane(s ports.Store) (string, error) {
	ops := r.NewOps(s)
	order, err := ops.Record(r.Ctx, r.Scope, control.LedgerWork{
		Order:    workorders.WorkOrder{ID: r.ID("plan"), Kind: workorders.KindPlan, Title: "Plan it", RepositoryID: AcceptanceRepository, MaxAttempts: 1},
		Criteria: []planning.Criterion{{ID: criterionOne, Text: "A contract is on the branch"}},
	}, r.member())
	if err != nil {
		return "", err
	}
	if _, err := ops.Pass(r.Ctx, r.Scope, order.ID, r.member()); err != nil {
		return "", err
	}
	return order.ID, r.anotherAttempt(s, order.ID, LaneFinished(acceptedHead, LaneCheck(criterionOne, true)))
}

// openFrom hands intake one source, and counts what it asked for.
func openFrom(source ports.WorkSource, asked *[]string) control.PlanSource {
	return func(_ context.Context, repository, commit string) (ports.WorkSource, error) {
		*asked = append(*asked, repository+"@"+commit)
		return source, nil
	}
}

func checkPlanTakenIn(r *Rig) error {
	s := r.Store()
	id, err := r.PlanLaneDone(s)
	if err != nil {
		return err
	}
	var asked []string
	taken, err := control.NewIntake(s).TakeInPlans(r.Ctx, r.Scope, openFrom(acceptedSource(map[string]string{intakeFile: ContractText("alpha", "Alpha", "One.")}), &asked))
	if err != nil {
		return err
	}
	if !slices.Equal(taken, []string{id}) || !slices.Equal(asked, []string{AcceptanceRepository + "@" + acceptedHead}) {
		return Violatedf("taken %v, read %v", taken, asked)
	}
	order, err := r.orderByTitle(s, plannedOrder)
	if err != nil {
		return err
	}
	if order.State != workorders.ContractReview {
		return Violatedf("a plan's work reads %s", order.State)
	}
	row, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	if row.PlanCommit != acceptedHead {
		return Violatedf("the plan reads %+v", row)
	}
	return r.CheckProjection(s)
}

func checkUnacceptedPlanStays(r *Rig) error {
	s := r.Store()
	if _, err := r.planLane(s); err != nil {
		return err
	}
	var asked []string
	taken, err := control.NewIntake(s).TakeInPlans(r.Ctx, r.Scope, openFrom(acceptedSource(map[string]string{intakeFile: ContractText("alpha", "Alpha", "One.")}), &asked))
	if err != nil {
		return err
	}
	if len(taken) != 0 || len(asked) != 0 {
		return Violatedf("a plan nobody accepted: taken %v, read %v", taken, asked)
	}
	return nil
}

func checkPlanTakenInOnce(r *Rig) error {
	s := r.Store()
	if _, err := r.PlanLaneDone(s); err != nil {
		return err
	}
	source := acceptedSource(map[string]string{intakeFile: ContractText("alpha", "Alpha", "One.")})
	var asked []string
	if _, err := control.NewIntake(s).TakeInPlans(r.Ctx, r.Scope, openFrom(source, &asked)); err != nil {
		return err
	}
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	taken, err := control.NewIntake(s).TakeInPlans(r.Ctx, r.Scope, openFrom(source, &asked))
	if err != nil {
		return err
	}
	after, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if len(taken) != 0 || len(asked) != 1 || after != before {
		return Violatedf("a second tick: taken %v, read %v, revision %d to %d", taken, asked, before, after)
	}
	return nil
}

func checkPlanProblemIsReported(r *Rig) error {
	s := r.Store()
	id, err := r.PlanLaneDone(s)
	if err != nil {
		return err
	}
	var asked []string
	source := acceptedSource(map[string]string{
		intakeFile: ContractText("alpha", "Alpha", "One."),
		"bad.md":   "+++\nkind = \"contract\"\nid = broken\n+++\n",
	})
	if _, err := control.NewIntake(s).TakeInPlans(r.Ctx, r.Scope, openFrom(source, &asked)); err != nil {
		return err
	}
	if _, err := r.orderByTitle(s, plannedOrder); err != nil {
		return Violatedf("a bad file stopped a good one: %v", err)
	}
	var bindings []planning.SourceBinding
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		bindings, err = tx.Planning().Bindings()
		return err
	})
	if err != nil {
		return err
	}
	if len(bindings) != 1 || bindings[0].Health != planning.HealthBadFiles || len(bindings[0].Problems) != 1 || bindings[0].Problems[0].Path != "bad.md" {
		return Violatedf("the source reads %+v", bindings)
	}
	row, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	if row.PlanCommit != acceptedHead {
		return Violatedf("the plan reads %+v", row)
	}
	return nil
}

func checkPlanMovesNoCursor(r *Rig) error {
	s := r.Store()
	if _, err := r.PlanLaneDone(s); err != nil {
		return err
	}
	main := fake.NewWorkSource()
	main.Write(intakeFile, []byte(ContractText("beta", "Beta", "One.")))
	if _, err := r.intake(s, main); err != nil {
		return err
	}
	cursor := func() (string, error) {
		var value string
		err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
			bindings, err := tx.Planning().Bindings()
			if err != nil || len(bindings) != 1 {
				return err
			}
			value = bindings[0].Cursor
			return nil
		})
		return value, err
	}
	before, err := cursor()
	if err != nil {
		return err
	}
	var asked []string
	source := acceptedSource(map[string]string{intakeFile: ContractText("alpha", "Alpha", "One."), "more.md": ContractText("gamma", "Gamma", "One.")})
	if _, err := control.NewIntake(s).TakeInPlans(r.Ctx, r.Scope, openFrom(source, &asked)); err != nil {
		return err
	}
	after, err := cursor()
	if err != nil {
		return err
	}
	if before == "" || before != after {
		return Violatedf("a plan's commit moved the source from %q to %q", before, after)
	}
	return nil
}

func checkPlanSourceFailure(r *Rig) error {
	s := r.Store()
	id, err := r.PlanLaneDone(s)
	if err != nil {
		return err
	}
	gone := errors.New("no such commit")
	taken, err := control.NewIntake(s).TakeInPlans(r.Ctx, r.Scope, func(context.Context, string, string) (ports.WorkSource, error) { return nil, gone })
	if !errors.Is(err, gone) || len(taken) != 0 {
		return Violatedf("a commit that cannot be opened: %v, %v", taken, err)
	}
	var asked []string
	taken, err = control.NewIntake(s).TakeInPlans(r.Ctx, r.Scope, openFrom(acceptedSource(map[string]string{intakeFile: ContractText("alpha", "Alpha", "One.")}), &asked))
	if err != nil {
		return err
	}
	if !slices.Equal(taken, []string{id}) {
		return Violatedf("taken %v", taken)
	}
	return nil
}
