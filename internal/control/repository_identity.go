package control

import (
	"cmp"
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

// rootsOf is every first commit a clone holds, sorted, with its ID among them.
func rootsOf(clone ports.Clone) []string {
	roots := slices.Clone(clone.Roots)
	if !slices.Contains(roots, clone.ID) {
		roots = append(roots, clone.ID)
	}
	slices.Sort(roots)
	return roots
}

// lowestRoot is the lowest first commit a clone holds, which names a record registered under the earlier rule.
func lowestRoot(clone ports.Clone) string { return rootsOf(clone)[0] }

// holdsRoot says a clone holds a first commit.
func holdsRoot(clone ports.Clone, root string) bool {
	return clone.ID == root || slices.Contains(clone.Roots, root)
}

// matchIn finds the record a clone registers as, as ledger.MatchRepository orders it: the record this worker
// keeps at the clone's path when its ID is one of the clone's roots, else the record whose ID is the clone's
// first-parent root, else a record that is not removed whose ID is its lowest root, which earlier says. With
// none, the clone is a new record under its first-parent root. A removed record is never matched by the
// lowest root, so a workspace that imported a design repository's history does not bring that repository's
// record back. A record with another remote than the clone's is another repository, and is not matched by
// the lowest root unless this worker keeps a clone of it at another path, which is then a clone that shares
// its first commit.
func matchIn(repositories ports.RepositoryReader, clone ports.Clone, keptHere []string, holders map[string]holder) (record ledger.Repository, found, earlier bool, err error) {
	apart := func(record ledger.Repository) bool {
		_, kept := holders[record.ID]
		return !kept && ledger.RemotesDiffer(record.Remote, clone.Remote)
	}
	return ledger.MatchRepository(clone.ID, rootsOf(clone), keptHere, apart, func(id string) (ledger.Repository, bool, error) {
		record, err := repositories.Repository(id)
		if errors.Is(err, ports.ErrNotFound) {
			return ledger.Repository{}, false, nil
		}
		return record, err == nil, err
	})
}

// keptAt lists the records this worker keeps the clone's path as the checkout of.
func (how registration) keptAt(clone ports.Clone) []string {
	var ids []string
	for id, path := range how.held {
		if path == clone.Path {
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
	for _, id := range []string{clone.ID, lowestRoot(clone)} {
		path, ok := how.held[id]
		if !ok || path == clone.Path {
			continue
		}
		found, _, err := how.kept.Find(ctx, []string{path}, 1)
		if err != nil {
			return nil, err
		}
		if at := slices.IndexFunc(found, func(c ports.Clone) bool { return c.Path == path && holdsRoot(c, id) }); at >= 0 {
			out[id] = holder{path: path, remote: found[at].Remote}
		}
	}
	return out, nil
}

// sharesFirstCommit says a clone and the clone this worker keeps for the record it matches are two
// repositories that share a first commit: a remote tells repositories apart, so two clones with the same
// remote are one repository, and two with different remotes, or with none, are not. The same path with a new
// remote is not a second clone, because a remote can move.
func sharesFirstCommit(kept holder, clone ports.Clone) bool {
	if kept.path == clone.Path {
		return false
	}
	was := ledger.RepositoryIdentity(kept.remote, "")
	return was == "" || was != ledger.RepositoryIdentity(clone.Remote, "")
}

// sharedFirstCommit says why a clone is refused for sharing its first commit with a repository this worker
// keeps.
func sharedFirstCommit(root, name, path string) string {
	return fmt.Sprintf("shares its first commit `%s` with repository `%s` at `%s`; ropeshift identifies a repository by that commit, so give this clone a remote of its own or a history of its own",
		short(root), name, path)
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

// again says what becomes of a clone that matches a record an earlier clone of the run registered: refused
// when the two share their first commit, and a second clone of the repository when they do not. It reads the
// records as matchIn does, by the first-parent root first and then by the lowest root.
func (s seenRun) again(clone ports.Clone) (Added, bool) {
	id := clone.ID
	if _, ok := s[id]; !ok {
		if id = lowestRoot(clone); id == clone.ID {
			return Added{}, false
		}
	}
	earlier, ok := s[id]
	if !ok {
		return Added{}, false
	}
	repository := earlier.added.Repository
	if sharesFirstCommit(holder{path: earlier.path, remote: earlier.remote}, clone) {
		return Added{
			Path: clone.Path, Outcome: ledger.RegistrationRefused, Repository: ledger.Repository{ID: id, Remote: clone.Remote},
			Reason: sharedFirstCommit(id, cmp.Or(repository.Name, id), earlier.path),
		}, true
	}
	return Added{
		Path: clone.Path, Outcome: ledger.AlreadyRegistered, Repository: repository, Checkout: earlier.added.Checkout,
		Reason: "a second clone of " + repository.Name,
	}, true
}
