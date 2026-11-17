package ledger

import (
	"slices"
	"strings"
)

// RepositoryLookup reads a project's record by ID, and says whether the project holds one.
type RepositoryLookup func(id string) (record Repository, held bool, err error)

// RemotesDiffer says two remotes name different repositories: both name one, and their normalized forms are
// not the same. A remote that is empty names none, so it differs from nothing.
func RemotesDiffer(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	return RepositoryIdentity(a, "") != RepositoryIdentity(b, "")
}

// MatchRepository finds the record a clone registers as, from its first-parent root and every root it holds
// (FR-REPO-14). In order:
//
//  1. a record the caller already ties to the clone, named in preferred, whose ID is a root of the clone and
//     that is not removed, such as the record this worker keeps at the clone's path;
//  2. the record whose ID is the first-parent root, removed or not;
//  3. a record that is not removed whose ID is the clone's lowest root, which earlier says, unless apart says
//     the record is another repository than the clone, as one with a different remote is.
//
// A removed record is never matched by the lowest root. A nil apart never says so. No record means the clone
// is a new record under its first-parent root.
func MatchRepository(first string, roots, preferred []string, apart func(Repository) bool, lookup RepositoryLookup) (record Repository, found, earlier bool, err error) {
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
	if err != nil || !found || record.Removed() || (apart != nil && apart(record)) {
		return Repository{}, false, false, err
	}
	return record, true, true, nil
}
