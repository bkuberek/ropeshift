package control

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// CIItem is one thing CI asks a person about a work order's pull request (ADR-0037): CI that has not finished, a
// run no runner picked up, or a finding the classification made that no agent can act on. None of them starts an
// agent, and each is read from what the control plane records of the pull request, so a read of it changes nothing.
type CIItem struct {
	WorkOrder   workorders.WorkOrder
	PullRequest ledger.WorkPullRequest
	Kind        string
	Since       int64
	Watch       ledger.CIWatch
	// Finding is what the classification found, for the kinds it raises.
	Finding *ledger.CIFinding
}

// ciReads is what the CI items read, which a read and a write transaction both give.
type ciReads struct {
	forge   ports.ForgeReader
	repos   ports.RepositoryReader
	waits   ports.CIAttentionReader
	history map[string]ledger.CIStats
}

func ciReadsOf(forge ports.ForgeReader, repos ports.RepositoryReader, waits ports.CIAttentionReader) *ciReads {
	return &ciReads{forge: forge, repos: repos, waits: waits, history: map[string]ledger.CIStats{}}
}

// stats reads a repository's CI history by its own keys, once for each repository.
func (r *ciReads) stats(repository ledger.Repository) (ledger.CIStats, error) {
	if stats, ok := r.history[repository.ID]; ok {
		return stats, nil
	}
	settings := repository.CI.Effective()
	records, err := r.forge.CIRecords(repository.ID, int(settings.HistoryWindow))
	if err != nil {
		return ledger.CIStats{}, err
	}
	stats := settings.Stats(records)
	r.history[repository.ID] = stats
	return stats, nil
}

// watch reads one pull request at now: its repository's limits, its history, and what members did about the head.
func (r *ciReads) watch(pr ledger.WorkPullRequest, waits []ledger.CIWaitRecord, now int64) (ledger.CIWatch, error) {
	repository, err := r.repos.Repository(pr.Repository)
	if errors.Is(err, ports.ErrNotFound) {
		// A pull request of a repository that was removed has no keys to be read by, and nothing to ask a person.
		return ledger.CIWatch{Head: pr.HeadCommit}, nil
	}
	if err != nil {
		return ledger.CIWatch{}, err
	}
	stats, err := r.stats(repository)
	if err != nil {
		return ledger.CIWatch{}, err
	}
	var waiting ledger.CIWaiting
	for _, held := range waits {
		if held.WorkOrder == pr.WorkOrder && held.Head == pr.HeadCommit {
			waiting = held.CIWaiting
		}
	}
	return repository.CI.Watch(pr, stats, waiting, now), nil
}

// ciItems lists what CI asks a person at now, oldest first: for each open pull request of work that is not over, the
// item its head raises by the clock, and the findings of its head.
func ciItems(r *ciReads, orders ports.WorkOrderReader, now int64) ([]CIItem, error) {
	prs, err := r.forge.PullRequests()
	if err != nil {
		return nil, err
	}
	waits, err := r.waits.CIWaits()
	if err != nil {
		return nil, err
	}
	findings, err := r.waits.CIFindings()
	if err != nil {
		return nil, err
	}
	var items []CIItem
	for _, pr := range prs {
		if pr.State != ledger.PullRequestOpen {
			continue
		}
		order, err := orders.Get(pr.WorkOrder)
		if err != nil {
			return nil, err
		}
		if workorders.Terminal(order.State) {
			continue
		}
		watch, err := r.watch(pr, waits, now)
		if err != nil {
			return nil, err
		}
		if watch.Kind != "" {
			items = append(items, CIItem{WorkOrder: order, PullRequest: pr, Kind: watch.Kind, Since: watch.Since, Watch: watch})
		}
		for i := range findings {
			if findings[i].WorkOrder == pr.WorkOrder && findings[i].Head == pr.HeadCommit {
				items = append(items, CIItem{
					WorkOrder: order, PullRequest: pr, Kind: findings[i].Kind, Since: findings[i].RaisedAt, Watch: watch, Finding: &findings[i],
				})
			}
		}
	}
	slices.SortStableFunc(items, func(a, b CIItem) int {
		return cmp.Or(cmp.Compare(a.Since, b.Since), cmp.Compare(a.WorkOrder.ID, b.WorkOrder.ID), cmp.Compare(a.Kind, b.Kind))
	})
	return items, nil
}

// CIItems lists what CI asks a person, for the Needs you view.
func CIItems(tx ports.ReadTx) ([]CIItem, error) {
	now, err := tx.Now()
	if err != nil {
		return nil, err
	}
	return ciItems(ciReadsOf(tx.Forge(), tx.Repositories(), tx.CI()), tx.WorkOrders(), now)
}

// CIWatchOf reads where a work order's pull request stands against its repository's CI limits, for the work order's
// page. It is nil for work that has no pull request or whose head has no required check.
func CIWatchOf(tx ports.ReadTx, workOrder string) (*ledger.CIWatch, error) {
	pr, err := tx.Forge().PullRequest(workOrder)
	if errors.Is(err, ports.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	now, err := tx.Now()
	if err != nil {
		return nil, err
	}
	waits, err := tx.CI().CIWaits()
	if err != nil {
		return nil, err
	}
	watch, err := ciReadsOf(tx.Forge(), tx.Repositories(), tx.CI()).watch(pr, waits, now)
	if err != nil || !watch.Watching {
		return nil, err
	}
	return &watch, nil
}

// CIAttention is a member's word on CI that waits for them: waiting longer, rerunning, and the finding a
// classification raises. None of it starts an agent.
type CIAttention struct{ store ports.Store }

// NewCIAttention returns the use cases over a Store.
func NewCIAttention(store ports.Store) *CIAttention { return &CIAttention{store: store} }

// itemFor finds the item a work order raises of one of the kinds.
func (c *CIAttention) itemFor(tx ports.WriteTx, workOrder string, kinds ...string) (CIItem, error) {
	items, err := ciItems(ciReadsOf(tx.Forge(), tx.Repositories(), tx.CI()), tx.WorkOrders(), tx.Now())
	if err != nil {
		return CIItem{}, err
	}
	for _, item := range items {
		if item.WorkOrder.ID == workOrder && slices.Contains(kinds, item.Kind) {
			return item, nil
		}
	}
	return CIItem{}, fmt.Errorf("%w: %s raises none of %v about its CI now", workorders.ErrInvalid, workOrder, kinds)
}

// ciEvent is the data of the events a member's word on CI leaves.
type ciEvent struct {
	Head       string `json:"head"`
	Kind       string `json:"kind,omitempty"`
	ExtendedMs int64  `json:"extended_ms,omitempty"`
	Reruns     int64  `json:"reruns,omitempty"`
	Check      string `json:"check,omitempty"`
	Conclusion string `json:"conclusion,omitempty"`
	// Auto says a rerun was the control plane's, which `ci_auto_rerun` bounds, and Attempt which of its kind it was.
	// Class is what allowed it: the infrastructure class of the checks, or a member's word. Request names the forge
	// request that makes it.
	Auto    bool   `json:"auto,omitempty"`
	Attempt int64  `json:"attempt,omitempty"`
	Class   string `json:"class,omitempty"`
	Request string `json:"request,omitempty"`
	// Checks counts the logs a worker read.
	Checks int64 `json:"checks,omitempty"`
	// Runs and Refused are what the forge did with a rerun, once a worker made it.
	Runs    []string `json:"runs,omitempty"`
	Refused string   `json:"refused,omitempty"`
	// Pending are the runs the forge did not reach, which are asked for again.
	Pending []string `json:"pending,omitempty"`
}

func appendCI(tx ports.WriteTx, eventType, workOrder string, data ciEvent, actor ledger.Actor) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = tx.Append(ledger.EventDraft{Type: eventType, Actor: actor, Subject: ledger.Subject{Kind: ledger.SubjectPullRequest, ID: workOrder}, Data: raw})
	return err
}

// Wait adds time to the CI timeout of a work order's head, which "CI has not finished" offers. With no time named it
// adds one more p85 of the repository's CI, or ci_no_history_p85 when it has no history. Each press adds once, so the
// item returns when the head is past the timeout again, and the head's runs are not touched. The time is bound by the
// largest CI timeout a repository may hold.
func (c *CIAttention) Wait(ctx context.Context, scope ledger.WorkspaceID, workOrder string, by time.Duration, actor ledger.Actor) (ledger.CIWatch, error) {
	if !authority.Allowed(actor, authority.ActionAnswerCI) {
		return ledger.CIWatch{}, workorders.ErrForbidden
	}
	if by != 0 && (by < time.Second || by > ledger.CITimeoutMax) {
		return ledger.CIWatch{}, fmt.Errorf("%w: waiting longer runs from 1s to %s", workorders.ErrInvalid, ledger.CITimeoutMax)
	}
	var watch ledger.CIWatch
	err := c.store.Write(ctx, scope, "work_order.ci_wait", func(tx ports.WriteTx) error {
		item, err := c.itemFor(tx, workOrder, ledger.CIItemNotFinished)
		if err != nil {
			return err
		}
		repository, err := tx.Repositories().Repository(item.PullRequest.Repository)
		if err != nil {
			return err
		}
		more := by
		if more == 0 {
			more = repository.CI.WaitStep(item.Watch)
		}
		waiting, err := tx.CI().ExtendCIWait(workOrder, item.PullRequest.HeadCommit, more.Milliseconds())
		if err != nil {
			return err
		}
		if err := appendCI(tx, ledger.EventCIWaitExtended, workOrder, ciEvent{Head: item.PullRequest.HeadCommit, ExtendedMs: more.Milliseconds()}, actor); err != nil {
			return err
		}
		pr := item.PullRequest
		stats, err := ciReadsOf(tx.Forge(), tx.Repositories(), tx.CI()).stats(repository)
		if err != nil {
			return err
		}
		watch = repository.CI.Watch(pr, stats, waiting, tx.Now())
		return nil
	})
	return watch, err
}

// Rerun is a member's rerun of the checks of a work order's head, which "CI has not finished" and an infrastructure
// failure offer. It counts as the member's own, which the automatic cap `ci_auto_rerun` never counts, clears the
// head's findings, and asks the worker that opened the pull request to rerun every run of the head that finished
// without passing, as a forge request the worker makes through the forge's port: nothing here starts an agent.
func (c *CIAttention) Rerun(ctx context.Context, scope ledger.WorkspaceID, workOrder string, actor ledger.Actor) (ledger.CIWaiting, error) {
	if !authority.Allowed(actor, authority.ActionAnswerCI) {
		return ledger.CIWaiting{}, workorders.ErrForbidden
	}
	var waiting ledger.CIWaiting
	err := c.store.Write(ctx, scope, "work_order.ci_rerun", func(tx ports.WriteTx) error {
		item, err := c.itemFor(tx, workOrder, ledger.CIItemNotFinished, ledger.CIItemInfrastructure)
		if err != nil {
			return err
		}
		repository, err := tx.Repositories().Repository(item.PullRequest.Repository)
		if err != nil {
			return err
		}
		if _, err := tx.CI().ClearCIFindings(workOrder, item.PullRequest.HeadCommit); err != nil {
			return err
		}
		waiting, err = requestRerun(tx, rerunAsked{pr: item.PullRequest, repository: repository, kind: item.Kind, class: ciClassMember}, actor)
		return err
	})
	return waiting, err
}

// Raise keeps a finding of the classification of a work order's head: an infrastructure failure that stayed after the
// automatic reruns, a failure the base branch's tip shares, or a check that waits for a person on the forge. The
// finding raises its Needs you item and notice while the head is the pull request's head, and it reports false for
// the kind of a head already raised. The classification of finished checks writes the same finding inside the
// transaction that kept the read, through the same keep.
func (c *CIAttention) Raise(ctx context.Context, scope ledger.WorkspaceID, finding ledger.CIFinding) (bool, error) {
	if !ledger.FindingKind(finding.Kind) {
		return false, fmt.Errorf("%w: %q is no finding of CI", workorders.ErrInvalid, finding.Kind)
	}
	var raised bool
	err := c.store.Write(ctx, scope, "ci.finding", func(tx ports.WriteTx) error {
		pr, err := tx.Forge().PullRequest(finding.WorkOrder)
		if err != nil {
			return err
		}
		if pr.HeadCommit != finding.Head {
			return fmt.Errorf("%w: %s is no longer the head of pull request %d", workorders.ErrInvalid, finding.Head, pr.Number)
		}
		raised, err = keepCIFinding(tx, finding)
		return err
	})
	return raised, err
}

// keepCIFinding keeps a finding and records it as an event, once for the kind of a head.
func keepCIFinding(tx ports.WriteTx, finding ledger.CIFinding) (bool, error) {
	finding.RaisedAt = tx.Now()
	raised, err := tx.CI().RaiseCIFinding(finding)
	if err != nil || !raised {
		return false, err
	}
	return true, appendCI(tx, ledger.EventCIFindingRaised, finding.WorkOrder, ciEvent{
		Head: finding.Head, Kind: finding.Kind, Check: finding.Check, Conclusion: finding.Conclusion,
	}, ledger.Actor{Kind: ledger.ActorSystem, ID: "ci"})
}
