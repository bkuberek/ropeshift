package control

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// PersonalSetups records what a member accepted of their own agent CLI setup that leaked into a machine's lanes
// (ADR-0040).
type PersonalSetups struct {
	store ports.Store
}

// NewPersonalSetups returns the use case over a store.
func NewPersonalSetups(store ports.Store) *PersonalSetups { return &PersonalSetups{store: store} }

// Accept records that a member accepted one piece of their own setup for a worker's lanes on one CLI version, so the
// leak is not raised again there. It is an event on the worker in the project's ledger, and accepting a piece again
// records nothing more. A worker none of the project's lanes ran on is refused.
func (p *PersonalSetups) Accept(ctx context.Context, scope ledger.WorkspaceID, worker string, accepted ledger.SetupAcceptance, actor ledger.Actor) error {
	if !authority.Allowed(actor, authority.ActionAcceptPersonalSetup) {
		return workorders.ErrForbidden
	}
	if worker == "" || accepted.Version == "" || accepted.Kind == "" || accepted.Name == "" {
		return fmt.Errorf("%w: an acceptance names a worker, a version, and a piece's kind and name", workorders.ErrInvalid)
	}
	data, err := json.Marshal(accepted)
	if err != nil {
		return err
	}
	return p.store.Write(ctx, scope, "personal_setup.accept", func(tx ports.WriteTx) error {
		if err := ranLanesOn(tx.Lanes(), worker); err != nil {
			return err
		}
		known, err := AcceptedSetups(tx, worker)
		if err != nil || slices.Contains(known, accepted) {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{
			Type: ledger.EventPersonalSetupAccepted, Actor: actor, Data: data,
			Subject: ledger.Subject{Kind: ledger.SubjectWorker, ID: worker},
		})
		return err
	})
}

// eventsAbout is the one read AcceptedSetups needs, which a read and a write both have.
type eventsAbout interface {
	EventsAbout(subject ledger.Subject) ([]ledger.Event, error)
}

// AcceptedSetups reads what members accepted of their own setup for one worker's lanes.
func AcceptedSetups(tx eventsAbout, worker string) ([]ledger.SetupAcceptance, error) {
	events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectWorker, ID: worker})
	if err != nil {
		return nil, err
	}
	var out []ledger.SetupAcceptance
	for _, e := range events {
		if e.Type != ledger.EventPersonalSetupAccepted {
			continue
		}
		var accepted ledger.SetupAcceptance
		if err := json.Unmarshal(e.Data, &accepted); err != nil {
			return nil, err
		}
		out = append(out, accepted)
	}
	return out, nil
}

// ranLanesOn refuses a worker that none of the project's lanes ran on, so an acceptance names a machine whose lanes
// the project knows.
func ranLanesOn(lanes ports.LaneReader, worker string) error {
	rows, err := lanes.Rows()
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(rows, func(row ledger.LaneRow) bool { return row.WorkerID == worker }) {
		return fmt.Errorf("%w: no lane of this project ran on worker %s", ports.ErrNotFound, worker)
	}
	return nil
}
