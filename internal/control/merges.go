package control

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// mergeActor is the control plane running the integration rule: no lane, worker, or orchestrator merges.
var mergeActor = ledger.Actor{Kind: ledger.ActorSystem, ID: "integration"}

// mergeID keys one round of the merge of one commit of a work order. A round ends when the merge lands, or
// when it is refused or held and a member decides it; a member's retry starts the next round, so a request
// made twice in one round is made once.
func mergeID(workOrder, commit string, round int) string {
	return fmt.Sprintf("merge:%s:%s:%d", workOrder, commit, round)
}

// mergesOnAccept says accepted work merges rather than ends: a change or a rework that left a commit, in a
// repository whose merge authority has ropeshift merge (ADR-0020). Planning work, reviews, and work at
// person_merges end as they always did.
func mergesOnAccept(tx ports.WriteTx, order workorders.WorkOrder) (bool, error) {
	if order.Kind != workorders.KindChange && order.Kind != workorders.KindRework {
		return false, nil
	}
	record, err := tx.Repositories().Repository(order.RepositoryID)
	if errors.Is(err, ports.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if record.Level() == ledger.PersonMerges {
		return false, nil
	}
	artifacts, err := tx.Evidence().Artifacts(order.ID)
	if err != nil {
		return false, err
	}
	return attemptBranch(artifacts, order.Fence).Digest != "", nil
}

// integrationFacts is what the integration step reads of one work order in `integrating`.
type integrationFacts struct {
	order  workorders.WorkOrder
	record ledger.Repository
	// branch is the attempt's branch artifact: the branch it left, and the reviewed commit.
	branch ledger.Artifact
	base   string
	// approval is the approval bound to the reviewed commit, when there is one.
	approval *ledger.IntegrationRecord
	pr       *ledger.WorkPullRequest
	// merges are the merge requests of the reviewed commit, oldest first.
	merges []ledger.ForgeRequest
	// requests are the work order's merge approval requests, oldest first.
	requests []authority.Approval
	// worker is the worker that ran the lane that left the commit, which holds its branch.
	worker string
}

func (f integrationFacts) commit() string { return f.branch.Digest }

func (f integrationFacts) method() ledger.MergeMethod {
	return cmp.Or(f.record.MergeMethod, ledger.MergeCommit)
}

// integrationReaders are what the integration step reads, from a read or a write.
type integrationReaders struct {
	repositories ports.RepositoryReader
	evidence     ports.EvidenceReader
	forge        ports.ForgeReader
	approvals    ports.ApprovalReader
	claims       ports.ClaimReader
	lanes        ports.LaneReader
}

func readersIn(tx ports.ReadTx) integrationReaders {
	return integrationReaders{tx.Repositories(), tx.Evidence(), tx.Forge(), tx.Approvals(), tx.Claims(), tx.Lanes()}
}

func readersOf(tx ports.WriteTx) integrationReaders {
	return integrationReaders{tx.Repositories(), tx.Evidence(), tx.Forge(), tx.Approvals(), tx.Claims(), tx.Lanes()}
}

// laneWorker is the worker that ran a lane, which holds its branch: the claim's, or the lane's status.
func laneWorker(tx integrationReaders, lane string) (string, error) {
	if lane == "" {
		return "", nil
	}
	claim, err := tx.claims.OfLane(lane)
	if err == nil && claim.Worker != "" {
		return claim.Worker, nil
	}
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		return "", err
	}
	row, err := tx.lanes.Row(lane)
	if errors.Is(err, ports.ErrNotFound) {
		return "", nil
	}
	return row.WorkerID, err
}

// readIntegration reads one work order's integration in one moment.
func readIntegration(tx integrationReaders, order workorders.WorkOrder) (integrationFacts, error) {
	f := integrationFacts{order: order, record: ledger.Repository{ID: order.RepositoryID}}
	record, err := tx.repositories.Repository(order.RepositoryID)
	switch {
	case err == nil:
		f.record = record
	case !errors.Is(err, ports.ErrNotFound):
		return f, err
	}
	f.base = cmp.Or(order.Base, f.record.Base())
	artifacts, err := tx.evidence.Artifacts(order.ID)
	if err != nil {
		return f, err
	}
	f.branch = attemptBranch(artifacts, order.Fence)
	approval, err := tx.forge.Integration(ledger.IntegrationID(order.ID, f.commit()))
	switch {
	case err == nil:
		f.approval = &approval
	case !errors.Is(err, ports.ErrNotFound):
		return f, err
	}
	pr, err := tx.forge.PullRequest(order.ID)
	switch {
	case err == nil:
		f.pr = &pr
	case !errors.Is(err, ports.ErrNotFound):
		return f, err
	}
	requests, err := tx.forge.RequestsOf(order.ID)
	if err != nil {
		return f, err
	}
	for _, request := range requests {
		if request.Kind == ledger.ForgeMerge && request.Commit == f.commit() {
			f.merges = append(f.merges, request)
		}
	}
	approvals, err := tx.approvals.OfWorkOrder(order.ID)
	if err != nil {
		return f, err
	}
	for _, request := range approvals {
		if request.Kind == authority.KindMerge {
			f.requests = append(f.requests, request)
		}
	}
	f.worker, err = laneWorker(tx, f.branch.Lane)
	return f, err
}

// gateInput is what the integration rule reads of the facts.
func (f integrationFacts) gateInput() workorders.GateInput {
	in := workorders.GateInput{Authority: f.record.Level(), Target: f.record.Target(), Commit: f.commit(), Approved: f.approval != nil}
	if f.pr != nil {
		in.PullRequest = &f.pr.PullRequest
	}
	return in
}

// lastMerge is the latest merge request of the reviewed commit, or nil.
func (f integrationFacts) lastMerge() *ledger.ForgeRequest {
	if len(f.merges) == 0 {
		return nil
	}
	return &f.merges[len(f.merges)-1]
}

// retried says a member decided a refused merge request and asked for the merge again.
func (f integrationFacts) retried(request ledger.ForgeRequest) bool {
	for _, approval := range f.requests {
		if approval.Subject == authority.MergeHeld && approval.State == authority.ApprovalUsed && authority.MergeDetailOf(approval).Request == request.ID {
			return true
		}
	}
	return false
}

// openRequest is the merge request of a subject that stands open for the reviewed commit, or nil.
func (f integrationFacts) openRequest(subject string) *authority.Approval {
	for i := range f.requests {
		if request := f.requests[i]; request.Subject == subject && request.Open() && request.SubjectDigest == f.commit() {
			return &f.requests[i]
		}
	}
	return nil
}

// waiting says what the merge of a work order in `integrating` waits on, or nothing when a merge is due:
// a merge on its way to the worker, a refusal a member has not decided, or what the integration rule says.
func (f integrationFacts) waiting() (workorders.Gate, *ledger.ForgeRequest) {
	if last := f.lastMerge(); last != nil {
		switch {
		case last.State == ledger.ForgeRequested && last.Detail != "":
			return workorders.Gate{Step: workorders.StepWait, Why: fmt.Sprintf("attempt %d to merge %s failed: %s", last.Attempt-1, short(f.commit()), last.Detail)}, last
		case last.State == ledger.ForgeRequested:
			return workorders.Gate{Step: workorders.StepWait, Why: fmt.Sprintf("the worker is merging %s into %s", short(f.commit()), f.base)}, last
		case last.Outcome != ledger.MergeMerged && !last.Outcome.Rework() && !f.retried(*last):
			return workorders.Gate{Step: workorders.StepHold, Why: fmt.Sprintf("the merge of %s was refused: %s", short(f.commit()), last.Detail)}, last
		}
	}
	gate := workorders.MergeGate(f.gateInput())
	if gate.Step == workorders.StepMerge && f.record.Target() != ledger.ReviewOnPullRequest && f.worker == "" {
		return workorders.Gate{Step: workorders.StepWait, Why: fmt.Sprintf("no worker on record holds branch %s", f.branch.URI)}, nil
	}
	return gate, nil
}

// integrate runs the integration step for one work order in `integrating`, inside a transaction (ADR-0020):
// it asks for the merge once the rule allows it, sends the work back when a required check failed, ends it
// when its pull request merged on the forge, and otherwise makes sure a member has what the merge waits on:
// a request to approve it at person_approves, or a request to decide a merge that cannot go on. Each of these
// is idempotent, so the tick and every event that changes what the rule reads may run it again.
func integrate(tx ports.WriteTx, id string) error {
	order, err := tx.WorkOrders().Get(id)
	if err != nil {
		return err
	}
	if order.State != workorders.Integrating {
		return nil
	}
	f, err := readIntegration(readersOf(tx), order)
	if err != nil || f.commit() == "" {
		return err
	}
	gate, request := f.waiting()
	switch gate.Step {
	case workorders.StepWait:
		if f.approval == nil && f.openRequest(authority.MergeApprove) == nil && request == nil {
			return raiseMerge(tx, f, authority.MergeApprove, "", "")
		}
		return nil
	case workorders.StepHold:
		if f.openRequest(authority.MergeHeld) != nil {
			return nil
		}
		requestID := ""
		if request != nil {
			requestID = request.ID
		}
		if err := appendIntegration(tx, ledger.EventIntegrationHeld, id, map[string]string{"commit": f.commit(), "reason": gate.Why, "request": requestID}, mergeActor); err != nil {
			return err
		}
		return raiseMerge(tx, f, authority.MergeHeld, gate.Why, requestID)
	case workorders.StepRework:
		note := workorders.ReworkNote{Reason: gate.Why + ". Make each required check pass on the next commit."}
		_, err := reworkFor(tx, id, note, nil, mergeActor, func() {})
		return err
	case workorders.StepMerged:
		return mergedOnForge(tx, f)
	}
	return requestMerge(tx, f)
}

// requestMerge asks a worker to merge the reviewed commit with the repository's merge method: the worker that
// opened the pull request, which holds the checkout and the sign-in, or with review target `branch` the worker
// that ran the lane, which holds the branch. A branch merge needs no forge, so it names no binding.
func requestMerge(tx ports.WriteTx, f integrationFacts) error {
	if !authority.Allowed(mergeActor, authority.ActionMerge) {
		return workorders.ErrForbidden
	}
	request := ledger.ForgeRequest{
		ID: mergeID(f.order.ID, f.commit(), len(f.merges)+1), Kind: ledger.ForgeMerge, WorkOrder: f.order.ID, Repository: f.record.ID,
		Branch: f.branch.URI, Base: f.base, Commit: f.commit(), Title: f.order.Title, Method: f.method(), RequestedAt: tx.Now(),
	}
	if f.record.Target() == ledger.ReviewOnPullRequest {
		if f.pr == nil {
			return nil
		}
		request.Worker, request.Binding, request.Number, request.Branch, request.Base = f.pr.Worker, f.record.ForgeBinding, f.pr.Number, f.pr.Branch, f.pr.Base
	} else {
		request.Worker = f.worker
	}
	first, err := tx.Forge().InsertRequest(request)
	if err != nil || !first {
		return err
	}
	return appendForge(tx, ledger.EventForgeRequested, f.order.ID, request, mergeActor)
}

// raiseMerge records a merge request a member decides, bound to the reviewed commit: the approval of the merge
// at person_approves, or a merge that cannot go on, with why and the merge request it answers.
func raiseMerge(tx ports.WriteTx, f integrationFacts, subject, reason, request string) error {
	detail := authority.MergeDetail{
		WorkOrder: f.order.ID, Commit: f.commit(), Branch: f.branch.URI, Base: f.base, Method: f.method(), Authority: f.record.Level(),
		Reason: reason, Request: request,
	}
	if f.pr != nil {
		detail.PullRequest, detail.URL, detail.Branch, detail.Base = f.pr.Number, f.pr.URL, f.pr.Branch, f.pr.Base
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	id, err := newID(tx.Now())
	if err != nil {
		return err
	}
	into := fmt.Sprintf("%s into %s", cmp.Or(detail.Branch, "its branch"), cmp.Or(detail.Base, "its base"))
	if detail.PullRequest != 0 {
		into = fmt.Sprintf("pull request #%d", detail.PullRequest)
	}
	summary := fmt.Sprintf("Approve the merge of %s, commit %s of work order %s (%s), at %s. The review's verdicts are on the work order.",
		into, short(f.commit()), f.order.ID, f.order.Title, detail.Authority)
	if subject == authority.MergeHeld {
		summary = fmt.Sprintf("The merge of %s, commit %s of work order %s (%s), cannot go on: %s. Approve to try it again, or deny to send the work back for rework.",
			into, short(f.commit()), f.order.ID, f.order.Title, reason)
	}
	approval := authority.Approval{
		ID: id, WorkOrder: f.order.ID, Kind: authority.KindMerge, Subject: subject, SubjectDigest: f.commit(), Summary: summary,
		RequestedBy: authority.Principal(mergeActor), State: authority.ApprovalPending, RaisedAt: tx.Now(), Detail: string(raw),
	}
	if err := approval.Validate(); err != nil {
		return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	added, err := tx.Approvals().Insert(approval)
	if err != nil || !added {
		return err
	}
	event, err := authority.ApprovalEvent(approval, mergeActor)
	if err != nil {
		return err
	}
	_, err = tx.Append(event)
	return err
}

// approveMerge records an approval of the reviewed commit for merge, once per commit: a later approval of the
// same commit is the same approval, and a new commit needs its own.
func approveMerge(tx ports.WriteTx, f integrationFacts, kind ledger.ApproverKind, by string, generation int64, actor ledger.Actor) error {
	record := ledger.IntegrationRecord{
		ID: ledger.IntegrationID(f.order.ID, f.commit()), WorkOrder: f.order.ID, ApprovedCommit: f.commit(), MergeAuthority: f.record.Level(),
		ApprovedByKind: kind, ApprovedByID: by, ApprovedAt: tx.Now(),
	}
	if f.pr != nil {
		record.PullRequest = f.pr.Number
	}
	added, err := tx.Forge().InsertIntegration(record)
	if err != nil || !added {
		return err
	}
	approved := struct {
		ledger.IntegrationRecord
		Generation int64 `json:"generation,omitempty"`
	}{record, generation}
	return appendIntegration(tx, ledger.EventIntegrationApproved, f.order.ID, approved, actor)
}

// appendIntegration records one integration event about a work order.
func appendIntegration(tx ports.WriteTx, eventType, workOrder string, data any, actor ledger.Actor) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = tx.Append(ledger.EventDraft{Type: eventType, Actor: actor, Subject: ledger.Subject{Kind: ledger.SubjectIntegration, ID: workOrder}, Data: raw})
	return err
}

// integrateAccepted moves accepted work to `integrating` and runs the integration step. A member's acceptance
// approves the reviewed commit at any level, and so does the review's pass at review_approves, through the
// review lane and its generation; anything else waits for a member's approval.
func integrateAccepted(tx ports.WriteTx, id string, assessment workorders.Assessment, review string, actor ledger.Actor, moved func()) error {
	payload, err := json.Marshal(acceptedOn{Assessment: assessment, Review: review})
	if err != nil {
		return err
	}
	if err := leaveAcceptance(tx, id, workorders.Change{Trigger: workorders.TriggerIntegrating, Payload: payload}, actor, moved); err != nil {
		return err
	}
	order, err := tx.WorkOrders().Get(id)
	if err != nil {
		return err
	}
	f, err := readIntegration(readersOf(tx), order)
	if err != nil {
		return err
	}
	switch {
	case authority.PartyOf(actor) == authority.PartyPerson:
		err = approveMerge(tx, f, ledger.ApprovedByMember, actor.ID, 0, actor)
	case review != "" && f.record.Level() == ledger.ReviewApproves:
		err = approveByReview(tx, f, review)
	}
	if err != nil {
		return err
	}
	return integrate(tx, id)
}

// approveByReview records the review's pass as the approval, by the review lane and its generation.
func approveByReview(tx ports.WriteTx, f integrationFacts, review string) error {
	verdicts, err := tx.Evidence().Verdicts(f.order.ID)
	if err != nil {
		return err
	}
	for _, v := range verdicts {
		if v.Review == review && v.Commit == f.commit() {
			return approveMerge(tx, f, ledger.ApprovedByReview, v.Lane, v.Generation, reviewActor)
		}
	}
	return nil
}

// approveIntegrating records a member's acceptance of work already in `integrating` as the approval of its
// reviewed commit, and runs the integration step.
func approveIntegrating(tx ports.WriteTx, order workorders.WorkOrder, actor ledger.Actor) error {
	f, err := readIntegration(readersOf(tx), order)
	if err != nil {
		return err
	}
	if err := approveMerge(tx, f, ledger.ApprovedByMember, actor.ID, 0, actor); err != nil {
		return err
	}
	if err := voidMergeRequests(tx, order.ID, authority.MergeApprove, "a member accepted the work"); err != nil {
		return err
	}
	return integrate(tx, order.ID)
}

// mergedWork is the payload of the move to `done` a merge made: the approval and the merge it records.
type mergedWork struct {
	Record      ledger.IntegrationRecord `json:"integration"`
	URL         string                   `json:"url,omitempty"`
	Method      ledger.MergeMethod       `json:"method,omitempty"`
	ForgeMerged string                   `json:"forge_merged_at,omitempty"`
}

// finishMerge records the merge of the approved commit and moves the work to `done`, as every accepted work
// order ends: plan reviews it passes, and what waits on it wakes.
func finishMerge(tx ports.WriteTx, f integrationFacts, record ledger.IntegrationRecord, merged mergedWork, actor ledger.Actor) error {
	if _, err := tx.Forge().RecordMerge(record.ID, record.MergedCommit, record.MergedAt); err != nil {
		return err
	}
	merged.Record = record
	if err := appendIntegration(tx, ledger.EventIntegrationMerged, f.order.ID, merged, actor); err != nil {
		return err
	}
	if err := voidMergeRequests(tx, f.order.ID, "", "the work merged"); err != nil {
		return err
	}
	current, err := tx.WorkOrders().Get(f.order.ID)
	if err != nil || current.State != workorders.Integrating {
		return err
	}
	payload, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	if err := leaveAcceptance(tx, f.order.ID, workorders.Change{Trigger: workorders.TriggerMerged, Payload: payload}, actor, func() {}); err != nil {
		return err
	}
	if err := passPlanReview(tx, f.order.ID, actor); err != nil {
		return err
	}
	_, err = fireWakes(tx, "done:"+f.order.ID, actor, parking.WorkOrderDone)
	return err
}

// mergedOnForge ends work whose pull request merged on the forge before ropeshift merged it, whoever merged
// it (FR-MERGE-9). An approval of the reviewed commit records the merge; with none, the merge is the person's
// on the forge, approved there.
func mergedOnForge(tx ports.WriteTx, f integrationFacts) error {
	pr := f.pr.PullRequest
	record := ledger.IntegrationRecord{
		ID: ledger.IntegrationID(f.order.ID, pr.HeadCommit), WorkOrder: f.order.ID, PullRequest: pr.Number, ApprovedCommit: pr.HeadCommit,
		MergeAuthority: f.record.Level(), ApprovedByKind: ledger.ApprovedByForge, ApprovedAt: tx.Now(),
	}
	if f.approval != nil && f.approval.ApprovedCommit == pr.HeadCommit {
		record = *f.approval
	} else if _, err := tx.Forge().InsertIntegration(record); err != nil {
		return err
	}
	record.MergedCommit, record.MergedAt = pr.MergeCommit, tx.Now()
	return finishMerge(tx, f, record, mergedWork{URL: pr.URL, ForgeMerged: pr.MergedAt}, forgeActor)
}

// mergeReported keeps what a worker's merge came to (ADR-0020). A merge that landed records the merged commit
// and ends the work. A conflict, or a branch behind a base that requires an up-to-date one, sends the work
// back to rework on the same branch, whose next lane merges the current base into it. Anything else the forge
// or git refused holds the work in `integrating` for a member, with why.
func mergeReported(tx ports.WriteTx, request ledger.ForgeRequest, report ledger.ForgeReport, actor ledger.Actor) error {
	if report.Merge == nil {
		return fmt.Errorf("%w: a merge report says nothing of the merge", workorders.ErrInvalid)
	}
	result := *report.Merge
	switch result.Outcome {
	case ledger.MergeMerged, ledger.MergeConflict, ledger.MergeBehind, ledger.MergeStaleHead, ledger.MergeRefused:
	default:
		return fmt.Errorf("%w: a merge came to %q", workorders.ErrInvalid, result.Outcome)
	}
	if _, err := tx.Forge().FinishMerge(request.ID, request.Attempt, tx.Now(), result.Outcome, result.Detail); err != nil {
		return err
	}
	order, err := tx.WorkOrders().Get(request.WorkOrder)
	if err != nil {
		return err
	}
	f, err := readIntegration(readersOf(tx), order)
	if err != nil {
		return err
	}
	f.branch.Digest = request.Commit
	switch {
	case result.Outcome == ledger.MergeMerged:
		return mergeLanded(tx, f, request, result, actor)
	case order.State != workorders.Integrating || order.Fence != f.order.Fence:
		return nil
	case result.Outcome.Rework():
		return reworkConflict(tx, f, request, result)
	}
	return integrate(tx, order.ID)
}

// mergeLanded records a merge the worker made, under the approval of the commit it merged.
func mergeLanded(tx ports.WriteTx, f integrationFacts, request ledger.ForgeRequest, result ledger.MergeResult, actor ledger.Actor) error {
	record, err := tx.Forge().Integration(ledger.IntegrationID(request.WorkOrder, request.Commit))
	if err != nil {
		return err
	}
	if f.pr != nil && request.Number == f.pr.Number {
		pr := *f.pr
		pr.State, pr.HeadCommit, pr.MergeCommit, pr.MergedAt, pr.ReadAt = ledger.PullRequestMerged, request.Commit, result.MergeCommit, result.MergedAt, tx.Now()
		if err := tx.Forge().SavePullRequest(pr); err != nil {
			return err
		}
	}
	record.MergedCommit, record.MergedAt = result.MergeCommit, tx.Now()
	merged := mergedWork{Method: request.Method, ForgeMerged: result.MergedAt}
	if f.pr != nil {
		merged.URL = f.pr.URL
	}
	return finishMerge(tx, f, record, merged, actor)
}

// reworkConflict sends work whose merge conflicts back to rework on the same branch: the work keeps its
// branch, the ended lane's worktree is taken away so the next attempt can check the branch out, and the
// rework's brief names the conflict and asks the lane to merge the current base into the branch. With review
// target `pull_request` the push of that rework moves the same pull request.
func reworkConflict(tx ports.WriteTx, f integrationFacts, request ledger.ForgeRequest, result ledger.MergeResult) error {
	if err := keepPullRequestBranch(tx, request); err != nil {
		return err
	}
	why := "conflicts with"
	if result.Outcome == ledger.MergeBehind {
		why = "is behind, and the base requires an up-to-date branch:"
	}
	reason := fmt.Sprintf("merging %s at %s into %s %s %s", request.Branch, short(request.Commit), request.Base, why, request.Base)
	if len(result.Files) != 0 {
		reason += " in " + strings.Join(result.Files, ", ")
	}
	if result.Detail != "" {
		reason += " (" + result.Detail + ")"
	}
	reason += fmt.Sprintf(". Merge the current %s into this branch, resolve each conflict, and commit the merge on this branch.", request.Base)
	_, err := reworkFor(tx, f.order.ID, workorders.ReworkNote{Reason: reason}, nil, mergeActor, func() {})
	return err
}

// voidMergeRequests closes the open merge requests of a work order, of one subject or of every subject: the
// work left `integrating`, or what a request asked was answered another way.
func voidMergeRequests(tx ports.WriteTx, workOrder, subject, reason string) error {
	rows, err := tx.Approvals().OfWorkOrder(workOrder)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Kind != authority.KindMerge || !row.Open() || (subject != "" && row.Subject != subject) {
			continue
		}
		next := row
		next.State, next.Reason = authority.ApprovalVoid, reason
		if err := noteApproval(tx, next, row.State, approvalExpiry); err != nil {
			return err
		}
	}
	return nil
}

// mergeMoved says a merge request's subject is no longer what it was bound to: the work left `integrating`,
// or its reviewed commit is another.
func mergeMoved(tx ports.WriteTx, request authority.Approval) (bool, error) {
	order, err := tx.WorkOrders().Get(request.WorkOrder)
	if err != nil {
		return false, err
	}
	if order.State != workorders.Integrating {
		return true, nil
	}
	artifacts, err := tx.Evidence().Artifacts(order.ID)
	if err != nil {
		return false, err
	}
	return attemptBranch(artifacts, order.Fence).Digest != request.SubjectDigest, nil
}

// grantMerge acts on a member's approval of a merge request, as that member's decision. Approving the merge
// at person_approves records the member's approval of the reviewed commit, which counts as accepting after
// the review (ADR-0019); approving a held merge asks for it again. The request is spent either way.
func grantMerge(tx ports.WriteTx, request authority.Approval, actor ledger.Actor) error {
	used, err := request.Use(request.SubjectDigest, request.ID)
	if err != nil {
		return err
	}
	if err := noteApproval(tx, used, request.State, actor); err != nil {
		return err
	}
	order, err := tx.WorkOrders().Get(request.WorkOrder)
	if err != nil {
		return err
	}
	if request.Subject == authority.MergeApprove {
		f, err := readIntegration(readersOf(tx), order)
		if err != nil {
			return err
		}
		if err := approveMerge(tx, f, ledger.ApprovedByMember, actor.ID, 0, actor); err != nil {
			return err
		}
		if err := calibrateDecision(tx, order, workorders.DecisionAccept, "approved the merge of "+short(request.SubjectDigest), actor); err != nil {
			return err
		}
	}
	return integrate(tx, order.ID)
}

// denyMerge sends work back to rework on a member's denial of a merge request, with the member's reason. A
// denied approval disagrees with the review's pass, which the calibration record keeps.
func denyMerge(tx ports.WriteTx, request authority.Approval, actor ledger.Actor) error {
	order, err := tx.WorkOrders().Get(request.WorkOrder)
	if err != nil {
		return err
	}
	reason := cmp.Or(strings.TrimSpace(request.Reason), "no reason given")
	what := "approve the merge of"
	if request.Subject == authority.MergeHeld {
		what = "retry the held merge of"
	}
	note := fmt.Sprintf("a member declined to %s %s: %s", what, short(request.SubjectDigest), reason)
	if request.Subject == authority.MergeApprove {
		if err := calibrateDecision(tx, order, workorders.DecisionRework, note, actor); err != nil {
			return err
		}
	}
	_, err = reworkFor(tx, order.ID, workorders.ReworkNote{Reason: note}, nil, actor, func() {})
	return err
}

// PullRequestChecks is the check source a forge's pull requests give (FR-MERGE-2): the checks a worker last
// read on the head of a pull request that the base branch requires, each once it has finished. A check the
// base does not require is information on the pull request and evidence of nothing, and one still running
// is not listed, so it holds the merge as pending. It reads the ledger and never the forge, because the
// worker that opened the pull request is the one that reads it.
type PullRequestChecks struct {
	Store ports.Store
}

var _ ports.CheckSource = PullRequestChecks{}

// Checks lists the finished required checks on a commit that is a pull request's head, by name.
func (c PullRequestChecks) Checks(ctx context.Context, scope ledger.WorkspaceID, request ports.ForgeRequest) ([]ports.ForgeCheck, error) {
	var out []ports.ForgeCheck
	err := c.Store.Read(ctx, scope, func(tx ports.ReadTx) error {
		prs, err := tx.Forge().PullRequests()
		if err != nil {
			return err
		}
		for _, pr := range prs {
			if pr.Repository != request.Repository || pr.HeadCommit != request.Commit {
				continue
			}
			for _, check := range pr.Checks {
				if !check.Required || check.State == ledger.CheckPending {
					continue
				}
				status := workorders.CheckFailed
				if check.State == ledger.CheckPassed {
					status = workorders.CheckPassed
				}
				out = append(out, ports.ForgeCheck{Name: check.Name, Status: status, DetailsURI: check.DetailsURI})
			}
		}
		return nil
	})
	slices.SortFunc(out, func(a, b ports.ForgeCheck) int { return strings.Compare(a.Name, b.Name) })
	return out, err
}
