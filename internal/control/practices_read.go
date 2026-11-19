package control

import (
	"context"
	"errors"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// PracticesRead is one repository and what the practices check recorded for it.
type PracticesRead struct {
	Repository ledger.Repository
	// Result is the repository's latest result, and nil before any check reported.
	Result *ledger.PracticesRecord
	// Open is the request offered for the repository that has not expired, and nil when none is.
	Open *ledger.PracticesRequest
	// Now is the time of the read, which the open request's state is read at.
	Now int64
}

// Read reads, for each repository named by ID or by name, or for each active repository when none is named, its latest
// result and its open request, by name. A name that is no active repository of the project is not found.
func (p *Practices) Read(ctx context.Context, scope ledger.WorkspaceID, named ...string) ([]PracticesRead, error) {
	var out []PracticesRead
	err := p.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		out = nil
		all, err := tx.Repositories().Repositories()
		if err != nil {
			return err
		}
		chosen, err := chooseRepositories(all, named)
		if err != nil {
			return err
		}
		now, err := tx.Now()
		if err != nil {
			return err
		}
		for _, repository := range chosen {
			read := PracticesRead{Repository: repository, Now: now}
			record, err := tx.Practices().Result(repository.ID)
			switch {
			case err == nil:
				read.Result = &record
			case !errors.Is(err, ports.ErrNotFound):
				return err
			}
			open, err := tx.Practices().Open(repository.ID, now)
			switch {
			case err == nil:
				read.Open = &open
			case !errors.Is(err, ports.ErrNotFound):
				return err
			}
			out = append(out, read)
		}
		return nil
	})
	return out, err
}

// WorkerNames names each worker by its ID, so a view of a request says which worker it was asked of.
func (p *Practices) WorkerNames(ctx context.Context, scope ledger.WorkspaceID) (map[string]string, error) {
	names := map[string]string{}
	err := p.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		workers, err := tx.Workers().List()
		for _, worker := range workers {
			names[worker.ID] = worker.Name
		}
		return err
	})
	return names, err
}

// Check reads one request, with the state it stands in now: a request offered past its expiry reads as expired,
// whether or not the tick has stored it so.
func (p *Practices) Check(ctx context.Context, scope ledger.WorkspaceID, id string) (ledger.PracticesRequest, error) {
	var request ledger.PracticesRequest
	err := p.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		if request, err = tx.Practices().Request(id); err != nil {
			return err
		}
		now, err := tx.Now()
		request.State = request.StateAt(now)
		return err
	})
	return request, err
}
