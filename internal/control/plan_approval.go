package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
// holds the approval to the digest the ledger has now, refuses a version that is still a placeholder, decides what the plan drafted as the member's
// decisions, passes the contract review of the work the version covers, approves the merge of the
// planning branch the version came from, and records the estimates and forecasts it showed as the
// baseline. A kill leaves all of that or none of it (FR-PLAN-9, FR-PLAN-10, FR-EST-10).
func approvePlan(tx ports.WriteTx, approval authority.Approval, member ledger.Actor) error {
	if missing := planDetailOf(approval).Unfinished; len(missing) != 0 {
		return fmt.Errorf("%w: the plan is not finished: %s", workorders.ErrInvalid, strings.Join(missing, " "))
	}
	version, err := tx.Planning().PlanVersion(approval.Plan, approval.PlanVersion)
	if errors.Is(err, ports.ErrNotFound) && approval.WorkOrder != "" {
		// The ledger does not hold this version: it is a branch nothing has read, and approving it is
		// accepting the work order whose lane wrote it. Intake finishes the approval when it reads it.
		if err := acceptLanesPlan(tx, approval, member); err != nil {
			return err
		}
		if err := removeConsolidatedDesign(tx, approval, member); err != nil {
			return err
		}
		return baselineApproval(tx, approval, member)
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
	// A request says what its version lacked when it was raised, and one raised before a placeholder was held back
	// says nothing, so the version the ledger holds is read again.
	finish, err := versionFinish(tx, version)
	if err != nil {
		return err
	}
	if missing := planning.Unfinished(finish); len(missing) != 0 {
		return fmt.Errorf("%w: the plan is not finished: %s", workorders.ErrInvalid, strings.Join(missing, " "))
	}
	if err := refuseMovedBranch(tx, approval); err != nil {
		return err
	}
	if err := decideDrafts(tx, version, member); err != nil {
		return err
	}
	if err := closeApprovedRevision(tx, version.PlanID, approval, member); err != nil {
		return err
	}
	if _, err := passCoveredReviews(tx, member); err != nil {
		return err
	}
	// A request that names a work order approves the merge of the commit it names, which is the commit the member read.
	// One whose planning work still waits for acceptance at that commit accepts it first, as approving a branch nothing
	// has read does: Needs you asks for this approval in place of that acceptance, such as for a second branch that
	// proposed the version another branch's accepted commit recorded.
	commit := ""
	if approval.WorkOrder != "" {
		commit = requestCommit(approval)
		if err := acceptNamedPlan(tx, approval, member); err != nil {
			return err
		}
	}
	if err := approvePlanMerge(tx, version, approval, member, commit); err != nil {
		return err
	}
	if err := removeConsolidatedDesign(tx, approval, member); err != nil {
		return err
	}
	return baselineApproval(tx, approval, member)
}

// planDecisionID names the decision a draft becomes in one version of one plan. It is derived, so the
// same approval decided twice makes one decision, and it carries the prefix no other writer may name a
// decision under.
func planDecisionID(plan string, version int64, draft string) string {
	sum := sha256.Sum256([]byte(plan + "\x00" + strconv.FormatInt(version, 10) + "\x00" + draft))
	return questions.PlanDecisionPrefix + hex.EncodeToString(sum[:])[:26]
}

// draftDecision is the decision a draft of one version of a plan becomes, as the member who approved it.
func draftDecision(version planning.PlanVersion, draft planning.DraftDecision, member ledger.Actor) questions.Decision {
	decision := questions.Decision{
		ID: planDecisionID(version.PlanID, version.Version, draft.ID), Status: questions.DecisionDecided,
		Statement: "Q: " + strings.TrimSpace(draft.Question) + " A: " + strings.TrimSpace(draft.Answer),
		Rationale: draft.Rationale, Citations: []string{planVersionCitation(version.PlanID, version.Version)},
		Scope: questions.ScopeOf(planning.SubjectPlan, version.PlanID), Reversibility: questions.Reversible, Categories: []string{questions.CategoryProduct},
		DecidedBy: member.ID,
	}
	if decision.Rationale == "" {
		decision.Rationale = "Drafted in the plan, and approved with it."
	}
	return decision
}

// planVersionCitation is the citation plan approval gives each decision it records, naming the plan version
// the member approved.
func planVersionCitation(plan string, version int64) string {
	return "plan_version:" + planning.ContractKey(plan, version)
}

// draftClash refuses to decide a version's drafts while a decision that is not plan approval's own holds an ID
// one of them derives, or one that is its own holds it with other content, and while a decision that is not its
// own is in force under the ID an earlier version derived for a draft, which approval would carry forward as the
// member's answer. Only plan approval names a decision under the prefix those IDs carry, but a ledger may hold
// one another writer recorded before that held, an imported backup of one included, and deciding over it, or
// beside it, would leave that decision standing as the member's answer whatever it says. The refusal names each
// such decision, who recorded it, and its draft. An overturn changes the decision's status, so it never makes a
// decision under this version's IDs the draft's, and a new version of the plan derives new IDs for its drafts;
// a decision under an earlier version's ID is read by every later version until it is out of force. The same
// content, recorded by plan approval, is the same decision, which a retry of the approval finds.
func draftClash(tx ports.WriteTx, version planning.PlanVersion) error {
	var clashes []string
	earlier := false
	for _, draft := range version.Decisions {
		want := draftDecision(version, draft, ledger.Actor{})
		held, err := tx.Questions().Decision(want.ID)
		switch {
		case errors.Is(err, ports.ErrNotFound):
		case err != nil:
			return err
		default:
			clash, err := heldClash(tx, held, want, version, draft.ID)
			if err != nil {
				return err
			}
			if clash != "" {
				clashes = append(clashes, clash)
			}
		}
		before, at, found, err := earlierPlanDecision(tx.Questions(), version.PlanID, version.Version, draft.ID)
		if err != nil {
			return err
		}
		// A decision that no longer stands is carried forward by nothing, so it clashes with nothing.
		if !found || !before.Status.InForce() {
			continue
		}
		own, err := planOwn(tx, before, version.PlanID, at)
		if err != nil {
			return err
		}
		if !own {
			by, err := decisionAuthor(tx, before.ID)
			if err != nil {
				return err
			}
			clashes = append(clashes, fmt.Sprintf("decision %s, recorded by %s and not by plan approval, is in force under the ID version %d derived for its draft decision %q, and says %q",
				before.ID, by, at, draft.ID, before.Statement))
			earlier = true
		}
	}
	if len(clashes) == 0 {
		return nil
	}
	remedy := "a new version of the plan derives new IDs"
	if earlier {
		remedy += ", and a decision in force under an earlier version's ID holds every later version until a member overturns it"
	}
	return fmt.Errorf("%w: version %d of plan %s drafts decisions whose IDs hold decisions plan approval did not record as its drafts say: %s; "+
		"no approval of this version decides its drafts while those decisions hold their IDs, and %s",
		questions.ErrDecisionClash, version.Version, version.PlanID, strings.Join(clashes, "; "), remedy)
}

// heldClash says how a decision held under the ID a draft of this version derives clashes with what approval
// would record, and is empty for plan approval's own decision with the same content.
func heldClash(tx ports.WriteTx, held, want questions.Decision, version planning.PlanVersion, draft string) (string, error) {
	own, err := planOwn(tx, held, version.PlanID, version.Version)
	if err != nil || (own && held.SameContent(want)) {
		return "", err
	}
	by, err := decisionAuthor(tx, held.ID)
	if err != nil {
		return "", err
	}
	if !own && held.SameContent(want) {
		return fmt.Sprintf("decision %s, recorded by %s and not by plan approval, says what its draft decision %q says", held.ID, by, draft), nil
	}
	return fmt.Sprintf("decision %s, recorded by %s, says %q, where its draft decision %q says %q", held.ID, by, held.Statement, draft, want.Statement), nil
}

// planOwn says a decision held under the ID plan approval derives for a draft of one version of a plan is that
// approval's own: a member recorded it, as both the decision and the event that recorded it say, and it cites
// that version as plan approval does. Anything else under that ID is another writer's, whatever it says.
func planOwn(tx ports.WriteTx, held questions.Decision, plan string, version int64) (bool, error) {
	if !slices.Contains(held.Citations, planVersionCitation(plan, version)) {
		return false, nil
	}
	by, err := decisionRecorder(tx, held.ID)
	if err != nil {
		return false, err
	}
	member, ok := authority.DirectMember(by)
	return ok && member == held.DecidedBy, nil
}

// decisionRecorder is the actor of the event that recorded a decision, and no actor when no event did.
func decisionRecorder(tx ports.WriteTx, id string) (ledger.Actor, error) {
	events, err := tx.EventsAbout(ledger.Subject{Kind: questions.SubjectDecision, ID: id})
	if err != nil {
		return ledger.Actor{}, err
	}
	for _, event := range events {
		if event.Type == questions.EventDecisionRecorded {
			return event.Actor, nil
		}
	}
	return ledger.Actor{}, nil
}

// decisionAuthor names who recorded a decision, from the event that recorded it.
func decisionAuthor(tx ports.WriteTx, id string) (string, error) {
	by, err := decisionRecorder(tx, id)
	if err != nil {
		return "", err
	}
	if by.Kind == "" {
		return "an unknown actor", nil
	}
	return authority.Principal(by), nil
}

// decideDrafts makes each answer a plan drafted a decision of the member who approved it, with a
// citation to the plan version, once no decision draftClash names holds an ID a draft derives. A draft the
// plan already decided the same way in an earlier version stays as it is, and one that changed replaces the
// earlier answer, so one answer to a question is in force. A draft whose earlier decision a decision outside
// the plan replaced or overturned is not decided again while its text is unchanged, and when it changed it is
// decided and supersedes the decision in force (FR-PLAN-13).
func decideDrafts(tx ports.WriteTx, version planning.PlanVersion, member ledger.Actor) error {
	if err := draftClash(tx, version); err != nil {
		return err
	}
	asked := &Questions{}
	for _, draft := range version.Decisions {
		decision := draftDecision(version, draft, member)
		standing, err := standingOf(tx.Questions(), version, draft, decision)
		if err != nil {
			return err
		}
		if standing.settled() {
			continue
		}
		decision.Supersedes = standing.supersedes()
		if _, err := asked.decideInTx(tx, decision, party{Actor: member}); err != nil {
			return err
		}
	}
	return nil
}

// earlierPlanDecision finds the decision under the ID an earlier version of the plan derived for a draft, the
// nearest version's whatever its status, and that version. Only plan approval names a decision under the IDs it
// derives now, but a ledger may hold one another writer recorded there before, which draftClash refuses (planOwn).
func earlierPlanDecision(q ports.QuestionReader, plan string, version int64, draft string) (questions.Decision, int64, bool, error) {
	for earlier := version - 1; earlier >= 1; earlier-- {
		decision, err := q.Decision(planDecisionID(plan, earlier, draft))
		if errors.Is(err, ports.ErrNotFound) {
			continue
		}
		if err != nil {
			return questions.Decision{}, 0, false, err
		}
		return decision, earlier, true, nil
	}
	return questions.Decision{}, 0, false, nil
}

// draftStanding is where a draft of a plan version stands against what earlier versions decided for it.
type draftStanding struct {
	// earlier is the decision the nearest earlier version derived for the draft, when one did.
	earlier questions.Decision
	found   bool
	// unchanged says the draft's text is the earlier decision's.
	unchanged bool
	// inForce is the decision standing for the draft: the earlier one while it is in force, and when it ended the
	// one that took its place. It is empty when the ledger holds none.
	inForce questions.Decision
	// held says a decision stands for the draft.
	held bool
}

// ended says the earlier decision no longer stands.
func (s draftStanding) ended() bool { return s.found && !s.earlier.Status.InForce() }

// settled says approval records nothing for the draft: an earlier version's decision stands for it, or one outside
// the plan replaced it and the draft says what the earlier decision said.
func (s draftStanding) settled() bool { return s.found && s.unchanged }

// supersedes is the decision a decision recorded for the draft replaces: the one in force.
func (s draftStanding) supersedes() string {
	if s.held {
		return s.inForce.ID
	}
	return ""
}

// standingOf reads where a draft stands. The draft's text is unchanged when it makes the statement and the
// rationale of the earlier decision, which is the decision it would make now.
func standingOf(q ports.QuestionReader, version planning.PlanVersion, draft planning.DraftDecision, decision questions.Decision) (draftStanding, error) {
	earlier, _, found, err := earlierPlanDecision(q, version.PlanID, version.Version, draft.ID)
	if err != nil || !found || (decidesReplacedDrafts() && !earlier.Status.InForce()) {
		return draftStanding{}, err
	}
	standing := draftStanding{
		earlier: earlier, found: true,
		unchanged: earlier.Statement == decision.Statement && earlier.Rationale == decision.Rationale,
	}
	standing.inForce, standing.held, err = inForceFrom(q, earlier)
	return standing, err
}

// inForceFrom follows a decision to the one that stands in its place: the decision itself when it is in force, and
// otherwise the decision that superseded it, and the one that superseded that. A decision that ends is superseded by
// exactly one, so the chain ends at a decision in force, or at none when the ledger holds no successor.
func inForceFrom(q ports.QuestionReader, decision questions.Decision) (questions.Decision, bool, error) {
	seen := map[string]bool{}
	for !seen[decision.ID] {
		if decision.Status.InForce() {
			return decision, true, nil
		}
		seen[decision.ID] = true
		next, err := q.ReplacedBy(decision.ID)
		if errors.Is(err, ports.ErrNotFound) {
			return questions.Decision{}, false, nil
		}
		if err != nil {
			return questions.Decision{}, false, err
		}
		decision = next
	}
	return questions.Decision{}, false, nil
}

// ReplacedDrafts lists the drafts of one version of a plan that approval will not decide again, because a decision
// outside the plan replaced or overturned what an earlier version decided for them and their text has not changed,
// each with the decision in force. It is the comparison a member reads before approving, and it reads the ledger as
// approval will (FR-PLAN-13).
func ReplacedDrafts(q ports.QuestionReader, plan string, version int64, drafts []planning.DraftDecision) ([]questions.ReplacedDraft, error) {
	at := planning.PlanVersion{PlanID: plan, Version: version}
	var replaced []questions.ReplacedDraft
	for _, draft := range drafts {
		standing, err := standingOf(q, at, draft, draftDecision(at, draft, ledger.Actor{}))
		if err != nil {
			return nil, err
		}
		if !standing.ended() || !standing.unchanged {
			continue
		}
		replaced = append(replaced, questions.ReplacedDraft{
			Draft: draft.ID, Decision: standing.earlier.ID, Status: standing.earlier.Status,
			InForce: standing.inForce.ID, Statement: standing.inForce.Statement,
		})
	}
	return replaced, nil
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
	current, err := PlanApprovalIsCurrent(tx.Evidence(), order, approval)
	if err != nil {
		return err
	}
	if !current {
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

// acceptNamedPlan accepts the `plan` work order a request for a version the ledger holds names, when it waits for
// acceptance, as acceptLanesPlan does for a branch nothing has read. A request that names no such work order accepts
// nothing.
func acceptNamedPlan(tx ports.WriteTx, approval authority.Approval, member ledger.Actor) error {
	order, err := tx.WorkOrders().Get(approval.WorkOrder)
	if errors.Is(err, ports.ErrNotFound) {
		return nil
	}
	if err != nil || order.Kind != workorders.KindPlan || order.State != workorders.Acceptance {
		return err
	}
	return acceptLanesPlan(tx, approval, member)
}

// PlanApprovalIsCurrent reports whether a request to approve a plan was raised for the commit a work order's
// branch holds in its current attempt, which approving it needs to accept the work order.
func PlanApprovalIsCurrent(evidence ports.EvidenceReader, order workorders.WorkOrder, approval authority.Approval) (bool, error) {
	commit, err := AttemptCommit(evidence, order)
	return commit != "" && commit == planDetailOf(approval).Source, err
}

// RequestBranch names the planning branch a plan request is about: the work order it names, the branch that work order
// keeps or its current attempt left, and the commit the request was raised for. A request that names no work order, or
// one the ledger does not hold, names none.
func RequestBranch(orders ports.WorkOrderReader, evidence ports.EvidenceReader, approval authority.Approval) (*planning.PlanBranch, error) {
	if approval.WorkOrder == "" {
		return nil, nil
	}
	order, err := orders.Get(approval.WorkOrder)
	if errors.Is(err, ports.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	artifacts, err := evidence.Artifacts(order.ID)
	if err != nil {
		return nil, err
	}
	branch := order.Branch
	if branch == "" {
		branch = attemptBranch(artifacts, order.Fence).URI
	}
	return &planning.PlanBranch{WorkOrder: order.ID, Branch: branch, Commit: requestCommit(approval)}, nil
}

// FinishingBranch names the planning branch whose commit finishes the placeholder version a request holds back, while
// that branch's work order waits for acceptance at that commit, which is when Needs you asks for its acceptance beside
// the request. A request no branch finishes, and one whose branch was sent back, accepted, or moved on, names none.
func FinishingBranch(orders ports.WorkOrderReader, evidence ports.EvidenceReader, approval authority.Approval) (*planning.PlanBranch, error) {
	finishing := planDetailOf(approval).FinishedBy
	if finishing == nil || len(planDetailOf(approval).Unfinished) == 0 {
		return nil, nil
	}
	order, err := orders.Get(finishing.WorkOrder)
	if errors.Is(err, ports.ErrNotFound) {
		return nil, nil
	}
	if err != nil || order.Kind != workorders.KindPlan || order.State != workorders.Acceptance {
		return nil, err
	}
	commit, err := AttemptCommit(evidence, order)
	if err != nil || commit != finishing.Commit {
		return nil, err
	}
	return finishing, nil
}

// AttemptCommit is the commit a work order's branch held when its current attempt last reported it: the one a
// member accepts, and empty when the attempt reported no branch.
func AttemptCommit(evidence ports.EvidenceReader, order workorders.WorkOrder) (string, error) {
	artifacts, err := evidence.Artifacts(order.ID)
	if err != nil {
		return "", err
	}
	return attemptBranch(artifacts, order.Fence).Digest, nil
}

// approvedBeforeRecorded finds the approval a member gave to the branch that this version came from, if
// intake read the same files at the commit that was approved: the request is bound to the version's
// digest and to the commit, and no version has claimed it. The same files at another commit are another
// approval, because the commit may hold more than the plan, which is what the merge takes in.
func approvedBeforeRecorded(tx ports.WriteTx, version planning.PlanVersion, ref, commit string) (authority.Approval, bool, error) {
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
			row.SubjectDigest == version.Digest && planDetailOf(row).Ref == ref && requestCommit(row) == commit {
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
	if err := closeApprovedRevision(tx, version.PlanID, approval, member); err != nil {
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
