package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Integrations holds the integration step: how reviewed work reaches its base (ADR-0020). At the levels
// where ropeshift merges, accepted work waits in `integrating` until the integration rule lets a worker merge
// its reviewed commit; at a level where the person merges, a pull request the worker read as merged ends the
// work it landed.
type Integrations struct {
	store ports.Store
}

// NewIntegrations returns the use cases over a Store.
func NewIntegrations(store ports.Store) *Integrations { return &Integrations{store: store} }

// mergedOn is the payload of an acceptance a merge made: the assessment it rested on, as any acceptance
// carries, and the pull request the person merged.
type mergedOn struct {
	workorders.Assessment
	PullRequest mergedPullRequest `json:"merged_pull_request"`
}

// mergedPullRequest names a merge: the pull request, the head it merged, and the commit the merge made.
type mergedPullRequest struct {
	Number      int64                 `json:"number"`
	URL         string                `json:"url"`
	HeadCommit  string                `json:"head_commit"`
	MergeCommit string                `json:"merge_commit"`
	MergedAt    string                `json:"merged_at,omitempty"`
	Authority   ledger.MergeAuthority `json:"merge_authority"`
}

// Settle records each merge a person made of work that waits for a member at a level where the person
// decides, and moves that work to `done` (FR-MERGE-9). The merge is the person's decision, made on the forge
// after the verdicts were posted there, so it writes the calibration record a member's acceptance writes
// (ADR-0019), naming no member, because the forge's account is not one. Then it runs the integration step
// for each work order in `integrating`, which every event the rule reads runs too, so the tick only catches
// what those missed. It is a tick: the list is read with no lock, and each item is one transaction that
// reads again. It returns the work orders it ended.
func (i *Integrations) Settle(ctx context.Context, scope ledger.WorkspaceID, actor ledger.Actor) ([]string, error) {
	if !authority.Allowed(actor, authority.ActionRecordMerge) || !authority.Allowed(actor, authority.ActionMerge) {
		return nil, workorders.ErrForbidden
	}
	var merged, integrating []string
	err := i.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		orders, err := tx.WorkOrders().List()
		if err != nil {
			return err
		}
		for _, order := range orders {
			if order.State == workorders.Integrating {
				integrating = append(integrating, order.ID)
			}
		}
		prs, err := tx.Forge().PullRequests()
		if err != nil {
			return err
		}
		for _, pr := range prs {
			if pr.State != ledger.PullRequestMerged {
				continue
			}
			order, err := tx.WorkOrders().Get(pr.WorkOrder)
			if err != nil {
				return err
			}
			if order.State == workorders.Acceptance {
				merged = append(merged, pr.WorkOrder)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var moved []string
	var failed []error
	for _, id := range merged {
		ended, err := i.settle(ctx, scope, id, actor)
		if err != nil {
			failed = append(failed, fmt.Errorf("work order %s: %w", id, err))
			continue
		}
		if ended {
			moved = append(moved, id)
		}
	}
	for _, id := range integrating {
		ended, err := i.step(ctx, scope, id)
		if err != nil {
			failed = append(failed, fmt.Errorf("work order %s: %w", id, err))
			continue
		}
		if ended {
			moved = append(moved, id)
		}
	}
	return moved, errors.Join(failed...)
}

// step runs the integration step for one work order, and reports whether it ended the work.
func (i *Integrations) step(ctx context.Context, scope ledger.WorkspaceID, id string) (bool, error) {
	ended := false
	err := i.store.Write(ctx, scope, "integration.step", func(tx ports.WriteTx) error {
		if err := integrate(tx, id); err != nil {
			return err
		}
		order, err := tx.WorkOrders().Get(id)
		ended = err == nil && order.State == workorders.Done
		return err
	})
	return ended, err
}

func (i *Integrations) settle(ctx context.Context, scope ledger.WorkspaceID, id string, actor ledger.Actor) (bool, error) {
	ended := false
	err := i.store.Write(ctx, scope, "integration.merged", func(tx ports.WriteTx) error {
		ended = false
		current, err := tx.WorkOrders().Get(id)
		if err != nil {
			return err
		}
		if current.State != workorders.Acceptance {
			return nil
		}
		pr, err := tx.Forge().PullRequest(id)
		if err != nil || pr.State != ledger.PullRequestMerged {
			return err
		}
		at, err := readAttempt(tx.Planning(), tx.Evidence(), tx.Repositories(), current)
		if err != nil {
			return err
		}
		if !at.authority.PersonDecides() {
			return nil
		}
		payload, err := json.Marshal(mergedOn{Assessment: at.assessment, PullRequest: mergedPullRequest{
			Number: pr.Number, URL: pr.URL, HeadCommit: pr.HeadCommit, MergeCommit: pr.MergeCommit, MergedAt: pr.MergedAt, Authority: at.authority,
		}})
		if err != nil {
			return err
		}
		reason := fmt.Sprintf("merged pull request #%d on the forge", pr.Number)
		if err := recordCalibration(tx, current, at, workorders.DecisionAccept, workorders.SourceDecision, reason, ledger.Actor{}); err != nil {
			return err
		}
		if err := recordPersonsMerge(tx, current, pr, at.authority); err != nil {
			return err
		}
		change := workorders.Change{Trigger: workorders.TriggerAcceptancePassed, Payload: payload}
		if err := leaveAcceptance(tx, id, change, actor, func() {}); err != nil {
			return err
		}
		if err := passPlanReview(tx, id, actor); err != nil {
			return err
		}
		if _, err := fireWakes(tx, "done:"+id, actor, parking.WorkOrderDone); err != nil {
			return err
		}
		ended = true
		return nil
	})
	return ended, err
}

// recordPersonsMerge keeps the person's merge on the forge as an approval and its merge (FR-MERGE-6): the
// person approved the head they merged, on the forge, under an account the ledger does not know.
func recordPersonsMerge(tx ports.WriteTx, order workorders.WorkOrder, pr ledger.WorkPullRequest, level ledger.MergeAuthority) error {
	record := ledger.IntegrationRecord{
		ID: ledger.IntegrationID(order.ID, pr.HeadCommit), WorkOrder: order.ID, PullRequest: pr.Number, ApprovedCommit: pr.HeadCommit,
		MergeAuthority: level, ApprovedByKind: ledger.ApprovedByForge, ApprovedAt: tx.Now(), MergedCommit: pr.MergeCommit, MergedAt: tx.Now(),
	}
	added, err := tx.Forge().InsertIntegration(record)
	if err != nil || !added {
		return err
	}
	return appendIntegration(tx, ledger.EventIntegrationMerged, order.ID, mergedWork{Record: record, URL: pr.URL, ForgeMerged: pr.MergedAt}, forgeActor)
}
