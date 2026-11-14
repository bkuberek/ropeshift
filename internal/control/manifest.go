package control

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// RegisterApproved registers, for the member who approved a plan, each repository the manifest of an
// approved plan version lists and the project lacks (ADR-0022, FR-REPO-8). The worker whose machine keeps
// the planning repository's clone reads each path beside it; a repository with no clone at its path is not
// registered, says why, and a later approval or a member's registration by hand registers it. A
// repository the project holds is named as held, and nothing changes its settings. Registration is a
// member's act, so no tick runs this: the approval does.
func (r *Repositories) RegisterApproved(ctx context.Context, scope ledger.WorkspaceID, kept ports.Checkouts, dryRun bool, actor ledger.Actor) ([]Added, error) {
	if !authority.Allowed(actor, authority.ActionRegisterRepository) {
		return nil, workorders.ErrForbidden
	}
	var entries []planning.ManifestRepository
	err := r.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		entries, err = approvedManifests(tx)
		return err
	})
	if err != nil || len(entries) == 0 {
		return nil, err
	}
	return r.registerEntries(ctx, scope, entries, kept, dryRun, actor)
}

// RegisterManifest registers what one manifest lists, as a member setting a project up by hand does with
// no plan approval. Its paths are read beside the planning repository's clone on this worker's machine.
func (r *Repositories) RegisterManifest(ctx context.Context, scope ledger.WorkspaceID, manifest planning.RepositoriesFile, kept ports.Checkouts, dryRun bool, actor ledger.Actor) ([]Added, error) {
	if !authority.Allowed(actor, authority.ActionRegisterRepository) {
		return nil, workorders.ErrForbidden
	}
	return r.registerEntries(ctx, scope, manifest.Repositories, kept, dryRun, actor)
}

// approvedManifests lists the repositories the manifest of each plan's newest approved version names, at
// the version of the manifest that approval covered. A name two manifests list is read once.
func approvedManifests(tx ports.ReadTx) ([]planning.ManifestRepository, error) {
	rows, err := tx.Approvals().All()
	if err != nil {
		return nil, err
	}
	newest := map[string]authority.Approval{}
	for _, row := range rows {
		if row.Kind == authority.KindPlan && row.State == authority.ApprovalApproved && row.Plan != "" && row.PlanVersion > newest[row.Plan].PlanVersion {
			newest[row.Plan] = row
		}
	}
	var out []planning.ManifestRepository
	seen := map[string]bool{}
	for _, plan := range slices.Sorted(maps.Keys(newest)) {
		row := newest[plan]
		version, err := tx.Planning().PlanVersion(plan, row.PlanVersion)
		if errors.Is(err, ports.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if version.Digest != row.SubjectDigest {
			continue
		}
		for _, entry := range version.Records {
			if planning.EntryKind(entry.Ref) != string(planning.FileRepositories) {
				continue
			}
			manifest, found, err := manifestAt(tx.Planning(), entry)
			if err != nil {
				return nil, err
			}
			for _, repository := range manifest.Repositories {
				if found && !seen[repository.Name] {
					seen[repository.Name] = true
					out = append(out, repository)
				}
			}
		}
	}
	return out, nil
}

// manifestAt reads the version of a manifest an approved plan version names by its digest.
func manifestAt(planned ports.PlanningReader, entry planning.Entry) (planning.RepositoriesFile, bool, error) {
	var manifest planning.RepositoriesFile
	latest, err := planned.LatestDocumentVersion(entry.Ref)
	if errors.Is(err, ports.ErrNotFound) {
		return manifest, false, nil
	}
	if err != nil {
		return manifest, false, err
	}
	for version := latest.Version; version >= 1; version-- {
		held, err := planned.DocumentVersion(entry.Ref, version)
		if errors.Is(err, ports.ErrNotFound) {
			continue
		}
		if err != nil {
			return manifest, false, err
		}
		if held.Digest == entry.Digest {
			return manifest, true, held.Decode(&manifest)
		}
	}
	return manifest, false, nil
}

// registerEntries registers each repository a manifest lists that the project lacks, from the clone at its
// path beside the planning repository's clone, under the manifest's name for it. Each is one transaction and
// one audited event with the settings a person who chose nothing gets, as registering a clone by hand.
func (r *Repositories) registerEntries(ctx context.Context, scope ledger.WorkspaceID, entries []planning.ManifestRepository, kept ports.Checkouts, dryRun bool, actor ledger.Actor) ([]Added, error) {
	held := map[string]ledger.Repository{}
	err := r.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		all, err := tx.Repositories().Repositories()
		for _, record := range all {
			if !record.Removed() && record.Name != "" {
				held[record.Name] = record
			}
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	dir, why, err := r.planningClone(ctx, scope, kept)
	if err != nil {
		return nil, err
	}
	checkouts, err := kept.Of(ctx, scope)
	if err != nil {
		return nil, err
	}
	if checkouts == nil {
		checkouts = map[string]string{}
	}
	out := make([]Added, 0, len(entries))
	claimed := map[string]bool{}
	for _, entry := range entries {
		at := entry.Path
		if dir.root != "" {
			at = filepath.Join(dir.root, filepath.FromSlash(entry.Path))
		}
		if record, ok := held[entry.Name]; ok {
			out = append(out, Added{Name: entry.Name, Path: at, Outcome: ledger.AlreadyRegistered, Repository: record, Checkout: checkouts[record.ID]})
			continue
		}
		refused := func(path, reason string) Added {
			return Added{Name: entry.Name, Path: path, Outcome: ledger.RegistrationRefused, Reason: fmt.Sprintf("%s is not registered: %s", entry.Name, reason)}
		}
		if dir.root == "" {
			out = append(out, refused(at, why))
			continue
		}
		real, reason := placeOf(ctx, kept, dir, at)
		if reason != "" {
			out = append(out, refused(cmp.Or(real, at), reason))
			continue
		}
		clone, reason := cloneAt(ctx, kept, entry, real)
		if reason != "" {
			out = append(out, refused(real, reason))
			continue
		}
		added, err := r.register(ctx, scope, clone, claimed, dryRun, actor)
		if err != nil {
			return out, err
		}
		added.Name, added.Path = entry.Name, real
		if added.Outcome != ledger.RegistrationRefused {
			if added.Checkout, err = keepCheckout(ctx, kept, scope, checkouts, clone, dryRun); err != nil {
				return out, err
			}
			claimed[added.Repository.Name] = true
		}
		out = append(out, added)
	}
	return out, nil
}

// planningDirectory is where this worker's machine keeps the planning repository's clone, which a manifest's
// paths are relative to: the checkout as the worker keeps it, the path that resolves to, and the directory
// that holds the checkout, resolved, which every path a manifest names must stay inside (ADR-0022).
type planningDirectory struct {
	root, clone, parent string
}

// planningClone reads where this worker's machine keeps the planning repository's clone, or why it keeps
// none it can read.
func (r *Repositories) planningClone(ctx context.Context, scope ledger.WorkspaceID, kept ports.Checkouts) (planningDirectory, string, error) {
	planner, err := r.Planning(ctx, scope)
	if errors.Is(err, ports.ErrNotFound) {
		return planningDirectory{}, "the project names no planning repository, whose clone a manifest's paths are relative to", nil
	}
	if err != nil {
		return planningDirectory{}, "", err
	}
	held, err := kept.Of(ctx, scope)
	if err != nil {
		return planningDirectory{}, "", err
	}
	root, ok := held[planner.ID]
	if !ok {
		return planningDirectory{}, fmt.Sprintf("this worker keeps no checkout of the planning repository %s, whose clone a manifest's paths are relative to",
			cmp.Or(planner.Name, planner.ID)), nil
	}
	dir := planningDirectory{root: root}
	if dir.clone, _, err = kept.Resolve(ctx, root); err != nil {
		return planningDirectory{}, fmt.Sprintf("the planning repository's clone at %s cannot be read: %v", root, err), nil
	}
	if dir.parent, _, err = kept.Resolve(ctx, filepath.Dir(root)); err != nil {
		return planningDirectory{}, fmt.Sprintf("%s, which holds the planning repository's clone, cannot be read: %v", filepath.Dir(root), err), nil
	}
	return dir, "", nil
}

// placeOf reads where a manifest's path leads on this worker's machine, every symbolic link followed. It
// must lead inside the directory that holds the planning repository's clone, and not to that clone itself or
// into a directory the installation keeps for itself; it returns why it may not be read when it may not.
func placeOf(ctx context.Context, kept ports.Checkouts, dir planningDirectory, at string) (string, string) {
	if !within(filepath.Dir(dir.root), at) && at != dir.root {
		return "", fmt.Sprintf("its path leads to %s, outside %s, the directory that holds the planning repository's clone", at, filepath.Dir(dir.root))
	}
	real, reserved, err := kept.Resolve(ctx, at)
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return "", fmt.Sprintf("no git clone is at %s, which does not exist", at)
	case err != nil:
		return "", fmt.Sprintf("%s cannot be read: %v", at, err)
	case reserved:
		return real, fmt.Sprintf("%s is inside a directory this installation keeps for itself", real)
	case real == dir.clone:
		return real, fmt.Sprintf("%s is the planning repository's own clone", real)
	case !within(dir.parent, real) && real != at:
		return real, fmt.Sprintf("%s resolves to %s, outside %s, the directory that holds the planning repository's clone", at, real, dir.parent)
	case !within(dir.parent, real):
		return real, fmt.Sprintf("%s is outside %s, the directory that holds the planning repository's clone", real, dir.parent)
	}
	return real, ""
}

// within says a path is inside a directory, and is not the directory itself.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// cloneAt reads the clone whose top is at a manifest entry's resolved path, whose remote is the one the
// manifest names when both name one. It returns why it is not when it is not, and names the clone after the
// entry, so the record takes the manifest's name for the repository when no other repository has it.
func cloneAt(ctx context.Context, kept ports.Checkouts, entry planning.ManifestRepository, real string) (ports.Clone, string) {
	found, unread, err := kept.Find(ctx, []string{real}, 0)
	if err != nil {
		return ports.Clone{}, fmt.Sprintf("%s cannot be read: %v", real, err)
	}
	top := slices.IndexFunc(found, func(clone ports.Clone) bool { return filepath.Clean(clone.Path) == real })
	if top < 0 {
		for _, path := range unread {
			if strings.HasPrefix(path.Reason, "is a git clone") {
				return ports.Clone{}, fmt.Sprintf("%s %s", real, path.Reason)
			}
		}
		return ports.Clone{}, fmt.Sprintf("no git clone is at %s", real)
	}
	clone := found[top]
	// A remote either side leaves empty cannot be compared, so the clone is taken on its path alone.
	if entry.Remote != "" && clone.Remote != "" && ledger.RepositoryIdentity(entry.Remote, "") != ledger.RepositoryIdentity(clone.Remote, clone.ID) {
		return ports.Clone{}, fmt.Sprintf("the clone at %s has remote %s, and the manifest names %s", real, clone.Remote, entry.Remote)
	}
	clone.Directory = entry.Name
	return clone, ""
}
