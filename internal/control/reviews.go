package control

import (
	"context"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// ReviewResult says where a review left a work order, and what still holds it back.
type ReviewResult struct {
	State    workorders.State          `json:"state"`
	Failures []workorders.ReadyFailure `json:"failures,omitempty"`
}

// Reviews holds the step from contract review to `ready`. A person records the
// review, and the pure definition of ready decides the move.
type Reviews struct {
	store ports.Store
}

// NewReviews returns the use cases over a Store.
func NewReviews(store ports.Store) *Reviews {
	return &Reviews{store: store}
}

// Pass records a member's passed review and runs the definition of ready. A failed
// check keeps the review on record, so the work order moves as soon as what held it
// back resolves. A repeated review changes nothing.
func (r *Reviews) Pass(ctx context.Context, scope ledger.WorkspaceID, id string, actor ledger.Actor) (ReviewResult, error) {
	if !authority.Allowed(actor, authority.ActionPassReview) {
		return ReviewResult{}, workorders.ErrForbidden
	}
	var result ReviewResult
	err := r.store.Write(ctx, scope, "review.pass", func(tx ports.WriteTx) error {
		current, err := tx.WorkOrders().Get(id)
		if err != nil {
			return err
		}
		if current.State != workorders.ContractReview {
			if current.ReviewedBy == "" {
				return fmt.Errorf("%w: a review from %s", workorders.ErrIllegalMove, current.State)
			}
			result = ReviewResult{State: current.State}
			return nil
		}
		if current.ReviewedBy == "" {
			if err := recordReview(tx, id, actor); err != nil {
				return err
			}
		}
		crashpoint.Hit("review.pass", "recorded")
		result, err = settleReview(tx, current, actor)
		return err
	})
	if err != nil {
		return ReviewResult{}, err
	}
	return result, nil
}

// SendBack returns a work order to `planning` because its contract must change. A member
// or a reviewing orchestrator decides that, and the review of this version is void.
func (r *Reviews) SendBack(ctx context.Context, scope ledger.WorkspaceID, id, reason string, actor ledger.Actor) error {
	if !authority.Allowed(actor, authority.ActionSendBack) {
		return workorders.ErrForbidden
	}
	return r.store.Write(ctx, scope, "review.send_back", func(tx ports.WriteTx) error {
		current, err := tx.WorkOrders().Get(id)
		if err != nil {
			return err
		}
		edge, err := workorders.Move(current, workorders.TriggerContractChange)
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
		crashpoint.Hit("review.send_back", "moved")
		if err := tx.WorkOrders().Reviewed(id, ""); err != nil {
			return err
		}
		event, err := workorders.ChangeEvent(workorders.EventType(edge.Trigger), id, workorders.Change{
			Trigger: edge.Trigger, Reason: reason,
		}, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
}

// Unblock takes work out of `blocked`, which only a person can do: a lane reports that outcome when no
// answer to a question would change it. TriggerResolved returns the work to `ready` with its review
// standing, and TriggerReplan returns it to `planning`, where the review of this version is void.
func (r *Reviews) Unblock(ctx context.Context, scope ledger.WorkspaceID, id string, trigger workorders.Trigger, reason string, actor ledger.Actor) error {
	if !authority.Allowed(actor, authority.ActionUnblock) {
		return workorders.ErrForbidden
	}
	return r.store.Write(ctx, scope, "review.unblock", func(tx ports.WriteTx) error {
		current, err := tx.WorkOrders().Get(id)
		if err != nil {
			return err
		}
		edge, err := workorders.Move(current, trigger)
		if err != nil {
			return err
		}
		if edge.From != workorders.Blocked {
			return workorders.ErrIllegalMove
		}
		won, err := tx.WorkOrders().Move(id, edge.From, edge.To)
		if err != nil {
			return err
		}
		if !won {
			return workorders.ErrIllegalMove
		}
		if trigger == workorders.TriggerReplan {
			if err := tx.WorkOrders().Reviewed(id, ""); err != nil {
				return err
			}
		}
		event, err := workorders.ChangeEvent(workorders.EventType(edge.Trigger), id, workorders.Change{Trigger: edge.Trigger, Reason: reason}, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
}

func recordReview(tx ports.WriteTx, id string, actor ledger.Actor) error {
	if err := tx.WorkOrders().Reviewed(id, actor.ID); err != nil {
		return err
	}
	_, err := tx.Append(ledger.EventDraft{
		Type:    workorders.EventReviewPassed,
		Actor:   actor,
		Subject: ledger.Subject{Kind: workorders.SubjectKind, ID: id},
	})
	return err
}

// settleReview runs the definition of ready on a reviewed work order.
func settleReview(tx ports.WriteTx, current workorders.WorkOrder, actor ledger.Actor) (ReviewResult, error) {
	failures, err := readyFailures(tx.Planning(), tx.WorkOrders(), tx.Questions(), current)
	if err != nil {
		return ReviewResult{}, err
	}
	if len(failures) > 0 {
		return ReviewResult{State: current.State, Failures: failures}, nil
	}
	if err := takeEdge(tx, current.ID, workorders.TriggerReadyPassed, actor); err != nil {
		return ReviewResult{}, err
	}
	return ReviewResult{State: workorders.Ready}, nil
}

// readyFailures gathers the facts and runs the pure function.
func readyFailures(planned ports.PlanningReader, orders ports.WorkOrderReader, asked ports.QuestionReader, w workorders.WorkOrder) ([]workorders.ReadyFailure, error) {
	var facts workorders.ReadyFacts
	var plan string
	if w.ContractVersion != 0 {
		contract, err := planned.Contract(w.ContractID, w.ContractVersion)
		if err != nil {
			return nil, err
		}
		facts.Criteria, facts.ContractVersion, facts.ContractDigest = len(contract.Criteria), contract.Version, contract.Digest
		plan = contract.PlanID
		known, traced, err := planRequirements(planned, contract.PlanID)
		if err != nil {
			return nil, err
		}
		if traced {
			for _, untraced := range planning.Trace(contract.Criteria, known) {
				facts.Untraced = append(facts.Untraced, untraced.String())
			}
		}
	}
	open, err := asked.OpenBlocking(w.ID, w.ContractID, plan)
	if err != nil {
		return nil, err
	}
	facts.OpenBlockingQuestions = int(open)
	dependencies, err := planned.Dependencies(w.ID)
	if err != nil {
		return nil, err
	}
	for _, d := range dependencies {
		fact, err := dependencyFact(planned, orders, d)
		if err != nil {
			return nil, err
		}
		facts.Dependencies = append(facts.Dependencies, fact)
	}
	return workorders.DefinitionOfReady(facts), nil
}

// dependencyFact resolves what a dependency names. A name nothing holds yet is an
// unknown dependency, which is an answer and not an error.
func dependencyFact(planned ports.PlanningReader, orders ports.WorkOrderReader, d planning.Dependency) (workorders.DependencyFact, error) {
	fact := workorders.DependencyFact{Name: d.DependsOn, Blocking: d.Kind.Blocks()}
	order, found, err := dependencyOrder(planned, orders, d)
	if err != nil || !found {
		return fact, err
	}
	fact.Known, fact.State = true, order.State
	return fact, nil
}

// dependencyOrder finds the work order a dependency names. A name nothing holds yet is not found, which
// is an answer and not an error.
func dependencyOrder(planned ports.PlanningReader, orders ports.WorkOrderReader, d planning.Dependency) (workorders.WorkOrder, bool, error) {
	id := d.DependsOn
	if key, external := d.Key(); external {
		link, err := planned.Link(workorders.SubjectKind, key)
		if errors.Is(err, ports.ErrNotFound) {
			return workorders.WorkOrder{}, false, nil
		}
		if err != nil {
			return workorders.WorkOrder{}, false, err
		}
		id = link.RecordID
	}
	order, err := orders.Get(id)
	if errors.Is(err, ports.ErrNotFound) {
		return workorders.WorkOrder{}, false, nil
	}
	return order, err == nil, err
}

// Promote moves every reviewed work order whose definition of ready now passes, after it passes the
// review of work an approved plan covers. It is a tick: the list is read with no lock, and each item
// is one transaction that reads again.
func (r *Reviews) Promote(ctx context.Context, scope ledger.WorkspaceID, actor ledger.Actor) ([]string, error) {
	// Work an approved plan covers is reviewed first, so the pass below moves it in the same tick.
	if _, err := r.PassCovered(ctx, scope, actor); err != nil {
		return nil, err
	}
	var waiting []string
	err := r.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		rows, err := tx.WorkOrders().List()
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.State == workorders.ContractReview && row.ReviewedBy != "" {
				waiting = append(waiting, row.ID)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var moved []string
	for _, id := range waiting {
		var result ReviewResult
		err := r.store.Write(ctx, scope, "review.promote", func(tx ports.WriteTx) error {
			current, err := tx.WorkOrders().Get(id)
			if err != nil {
				return err
			}
			if current.State != workorders.ContractReview {
				return nil
			}
			result, err = settleReview(tx, current, actor)
			return err
		})
		if err != nil {
			return moved, err
		}
		if result.State == workorders.Ready {
			moved = append(moved, id)
		}
	}
	return moved, nil
}
