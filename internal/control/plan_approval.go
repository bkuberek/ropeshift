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
// decisions, and passes the contract review of the work the version covers. A kill leaves all of
// that or none of it (FR-PLAN-9, FR-PLAN-10).
func approvePlan(tx ports.WriteTx, approval authority.Approval, member ledger.Actor) error {
	version, err := tx.Planning().PlanVersion(approval.Plan, approval.PlanVersion)
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
	_, err = passCoveredReviews(tx, member)
	return err
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

// planCoverage maps each contract version to the plan approval that covers it: one in a state given, for
// a plan version that lists this contract at this digest. An approved request is the one cited when
// more than one covers it.
func planCoverage(approvals ports.ApprovalReader, planned ports.PlanningReader, states ...authority.ApprovalState) (map[coverKey]authority.Approval, error) {
	rows, err := approvals.All()
	if err != nil {
		return nil, err
	}
	covered := map[coverKey]authority.Approval{}
	for _, row := range rows {
		if row.Kind != authority.KindPlan || row.Plan == "" || !stateIn(row.State, states) {
			continue
		}
		version, err := planned.PlanVersion(row.Plan, row.PlanVersion)
		if errors.Is(err, ports.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range version.Records {
			if planning.EntryKind(entry.Ref) != "contract" || entry.Record == "" {
				continue
			}
			key := coverKey{contract: entry.Record, digest: entry.Digest}
			if held, taken := covered[key]; !taken || (row.State == authority.ApprovalApproved && held.State != authority.ApprovalApproved) ||
				(row.State == held.State && row.PlanVersion > held.PlanVersion) {
				covered[key] = row
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

// coveredByApproval says which approved plan version covers a work order's contract version, if any.
func coveredByApproval(tx ports.WriteTx, order workorders.WorkOrder, covered map[coverKey]authority.Approval) (authority.Approval, bool, error) {
	if order.State != workorders.ContractReview || order.ReviewedBy != "" || order.ContractVersion == 0 ||
		(order.Kind != workorders.KindChange && order.Kind != workorders.KindRework) {
		return authority.Approval{}, false, nil
	}
	contract, err := tx.Planning().Contract(order.ContractID, order.ContractVersion)
	if err != nil {
		return authority.Approval{}, false, err
	}
	approval, held := covered[coverKey{contract: contract.ID, digest: contract.Digest}]
	return approval, held, nil
}

// passCoveredReviews passes the contract review of every work order whose contract version an approved
// plan version covers, with the member who approved that version as the reviewer, and runs the
// definition of ready on each. The review still collects every question, and a question still open, a
// dependency, or an untraced criterion keeps the work in review with the pass on record. It is
// idempotent, and it returns the work orders it passed.
func passCoveredReviews(tx ports.WriteTx, actor ledger.Actor) ([]string, error) {
	covered, err := planCoverage(tx.Approvals(), tx.Planning(), authority.ApprovalApproved)
	if err != nil || len(covered) == 0 {
		return nil, err
	}
	orders, err := tx.WorkOrders().List()
	if err != nil {
		return nil, err
	}
	var passed []string
	for _, order := range orders {
		approval, held, err := coveredByApproval(tx, order, covered)
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
			covered, err := planCoverage(tx.Approvals(), tx.Planning(), authority.ApprovalApproved)
			if err != nil {
				return err
			}
			approval, held, err := coveredByApproval(tx, order, covered)
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
