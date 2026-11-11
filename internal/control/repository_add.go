package control

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// RepositoryAdd is a member's registration of the clones at paths on this worker's machine (ADR-0022).
type RepositoryAdd struct {
	// Paths are absolute paths on the worker's machine.
	Paths []string
	// Depth is how many levels below a path that is no clone are searched. Below 1 searches one.
	Depth int
	// DryRun says what would happen, and records nothing.
	DryRun bool
}

// Added is what became of one clone, or of a path that holds none.
type Added struct {
	Path    string
	Outcome ledger.RepositoryOutcome
	// Repository is the record as it stands, or as it would after a dry run. A path that holds no clone
	// has none, and a refused clone has only what git said of it.
	Repository ledger.Repository
	// Checkout is the path this worker keeps for the repository after the run.
	Checkout string
	// Reason says why a clone or a path was refused, or that a clone is a second one of a repository.
	Reason string
}

// errDryRun undoes a dry run's transaction, which wrote what the registration would have.
var errDryRun = errors.New("a dry run records nothing")

// Add registers, in a project, the repositories of the clones at paths on this worker's machine
// (ADR-0022). Each registration reads git through the worker's checkouts, is one transaction with one
// audited event, and records the clone as this worker's checkout of it. A repository another project holds
// is refused, naming that project, and the others are still registered. A dry run gives the same answers
// and records nothing.
func (r *Repositories) Add(ctx context.Context, scope ledger.WorkspaceID, in RepositoryAdd, kept ports.Checkouts, actor ledger.Actor) ([]Added, error) {
	if !authority.Allowed(actor, authority.ActionRegisterRepository) {
		return nil, workorders.ErrForbidden
	}
	if len(in.Paths) == 0 {
		return nil, fmt.Errorf("%w: name a path to register", ledger.ErrInvalidRepository)
	}
	for _, path := range in.Paths {
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("%w: path %q is not absolute", ledger.ErrInvalidRepository, path)
		}
	}
	clones, unread, err := kept.Find(ctx, in.Paths, max(in.Depth, 1))
	if err != nil {
		return nil, err
	}
	held, err := kept.Of(ctx, scope)
	if err != nil {
		return nil, err
	}
	if held == nil {
		held = map[string]string{}
	}
	var out []Added
	claimed := map[string]bool{}
	first := map[string]Added{}
	for _, clone := range clones {
		if earlier, ok := first[clone.ID]; ok {
			out = append(out, Added{
				Path: clone.Path, Outcome: ledger.AlreadyRegistered, Repository: earlier.Repository, Checkout: earlier.Checkout,
				Reason: "a second clone of " + earlier.Repository.Name,
			})
			continue
		}
		added, err := r.register(ctx, scope, clone, claimed, in.DryRun, actor)
		if err != nil {
			return out, err
		}
		if added.Outcome != ledger.RegistrationRefused {
			if added.Checkout, err = keepCheckout(ctx, kept, scope, held, clone, in.DryRun); err != nil {
				return out, err
			}
			claimed[added.Repository.Name] = true
			first[clone.ID] = added
		}
		out = append(out, added)
	}
	for _, path := range unread {
		out = append(out, Added{Path: path.Path, Outcome: ledger.RegistrationRefused, Reason: path.Reason})
	}
	return out, nil
}

// register makes or refreshes one clone's record in its own transaction, so a refusal undoes only that
// clone's.
func (r *Repositories) register(ctx context.Context, scope ledger.WorkspaceID, clone ports.Clone, claimed map[string]bool, dryRun bool, actor ledger.Actor) (Added, error) {
	added := Added{Path: clone.Path}
	found := DetectedRepository{ID: clone.ID, Remote: clone.Remote, DefaultBranch: clone.DefaultBranch, Directory: clone.Directory}
	err := r.store.Write(ctx, scope, "repository.register", func(tx ports.WriteTx) error {
		look, err := detectIn(tx.Repositories(), found, claimed)
		if err != nil {
			return err
		}
		added.Repository, added.Outcome = look.record, ledger.AlreadyRegistered
		if look.made {
			added.Outcome = ledger.Registered
		}
		if look.moved {
			kind := ledger.EventRepositoryDetected
			if look.made {
				kind = ledger.EventRepositoryRegistered
			}
			event, err := repositoryEvent(kind, clone.ID, detectedEvent{Name: look.named, Remote: clone.Remote, DefaultBranch: clone.DefaultBranch}, actor)
			if err != nil {
				return err
			}
			if _, err := tx.Append(event); err != nil {
				return err
			}
		}
		if dryRun {
			return errDryRun
		}
		return nil
	})
	switch {
	case errors.Is(err, errDryRun):
		return added, nil
	case errors.Is(err, ledger.ErrRepositoryHeld):
		return Added{
			Path: clone.Path, Outcome: ledger.RegistrationRefused, Repository: ledger.Repository{ID: clone.ID, Remote: clone.Remote}, Reason: err.Error(),
		}, nil
	}
	return added, err
}

// keepCheckout records a clone as this worker's checkout of its repository, unless the worker keeps
// another that still holds that repository, because a worker keeps one checkout of each (ADR-0021). It
// says the path kept, and a dry run the path it would keep.
func keepCheckout(ctx context.Context, kept ports.Checkouts, scope ledger.WorkspaceID, held map[string]string, clone ports.Clone, dryRun bool) (string, error) {
	if path, ok := held[clone.ID]; ok && path != clone.Path {
		found, _, err := kept.Find(ctx, []string{path}, 1)
		if err != nil {
			return "", err
		}
		if slices.ContainsFunc(found, func(c ports.Clone) bool { return c.Path == path && c.ID == clone.ID }) {
			return path, nil
		}
	}
	if !dryRun {
		if err := kept.Keep(ctx, scope, clone.ID, clone.Path); err != nil {
			return "", err
		}
		held[clone.ID] = clone.Path
	}
	return clone.Path, nil
}
