package control

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// A repository is identified by the root commit reached by following first parents from its default branch
// tip, so merging an unrelated history into it never changes its identity (FR-REPO-14, ADR-0027). A record
// registered before that rule is named by the lowest root the repository had, which is still one of its
// roots, and it keeps its ID.

// earlierIdentity is what a registration says when it matched a clone to a record by the lowest root.
const earlierIdentity = "matched by its earlier identity"

// errSharesFirstCommit ends a registration of a clone that shares its first commit with another repository's.
var errSharesFirstCommit = errors.New("the clone shares its first commit with another repository's")

// errSecondClone ends a registration of a clone that is a second clone of one an earlier part of the run registered.
var errSecondClone = errors.New("the clone is a second clone of one the run registered")

// rootsOf is every first commit a clone holds, sorted, with its ID among them.
func rootsOf(clone ports.Clone) []string {
	roots := slices.Clone(clone.Roots)
	if !slices.Contains(roots, clone.ID) {
		roots = append(roots, clone.ID)
	}
	slices.Sort(roots)
	return roots
}

// holdsRoot says a clone holds a first commit.
func holdsRoot(clone ports.Clone, root string) bool {
	return clone.ID == root || slices.Contains(clone.Roots, root)
}

// matchIn finds the record a clone registers as, as ledger.MatchRepository orders it: the record this worker
// keeps at the clone's path when its ID is one of the clone's roots, else the record whose ID is the clone's
// first-parent root, else a legacy record that is not removed whose ID is another of its roots, lowest first,
// which earlier says. With none, the clone is a new record under its first-parent root. A removed record is
// never matched by another root, so a workspace that imported a design repository's history does not bring
// that repository's record back. A record with another remote than the clone's is another repository, and is
// not matched by another root unless this worker keeps a clone of it at another path, which is then a clone
// that shares its first commit. The records of the clones an earlier part of the same run registered count as
// the project's, which a dry run has not recorded, and as kept clones.
func matchIn(repositories ports.RepositoryReader, clone ports.Clone, keptHere []string, holders map[string]holder, seen seenRun) (record ledger.Repository, found, earlier bool, err error) {
	apart := func(record ledger.Repository) bool {
		_, kept := holders[record.ID]
		_, earlierInRun := seen[record.ID]
		return !kept && !earlierInRun && ledger.RemotesDiffer(record.Remote, clone.Remote)
	}
	return ledger.MatchRepository(clone.ID, rootsOf(clone), keptHere, apart, func(id string) (ledger.Repository, bool, error) {
		record, err := repositories.Repository(id)
		if errors.Is(err, ports.ErrNotFound) {
			if earlier, ok := seen[id]; ok {
				return earlier.added.Repository, true, nil
			}
			return ledger.Repository{}, false, nil
		}
		return record, err == nil, err
	})
}

// samePath says two paths name one directory on this worker's machine, however each is spelled: a checkout kept
// through a symbolic link, as a temporary directory is on a machine whose /var is a link, is the clone at the
// path the link leads to.
func samePath(ctx context.Context, kept ports.Checkouts, a, b string) bool {
	if a == b {
		return true
	}
	if kept == nil {
		return false
	}
	same, err := kept.SameFile(ctx, a, b)
	return err == nil && same
}

// keptAt lists the records this worker keeps the clone's path as the checkout of.
func (how registration) keptAt(ctx context.Context, clone ports.Clone) []string {
	var ids []string
	for id, path := range how.held {
		if samePath(ctx, how.kept, path, clone.Path) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// refuseHeldRoots refuses a clone with no remote when another project holds any first commit it has. A
// repository with no remote is identified by its first commit, so one that merged a history, or whose
// record another project holds under a lower root, is that other project's repository.
func refuseHeldRoots(repositories ports.RepositoryWriter, clone ports.Clone) error {
	if clone.Remote != "" {
		return nil
	}
	for _, root := range rootsOf(clone) {
		if err := repositories.RefuseHeld(root); err != nil {
			return err
		}
	}
	return nil
}

// holder is the clone this worker keeps for a record.
type holder struct {
	path, remote string
}

// holdersOf reads, for each record a clone may match, the clone this worker keeps for it at another path,
// when that path still exists and still holds the record's first commit.
func (how registration) holdersOf(ctx context.Context, clone ports.Clone) (map[string]holder, error) {
	out := map[string]holder{}
	if how.kept == nil {
		return out, nil
	}
	for _, id := range rootsOf(clone) {
		path, ok := how.held[id]
		if !ok || samePath(ctx, how.kept, path, clone.Path) {
			continue
		}
		found, _, err := how.kept.Find(ctx, []string{path}, 1)
		if err != nil {
			return nil, err
		}
		if at := slices.IndexFunc(found, func(c ports.Clone) bool { return samePath(ctx, how.kept, c.Path, path) && holdsRoot(c, id) }); at >= 0 {
			out[id] = holder{path: found[at].Path, remote: found[at].Remote}
		}
	}
	return out, nil
}

// sharesFirstCommit says a clone and the clone this worker keeps for the record it matches are two
// repositories that share a first commit: a remote tells repositories apart, so two clones with the same
// remote are one repository, and two with different remotes, or with none, are not. The same path with a new
// remote is not a second clone, because a remote can move.
func sharesFirstCommit(ctx context.Context, machine ports.Checkouts, kept holder, clone ports.Clone) bool {
	if samePath(ctx, machine, kept.path, clone.Path) {
		return false
	}
	return ledger.SharesFirstCommit(kept.remote, clone.Remote)
}

// heldCommit says why a clone with no remote is refused when another project holds the repository its first
// commit names, which is how a repository with no remote is identified.
func heldCommit(held *ledger.HeldError) string {
	return fmt.Sprintf("shares its first commit `%s` with a repository that project %s holds; ropeshift identifies a repository with no remote by that commit, so give this clone a remote of its own or a history of its own",
		short(held.Identity), held.Project)
}

// seenClone is a clone a run registered, and what became of it.
type seenClone struct {
	path, remote string
	added        Added
}

// seenRun is the clones a run that registers several has registered so far, by the ID of the record each came
// to. A dry run records none, so this is also how its later clones learn of its earlier ones.
type seenRun map[string]seenClone

// note remembers a clone's registration, unless it was refused.
func (s seenRun) note(clone ports.Clone, added Added) {
	if added.Outcome != ledger.RegistrationRefused {
		s[added.Repository.ID] = seenClone{path: clone.Path, remote: clone.Remote, added: added}
	}
}

// secondClone is what becomes of a clone that matches a record an earlier clone of the run registered, and does
// not share its first commit with that clone: a second clone of the repository, which keeps the checkout the
// first one has.
func secondClone(clone ports.Clone, earlier seenClone) Added {
	repository := earlier.added.Repository
	return Added{
		Path: clone.Path, Outcome: ledger.AlreadyRegistered, Repository: repository, Checkout: earlier.added.Checkout,
		Reason: "a second clone of " + repository.Name,
	}
}
