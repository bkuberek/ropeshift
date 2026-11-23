package control

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// The reason a plan that is not finished goes back with, and the one Send back offers a member. Both name what is
// missing, and the first says ropeshift sent it.
const (
	unfinishedSentBack = "Ropeshift sent this plan back because it is not finished:"
	unfinishedMissing  = "The plan is not finished:"
)

func unfinishedReason(lead string, missing []string) string {
	var out strings.Builder
	out.WriteString(lead)
	for _, line := range missing {
		out.WriteString("\n- " + line)
	}
	return out.String()
}

// The record of what ropeshift did about a planning branch it held back as unfinished is an event of its own, about the
// plan work order but of a subject kind of its own, so a replay of the work order skips it and a typed read finds it.
const (
	planHeldSubject = "plan_held"
	planHeldEvent   = "plan_held.recorded"
	// planHeldSentBack says ropeshift sent the work back itself, and planHeldCalled that it called a member instead.
	planHeldSentBack = "sent_back"
	planHeldCalled   = "called_member"
)

// eventsAbout is what a read of the held records needs of a transaction, which a read and a write both have.
type eventsAbout interface {
	EventsAbout(subject ledger.Subject) ([]ledger.Event, error)
}

type planHeld struct {
	Action  string   `json:"action"`
	Commit  string   `json:"commit"`
	Fence   int64    `json:"fence"`
	Missing []string `json:"missing"`
}

// planHeldOf reads what ropeshift did about a plan work order's branch, oldest first.
func planHeldOf(tx eventsAbout, workOrder string) ([]planHeld, error) {
	events, err := tx.EventsAbout(ledger.Subject{Kind: planHeldSubject, ID: workOrder})
	if err != nil {
		return nil, err
	}
	var out []planHeld
	for _, event := range events {
		var held planHeld
		if event.Type == planHeldEvent && json.Unmarshal(event.Data, &held) == nil {
			out = append(out, held)
		}
	}
	return out, nil
}

// heldAt says ropeshift already dealt with the branch of a plan work order at this commit, so it is not read again until
// the lane commits something new.
func heldAt(tx eventsAbout, workOrder, commit string) (bool, error) {
	held, err := planHeldOf(tx, workOrder)
	if err != nil || len(held) == 0 {
		return false, err
	}
	return held[len(held)-1].Commit == commit, nil
}

// holdPlanBack handles a planning branch whose plans are not all finished, which is never offered for approval, as a whole.
// The first time for a work order, ropeshift sends it back itself, with what is missing as the reason, as a system
// event, so the next lane finishes the plan. Whatever comes after, it keeps what is missing for the work order's card
// in Needs you, which leads with Send back. A branch is handled once for a commit.
func holdPlanBack(tx ports.WriteTx, workOrder, commit string, missing []string) error {
	current, err := tx.WorkOrders().Get(workOrder)
	if err != nil {
		return err
	}
	if current.State != workorders.Acceptance {
		return nil
	}
	held, err := planHeldOf(tx, workOrder)
	if err != nil {
		return err
	}
	if len(held) > 0 && held[len(held)-1].Commit == commit {
		return nil
	}
	action := planHeldCalled
	if !slices.ContainsFunc(held, func(h planHeld) bool { return h.Action == planHeldSentBack }) {
		action = planHeldSentBack
		if _, err := reworkFor(tx, workOrder, workorders.ReworkNote{Reason: unfinishedReason(unfinishedSentBack, missing)}, nil, intakeActor, func() {}); err != nil {
			return err
		}
	}
	data, err := json.Marshal(planHeld{Action: action, Commit: commit, Fence: current.Fence, Missing: missing})
	if err != nil {
		return err
	}
	_, err = tx.Append(ledger.EventDraft{Type: planHeldEvent, Actor: intakeActor, Subject: ledger.Subject{Kind: planHeldSubject, ID: workOrder}, Data: data})
	return err
}

// UnfinishedOf is what a plan work order waiting for acceptance lacks, and the reason that sends it back, when
// ropeshift held its plan back and called a member for it. A work order that was only sent back has none.
func UnfinishedOf(tx eventsAbout, order workorders.WorkOrder) ([]string, string, error) {
	if order.Kind != workorders.KindPlan || order.State != workorders.Acceptance {
		return nil, "", nil
	}
	held, err := planHeldOf(tx, order.ID)
	if err != nil || len(held) == 0 {
		return nil, "", err
	}
	last := held[len(held)-1]
	if last.Action != planHeldCalled || last.Fence != order.Fence {
		return nil, "", nil
	}
	return last.Missing, unfinishedReason(unfinishedMissing, last.Missing), nil
}

// branchFinish reads what a branch's plan holds that says whether it is finished.
func branchFinish(batch ports.ChangeBatch, plan ports.RecordItem, goal *planning.DetailGoal) planning.Finish {
	finish := planning.Finish{
		Title: plan.Plan.Title, GoalNamed: plan.Plan.Goal != "", Goal: goal,
		Documents: len(plan.Plan.Documents), Decisions: len(plan.Plan.Decisions), Body: plan.Statement,
	}
	for _, item := range batch.Items {
		if item.Contract.Plan == plan.Plan.ID {
			finish.Contracts = append(finish.Contracts, planning.FinishContract{
				ID: item.Contract.ID, Statement: item.Statement, WorkOrders: len(item.Contract.WorkOrders), Existing: item.Contract.Existing,
			})
		}
	}
	return finish
}

// versionFinish reads the same of a version the ledger holds.
func versionFinish(tx ports.WriteTx, version planning.PlanVersion) (planning.Finish, error) {
	record, err := tx.Planning().Plan(version.PlanID)
	if err != nil {
		return planning.Finish{}, err
	}
	finish := planning.Finish{
		Title: record.Title, GoalNamed: record.GoalID != "",
		Documents: len(version.Documents), Decisions: len(version.Decisions), Body: version.Body,
	}
	if record.GoalID != "" {
		goal, err := tx.Planning().Goal(record.GoalID)
		switch {
		case err == nil:
			finish.Goal = &planning.DetailGoal{Title: goal.Title, Statement: goal.Statement, SuccessMeasures: goal.SuccessMeasures}
		case !errors.Is(err, ports.ErrNotFound):
			return planning.Finish{}, err
		}
	}
	contracts, err := newestContracts(tx.Planning(), version.PlanID)
	if err != nil {
		return planning.Finish{}, err
	}
	for _, contract := range contracts {
		finish.Contracts = append(finish.Contracts, planning.FinishContract{
			ID: contract.ID, Statement: contract.Statement, WorkOrders: len(contract.Orders), Existing: contract.Existing,
		})
	}
	return finish, nil
}

// unfinishedPlans lists what the plans of a branch lack, each line naming its plan when the branch holds more than one,
// and none when every plan is finished.
func unfinishedPlans(tx ports.WriteTx, batch ports.ChangeBatch) ([]string, error) {
	plans := 0
	for _, item := range batch.Records {
		if item.Plan != nil {
			plans++
		}
	}
	var out []string
	for _, item := range batch.Records {
		if item.Plan == nil {
			continue
		}
		goal, err := branchGoal(tx, batch, item)
		if err != nil {
			return nil, err
		}
		for _, line := range planning.Unfinished(branchFinish(batch, item, goal)) {
			if plans > 1 {
				line = fmt.Sprintf("Plan %s: %s", item.Plan.ID, line)
			}
			out = append(out, line)
		}
	}
	return out, nil
}

// unfinishedRequest is the request that stands for a version of a plan only to say the version is a placeholder. A
// member cannot approve it, and a version that finishes the plan replaces it.
func unfinishedRequest(approvals ports.ApprovalReader, plan string, version int64) (authority.Approval, bool, error) {
	rows, err := approvals.All()
	if err != nil {
		return authority.Approval{}, false, err
	}
	for _, row := range rows {
		if row.Kind == authority.KindPlan && row.Open() && row.Plan == plan && row.PlanVersion == version && len(planDetailOf(row).Unfinished) != 0 {
			return row, true, nil
		}
	}
	return authority.Approval{}, false, nil
}

// offerHeldVersion asks again about the newest version of a plan that stands as a placeholder, when something other than
// its digest finished it, such as the plan's goal, which the digest does not cover. A version that is still unfinished
// keeps its request, with what is missing now. When intake read the commit of an accepted plan work order and that
// commit changed the plan's goal, which goals holds of the batch, the work order's commit is what changed the plan, so
// the request names it: approving the request is then the approval of the branch's merge, which a version made from the
// repository, with no work order of its own, could not be. A commit that left the plan as the ledger holds it changed
// nothing of it, such as a branch read before a poll of the repository that stopped short asked about the goal the poll
// recorded, so its request names the version's own work order and source, and approving it approves no branch's merge.
// A request that named a planning work order still waiting in integrating at the commit the request names keeps naming
// it, whatever read replaces the request, such as a poll of main that finished the goal the branch's commit holds as
// the ledger does: the branch still waits for the request's approval, and a request naming no work order would approve
// no merge.
func offerHeldVersion(tx ports.WriteTx, plan string, origin planOrigin, goals map[string]bool) error {
	latest, err := tx.Planning().LatestPlanVersion(plan)
	if err != nil {
		return err
	}
	request, held, err := unfinishedRequest(tx.Approvals(), plan, latest.Version)
	if err != nil || !held {
		return err
	}
	finish, err := versionFinish(tx, latest)
	if err != nil {
		return err
	}
	missing := planning.Unfinished(finish)
	if slices.Equal(missing, planDetailOf(request).Unfinished) {
		return nil
	}
	if origin.workOrder != "" {
		record, err := tx.Planning().Plan(plan)
		if err != nil {
			return err
		}
		if record.GoalID == "" || !goals[record.GoalID] {
			origin = planOrigin{}
		}
	}
	if origin.workOrder == "" && request.WorkOrder != "" {
		order, err := tx.WorkOrders().Get(request.WorkOrder)
		if err != nil {
			return err
		}
		if held, err := AttemptCommit(tx.Evidence(), order); err != nil {
			return err
		} else if order.Kind == workorders.KindPlan && order.State == workorders.Integrating && held == requestCommit(request) {
			origin = planOrigin{workOrder: order.ID, commit: held}
		}
	}
	return askAboutVersion(tx, latest, origin, missing)
}

// pointHeldRequest has the request that holds a version back as a placeholder name the planning branch whose commit
// finishes it, which raised no request of its own because its plan's digest is the version's. The branch's plans are
// all finished, or askAboutBranch would have held the branch back, so its commit finishes the version: what it changed
// is what the digest does not cover, such as the plan's goal. A request that names that branch at that commit already
// is left as it is, and a version with no such request has nothing to point.
func pointHeldRequest(tx ports.WriteTx, version planning.PlanVersion, branch acceptedPlan) error {
	request, held, err := unfinishedRequest(tx.Approvals(), version.PlanID, version.Version)
	if err != nil || !held {
		return err
	}
	artifacts, err := tx.Evidence().Artifacts(branch.order.ID)
	if err != nil {
		return err
	}
	name := cmp.Or(branch.order.Branch, attemptBranch(artifacts, branch.order.Fence).URI)
	finishing := planning.PlanBranch{WorkOrder: branch.order.ID, Branch: name, Commit: branch.commit}
	detail := planDetailOf(request)
	if detail.FinishedBy != nil && *detail.FinishedBy == finishing {
		return nil
	}
	detail.FinishedBy = &finishing
	data, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	next := request
	next.Detail = string(data)
	saved, err := tx.Approvals().Save(next, request.State)
	if err != nil || !saved {
		return err
	}
	event, err := authority.ApprovalEvent(next, intakeActor)
	if err != nil {
		return err
	}
	_, err = tx.Append(event)
	return err
}

// askAboutVersion asks about a version the ledger holds again, saying what it lacks, if anything. A request made when
// intake read an accepted plan work order's commit names that work order and commit, so approving it is the approval
// of the branch's merge; any other names the version's own work order and source.
func askAboutVersion(tx ports.WriteTx, version planning.PlanVersion, origin planOrigin, missing []string) error {
	record, err := tx.Planning().Plan(version.PlanID)
	if err != nil {
		return err
	}
	workOrder, source := version.WorkOrder, version.Source
	if origin.workOrder != "" {
		workOrder, source = origin.workOrder, origin.commit
	}
	return askForPlanApproval(tx, planAsk{
		plan: version.PlanID, ref: planFileRef(version.Records), title: record.Title, version: version.Version, digest: version.Digest,
		entries: version.Records, decisions: len(version.Decisions), source: source, workOrder: workOrder, unfinished: missing,
	})
}
