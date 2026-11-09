package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// opEffectDecide names the transaction that keeps a person's word on an effect.
const opEffectDecide = "effect.decide"

// Effects is what a member reads and decides of an effect nothing can observe. A worker's journal holds
// the effect, so the ledger holds only what a person needs, and the worker hears the answer through
// desired state.
type Effects struct {
	store ports.Store
}

// NewEffects returns the use cases over a Store.
func NewEffects(store ports.Store) *Effects { return &Effects{store: store} }

// List reads every effect a person heard of, oldest first.
func (e *Effects) List(ctx context.Context, scope ledger.WorkspaceID) ([]ledger.UnknownEffect, error) {
	var out []ledger.UnknownEffect
	err := e.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.Effects().Effects()
		return err
	})
	return out, err
}

// Get reads one.
func (e *Effects) Get(ctx context.Context, scope ledger.WorkspaceID, id string) (ledger.UnknownEffect, error) {
	var out ledger.UnknownEffect
	err := e.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.Effects().Effect(id)
		return err
	})
	return out, err
}

// LaneEnded says nothing is left that could run a lane's effect again. A lane the ledger has no row
// for may be one whose start has yet to arrive, so it does not count as ended.
func LaneEnded(lanes ports.LaneReader, lane string) (bool, error) {
	row, err := lanes.Row(lane)
	if errors.Is(err, ports.ErrNotFound) {
		return false, nil
	}
	return row.Status.ActualState == ledger.StateEnded, err
}

// Decide keeps a person's word, which only a member has. A worker has no way to know, and neither
// has an agent. A retry after a lost answer hears the same decision, and any other word is refused,
// because the worker may already have acted.
func (e *Effects) Decide(ctx context.Context, scope ledger.WorkspaceID, decision ledger.EffectDecision, actor ledger.Actor) (ledger.UnknownEffect, error) {
	if !authority.Allowed(actor, authority.ActionDecideEffect) {
		return ledger.UnknownEffect{}, workorders.ErrForbidden
	}
	var record ledger.UnknownEffect
	err := e.store.Write(ctx, scope, opEffectDecide, func(tx ports.WriteTx) error {
		row, err := tx.Effects().Effect(decision.ID)
		if err != nil {
			return err
		}
		if row.Replayed(decision) {
			record = row
			return nil
		}
		ended, err := LaneEnded(tx.Lanes(), row.Lane)
		if err != nil {
			return err
		}
		if err := row.Check(decision, ended); err != nil {
			return err
		}
		decided := ledger.EffectDecided{Outcome: decision.Outcome, Reason: decision.Reason, DecidedBy: actor.ID, DecidedAt: tx.Now()}
		moved, err := tx.Effects().Decide(row.ID, decided, string(actor.Kind))
		if err != nil {
			return err
		}
		if !moved {
			return ledger.ErrEffectDecided
		}
		record = row
		record.Outcome, record.Reason, record.DecidedBy, record.DecidedAt = decided.Outcome, decided.Reason, decided.DecidedBy, decided.DecidedAt
		return appendEffect(tx, ledger.EventDraft{Type: ledger.EventEffectDecided, Actor: actor}, row.ID, decided)
	})
	if err != nil {
		return ledger.UnknownEffect{}, err
	}
	return record, nil
}

func appendEffect(tx ports.WriteTx, draft ledger.EventDraft, id string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	draft.Data, draft.Subject = raw, ledger.Subject{Kind: ledger.SubjectEffect, ID: id}
	_, err = tx.Append(draft)
	return err
}

// effectUpload stores what a worker says of an effect. It passes no fence: what a worker may have done
// stays true after it lost its lease, and a person still has to hear of it. A worker speaks only for
// its own journal, because each row names the worker that raised it.
func effectUpload(tx ports.WriteTx, worker, id string, e ledger.UploadEvent) error {
	draft := ledger.EventDraft{ID: id, OccurredAt: e.OccurredAt, Actor: ledger.Actor{Kind: ledger.ActorWorker, ID: worker}}
	if e.Type == ledger.UploadEffectUnknown {
		var report ledger.EffectReport
		if err := json.Unmarshal(e.Payload, &report); err != nil {
			return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
		}
		row := ledger.UnknownEffect{
			ID: id, WorkerID: worker, Key: report.Key, Kind: report.Kind, Lane: e.Lane, WorkOrder: e.WorkOrder, Generation: e.Generation,
			Target: report.Target, Attempt: report.Attempt, RaisedAt: tx.Now(),
		}
		first, err := tx.Effects().Insert(row)
		if err != nil || !first {
			return err
		}
		draft.Type = ledger.EventEffectUnknown
		return appendEffect(tx, draft, id, row)
	}
	var settlement ledger.EffectSettlement
	if err := json.Unmarshal(e.Payload, &settlement); err != nil {
		return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	row, err := tx.Effects().Effect(settlement.ID)
	if err != nil {
		return err
	}
	if row.WorkerID != worker {
		return fmt.Errorf("%w: effect %s is not this worker's", workorders.ErrInvalid, settlement.ID)
	}
	settled := ledger.EffectSettled{SettledAt: tx.Now()}
	first, err := tx.Effects().Settle(row.ID, settled.SettledAt)
	if err != nil {
		return err
	}
	if !first {
		return fmt.Errorf("%w: effect %s has no decision to settle", workorders.ErrInvalid, settlement.ID)
	}
	draft.Type = ledger.EventEffectSettled
	return appendEffect(tx, draft, row.ID, settled)
}

// desiredEffects lists what a person decided and a worker has not acted on. The control plane offers
// each until the worker says it acted, so a lost read costs latency.
func desiredEffects(tx ports.ReadTx, worker string) ([]ledger.DesiredEffect, error) {
	rows, err := tx.Effects().DecidedFor(worker)
	if err != nil {
		return nil, err
	}
	out := make([]ledger.DesiredEffect, 0, len(rows))
	for _, row := range rows {
		out = append(out, ledger.DesiredEffect{ID: row.ID, Key: row.Key, Attempt: row.Attempt, Outcome: row.Outcome})
	}
	return out, nil
}
