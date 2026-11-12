package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// approvePlan is what approving a plan version does, in the transaction that records the approval. It
// holds the approval to the digest the ledger has now, decides what the plan drafted as the member's
// decisions, passes the contract review of the work the version covers, and approves the merge of the
// planning branch the version came from. A kill leaves all of that or none of it (FR-PLAN-9, FR-PLAN-10).
func approvePlan(tx ports.WriteTx, approval authority.Approval, member ledger.Actor) error {
	version, err := tx.Planning().PlanVersion(approval.Plan, approval.PlanVersion)
	if errors.Is(err, ports.ErrNotFound) && approval.WorkOrder != "" {
		// The ledger does not hold this version: it is a branch nothing has read, and approving it is
		// accepting the work order whose lane wrote it. Intake finishes the approval when it reads it.
		return acceptLanesPlan(tx, approval, member)
	}
	if err != nil {
		return err
	}
	if version.Digest != approval.SubjectDigest {
		return fmt.Errorf("%w: version %d of the plan is %s", authority.ErrStaleDigest, version.Version, version.Digest)
	}
	// What a member saw is what the ledger holds now. A record that changed since the version was made
	// has a version of its own coming, and it needs its own approval.
	native, err := readNativeIDs(tx.Planning())
	if err != nil {
		return err
	}
	current, _, err := planDigestOf(tx.Planning(), native, approval.Plan, nil)
	if err != nil {
		return err
	}
	if current != approval.SubjectDigest {
		return fmt.Errorf("%w: the plan is now %s", authority.ErrStaleDigest, current)
	}
	if err := decideDrafts(tx, version, member); err != nil {
		return err
	}
	if _, err := passCoveredReviews(tx, member); err != nil {
		return err
	}
	return approvePlanMerge(tx, version, approval, member, "")
}

// planDecisionID names the decision a draft becomes in one version of one plan. It is derived, so the
// same approval decided twice makes one decision.
func planDecisionID(plan string, version int64, draft string) string {
	sum := sha256.Sum256([]byte(plan + "\x00" + strconv.FormatInt(version, 10) + "\x00" + draft))
	return "pd-" + hex.EncodeToString(sum[:])[:26]
}

// decideDrafts makes each answer a plan drafted a decision of the member who approved it, with a
// citation to the plan version. A draft the plan already decided the same way in an earlier version stays
// as it is, and one that changed replaces the earlier answer, so one answer to a question is in force.
func decideDrafts(tx ports.WriteTx, version planning.PlanVersion, member ledger.Actor) error {
	asked := &Questions{}
	for _, draft := range version.Decisions {
		decision := questions.Decision{
			ID: planDecisionID(version.PlanID, version.Version, draft.ID), Status: questions.DecisionDecided,
			Statement: "Q: " + strings.TrimSpace(draft.Question) + " A: " + strings.TrimSpace(draft.Answer),
			Rationale: draft.Rationale, Citations: []string{"plan_version:" + planning.ContractKey(version.PlanID, version.Version)},
			Scope: "plan:" + version.PlanID, Reversibility: questions.Reversible, Categories: []string{questions.CategoryProduct},
			DecidedBy: member.ID,
		}
		if decision.Rationale == "" {
			decision.Rationale = "Drafted in the plan, and approved with it."
		}
		earlier, found, err := earlierPlanDecision(tx, version, draft.ID)
		if err != nil {
			return err
		}
		if found && earlier.Statement == decision.Statement && earlier.Rationale == decision.Rationale {
			continue
		}
		if found {
			decision.Supersedes = earlier.ID
		}
		if _, err := asked.decideInTx(tx, decision, member); err != nil {
			return err
		}
	}
	return nil
}

// earlierPlanDecision finds the decision in force that an earlier version of the plan made of a draft.
func earlierPlanDecision(tx ports.WriteTx, version planning.PlanVersion, draft string) (questions.Decision, bool, error) {
	for earlier := version.Version - 1; earlier >= 1; earlier-- {
		decision, err := tx.Questions().Decision(planDecisionID(version.PlanID, earlier, draft))
		if errors.Is(err, ports.ErrNotFound) {
			continue
		}
		if err != nil {
			return questions.Decision{}, false, err
		}
		if decision.Status.InForce() {
			return decision, true, nil
		}
	}
	return questions.Decision{}, false, nil
}

// coverKey names a contract version by the record and the digest of its file.
type coverKey struct{ contract, digest string }

// Coverage says which plan approvals cover which contract versions.
type Coverage struct {
	held map[coverKey]authority.Approval
}

// Of finds the plan approval that covers a contract version, if one does.
func (c Coverage) Of(contract planning.Contract) (authority.Approval, bool) {
	approval, held := c.held[coverKey{contract: contract.ID, digest: contract.Digest}]
	return approval, held
}

// Empty says nothing is covered.
func (c Coverage) Empty() bool { return len(c.held) == 0 }

// ReadCoverage maps each contract version to the plan approval that covers it: one in a state given, for
// a plan version that lists this contract at this digest. An approved request is the one cited when more
// than one covers it, and the newest version when they stand alike.
func ReadCoverage(approvals ports.ApprovalReader, planned ports.PlanningReader, states ...authority.ApprovalState) (Coverage, error) {
	rows, err := approvals.All()
	if err != nil {
		return Coverage{}, err
	}
	covered := Coverage{held: map[coverKey]authority.Approval{}}
	for _, row := range rows {
		if row.Kind != authority.KindPlan || row.Plan == "" || !stateIn(row.State, states) {
			continue
		}
		version, err := planned.PlanVersion(row.Plan, row.PlanVersion)
		if errors.Is(err, ports.ErrNotFound) {
			continue
		}
		if err != nil {
			return Coverage{}, err
		}
		if version.Digest != row.SubjectDigest {
			// The version is not the one this request was bound to, so nothing it says is covered.
			continue
		}
		for _, entry := range version.Records {
			if planning.EntryKind(entry.Ref) != "contract" || entry.Record == "" {
				continue
			}
			key := coverKey{contract: entry.Record, digest: entry.Digest}
			if held, taken := covered.held[key]; !taken || (row.State == authority.ApprovalApproved && held.State != authority.ApprovalApproved) ||
				(row.State == held.State && row.PlanVersion > held.PlanVersion) {
				covered.held[key] = row
			}
		}
	}
	return covered, nil
}

func stateIn(state authority.ApprovalState, states []authority.ApprovalState) bool {
	for _, s := range states {
		if s == state {
			return true
		}
	}
	return false
}

// reviewable says a work order waits for a contract review that nobody has given and that a plan could
// give: work of a contract version, in review.
func reviewable(order workorders.WorkOrder) bool {
	return order.State == workorders.ContractReview && order.ReviewedBy == "" && order.ContractVersion != 0 &&
		(order.Kind == workorders.KindChange || order.Kind == workorders.KindRework)
}

// coveredByApproval says which approved plan version covers a work order's contract version, if any.
func coveredByApproval(planned ports.PlanningReader, order workorders.WorkOrder, covered Coverage) (authority.Approval, bool, error) {
	if !reviewable(order) {
		return authority.Approval{}, false, nil
	}
	contract, err := planned.Contract(order.ContractID, order.ContractVersion)
	if err != nil {
		return authority.Approval{}, false, err
	}
	approval, held := covered.Of(contract)
	return approval, held, nil
}

// WaitsOnAMember says a work order waits for a member's contract review because no plan approval, decided
// or open, stands behind its contract version. One that a pending approval covers waits for that
// approval instead, which is what the member is asked for.
func WaitsOnAMember(planned ports.PlanningReader, order workorders.WorkOrder, covered Coverage) (bool, error) {
	if !reviewable(order) {
		return false, nil
	}
	_, held, err := coveredByApproval(planned, order, covered)
	return !held, err
}

// passCoveredReviews passes the contract review of every work order whose contract version an approved
// plan version covers, with the member who approved that version as the reviewer, and runs the
// definition of ready on each. The review still collects every question, and a question still open, a
// dependency, or an untraced criterion keeps the work in review with the pass on record. It is
// idempotent, and it returns the work orders it passed.
func passCoveredReviews(tx ports.WriteTx, actor ledger.Actor) ([]string, error) {
	covered, err := ReadCoverage(tx.Approvals(), tx.Planning(), authority.ApprovalApproved)
	if err != nil || covered.Empty() {
		return nil, err
	}
	orders, err := tx.WorkOrders().List()
	if err != nil {
		return nil, err
	}
	var passed []string
	for _, order := range orders {
		approval, held, err := coveredByApproval(tx.Planning(), order, covered)
		if err != nil {
			return passed, err
		}
		if !held {
			continue
		}
		if err := recordCoveredReview(tx, order.ID, approval); err != nil {
			return passed, err
		}
		if _, err := settleReview(tx, order, actor); err != nil {
			return passed, err
		}
		passed = append(passed, order.ID)
	}
	return passed, nil
}

// coveredReview is what a review that a plan approval passed cites.
type coveredReview struct {
	Approval string `json:"plan_approval"`
	Plan     string `json:"plan"`
	Version  int64  `json:"plan_version"`
	Digest   string `json:"digest"`
}

// recordCoveredReview records a review as the member who approved the plan version passing it. The
// event names that member, because the review is theirs: no tick and no agent holds the authority to
// pass one, and this holds the member's decision to the work it covers.
func recordCoveredReview(tx ports.WriteTx, id string, approval authority.Approval) error {
	reviewer := ledger.Actor{Kind: ledger.ActorMember, ID: strings.TrimPrefix(approval.DecidedBy, string(ledger.ActorMember)+":")}
	if err := tx.WorkOrders().Reviewed(id, reviewer.ID); err != nil {
		return err
	}
	cite, err := json.Marshal(coveredReview{Approval: approval.ID, Plan: approval.Plan, Version: approval.PlanVersion, Digest: approval.SubjectDigest})
	if err != nil {
		return err
	}
	_, err = tx.Append(ledger.EventDraft{
		Type: workorders.EventReviewPassed, Actor: reviewer, Subject: ledger.Subject{Kind: workorders.SubjectKind, ID: id}, Data: cite,
	})
	return err
}

// PassCovered passes the contract review of work an approved plan version covers, for a tick. Each work
// order is a transaction of its own that reads it again, so a work order a member reviewed in the
// meantime is left alone.
func (r *Reviews) PassCovered(ctx context.Context, scope ledger.WorkspaceID, actor ledger.Actor) ([]string, error) {
	var waiting []string
	err := r.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		rows, err := tx.WorkOrders().List()
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.State == workorders.ContractReview && row.ReviewedBy == "" && row.ContractVersion != 0 {
				waiting = append(waiting, row.ID)
			}
		}
		return nil
	})
	if err != nil || len(waiting) == 0 {
		return nil, err
	}
	var passed []string
	for _, id := range waiting {
		err := r.store.Write(ctx, scope, "review.pass_covered", func(tx ports.WriteTx) error {
			order, err := tx.WorkOrders().Get(id)
			if err != nil {
				return err
			}
			covered, err := ReadCoverage(tx.Approvals(), tx.Planning(), authority.ApprovalApproved)
			if err != nil {
				return err
			}
			approval, held, err := coveredByApproval(tx.Planning(), order, covered)
			if err != nil || !held {
				return err
			}
			if err := recordCoveredReview(tx, id, approval); err != nil {
				return err
			}
			if _, err := settleReview(tx, order, actor); err != nil {
				return err
			}
			passed = append(passed, id)
			return nil
		})
		if err != nil {
			return passed, err
		}
	}
	return passed, nil
}

// acceptLanesPlan is what approving a branch nothing has read does: it accepts the `plan` work order
// whose lane wrote it, at the commit the member was shown. Intake reads that commit on its next tick,
// the way it reads any accepted plan (D17), and finishes the approval then, if what it records is what
// the member approved.
func acceptLanesPlan(tx ports.WriteTx, approval authority.Approval, member ledger.Actor) error {
	order, err := tx.WorkOrders().Get(approval.WorkOrder)
	if err != nil {
		return err
	}
	if order.State != workorders.Acceptance {
		return nil
	}
	artifacts, err := tx.Evidence().Artifacts(order.ID)
	if err != nil {
		return err
	}
	if branch := attemptBranch(artifacts, order.Fence); branch.Digest == "" || branch.Digest != planDetailOf(approval).Source {
		return fmt.Errorf("%w: the lane's branch is no longer the one the member was shown", authority.ErrStaleDigest)
	}
	assessment, err := assessAttempt(tx.Planning(), tx.Evidence(), order)
	if err != nil {
		return err
	}
	if len(assessment.Failed) != 0 {
		return fmt.Errorf("%w: %s", workorders.ErrNotAcceptable, strings.Join(assessment.Failed, ", "))
	}
	return accept(tx, order.ID, assessment, member, func() {})
}

// approvedBeforeRecorded finds the approval a member gave to the branch that this version came from, if
// intake read the same files at the commit that was approved: the request is bound to the version's
// digest, and no version has claimed it.
func approvedBeforeRecorded(tx ports.WriteTx, version planning.PlanVersion, ref string) (authority.Approval, bool, error) {
	if version.WorkOrder == "" {
		return authority.Approval{}, false, nil
	}
	rows, err := tx.Approvals().OfWorkOrder(version.WorkOrder)
	if err != nil {
		return authority.Approval{}, false, err
	}
	for _, row := range rows {
		named := row.Plan == "" || (row.Plan == version.PlanID && row.PlanVersion == version.Version)
		if row.Kind == authority.KindPlan && row.State == authority.ApprovalApproved && named &&
			row.SubjectDigest == version.Digest && planDetailOf(row).Ref == ref {
			return row, true, nil
		}
	}
	return authority.Approval{}, false, nil
}

// finishApproved completes the approval of a branch once the ledger holds what it approved, read at commit:
// the request names its version, the drafts are decided as the approving member's, the review of the work
// the version covers passes, and the branch's merge is approved. The ledger holds the records because the
// member accepted the commit, so no word of the lane's reached them before.
func finishApproved(tx ports.WriteTx, version planning.PlanVersion, approval authority.Approval, commit string) error {
	member := ledger.Actor{Kind: ledger.ActorMember, ID: strings.TrimPrefix(approval.DecidedBy, string(ledger.ActorMember)+":")}
	if approval.Plan == "" {
		linked, err := tx.Approvals().LinkPlan(approval.ID, version.PlanID, version.Version)
		if err != nil || !linked {
			return err
		}
		approval.Plan, approval.PlanVersion = version.PlanID, version.Version
		event, err := authority.ApprovalEvent(approval, member)
		if err != nil {
			return err
		}
		if _, err := tx.Append(event); err != nil {
			return err
		}
	}
	if err := decideDrafts(tx, version, member); err != nil {
		return err
	}
	if _, err := passCoveredReviews(tx, member); err != nil {
		return err
	}
	return approvePlanMerge(tx, version, approval, member, commit)
}

// voidUnclaimed closes the approvals of a branch that no version of the ledger claimed, because intake
// read other files at the commit than the ones the member approved. A decision is void when the
// artifact it approved changed.
func voidUnclaimed(tx ports.WriteTx, workOrder string) error {
	rows, err := tx.Approvals().OfWorkOrder(workOrder)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Kind != authority.KindPlan || row.State != authority.ApprovalApproved {
			continue
		}
		if row.Plan != "" {
			version, err := tx.Planning().PlanVersion(row.Plan, row.PlanVersion)
			if err == nil && version.Digest == row.SubjectDigest {
				continue
			}
			if err != nil && !errors.Is(err, ports.ErrNotFound) {
				return err
			}
		}
		next := row
		next.State = authority.ApprovalVoid
		if err := noteApproval(tx, next, row.State, intakeActor); err != nil {
			return err
		}
	}
	return nil
}
