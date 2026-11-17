package ledger

import "slices"

// RepositoryLookup reads a project's record by ID, and says whether the project holds one.
type RepositoryLookup func(id string) (record Repository, held bool, err error)

// MatchRepository finds the record a clone registers as, from its first-parent root and every root it holds
// (FR-REPO-14). In order:
//
//  1. a record the caller already ties to the clone, named in preferred, whose ID is a root of the clone and
//     that is not removed, such as the record this worker keeps at the clone's path;
//  2. the record whose ID is the first-parent root, removed or not;
//  3. a record that is not removed whose ID is the clone's lowest root, which earlier says.
//
// A removed record is never matched by the lowest root. No record means the clone is a new record under its
// first-parent root.
func MatchRepository(first string, roots, preferred []string, lookup RepositoryLookup) (record Repository, found, earlier bool, err error) {
	for _, id := range preferred {
		if id != first && !slices.Contains(roots, id) {
			continue
		}
		held, ok, err := lookup(id)
		if err != nil {
			return Repository{}, false, false, err
		}
		if ok && !held.Removed() {
			return held, true, id != first, nil
		}
	}
	record, found, err = lookup(first)
	if err != nil || found {
		return record, found, false, err
	}
	lowest := first
	for _, root := range roots {
		lowest = min(lowest, root)
	}
	if lowest == first {
		return Repository{}, false, false, nil
	}
	record, found, err = lookup(lowest)
	if err != nil || !found || record.Removed() {
		return Repository{}, false, false, err
	}
	return record, true, true, nil
}
