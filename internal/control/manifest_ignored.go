package control

import (
	"context"
	"fmt"
	"path/filepath"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// unignoredReason is what a manifest path inside the planning repository's clone says when that repository does
// not ignore it, as an outcome's reason and as a problem of the manifest (FR-REPO-15): the clone there is
// registered all the same, and its files show as untracked content in the planning repository.
func unignoredReason(path string) string {
	return fmt.Sprintf("the planning repository does not ignore `%s`; add it to `.gitignore`, or its files show as untracked content there and can be committed by mistake", path)
}

// unignoredIn says the path a manifest entry names, joined to the checkout of the planning repository, leads
// inside the planning repository's clone, symbolic links followed, and that repository does not ignore it. A
// path that leads nowhere, outside the clone, or to the clone itself is no folder of it, and where such a path
// leads is said when the entry is read.
func unignoredIn(ctx context.Context, kept ports.Checkouts, clone, at string) (bool, error) {
	real, reserved, err := kept.Resolve(ctx, at)
	if err != nil || reserved || !within(clone, real) {
		return false, nil
	}
	rel, err := filepath.Rel(clone, real)
	if err != nil {
		return false, err
	}
	ignored, err := kept.Ignored(ctx, clone, filepath.ToSlash(rel))
	return !ignored, err
}

// withUnignoredNote adds to an outcome that the planning repository does not ignore the path of its entry, which
// is joined to the planning repository's checkout as at. A refused entry is left as it is.
func withUnignoredNote(ctx context.Context, kept ports.Checkouts, dir planningDirectory, entry planning.ManifestRepository, added Added, at string) (Added, error) {
	if added.Outcome == ledger.RegistrationRefused || dir.root == "" {
		return added, nil
	}
	unignored, err := unignoredIn(ctx, kept, dir.clone, at)
	if err != nil || !unignored {
		return added, err
	}
	added.Reason = noted(added.Reason, unignoredReason(entry.Path))
	return added, nil
}

// noted adds a note to a reason, after what it already says.
func noted(reason, note string) string {
	if reason == "" {
		return note
	}
	return reason + "; " + note
}

// unignoredPaths finds, for each manifest of a batch whose repository's clone this worker keeps, the paths
// that lead inside that clone and that its repository does not ignore, by the manifest file's path, in the
// order the manifest lists them. It reads git before any transaction, because a transaction asks no source
// (R17). A manifest whose repository this machine keeps no clone of, and a path git cannot answer for, say
// nothing here, and registering the entry says it again.
func (i *Intake) unignoredPaths(ctx context.Context, scope ledger.WorkspaceID, batch ports.ChangeBatch) map[string][]string {
	if i.kept == nil {
		return nil
	}
	var held map[string]string
	out := map[string][]string{}
	for _, item := range batch.Records {
		manifest := item.File.Repositories
		if manifest == nil || len(manifest.Repositories) == 0 {
			continue
		}
		if held == nil {
			var err error
			if held, err = i.kept.Of(ctx, scope); err != nil || held == nil {
				return nil
			}
		}
		root := held[item.Key.Instance]
		if root == "" {
			continue
		}
		clone, _, err := i.kept.Resolve(ctx, root)
		if err != nil {
			continue
		}
		for _, entry := range manifest.Repositories {
			if unignored, err := unignoredIn(ctx, i.kept, clone, filepath.Join(root, filepath.FromSlash(entry.Path))); err == nil && unignored {
				out[item.Path] = append(out[item.Path], entry.Path)
			}
		}
	}
	return out
}
