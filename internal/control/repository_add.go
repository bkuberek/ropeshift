package control

import (
	"cmp"
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
	// Name, Manifest, and Remote are the repository's name, the path of its clone, and its remote as the
	// manifest that listed it writes them, for a registration from one. Path is then where that path led.
	Name     string
	Manifest string
	Remote   string
	Path     string
	Outcome  ledger.RepositoryOutcome
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

// errStaysRemoved ends a registration that may not bring back the repository the project removed.
var errStaysRemoved = errors.New("the repository stays removed")

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
	seen := seenRun{}
	for _, clone := range clones {
		if again, ok := seen.again(clone); ok {
			out = append(out, again)
			continue
		}
		added, err := r.register(ctx, scope, clone, registration{claimed: claimed, dryRun: in.DryRun, restore: true, kept: kept, held: held}, actor)
		if err != nil {
			return out, err
		}
		if added.Outcome != ledger.RegistrationRefused {
			if added.Checkout, err = keepCheckout(ctx, kept, scope, held, clone, added.Repository.ID, in.DryRun); err != nil {
				return out, err
			}
			if added, err = r.withRootNote(ctx, scope, kept, added, added.Checkout); err != nil {
				return out, err
			}
			claimed[added.Repository.Name] = true
			seen.note(clone, added)
		}
		out = append(out, added)
	}
	for _, path := range unread {
		out = append(out, Added{Path: path.Path, Outcome: ledger.RegistrationRefused, Reason: path.Reason})
	}
	return out, nil
}

// registration is how one clone is registered.
type registration struct {
	// claimed are the names a run that registers several clones has taken so far.
	claimed map[string]bool
	dryRun  bool
	// restore brings a repository the project removed back into it.
	restore bool
	// planning names a repository the project's planning repository when this registration makes a record,
	// unless a member named one already (ADR-0022): the project's one repository planned until now, and a
	// second one would leave it none.
	planning string
	// cause is what the registration's event names it came from.
	cause registrationCause
	// kept and held are this worker's machine and its checkouts in the project, by record ID, which tell a
	// clone that shares its first commit with another repository's from a second clone of one.
	kept ports.Checkouts
	held map[string]string
}

// register makes or refreshes one clone's record in its own transaction, so a refusal undoes only that
// clone's.
func (r *Repositories) register(ctx context.Context, scope ledger.WorkspaceID, clone ports.Clone, how registration, actor ledger.Actor) (Added, error) {
	added := Added{Path: clone.Path}
	holders, err := how.holdersOf(ctx, clone)
	if err != nil {
		return added, err
	}
	// The record the clone registers as is read in the transaction, and these say what it found.
	id, sharedWith, keptHere := clone.ID, "", how.keptAt(clone)
	var gone ledger.Repository
	err = r.store.Write(ctx, scope, "repository.register", func(tx ports.WriteTx) error {
		if err := refuseHeldRoots(tx.Repositories(), clone); err != nil {
			return err
		}
		record, matched, earlier, err := matchIn(tx.Repositories(), clone, keptHere)
		if err != nil {
			return err
		}
		id, sharedWith, added.Reason = clone.ID, "", ""
		if matched {
			id = record.ID
			if kept, ok := holders[id]; ok && !record.Removed() && sharesFirstCommit(kept, clone) {
				sharedWith = sharedFirstCommit(id, cmp.Or(record.Name, id), kept.path)
				return errSharesFirstCommit
			}
		}
		if earlier {
			added.Reason = earlierIdentity
		}
		found := DetectedRepository{ID: id, Remote: clone.Remote, DefaultBranch: clone.DefaultBranch, Directory: clone.Directory}
		look, err := detectIn(tx.Repositories(), found, how.claimed)
		switch {
		case look.record.Removed() && !how.restore:
			gone = look.record
			return errStaysRemoved
		case look.record.Removed():
			// The project removed it, and a member registering it again brings it back.
			if look, err = restoreIn(tx.Repositories(), found); err != nil {
				return err
			}
		}
		if err != nil {
			return err
		}
		added.Repository, added.Outcome = look.record, ledger.AlreadyRegistered
		if look.made {
			added.Outcome = ledger.Registered
		}
		if err := appendUnshadowed(tx, look, actor); err != nil {
			return err
		}
		if look.made && how.planning != "" {
			if err := namePlanning(tx, how.planning, actor); err != nil {
				return err
			}
		}
		if look.moved {
			kind := ledger.EventRepositoryDetected
			if look.made {
				kind = ledger.EventRepositoryRegistered
			}
			event, err := repositoryEvent(kind, id, detectedEvent{Name: look.named, Remote: clone.Remote, DefaultBranch: clone.DefaultBranch, registrationCause: how.cause}, actor)
			if err != nil {
				return err
			}
			if _, err := tx.Append(event); err != nil {
				return err
			}
		}
		if how.dryRun {
			return errDryRun
		}
		return nil
	})
	switch {
	case errors.Is(err, errDryRun):
		return added, nil
	case errors.Is(err, errStaysRemoved):
		return Added{
			Path: clone.Path, Outcome: ledger.RegistrationRefused, Repository: ledger.Repository{ID: id, Remote: clone.Remote}, Reason: staysRemoved(gone),
		}, nil
	case errors.Is(err, errSharesFirstCommit):
		return Added{
			Path: clone.Path, Outcome: ledger.RegistrationRefused, Repository: ledger.Repository{ID: id, Remote: clone.Remote}, Reason: sharedWith,
		}, nil
	case errors.Is(err, ledger.ErrRepositoryHeld):
		reason := err.Error()
		if held := (*ledger.HeldError)(nil); clone.Remote == "" && errors.As(err, &held) {
			reason = heldCommit(held)
		}
		return Added{
			Path: clone.Path, Outcome: ledger.RegistrationRefused, Repository: ledger.Repository{ID: id, Remote: clone.Remote}, Reason: reason,
		}, nil
	}
	return added, err
}

// namePlanning names a repository the project's planning repository, as a member's change to its record,
// when no repository is named yet.
func namePlanning(tx ports.WriteTx, id string, actor ledger.Actor) error {
	all, err := tx.Repositories().Repositories()
	if err != nil || slices.ContainsFunc(all, func(record ledger.Repository) bool { return record.Planning && !record.Removed() }) {
		return err
	}
	before, err := tx.Repositories().Repository(id)
	if err != nil || before.Removed() {
		return err
	}
	after := before
	after.Planning = true
	return saveSettings(tx, before, after, actor)
}

// restoreIn brings a removed record back into its project, refreshed with what git says of it now, as a
// registration that made it.
func restoreIn(repositories ports.RepositoryWriter, found DetectedRepository) (detection, error) {
	if err := repositories.Restore(found.ID); err != nil {
		return detection{}, err
	}
	if _, err := repositories.Detected(found.ID, found.Remote, found.DefaultBranch); err != nil {
		return detection{}, err
	}
	record, err := repositories.Repository(found.ID)
	return detection{record: record, named: record.Name, made: true, moved: true}, err
}

// keepCheckout records a clone as this worker's checkout of its repository, unless the worker keeps
// another that still holds that repository, because a worker keeps one checkout of each (ADR-0021). It
// says the path kept, and a dry run the path it would keep.
func keepCheckout(ctx context.Context, kept ports.Checkouts, scope ledger.WorkspaceID, held map[string]string, clone ports.Clone, id string, dryRun bool) (string, error) {
	if path, ok := held[id]; ok && path != clone.Path {
		found, _, err := kept.Find(ctx, []string{path}, 1)
		if err != nil {
			return "", err
		}
		if slices.ContainsFunc(found, func(c ports.Clone) bool { return c.Path == path && holdsRoot(c, id) }) {
			return path, nil
		}
	}
	if !dryRun {
		if err := kept.Keep(ctx, scope, id, clone.Path); err != nil {
			return "", err
		}
		held[id] = clone.Path
	}
	return clone.Path, nil
}
