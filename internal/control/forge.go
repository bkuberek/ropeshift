package control

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// forgeActor is the control plane acting on what a worker reported of a forge, by rule.
var forgeActor = ledger.Actor{Kind: ledger.ActorSystem, ID: "forge"}

// publishID keys the publish of one attempt's commit, so the end of a lane stored twice asks once.
func publishID(workOrder, commit string) string { return "publish:" + workOrder + ":" + commit }

// requestPublish asks the worker that ran a lane to push the branch it left and open the pull request of
// it against the work order's base, when the repository reviews through pull requests (ADR-0020). It runs in
// the transaction that ends the lane and makes the review, and the review waits, held, until the pull
// request is open with the commit under review as its head. A repository that reviews branches, a record
// with no forge binding, and a base nobody named ask for nothing, and the review starts as it always did.
func requestPublish(tx ports.WriteTx, reviewed, review workorders.WorkOrder, holder workorders.Holder, actor ledger.Actor) error {
	record, err := tx.Repositories().Repository(reviewed.RepositoryID)
	if errors.Is(err, ports.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	base := cmp.Or(reviewed.Base, record.Base())
	if record.Target() != ledger.ReviewOnPullRequest || record.ForgeBinding == "" || base == "" {
		return nil
	}
	artifacts, err := tx.Evidence().Artifacts(reviewed.ID)
	if err != nil {
		return err
	}
	branch := attemptBranch(artifacts, review.ReviewsFence)
	claim, err := tx.Claims().Find(holder.ClaimID)
	if err != nil {
		return err
	}
	body, err := pullRequestBody(tx.Planning(), reviewed)
	if err != nil {
		return err
	}
	request := ledger.ForgeRequest{
		ID: publishID(reviewed.ID, branch.Digest), Kind: ledger.ForgePublish, WorkOrder: reviewed.ID, Repository: record.ID,
		Worker: claim.Worker, Binding: record.ForgeBinding, Branch: branch.URI, Base: base, Commit: branch.Digest,
		Title: reviewed.Title, Body: body, RequestedAt: tx.Now(),
	}
	first, err := tx.Forge().InsertRequest(request)
	if err != nil || !first {
		return err
	}
	if err := appendForge(tx, ledger.EventForgeRequested, reviewed.ID, request, actor); err != nil {
		return err
	}
	return refreshReviewHolds(tx, reviewed.ID)
}

// pullRequestBody describes the work order, its contract, and its criteria, which is what a person reading
// the pull request needs to judge it beside the review's verdicts.
func pullRequestBody(planned ports.PlanningReader, w workorders.WorkOrder) (string, error) {
	var out strings.Builder
	fmt.Fprintf(&out, "Ropeshift work order `%s`: %s\n", w.ID, w.Title)
	if w.ContractVersion != 0 {
		contract, err := planned.Contract(w.ContractID, w.ContractVersion)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&out, "\n## Contract\n\n`%s` version %d: %s\n", contract.ID, contract.Version, contract.Title)
		if statement := strings.TrimSpace(contract.Statement); statement != "" {
			fmt.Fprintf(&out, "\n%s\n", statement)
		}
		if len(contract.Criteria) > 0 {
			out.WriteString("\n## Criteria\n\n")
			for _, criterion := range contract.Criteria {
				fmt.Fprintf(&out, "- `%s`: %s\n", criterion.ID, criterion.Text)
			}
		}
	}
	out.WriteString("\nThe ropeshift worker pushed this branch and opened this pull request. The review agent's verdicts are posted here as comments, each bound to the commit it judged.\n")
	return out.String(), nil
}

// appendForge records one forge event about the pull request of a work order.
func appendForge(tx ports.WriteTx, eventType, workOrder string, data any, actor ledger.Actor) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = tx.Append(ledger.EventDraft{Type: eventType, Actor: actor, Subject: ledger.Subject{Kind: ledger.SubjectPullRequest, ID: workOrder}, Data: raw})
	return err
}

// publishOf finds the publish of one commit of a work order, which says the commit reaches review through a
// pull request.
func publishOf(forge ports.ForgeReader, workOrder, commit string) (ledger.ForgeRequest, bool, error) {
	request, err := forge.Request(publishID(workOrder, commit))
	if errors.Is(err, ports.ErrNotFound) {
		return ledger.ForgeRequest{}, false, nil
	}
	return request, err == nil, err
}

// pullRequestHold is what holds a review of a commit that reaches review through a pull request: nothing
// once the pull request is open with that commit as its head, and otherwise the worker's forge sign-in when
// it cannot run, or the pull request still to come with what the last attempt said.
func pullRequestHold(forge ports.ForgeReader, workers ports.WorkerReader, workOrder, commit string) (*workorders.StartHold, error) {
	request, found, err := publishOf(forge, workOrder, commit)
	if err != nil || !found {
		return nil, err
	}
	pr, err := forge.PullRequest(workOrder)
	opened := err == nil
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		return nil, err
	}
	if opened && pr.State == ledger.PullRequestOpen && pr.HeadCommit == commit {
		return nil, nil
	}
	name := workerName(workers, request.Worker)
	health, err := healthOf(forge, request.Worker, request.Repository)
	if err != nil {
		return nil, err
	}
	if health != nil && !health.Healthy() {
		return &workorders.StartHold{
			Reason: workorders.HoldForgeUnavailable,
			Detail: strings.TrimSpace(fmt.Sprintf("gh on worker %s is %s: %s", name, health.State, health.Detail)),
		}, nil
	}
	hold := &workorders.StartHold{Reason: workorders.HoldPullRequestPending}
	switch {
	case opened && pr.State != ledger.PullRequestOpen:
		hold.Detail = fmt.Sprintf("pull request #%d is %s", pr.Number, pr.State)
	case opened && request.State == ledger.ForgeDone:
		hold.Detail = fmt.Sprintf("pull request #%d's head is %s, not yet the commit under review, %s", pr.Number, short(pr.HeadCommit), short(commit))
	case request.Detail != "":
		hold.Detail = fmt.Sprintf("attempt %d to push %s and open its pull request failed: %s", request.Attempt-1, request.Branch, request.Detail)
	default:
		hold.Detail = fmt.Sprintf("waiting for worker %s to push %s and open its pull request against %s", name, request.Branch, request.Base)
	}
	return hold, nil
}

func workerName(workers ports.WorkerReader, id string) string {
	record, err := workers.Find(id)
	if err != nil || record.Name == "" {
		return id
	}
	return record.Name
}

// healthOf finds what one worker last said of its forge sign-in for a repository, or nil when it has said
// nothing yet.
func healthOf(forge ports.ForgeReader, worker, repository string) (*ledger.ForgeHealth, error) {
	all, err := forge.Health()
	if err != nil {
		return nil, err
	}
	for _, health := range all {
		if health.Worker == worker && health.Repository == repository {
			return &health, nil
		}
	}
	return nil, nil
}

// reviewHold is what holds one open review from starting, read in a write transaction.
func reviewHold(tx ports.WriteTx, review workorders.WorkOrder) (*workorders.StartHold, error) {
	artifacts, err := tx.Evidence().Artifacts(review.Reviews)
	if err != nil {
		return nil, err
	}
	branch := attemptBranch(artifacts, review.ReviewsFence)
	if branch.Digest == "" {
		return review.StartHeld, nil
	}
	return pullRequestHold(tx.Forge(), tx.Workers(), review.Reviews, branch.Digest)
}

// refreshReviewHolds reads again what holds each ready review of a work order, so a pull request a worker
// just opened releases its review in the transaction that records it.
func refreshReviewHolds(tx ports.WriteTx, workOrder string) error {
	reviews, err := tx.WorkOrders().ReviewsOf(workOrder)
	if err != nil {
		return err
	}
	for _, review := range reviews {
		if review.State != workorders.Ready {
			continue
		}
		hold, err := reviewHold(tx, review)
		if err != nil {
			return err
		}
		if _, err := holdIn(tx, review, hold, forgeActor); err != nil {
			return err
		}
	}
	return nil
}

// holdIn records the reason ready work is not claimed, or takes it back, inside a transaction. The same
// reason again appends nothing.
func holdIn(tx ports.WriteTx, current workorders.WorkOrder, want *workorders.StartHold, actor ledger.Actor) (bool, error) {
	if current.State != workorders.Ready || sameHold(current.StartHeld, want) {
		return false, nil
	}
	if err := tx.WorkOrders().Hold(current.ID, want); err != nil {
		return false, err
	}
	eventType := workorders.EventStartResolved
	if want != nil {
		eventType = workorders.EventStartHeld
	}
	event, err := workorders.ChangeEvent(eventType, current.ID, workorders.Change{Hold: want}, actor)
	if err != nil {
		return false, err
	}
	_, err = tx.Append(event)
	return err == nil, err
}

// forgeReported keeps how one attempt at a forge request came out. A report answers the request it names at
// the attempt it names, from the worker asked, so a stale or a stranger's report changes nothing. A failed
// attempt keeps why and offers the next, and one that worked records what the forge now holds.
func forgeReported(tx ports.WriteTx, worker, id string, e ledger.UploadEvent) error {
	var report ledger.ForgeReport
	if err := json.Unmarshal(e.Payload, &report); err != nil {
		return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	request, err := tx.Forge().Request(report.Request)
	if err != nil {
		return err
	}
	if request.Worker != worker {
		return fmt.Errorf("%w: forge request %s is not this worker's", workorders.ErrInvalid, request.ID)
	}
	if request.State != ledger.ForgeRequested || request.Attempt != report.Attempt {
		return nil
	}
	actor := ledger.Actor{Kind: ledger.ActorWorker, ID: worker}
	if report.Failed {
		if _, err := tx.Forge().FailRequest(request.ID, request.Attempt, report.Detail); err != nil {
			return err
		}
		failed := struct {
			Request string `json:"request"`
			Attempt int64  `json:"attempt"`
			Detail  string `json:"detail"`
		}{request.ID, request.Attempt, report.Detail}
		if err := appendForgeAs(tx, id, e.OccurredAt, ledger.EventForgeFailed, request.WorkOrder, failed, actor); err != nil {
			return err
		}
		return refreshReviewHolds(tx, request.WorkOrder)
	}
	if request.Kind != ledger.ForgeMerge {
		if _, err := tx.Forge().FinishRequest(request.ID, request.Attempt, tx.Now()); err != nil {
			return err
		}
	}
	switch request.Kind {
	case ledger.ForgePublish:
		return publishReported(tx, request, report, id, e.OccurredAt, actor)
	case ledger.ForgeMerge:
		return mergeReported(tx, request, report, actor)
	case ledger.ForgeComment:
		commented := struct {
			Request string `json:"request"`
			Number  int64  `json:"number"`
			Commit  string `json:"commit"`
		}{request.ID, request.Number, request.Commit}
		return appendForgeAs(tx, id, e.OccurredAt, ledger.EventPullRequestCommented, request.WorkOrder, commented, actor)
	}
	return fmt.Errorf("%w: a forge request of kind %s", workorders.ErrInvalid, request.Kind)
}

// appendForgeAs records a forge event under the uploaded event's identity, so a retried upload stores it once.
func appendForgeAs(tx ports.WriteTx, id string, at int64, eventType, workOrder string, data any, actor ledger.Actor) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = tx.Append(ledger.EventDraft{
		ID: id, OccurredAt: at, Type: eventType, Actor: actor, Subject: ledger.Subject{Kind: ledger.SubjectPullRequest, ID: workOrder}, Data: raw,
	})
	return err
}

// openedEvent is the data of EventPullRequestOpened: the pull request, at the head the worker read.
type openedEvent struct {
	Request string `json:"request"`
	Number  int64  `json:"number"`
	URL     string `json:"url"`
	Branch  string `json:"branch"`
	Base    string `json:"base"`
	Head    string `json:"head_commit"`
}

// publishReported records the pull request a worker opened, or found open, and releases the review that
// waited for it once its head is the commit under review.
func publishReported(tx ports.WriteTx, request ledger.ForgeRequest, report ledger.ForgeReport, id string, at int64, actor ledger.Actor) error {
	if report.PullRequest == nil || report.PullRequest.Number <= 0 {
		return fmt.Errorf("%w: a publish that worked names no pull request", workorders.ErrInvalid)
	}
	pr := *report.PullRequest
	err := tx.Forge().SavePullRequest(ledger.WorkPullRequest{
		PullRequest: pr, WorkOrder: request.WorkOrder, Repository: request.Repository, Worker: request.Worker, ReadAt: tx.Now(),
	})
	if err != nil {
		return err
	}
	opened := openedEvent{Request: request.ID, Number: pr.Number, URL: pr.URL, Branch: pr.Branch, Base: pr.Base, Head: pr.HeadCommit}
	if err := appendForgeAs(tx, id, at, ledger.EventPullRequestOpened, request.WorkOrder, opened, actor); err != nil {
		return err
	}
	if err := keepPullRequestBranch(tx, request); err != nil {
		return err
	}
	return refreshReviewHolds(tx, request.WorkOrder)
}

// keepPullRequestBranch has the work keep the branch its pull request opened from, so a rework continues it
// and its push moves the same pull request (ADR-0020). The lane that left the commit has ended, and the
// remote now holds what its worktree held, so its worktree is taken away, which frees the branch for the
// next attempt's worktree.
func keepPullRequestBranch(tx ports.WriteTx, request ledger.ForgeRequest) error {
	order, err := tx.WorkOrders().Get(request.WorkOrder)
	if err != nil {
		return err
	}
	if order.Branch != request.Branch {
		if err := tx.WorkOrders().KeepBranch(order.ID, request.Branch); err != nil {
			return err
		}
		event, err := workorders.ChangeEvent(workorders.EventBranchKept, order.ID, workorders.Change{Branch: request.Branch}, forgeActor)
		if err != nil {
			return err
		}
		if _, err := tx.Append(event); err != nil {
			return err
		}
	}
	artifacts, err := tx.Evidence().Artifacts(order.ID)
	if err != nil {
		return err
	}
	for _, artifact := range artifacts {
		if artifact.Kind != ledger.ArtifactBranch || artifact.Digest != request.Commit || artifact.Lane == "" {
			continue
		}
		ended, err := LaneEnded(tx.Lanes(), artifact.Lane)
		if err != nil || !ended {
			return err
		}
		if err := tx.Lanes().RequestArchive(artifact.Lane, false); err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{
			Type: ledger.EventLaneArchiveRequested, Actor: forgeActor, Subject: ledger.Subject{Kind: ledger.SubjectLane, ID: artifact.Lane},
		})
		return err
	}
	return nil
}

// forgeHealthReported keeps what a worker found of its forge sign-in for a repository. A change appends an
// event and reads again what holds the reviews that wait on that worker there.
func forgeHealthReported(tx ports.WriteTx, worker, id string, e ledger.UploadEvent) error {
	var report ledger.ForgeHealthReport
	if err := json.Unmarshal(e.Payload, &report); err != nil {
		return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	switch report.State {
	case ledger.ForgeHealthy, ledger.ForgeSignedOut, ledger.ForgeMissing:
	default:
		return fmt.Errorf("%w: forge health %q", workorders.ErrInvalid, report.State)
	}
	if _, err := tx.Repositories().Repository(report.Repository); err != nil {
		return err
	}
	changed, err := tx.Forge().SaveHealth(ledger.ForgeHealth{
		Worker: worker, Repository: report.Repository, State: report.State, Detail: report.Detail, CheckedAt: tx.Now(),
	})
	if err != nil || !changed {
		return err
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	_, err = tx.Append(ledger.EventDraft{
		ID: id, OccurredAt: e.OccurredAt, Type: ledger.EventForgeHealth, Actor: ledger.Actor{Kind: ledger.ActorWorker, ID: worker},
		Subject: ledger.Subject{Kind: ledger.SubjectRepository, ID: report.Repository}, Data: raw,
	})
	if err != nil {
		return err
	}
	orders, err := tx.WorkOrders().List()
	if err != nil {
		return err
	}
	for _, order := range orders {
		if order.RepositoryID == report.Repository && order.Kind != workorders.KindReview && !workorders.Terminal(order.State) {
			if err := refreshReviewHolds(tx, order.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// desiredForge lists what one worker has been asked of a forge and has not reported done, and the
// repositories whose review target needs the forge, for the worker to report its sign-in's health there.
func desiredForge(tx ports.ReadTx, worker string, state *ledger.DesiredState) error {
	requests, err := tx.Forge().RequestsFor(worker)
	if err != nil {
		return err
	}
	for _, r := range requests {
		state.Forge = append(state.Forge, ledger.DesiredForge{
			ID: r.ID, Kind: r.Kind, WorkOrder: r.WorkOrder, Repository: r.Repository, Binding: r.Binding, Branch: r.Branch, Base: r.Base,
			Commit: r.Commit, Number: r.Number, Title: r.Title, Body: r.Body, Method: r.Method, Attempt: r.Attempt,
		})
	}
	records, err := tx.Repositories().Repositories()
	if err != nil {
		return err
	}
	bindings := map[string]string{}
	for _, record := range records {
		bindings[record.ID] = record.ForgeBinding
		if !record.Removed() && record.Target() == ledger.ReviewOnPullRequest && record.ForgeBinding != "" {
			state.ForgeRepositories = append(state.ForgeRepositories, ledger.DesiredForgeRepository{Repository: record.ID, Binding: record.ForgeBinding})
		}
	}
	return watched(tx, worker, bindings, state)
}

// watched lists the open pull requests a worker opened whose work is still open, which it reads for the
// ledger: for a merge the person makes, and for a head the forge shows a moment after a push.
func watched(tx ports.ReadTx, worker string, bindings map[string]string, state *ledger.DesiredState) error {
	prs, err := tx.Forge().PullRequests()
	if err != nil {
		return err
	}
	for _, pr := range prs {
		if pr.Worker != worker || pr.State != ledger.PullRequestOpen || bindings[pr.Repository] == "" {
			continue
		}
		order, err := tx.WorkOrders().Get(pr.WorkOrder)
		if err != nil {
			return err
		}
		if workorders.Terminal(order.State) {
			continue
		}
		state.PullRequests = append(state.PullRequests, ledger.DesiredPullRequest{
			WorkOrder: pr.WorkOrder, Repository: pr.Repository, Binding: bindings[pr.Repository], Number: pr.Number, State: pr.State,
			HeadCommit: pr.HeadCommit, MergeCommit: pr.MergeCommit,
		})
	}
	return nil
}

// commentID keys the comment of one review, so a review's end stored twice asks once.
func commentID(review string) string { return "comment:" + review }

// requestVerdictComment asks the worker that opened the reviewed work's pull request to post the review's
// verdicts on it as a comment (FR-REVIEW-9), in the transaction that ends the review. The ledger keeps the
// verdicts as it always does. A review of a commit that reached review on its branch asks for nothing.
func requestVerdictComment(tx ports.WriteTx, review workorders.WorkOrder, result workorders.LaneResult, actor ledger.Actor) error {
	if result.Outcome != workorders.OutcomeCompleted || len(result.Verdicts) == 0 {
		return nil
	}
	artifacts, err := tx.Evidence().Artifacts(review.Reviews)
	if err != nil {
		return err
	}
	commit := attemptBranch(artifacts, review.ReviewsFence).Digest
	publish, found, err := publishOf(tx.Forge(), review.Reviews, commit)
	if err != nil || !found {
		return err
	}
	pr, err := tx.Forge().PullRequest(review.Reviews)
	if errors.Is(err, ports.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	body, err := verdictBody(tx, review, commit, result.Verdicts)
	if err != nil {
		return err
	}
	request := ledger.ForgeRequest{
		ID: commentID(review.ID), Kind: ledger.ForgeComment, WorkOrder: review.Reviews, Repository: publish.Repository, Worker: pr.Worker,
		Binding: publish.Binding, Branch: pr.Branch, Base: pr.Base, Commit: commit, Number: pr.Number, Body: body, RequestedAt: tx.Now(),
	}
	first, err := tx.Forge().InsertRequest(request)
	if err != nil || !first {
		return err
	}
	return appendForge(tx, ledger.EventForgeRequested, review.Reviews, request, actor)
}

// verdictBody writes a review's verdicts as a pull request comment: each criterion with its verdict, its
// reasons, and its citations, the commit they bind to, and where the verdicts left the work.
func verdictBody(tx ports.WriteTx, review workorders.WorkOrder, commit string, verdicts []workorders.CriterionVerdict) (string, error) {
	texts := map[string]string{}
	if review.ContractVersion != 0 {
		contract, err := tx.Planning().Contract(review.ContractID, review.ContractVersion)
		if err != nil {
			return "", err
		}
		for _, criterion := range contract.Criteria {
			texts[criterion.ID] = criterion.Text
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "**Ropeshift review `%s`** of commit `%s`\n\n", review.ID, short(commit))
	for _, v := range verdicts {
		fmt.Fprintf(&out, "- `%s` **%s**", v.Criterion, v.Verdict)
		if text := texts[v.Criterion]; text != "" {
			fmt.Fprintf(&out, " (%s)", text)
		}
		fmt.Fprintf(&out, ": %s", strings.Join(strings.Fields(v.Reasons), " "))
		if len(v.Citations) > 0 {
			fmt.Fprintf(&out, " Cites %s.", "`"+strings.Join(v.Citations, "`, `")+"`")
		}
		out.WriteString("\n")
	}
	reviewed, err := tx.WorkOrders().Get(review.Reviews)
	if err != nil {
		return "", err
	}
	switch reviewed.State {
	case workorders.Ready, workorders.ContractReview:
		out.WriteString("\nThe work goes back for rework on this branch, and its next commit is reviewed again.\n")
	case workorders.Done:
		out.WriteString("\nThe review accepted the work at the repository's merge authority.\n")
	case workorders.Integrating:
		record, err := tx.Repositories().Repository(reviewed.RepositoryID)
		if err != nil {
			return "", err
		}
		if record.Level() == ledger.ReviewApproves {
			out.WriteString("\nThe review approved the work at review_approves: ropeshift merges this commit once every check the base requires is green on it.\n")
		} else {
			fmt.Fprintf(&out, "\nThe review passed every criterion. At %s, the merge of this commit waits for a member's approval.\n", record.Level())
		}
	default:
		out.WriteString("\nThe work waits for a member's decision. These verdicts bind to this commit only; a new commit voids them.\n")
	}
	return out.String(), nil
}

// pullRequestRead keeps what the worker that opened a pull request read of it, when it differs from what the
// ledger holds: a merge or a close, a new head, a change in mergeability or checks. A read that changes
// nothing writes nothing, and the integration tick decides what a merge ends.
func pullRequestRead(tx ports.WriteTx, worker, id string, e ledger.UploadEvent) error {
	var report ledger.PullRequestReport
	if err := json.Unmarshal(e.Payload, &report); err != nil {
		return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	held, err := tx.Forge().PullRequest(report.WorkOrder)
	if err != nil {
		return err
	}
	read := report.PullRequest
	if held.Worker != worker || held.Number != read.Number {
		return fmt.Errorf("%w: pull request %d of %s is not this worker's to read", workorders.ErrInvalid, read.Number, report.WorkOrder)
	}
	if samePullRequest(held.PullRequest, read) {
		return nil
	}
	if err := tx.Forge().SavePullRequest(ledger.WorkPullRequest{
		PullRequest: read, WorkOrder: held.WorkOrder, Repository: held.Repository, Worker: held.Worker, ReadAt: tx.Now(),
	}); err != nil {
		return err
	}
	eventType := ledger.EventPullRequestUpdated
	switch {
	case read.State == ledger.PullRequestMerged && held.State != ledger.PullRequestMerged:
		eventType = ledger.EventPullRequestMerged
	case read.State == ledger.PullRequestClosed && held.State != ledger.PullRequestClosed:
		eventType = ledger.EventPullRequestClosed
	}
	if err := appendForgeAs(tx, id, e.OccurredAt, eventType, held.WorkOrder, read, ledger.Actor{Kind: ledger.ActorWorker, ID: worker}); err != nil {
		return err
	}
	if err := refreshReviewHolds(tx, held.WorkOrder); err != nil {
		return err
	}
	// A required check that turned green, a head that moved, or a merge on the forge is what the integration
	// rule reads, so the step runs at once rather than at the next tick.
	return integrate(tx, held.WorkOrder)
}

// samePullRequest compares what a read can change.
func samePullRequest(a, b ledger.PullRequest) bool {
	return a.State == b.State && a.HeadCommit == b.HeadCommit && a.Mergeable == b.Mergeable && a.MergeCommit == b.MergeCommit &&
		a.MergedAt == b.MergedAt && a.Base == b.Base && slices.Equal(a.Checks, b.Checks)
}
