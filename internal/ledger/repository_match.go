package ledger

import (
	"fmt"
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

// NetworkRemote says a remote names a repository on a host, as a URL or git's scp-like form does. A path on this
// machine, relative or absolute, or a file URL names a place that means something only beside the clone that holds
// it, so two clones can share one, and it names no repository.
func NetworkRemote(remote string) bool {
	host, _ := splitRemote(strings.TrimSpace(remote))
	return host != ""
}

// MatchRepository finds the record a clone registers as, from its first-parent root and every root it holds
// (FR-REPO-14). In order:
//
//  1. a record the caller already ties to the clone, named in preferred, whose ID is a root of the clone and
//     that is not removed, such as the record this worker keeps at the clone's path;
//  2. the record whose ID is the first-parent root, removed or not;
//  3. the first legacy record, over the clone's other roots in ascending order, that is not removed and that
//     apart does not say is another repository than the clone, as one with a different remote is. Earlier
//     says it. A record made since is named by its first-parent root, and no other root of a clone matches
//     it, so two repositories that merged one history are each their own record.
//
// A removed record is never matched by one of the other roots. A nil apart never says so. No record means
// the clone is a new record under its first-parent root.
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
	for _, root := range slices.Sorted(slices.Values(roots)) {
		if root == first {
			continue
		}
		record, found, err = lookup(root)
		if err != nil {
			return Repository{}, false, false, err
		}
		if found && record.IdentityLegacy && !record.Removed() && (apart == nil || !apart(record)) {
			return record, true, true, nil
		}
	}
	return Repository{}, false, false, nil
}

// SharesFirstCommit says two clones of one first commit are two repositories: a remote tells repositories
// apart, so two clones with the same remote are one repository, and two with different remotes, or one with
// none, are not.
func SharesFirstCommit(keptRemote, cloneRemote string) bool {
	was := RepositoryIdentity(keptRemote, "")
	return was == "" || was != RepositoryIdentity(cloneRemote, "")
}

// SharedFirstCommit says why a clone is refused for sharing its first commit with a repository this worker
// keeps at a path.
func SharedFirstCommit(root, name, path string) string {
	return fmt.Sprintf("shares its first commit `%s` with repository `%s` at `%s`; ropeshift identifies a repository by that commit, so give this clone a remote of its own or a history of its own",
		ShortCommit(root), name, path)
}

// ShallowClone says why a shallow clone is refused, after its path: the commit it holds with no parent is a
// grafted one that changes with its depth, not the first commit that identifies its repository.
const ShallowClone = "is a shallow clone; its first commit is not its repository's; run git fetch --unshallow"

// ShortCommit is the first eight characters of a commit ID, which name it in a message.
func ShortCommit(commit string) string {
	if len(commit) > 8 {
		return commit[:8]
	}
	return commit
}
