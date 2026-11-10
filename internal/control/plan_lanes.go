package control

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// The contract a review lane works to. It is a contract of its own, because a review answers for
// what it found and not for what the contracts it reads ask of their work.
const (
	reviewCriterionID   = "r1"
	reviewCriterionText = "Every question this review found is on the ledger."
	reviewStatement     = "Read this plan and its contracts, and find what a lane would have to stop and ask."
)

// newestContracts reads the newest version of each contract of a plan. Versions arrive in order, so
// the last of an ID is its newest.
func newestContracts(planned ports.PlanningReader, plan string) ([]planning.Contract, error) {
	versions, err := planned.Contracts()
	if err != nil {
		return nil, err
	}
	var out []planning.Contract
	for _, version := range versions {
		if version.PlanID != plan {
			continue
		}
		if len(out) != 0 && out[len(out)-1].ID == version.ID {
			out[len(out)-1] = version
			continue
		}
		out = append(out, version)
	}
	return out, nil
}

// ReviewPlan starts a contract review as a lane reads it (FR-PLAN-4). The client generates the ID,
// so a retry returns what the first attempt wrote. It pins each contract's newest version, because a
// review covers what it read. A member asked for it, so the review itself needs no review, and it
// stands `ready` for a member to claim.
func (w *WorkOrders) ReviewPlan(ctx context.Context, scope ledger.WorkspaceID, id, plan string, actor ledger.Actor) (workorders.WorkOrder, error) {
	if !authority.Allowed(actor, authority.ActionPassReview) {
		return workorders.WorkOrder{}, workorders.ErrForbidden
	}
	var record workorders.WorkOrder
	err := w.store.Write(ctx, scope, "work_order.review_plan", func(tx ports.WriteTx) error {
		existing, err := tx.WorkOrders().Get(id)
		if err == nil {
			record = existing
			return nil
		}
		if !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		draft, err := reviewDraft(tx, id, plan)
		if err != nil {
			return err
		}
		pinned, err := recordLedgerWork(tx, draft, LedgerWork{
			Order: draft, Statement: reviewStatement, Criteria: []planning.Criterion{{ID: reviewCriterionID, Text: reviewCriterionText}},
		}, actor)
		if err != nil {
			return err
		}
		crashpoint.Hit("work_order.review_plan", "recorded")
		if err := recordReview(tx, pinned.ID, actor); err != nil {
			return err
		}
		if _, err := settleReview(tx, pinned, actor); err != nil {
			return err
		}
		record, err = tx.WorkOrders().Get(pinned.ID)
		return err
	})
	if err != nil {
		return workorders.WorkOrder{}, err
	}
	return record, nil
}

// reviewDraft is the review of a plan, in the repository its work runs in, which is where a lane
// reads the files around a contract.
func reviewDraft(tx ports.WriteTx, id, plan string) (workorders.WorkOrder, error) {
	reviewed, err := tx.Planning().Plan(plan)
	if err != nil {
		return workorders.WorkOrder{}, err
	}
	contracts, err := newestContracts(tx.Planning(), plan)
	if err != nil {
		return workorders.WorkOrder{}, err
	}
	draft := workorders.WorkOrder{ID: id, Kind: workorders.KindContractReview, Title: "Review " + reviewed.Title, ReviewsPlan: plan}
	for _, contract := range contracts {
		draft.ReviewPins = append(draft.ReviewPins, planning.ContractKey(contract.ID, contract.Version))
		orders, err := tx.WorkOrders().ByContract(contract.ID)
		if err != nil {
			return workorders.WorkOrder{}, err
		}
		for _, order := range orders {
			if draft.RepositoryID == "" {
				draft.RepositoryID = order.RepositoryID
			}
		}
	}
	if draft.RepositoryID == "" {
		return workorders.WorkOrder{}, fmt.Errorf("%w: plan %s has no work to review", workorders.ErrInvalid, plan)
	}
	return workorders.New(draft)
}

// desiredReview is what a review lane reads, at the versions its review covers. A version it cannot
// find stops the read, because a lane told a part of a plan would pass the rest.
func desiredReview(planned ports.PlanningReader, row workorders.WorkOrder) (*ledger.DesiredReview, error) {
	if row.ReviewsPlan == "" {
		return nil, nil
	}
	plan, err := planned.Plan(row.ReviewsPlan)
	if err != nil {
		return nil, err
	}
	review := &ledger.DesiredReview{Plan: plan.Title}
	for _, pin := range row.ReviewPins {
		id, version, ok := planning.ParseContractKey(pin)
		if !ok {
			return nil, fmt.Errorf("%w: a review pinned %q", workorders.ErrInvalid, pin)
		}
		contract, err := planned.Contract(id, version)
		if err != nil {
			return nil, err
		}
		read := ledger.ReviewedContract{Title: contract.Title, Statement: contract.Statement}
		for _, criterion := range contract.Criteria {
			read.Criteria = append(read.Criteria, criterion.ID+": "+criterion.Text)
		}
		review.Contracts = append(review.Contracts, read)
	}
	return review, nil
}

// passPlanReview is what accepting a review means for the work it read: a person who may pass a
// review has passed it, for the versions it read. A version it did not read, and work a person sent
// elsewhere, stay as they are, and the definition of ready decides each move.
func passPlanReview(tx ports.WriteTx, id string, actor ledger.Actor) error {
	review, err := tx.WorkOrders().Get(id)
	if err != nil {
		return err
	}
	if review.Kind != workorders.KindContractReview || !authority.Allowed(actor, authority.ActionPassReview) {
		return nil
	}
	for _, pin := range review.ReviewPins {
		contract, _, ok := planning.ParseContractKey(pin)
		if !ok {
			return fmt.Errorf("%w: a review pinned %q", workorders.ErrInvalid, pin)
		}
		orders, err := tx.WorkOrders().ByContract(contract)
		if err != nil {
			return err
		}
		for _, order := range orders {
			read := slices.Contains(review.ReviewPins, planning.ContractKey(order.ContractID, order.ContractVersion)) || passesAnyVersion()
			if !read || order.State != workorders.ContractReview || order.ReviewedBy != "" {
				continue
			}
			if err := recordReview(tx, order.ID, actor); err != nil {
				return err
			}
			if _, err := settleReview(tx, order, actor); err != nil {
				return err
			}
		}
	}
	return nil
}

// reviewsUnderWay names the plans a review lane reads now. What it has asked waits for it, so a person
// gets one batch from one review however long it reads, and a lane that parked or ended holds nothing.
func reviewsUnderWay(orders ports.WorkOrderReader) (map[string]bool, error) {
	rows, err := orders.List()
	if err != nil {
		return nil, err
	}
	under := map[string]bool{}
	for _, row := range rows {
		if releasesReviews() {
			break
		}
		if row.ReviewsPlan != "" && (row.State == workorders.Claimed || row.State == workorders.Running) {
			under[row.ReviewsPlan] = true
		}
	}
	return under, nil
}

// PlanSource opens a repository at one commit, for what an accepted plan left there.
type PlanSource func(ctx context.Context, repository, commit string) (ports.WorkSource, error)

// acceptedPlan is a `plan` work order whose files intake has not read.
type acceptedPlan struct {
	order  workorders.WorkOrder
	commit string
}

func (i *Intake) acceptedPlans(ctx context.Context, scope ledger.WorkspaceID) ([]acceptedPlan, error) {
	var out []acceptedPlan
	err := i.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		rows, err := tx.WorkOrders().List()
		if err != nil {
			return err
		}
		for _, row := range rows {
			accepted := row.State == workorders.Done || (takesInUnaccepted() && row.State == workorders.Acceptance)
			if row.Kind != workorders.KindPlan || !accepted || row.PlanCommit != "" {
				continue
			}
			artifacts, err := tx.Evidence().Artifacts(row.ID)
			if err != nil {
				return err
			}
			if branch := attemptBranch(artifacts, row.Fence); branch.Digest != "" {
				out = append(out, acceptedPlan{order: row, commit: branch.Digest})
			}
		}
		return nil
	})
	return out, err
}

// TakeInPlans reads what accepted plans left, each at the commit a person accepted (D17). The
// deterministic core sees only the records that result, and nothing a lane wrote is a record before
// then. It is a tick: a poll that stops part way reads the same commit again, and a digest the ledger
// holds appends nothing. It moves no cursor, because this commit is not where the source stands.
func (i *Intake) TakeInPlans(ctx context.Context, scope ledger.WorkspaceID, open PlanSource) ([]string, error) {
	plans, err := i.acceptedPlans(ctx, scope)
	if err != nil {
		return nil, err
	}
	var taken []string
	var failed []error
	for _, plan := range plans {
		source, err := open(ctx, plan.order.RepositoryID, plan.commit)
		if err != nil {
			failed = append(failed, fmt.Errorf("plan %s: %w", plan.order.ID, err))
			continue
		}
		if err := i.takeIn(ctx, scope, plan, source); err != nil {
			failed = append(failed, fmt.Errorf("plan %s: %w", plan.order.ID, err))
			continue
		}
		taken = append(taken, plan.order.ID)
	}
	return taken, errors.Join(failed...)
}

func (i *Intake) takeIn(ctx context.Context, scope ledger.WorkspaceID, plan acceptedPlan, source ports.WorkSource) error {
	description, err := source.Describe(ctx)
	if err != nil {
		return err
	}
	batch, err := source.Poll(ctx, "")
	if err != nil {
		return err
	}
	crashpoint.Hit("intake.plan", "polled")
	report, err := i.record(ctx, scope, source, batch, planOrigin{workOrder: plan.order.ID})
	if err != nil {
		return err
	}
	crashpoint.Hit("intake.plan", "recorded")
	health, err := source.Health(ctx)
	if err != nil {
		return err
	}
	binding := planning.SourceBinding{
		ID: BindingID(description.Adapter, description.Instance), Port: planning.PortWork, Adapter: description.Adapter, Instance: description.Instance,
		Health: health.State, Detail: health.Detail,
	}
	if movesPlanCursor() {
		binding.Cursor = string(batch.Cursor)
	}
	read := settling{binding: binding, report: report, plan: plan.order.ID, recorded: movesPlanCursor()}
	if err := i.settle(ctx, scope, source, read); err != nil {
		return err
	}
	crashpoint.Hit("intake.plan", "settled")
	detail := fmt.Sprintf("%d contract versions, %d work orders, %d records, %d conflicts, %d files it could not read",
		report.Versions, report.WorkOrders, report.Records, report.Conflicts, len(report.Problems)-report.Conflicts)
	return i.store.Write(ctx, scope, "intake.plan", func(tx ports.WriteTx) error {
		if err := tx.WorkOrders().TakenIn(plan.order.ID, plan.commit); err != nil {
			return err
		}
		event, err := workorders.ChangeEvent(workorders.EventPlanTakenIn, plan.order.ID, workorders.Change{PlanCommit: plan.commit, Reason: detail}, intakeActor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
}
