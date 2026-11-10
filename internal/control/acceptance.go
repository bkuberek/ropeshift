package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Acceptances holds the step from `acceptance` to `done`, or back to `ready`. A lane cannot
// accept its own work, because that would be an agent approving its own request, so each
// step compares the contract's criteria with evidence the lane did not write by itself.
type Acceptances struct {
	store ports.Store
}

// NewAcceptances returns the use cases over a Store.
func NewAcceptances(store ports.Store) *Acceptances {
	return &Acceptances{store: store}
}

// Standing reads what a work order's evidence leaves to do, with everything linked to it.
func (a *Acceptances) Standing(ctx context.Context, scope ledger.WorkspaceID, id string) (workorders.AcceptanceView, error) {
	var view workorders.AcceptanceView
	err := a.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		view, err = StandingIn(tx, id)
		return err
	})
	return view, err
}

// StandingIn reads where a work order stands inside a read, so a read model sees one moment.
func StandingIn(tx ports.ReadTx, id string) (workorders.AcceptanceView, error) {
	current, err := tx.WorkOrders().Get(id)
	if err != nil {
		return workorders.AcceptanceView{}, err
	}
	at, err := readAttempt(tx.Planning(), tx.Evidence(), tx.Repositories(), current)
	if err != nil {
		return workorders.AcceptanceView{}, err
	}
	reviews, err := tx.WorkOrders().ReviewsOf(id)
	if err != nil {
		return workorders.AcceptanceView{}, err
	}
	artifacts, err := tx.Evidence().Artifacts(id)
	if err != nil {
		return workorders.AcceptanceView{}, err
	}
	checks, err := tx.Evidence().Checks(id)
	if err != nil {
		return workorders.AcceptanceView{}, err
	}
	view := workorders.AcceptanceView{
		WorkOrder: id, State: current.State, Assessment: at.assessment,
		Artifacts: append([]ledger.Artifact{}, artifacts...), Checks: append([]workorders.Evidence{}, checks...),
		Verdicts: at.verdicts, Rounds: at.rounds, Authority: at.authority,
	}
	if current.State == workorders.Acceptance {
		view.Why = at.judgement.Why
	}
	for _, review := range reviews {
		view.Reviews = append(view.Reviews, workorders.ReviewLine{ID: review.ID, State: review.State, Attempt: review.ReviewsFence})
	}
	return view, nil
}

// attemptStanding is where one attempt stands: what its checks say, and the verdicts that speak for it.
type attemptStanding struct {
	assessment workorders.Assessment
	// verdicts are the ones bound to this attempt and the commit it left, in the order of the contract.
	verdicts []workorders.Verdict
	// rounds is how many reviews have judged this work order, whichever attempt they read.
	rounds    int64
	authority ledger.MergeAuthority
	judgement workorders.Judgement
}

// readAttempt reads an attempt's evidence and verdicts, and what they settle at the repository's merge
// authority. A work order whose repository has no record is at the default level.
func readAttempt(planned ports.PlanningReader, evidence ports.EvidenceReader, repositories ports.RepositoryReader, w workorders.WorkOrder) (attemptStanding, error) {
	criteria, err := criteriaOf(planned, w)
	if err != nil {
		return attemptStanding{}, err
	}
	rows, err := evidence.Attempt(w.ID, w.Fence)
	if err != nil {
		return attemptStanding{}, err
	}
	at := attemptStanding{assessment: workorders.Assess(criteria, rows), authority: ledger.PersonMerges}
	artifacts, err := evidence.Artifacts(w.ID)
	if err != nil {
		return attemptStanding{}, err
	}
	all, err := evidence.Verdicts(w.ID)
	if err != nil {
		return attemptStanding{}, err
	}
	at.rounds = workorders.Rounds(all)
	at.verdicts = workorders.Counted(all, w.Fence, attemptBranch(artifacts, w.Fence).Digest)
	slices.SortStableFunc(at.verdicts, func(a, b workorders.Verdict) int {
		return slices.Index(criteria, a.Criterion) - slices.Index(criteria, b.Criterion)
	})
	var bound int64
	for _, v := range at.verdicts {
		bound = max(bound, v.MaxRounds)
	}
	if w.RepositoryID != "" {
		record, err := repositories.Repository(w.RepositoryID)
		switch {
		case err == nil:
			at.authority = record.Level()
		case !errors.Is(err, ports.ErrNotFound):
			return attemptStanding{}, err
		}
	}
	at.judgement = workorders.Judge(workorders.JudgeInput{
		Criteria: criteria, Verdicts: at.verdicts, Assessment: at.assessment, Authority: at.authority, Rounds: at.rounds, MaxRounds: bound,
	})
	return at, nil
}

// criteriaOf lists the IDs of the criteria a work order's pinned contract version holds.
func criteriaOf(planned ports.PlanningReader, w workorders.WorkOrder) ([]string, error) {
	if w.ContractVersion == 0 {
		return nil, nil
	}
	contract, err := planned.Contract(w.ContractID, w.ContractVersion)
	if err != nil {
		return nil, err
	}
	criteria := make([]string, 0, len(contract.Criteria))
	for _, criterion := range contract.Criteria {
		criteria = append(criteria, criterion.ID)
	}
	return criteria, nil
}

// assessAttempt compares the pinned contract with the current attempt's evidence.
func assessAttempt(planned ports.PlanningReader, evidence ports.EvidenceReader, w workorders.WorkOrder) (workorders.Assessment, error) {
	criteria, err := criteriaOf(planned, w)
	if err != nil {
		return workorders.Assessment{}, err
	}
	rows, err := evidence.Attempt(w.ID, w.Fence)
	if err != nil {
		return workorders.Assessment{}, err
	}
	return workorders.Assess(criteria, rows), nil
}

// acceptAction names what an assessment asks of whoever accepts.
func acceptAction(needs workorders.Need) authority.Action {
	switch needs {
	case workorders.NeedNothing:
		return authority.ActionAccept
	case workorders.NeedSecondPrincipal:
		return authority.ActionAcceptLaneChecks
	}
	return authority.ActionAcceptUnchecked
}

// Accept moves accepted work to `done`, once the caller may answer for what the evidence
// leaves open. A failed check cannot be accepted, whoever asks. A repeated acceptance changes
// nothing, and ropeshift does not merge: a branch artifact ends the work (D17).
func (a *Acceptances) Accept(ctx context.Context, scope ledger.WorkspaceID, id string, actor ledger.Actor) (workorders.State, error) {
	state := workorders.Done
	err := a.store.Write(ctx, scope, "acceptance.accept", func(tx ports.WriteTx) error {
		current, err := tx.WorkOrders().Get(id)
		if err != nil {
			return err
		}
		assessment, err := assessAttempt(tx.Planning(), tx.Evidence(), current)
		if err != nil {
			return err
		}
		if !authority.Allowed(actor, acceptAction(assessment.Needs)) {
			return workorders.ErrForbidden
		}
		if current.State == workorders.Done {
			return nil
		}
		if len(assessment.Failed) != 0 {
			return fmt.Errorf("%w: %s", workorders.ErrNotAcceptable, strings.Join(assessment.Failed, ", "))
		}
		if err := endOpenReviews(tx, id, actor); err != nil {
			return err
		}
		return accept(tx, id, assessment, actor, func() { crashpoint.Hit("acceptance.accept", "moved") })
	})
	if err != nil {
		return "", err
	}
	return state, nil
}

func accept(tx ports.WriteTx, id string, assessment workorders.Assessment, actor ledger.Actor, moved func()) error {
	return acceptCiting(tx, id, assessment, "", actor, moved)
}

// acceptedOn is the payload of an acceptance: the assessment it rested on, and the review whose verdicts
// stood for a member's word when one did.
type acceptedOn struct {
	workorders.Assessment
	Review string `json:"review_id,omitempty"`
}

func acceptCiting(tx ports.WriteTx, id string, assessment workorders.Assessment, review string, actor ledger.Actor, moved func()) error {
	payload, err := json.Marshal(acceptedOn{Assessment: assessment, Review: review})
	if err != nil {
		return err
	}
	if err := leaveAcceptance(tx, id, workorders.Change{Trigger: workorders.TriggerAcceptancePassed, Payload: payload}, actor, moved); err != nil {
		return err
	}
	if err := passPlanReview(tx, id, actor); err != nil {
		return err
	}
	_, err = fireWakes(tx, "done:"+id, actor, parking.WorkOrderDone)
	return err
}

// leaveAcceptance takes one edge out of `acceptance`. The compare-and-set repeats the state
// read, so a racing move loses instead of moving the work twice. moved names the caller's
// crash step, which has to be a literal for the matrix to find it.
func leaveAcceptance(tx ports.WriteTx, id string, change workorders.Change, actor ledger.Actor, moved func()) error {
	current, err := tx.WorkOrders().Get(id)
	if err != nil {
		return err
	}
	edge, err := workorders.Move(current, change.Trigger)
	if err != nil {
		return err
	}
	won, err := tx.WorkOrders().Move(id, edge.From, edge.To)
	if err != nil {
		return err
	}
	if !won {
		return workorders.ErrIllegalMove
	}
	moved()
	event, err := workorders.ChangeEvent(workorders.EventType(change.Trigger), id, change, actor)
	if err != nil {
		return err
	}
	_, err = tx.Append(event)
	return err
}

// Rework sends completed work back for another attempt. The evidence of this one stays linked
// to the work order and stops counting, because the next lane's end moves the fence.
func (a *Acceptances) Rework(ctx context.Context, scope ledger.WorkspaceID, id, reason string, actor ledger.Actor) (workorders.State, error) {
	if !authority.Allowed(actor, authority.ActionRework) {
		return "", workorders.ErrForbidden
	}
	if strings.TrimSpace(reason) == "" {
		return "", fmt.Errorf("%w: a rework needs a reason", workorders.ErrInvalid)
	}
	var state workorders.State
	err := a.store.Write(ctx, scope, "acceptance.rework", func(tx ports.WriteTx) error {
		if err := endOpenReviews(tx, id, actor); err != nil {
			return err
		}
		var err error
		state, err = rework(tx, id, reason, actor, func() { crashpoint.Hit("acceptance.rework", "moved") })
		return err
	})
	if err != nil {
		return "", err
	}
	return state, nil
}

// rework runs the definition of ready, as every move into `ready` does, so a question that
// opened during the attempt holds the work back in `contract_review`.
func rework(tx ports.WriteTx, id, reason string, actor ledger.Actor, moved func()) (workorders.State, error) {
	return reworkFor(tx, id, workorders.ReworkNote{Reason: reason}, nil, actor, moved)
}

// reworkedOn is the payload of a rework a review decided: the review, and each criterion it failed with
// what it said, so the audit reads without the verdicts table.
type reworkedOn struct {
	Review string          `json:"review_id"`
	Failed []reworkFinding `json:"failed,omitempty"`
}

type reworkFinding struct {
	Criterion string   `json:"criterion"`
	Reasons   string   `json:"reasons"`
	Citations []string `json:"citations,omitempty"`
}

// reworkFor sends completed work back, and keeps why with the work order, for the brief of the attempt that
// follows. A note that names a review carries the criteria that review failed.
func reworkFor(tx ports.WriteTx, id string, note workorders.ReworkNote, failed []workorders.Verdict, actor ledger.Actor, moved func()) (workorders.State, error) {
	current, err := tx.WorkOrders().Get(id)
	if err != nil {
		return "", err
	}
	change := workorders.Change{Trigger: workorders.TriggerRework, Reason: note.Reason}
	if note.Review != "" {
		on := reworkedOn{Review: note.Review}
		for _, v := range failed {
			on.Failed = append(on.Failed, reworkFinding{Criterion: v.Criterion, Reasons: v.Reasons, Citations: v.Citations})
		}
		if change.Payload, err = json.Marshal(on); err != nil {
			return "", err
		}
	}
	if err := leaveAcceptance(tx, id, change, actor, moved); err != nil {
		return "", err
	}
	note.WorkOrder, note.Attempt = id, current.Fence
	if err := tx.Evidence().RecordRework(note); err != nil {
		return "", err
	}
	back, err := tx.WorkOrders().Get(id)
	if err != nil {
		return "", err
	}
	failures, err := readyFailures(tx.Planning(), tx.WorkOrders(), tx.Questions(), back)
	if err != nil {
		return "", err
	}
	if len(failures) == 0 {
		return workorders.Ready, nil
	}
	if err := takeEdge(tx, id, workorders.TriggerBackToReview, actor); err != nil {
		return "", err
	}
	return workorders.ContractReview, nil
}

// awaiting is a work order in `acceptance`, as a tick read it with no lock.
type awaiting struct {
	order  workorders.WorkOrder
	branch ledger.Artifact
}

// Settle reads what a forge ran on each waiting attempt's commit, and moves the work its
// evidence decides by itself. It is a tick: the list is read with no lock, the forge is read
// outside every transaction, and each item is one transaction that reads again. A nil forge
// means no independent source exists, which leaves a lane's checks to a member (D16). A forge that fails decides nothing about its item, and the rest go on.
func (a *Acceptances) Settle(ctx context.Context, scope ledger.WorkspaceID, forge ports.Forge, actor ledger.Actor) ([]string, error) {
	waiting, err := a.waiting(ctx, scope)
	if err != nil {
		return nil, err
	}
	var moved []string
	var failed []error
	for _, item := range waiting {
		checks, err := forgeChecks(ctx, scope, forge, item)
		if err != nil {
			failed = append(failed, fmt.Errorf("work order %s: %w", item.order.ID, err))
			continue
		}
		settled, err := a.settle(ctx, scope, item, checks, actor)
		if err != nil {
			failed = append(failed, fmt.Errorf("work order %s: %w", item.order.ID, err))
			continue
		}
		if settled {
			moved = append(moved, item.order.ID)
		}
	}
	return moved, errors.Join(failed...)
}

func (a *Acceptances) waiting(ctx context.Context, scope ledger.WorkspaceID) ([]awaiting, error) {
	var out []awaiting
	err := a.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		rows, err := tx.WorkOrders().List()
		if err != nil {
			return err
		}
		reviewed := map[string]bool{}
		for _, row := range rows {
			if row.OpenReview() {
				reviewed[row.Reviews] = true
			}
		}
		for _, row := range rows {
			// Work waits for its review. A member can still accept it or send it back, and a tick does
			// neither until the review ends.
			if row.State != workorders.Acceptance || reviewed[row.ID] {
				continue
			}
			artifacts, err := tx.Evidence().Artifacts(row.ID)
			if err != nil {
				return err
			}
			out = append(out, awaiting{order: row, branch: attemptBranch(artifacts, row.Fence)})
		}
		return nil
	})
	return out, err
}

// attemptBranch finds the branch one attempt left. An attempt with none has no commit a forge
// could have run on.
func attemptBranch(artifacts []ledger.Artifact, fence int64) ledger.Artifact {
	for _, artifact := range artifacts {
		if artifact.Kind == ledger.ArtifactBranch && artifact.Fence == fence {
			return artifact
		}
	}
	return ledger.Artifact{}
}

func forgeChecks(ctx context.Context, scope ledger.WorkspaceID, forge ports.Forge, item awaiting) ([]ports.ForgeCheck, error) {
	if forge == nil || item.branch.Digest == "" {
		return nil, nil
	}
	return forge.Checks(ctx, scope, ports.ForgeRequest{Repository: item.order.RepositoryID, Commit: item.branch.Digest})
}

// settle decides one attempt. The fence is compared, because what the forge said is about the
// attempt the tick read, and a later one may have started since.
func (a *Acceptances) settle(ctx context.Context, scope ledger.WorkspaceID, item awaiting, checks []ports.ForgeCheck, actor ledger.Actor) (bool, error) {
	settled := false
	err := a.store.Write(ctx, scope, "acceptance.settle", func(tx ports.WriteTx) error {
		settled = false
		current, err := tx.WorkOrders().Get(item.order.ID)
		if err != nil {
			return err
		}
		if current.State != workorders.Acceptance || current.Fence != item.order.Fence {
			return nil
		}
		recorded, err := recordForgeChecks(tx, current, item.branch, checks)
		if err != nil {
			return err
		}
		crashpoint.Hit("acceptance.settle", "recorded")
		at, err := readAttempt(tx.Planning(), tx.Evidence(), tx.Repositories(), current)
		if err != nil {
			return err
		}
		assessment := at.assessment
		moved := func() { crashpoint.Hit("acceptance.settle", "moved") }
		spoke, moves, err := settleByVerdicts(tx, current, at, actor, moved)
		if err != nil {
			return err
		}
		if moves {
			settled = true
			return nil
		}
		switch {
		case spoke:
			// The verdicts hold the work for a member. A forge result still lands on record.
			if recorded {
				_, err = tx.Append(ledger.EventDraft{
					Type: workorders.EventEvidenceRecorded, Actor: actor, Subject: ledger.Subject{Kind: workorders.SubjectKind, ID: current.ID},
				})
			}
		case len(assessment.Failed) != 0:
			settled = true
			_, err = rework(tx, current.ID, "failed: "+strings.Join(assessment.Failed, ", "), actor, moved)
			return err
		case assessment.Needs == workorders.NeedNothing:
			if !authority.Allowed(actor, acceptAction(assessment.Needs)) {
				return workorders.ErrForbidden
			}
			settled = true
			return accept(tx, current.ID, assessment, actor, moved)
		case recorded:
			_, err = tx.Append(ledger.EventDraft{
				Type: workorders.EventEvidenceRecorded, Actor: actor, Subject: ledger.Subject{Kind: workorders.SubjectKind, ID: current.ID},
			})
		}
		return err
	})
	return settled, err
}

// recordForgeChecks keeps what a forge said about one attempt, and reports whether that added
// anything. Each check's ID is its name within the attempt, so a check run again replaces
// its own row and a tick that learned nothing writes nothing.
func recordForgeChecks(tx ports.WriteTx, w workorders.WorkOrder, branch ledger.Artifact, checks []ports.ForgeCheck) (bool, error) {
	existing, err := tx.Evidence().Attempt(w.ID, w.Fence)
	if err != nil {
		return false, err
	}
	recorded := false
	for _, check := range checks {
		id := fmt.Sprintf("forge:%s:%d:%s", w.ID, w.Fence, check.Name)
		held := slices.IndexFunc(existing, func(row workorders.Evidence) bool { return row.ID == id })
		if held >= 0 && existing[held].Status == check.Status && existing[held].DetailsURI == check.DetailsURI {
			continue
		}
		err := tx.Evidence().Record(workorders.Evidence{
			ID: id, WorkOrder: w.ID, Lane: branch.Lane, Fence: w.Fence, ArtifactID: branch.ID, Criterion: check.Criterion,
			Name: check.Name, Status: check.Status, Source: workorders.SourceForge, DetailsURI: check.DetailsURI,
		})
		if err != nil {
			return false, err
		}
		recorded = true
	}
	return recorded, nil
}

// recordEvidence keeps what a lane's end says, in the transaction that stores the end, so an
// outcome can never stand without the artifacts and checks it rests on (FR-LANE-2). Only the
// branch comes from the worker. A lane's checks hang from it as the artifact they prove.
func recordEvidence(tx ports.WriteTx, holder workorders.Holder, fence int64, result workorders.LaneResult) error {
	addArtifact := func(kind, uri, digest string) (string, error) {
		id, err := newID(tx.Now())
		if err != nil {
			return "", err
		}
		return id, tx.Lanes().AddArtifact(ledger.Artifact{ID: id, Lane: holder.Lane, Kind: kind, URI: uri, Digest: digest, Fence: fence})
	}
	var branch string
	if result.Branch != "" {
		var err error
		if branch, err = addArtifact(ledger.ArtifactBranch, result.Branch, result.Head); err != nil {
			return err
		}
	}
	for _, uri := range result.Artifacts {
		if _, err := addArtifact(ledger.ArtifactReport, uri, ""); err != nil {
			return err
		}
	}
	for _, check := range result.Checks {
		id, err := newID(tx.Now())
		if err != nil {
			return err
		}
		status := workorders.CheckFailed
		if check.Passed {
			status = workorders.CheckPassed
		}
		err = tx.Evidence().Record(workorders.Evidence{
			ID: id, WorkOrder: holder.WorkOrder, Lane: holder.Lane, Fence: fence, ArtifactID: branch, Criterion: check.Criterion,
			Name: check.Name, Status: status, Source: workorders.SourceLane,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// reviewActor is the control plane closing a review by rule: no lane and no worker accepts a thing.
var reviewActor = ledger.Actor{Kind: ledger.ActorSystem, ID: "review"}

// settleByVerdicts moves an attempt its verdicts decide, as the control plane by rule. It reports whether the
// verdicts spoke at all, which keeps the older rule on evidence from deciding what they hold for a member,
// and whether it moved the work.
func settleByVerdicts(tx ports.WriteTx, current workorders.WorkOrder, at attemptStanding, actor ledger.Actor, moved func()) (spoke, moves bool, err error) {
	switch at.judgement.Settlement {
	case workorders.SettleNone:
		return false, false, nil
	case workorders.SettleMember:
		return true, false, nil
	case workorders.SettleRework:
		note := reworkNoteOf(at)
		_, err = reworkFor(tx, current.ID, note, at.judgement.Failed, actor, moved)
		return true, err == nil, err
	}
	if !authority.Allowed(actor, authority.ActionAcceptReviewed) {
		return true, false, workorders.ErrForbidden
	}
	err = acceptCiting(tx, current.ID, at.assessment, at.verdicts[0].Review, actor, moved)
	return true, err == nil, err
}

// reworkNoteOf says why an attempt goes back, citing the review whose criteria failed and the checks that
// did. A note names a review only when it failed a criterion, because the next brief lists those.
func reworkNoteOf(at attemptStanding) workorders.ReworkNote {
	var note workorders.ReworkNote
	var parts []string
	if failed := at.judgement.Failed; len(failed) != 0 {
		ids := make([]string, 0, len(failed))
		for _, v := range failed {
			ids = append(ids, v.Criterion)
		}
		note.Review = failed[0].Review
		parts = append(parts, fmt.Sprintf("review %s failed criteria %s", note.Review, strings.Join(ids, ", ")))
	}
	if checks := at.assessment.Failed; len(checks) != 0 {
		parts = append(parts, "failed checks: "+strings.Join(checks, ", "))
	}
	note.Reason = strings.Join(parts, "; ")
	return note
}

// endOpenReviews cancels the reviews of a work order that have not ended, once a member has decided it
// without them. A member's word does not wait for a review, and a review nobody reads is a lane for
// nothing. Only a member's decision ends a review this way.
func endOpenReviews(tx ports.WriteTx, id string, actor ledger.Actor) error {
	if !authority.Allowed(actor, authority.ActionCancelWork) {
		return nil
	}
	reviews, err := tx.WorkOrders().ReviewsOf(id)
	if err != nil {
		return err
	}
	for _, review := range reviews {
		if !review.OpenReview() {
			continue
		}
		if err := cancelWork(tx, review.ID, "a member decided "+id+" before this review ended", actor); err != nil {
			return err
		}
	}
	return nil
}

// recordVerdicts keeps what a completed review said, in the transaction that ends its lane, bound to the
// commit that attempt of the reviewed work left. It refuses a report that does not hold to the contract the
// review pinned, and a verdict from any lane but a review's own.
func recordVerdicts(tx ports.WriteTx, review workorders.WorkOrder, holder workorders.Holder, fence int64, result workorders.LaneResult) error {
	if review.Kind != workorders.KindReview {
		if len(result.Verdicts) != 0 {
			return fmt.Errorf("%w: only a review reports verdicts, and %s is a %s", workorders.ErrInvalid, review.ID, review.Kind)
		}
		return nil
	}
	if result.Outcome != workorders.OutcomeCompleted {
		return nil
	}
	criteria, err := criteriaOf(tx.Planning(), review)
	if err != nil {
		return err
	}
	if err := result.CheckVerdicts(criteria); err != nil {
		return err
	}
	artifacts, err := tx.Evidence().Artifacts(review.Reviews)
	if err != nil {
		return err
	}
	commit := attemptBranch(artifacts, review.ReviewsFence).Digest
	for _, v := range result.Verdicts {
		err := tx.Evidence().RecordVerdict(workorders.Verdict{
			CriterionVerdict: v, Review: review.ID, WorkOrder: review.Reviews, Attempt: review.ReviewsFence, Commit: commit,
			Lane: holder.Lane, Generation: holder.Generation, Fence: fence, MaxRounds: result.MaxReviewRounds,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// settleReviewed decides the work a review judged, once the review ended with its verdicts. The work moves
// only if it still waits at the attempt the review read: a member may have decided it, or sent it back, while
// the review ran.
func settleReviewed(tx ports.WriteTx, review workorders.WorkOrder) error {
	reviewed, err := tx.WorkOrders().Get(review.Reviews)
	if err != nil {
		return err
	}
	if reviewed.State != workorders.Acceptance || reviewed.Fence != review.ReviewsFence {
		return nil
	}
	at, err := readAttempt(tx.Planning(), tx.Evidence(), tx.Repositories(), reviewed)
	if err != nil {
		return err
	}
	_, _, err = settleByVerdicts(tx, reviewed, at, reviewActor, func() {})
	return err
}

// afterLaneEnd does what the end of a lane owes beyond moving its own work order, in the transaction that
// moves it. A change or a rework that completed leaves a review of its commit. A review's lane leaves its
// worktree to be taken away, whichever way it ended, and a review that completed is done: what it judged
// is the reviewed work's to decide, and a review is not reviewed.
func afterLaneEnd(tx ports.WriteTx, ended workorders.WorkOrder, holder workorders.Holder, result workorders.LaneResult, actor ledger.Actor) error {
	if ended.Kind == workorders.KindReview {
		if err := discardLane(tx, holder.Lane); err != nil {
			return err
		}
		if result.Outcome != workorders.OutcomeCompleted {
			return nil
		}
		if err := closeReview(tx, ended.ID); err != nil {
			return err
		}
		return settleReviewed(tx, ended)
	}
	if result.Outcome != workorders.OutcomeCompleted {
		return nil
	}
	return openReview(tx, ended, ended.Fence+1, actor)
}

// openReview makes the review of an attempt that completed at fence, and says nothing when the attempt
// left no commit: there is nothing for a review to read.
func openReview(tx ports.WriteTx, reviewed workorders.WorkOrder, fence int64, actor ledger.Actor) error {
	if reviewed.Kind != workorders.KindChange && reviewed.Kind != workorders.KindRework {
		return nil
	}
	artifacts, err := tx.Evidence().Artifacts(reviewed.ID)
	if err != nil {
		return err
	}
	if attemptBranch(artifacts, fence).Digest == "" {
		return nil
	}
	id, err := newID(tx.Now())
	if err != nil {
		return err
	}
	review, err := workorders.NewReview(id, reviewed, fence)
	if err != nil {
		return err
	}
	if err := tx.WorkOrders().Insert(review); err != nil {
		return err
	}
	event, err := workorders.CreatedEvent(review, actor)
	if err != nil {
		return err
	}
	_, err = tx.Append(event)
	return err
}

// closeReview moves a review whose lane completed to `done`, by the edge every accepted work order takes.
func closeReview(tx ports.WriteTx, id string) error {
	current, err := tx.WorkOrders().Get(id)
	if err != nil {
		return err
	}
	assessment, err := assessAttempt(tx.Planning(), tx.Evidence(), current)
	if err != nil {
		return err
	}
	return accept(tx, id, assessment, reviewActor, func() {})
}

// discardLane asks the worker that ran a lane to take its worktree away, whatever it holds. A review
// lane's worktree is a checkout with no branch, so there is nothing in it to save.
func discardLane(tx ports.WriteTx, lane string) error {
	if err := tx.Lanes().RequestArchive(lane, true); err != nil {
		return err
	}
	_, err := tx.Append(ledger.EventDraft{
		Type: ledger.EventLaneArchiveRequested, Actor: reviewActor, Subject: ledger.Subject{Kind: ledger.SubjectLane, ID: lane},
	})
	return err
}

// refuseOwnWork keeps a review from a lane of the work it judges: an agent does not review what it wrote
// (FR-REVIEW-5). A lane that does not exist yet is nobody's.
func refuseOwnWork(tx ports.WriteTx, order workorders.WorkOrder, lane string) error {
	if order.Kind != workorders.KindReview {
		return nil
	}
	row, err := tx.Lanes().Row(lane)
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return nil
	case err != nil:
		return err
	case row.WorkOrder == order.Reviews:
		return fmt.Errorf("%w: lane %s ran %s, the work this review judges", workorders.ErrForbidden, lane, order.Reviews)
	}
	return nil
}

// reviewStart is where a review's lane begins: the commit the reviewed attempt ended at, on the branch it
// left. Nothing of it comes from git, so it cannot move with a base branch.
func reviewStart(tx ports.ReadTx, review workorders.WorkOrder) (workorders.Start, *workorders.StartHold, error) {
	artifacts, err := tx.Evidence().Artifacts(review.Reviews)
	if err != nil {
		return workorders.Start{}, nil, err
	}
	branch := attemptBranch(artifacts, review.ReviewsFence)
	if branch.Digest == "" {
		return workorders.Start{}, unavailable("the commit under review is not on record: %s left no branch at attempt %d", review.Reviews, review.ReviewsFence), nil
	}
	return workorders.Start{Ref: branch.URI, Commit: branch.Digest}, nil, nil
}

// desiredAttempt tells a review lane's worker what it judges: the attempt, its commit, and where the
// lane that made it began, which the diff reads from.
func desiredAttempt(tx ports.ReadTx, review workorders.WorkOrder) (*ledger.DesiredAttempt, error) {
	if review.Kind != workorders.KindReview {
		return nil, nil
	}
	reviewed, err := tx.WorkOrders().Get(review.Reviews)
	if err != nil {
		return nil, err
	}
	artifacts, err := tx.Evidence().Artifacts(review.Reviews)
	if err != nil {
		return nil, err
	}
	branch := attemptBranch(artifacts, review.ReviewsFence)
	attempt := &ledger.DesiredAttempt{WorkOrder: reviewed.ID, Title: reviewed.Title, Branch: branch.URI, Commit: branch.Digest}
	claim, err := tx.Claims().OfLane(branch.Lane)
	switch {
	case err == nil:
		attempt.BaseRef, attempt.BaseCommit = claim.BaseRef, claim.BaseCommit
	case !errors.Is(err, ports.ErrNotFound):
		return nil, err
	}
	return attempt, nil
}
