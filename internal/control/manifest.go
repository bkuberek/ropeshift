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
	root, why, err := r.planningClone(ctx, scope, kept)
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
		if root != "" {
			at = filepath.Join(root, entry.Path)
		}
		if record, ok := held[entry.Name]; ok {
			out = append(out, Added{Path: at, Outcome: ledger.AlreadyRegistered, Repository: record, Checkout: checkouts[record.ID]})
			continue
		}
		if root == "" {
			out = append(out, Added{Path: at, Outcome: ledger.RegistrationRefused, Reason: fmt.Sprintf("%s is not registered: %s", entry.Name, why)})
			continue
		}
		clone, reason, err := cloneFor(ctx, kept, entry, at)
		if err != nil {
			return out, err
		}
		if reason != "" {
			out = append(out, Added{Path: at, Outcome: ledger.RegistrationRefused, Reason: fmt.Sprintf("%s is not registered: %s", entry.Name, reason)})
			continue
		}
		added, err := r.register(ctx, scope, clone, claimed, dryRun, actor)
		if err != nil {
			return out, err
		}
		added.Path = at
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

// planningClone is where this worker's machine keeps the planning repository's clone, which a manifest's
// paths are relative to, or why there is none.
func (r *Repositories) planningClone(ctx context.Context, scope ledger.WorkspaceID, kept ports.Checkouts) (root, why string, err error) {
	planner, err := r.Planning(ctx, scope)
	if errors.Is(err, ports.ErrNotFound) {
		return "", "the project names no planning repository, whose clone a manifest's paths are relative to", nil
	}
	if err != nil {
		return "", "", err
	}
	held, err := kept.Of(ctx, scope)
	if err != nil {
		return "", "", err
	}
	if path, ok := held[planner.ID]; ok {
		return path, "", nil
	}
	return "", fmt.Sprintf("this worker keeps no checkout of the planning repository %s, whose clone a manifest's paths are relative to",
		cmp.Or(planner.Name, planner.ID)), nil
}

// cloneFor reads the clone at a manifest entry's path: the top of a git clone, whose remote is the one the
// manifest names when it names one. It returns why it is not when it is not, and names the clone after
// the entry, so the record takes the manifest's name for the repository when no other repository has it.
func cloneFor(ctx context.Context, kept ports.Checkouts, entry planning.ManifestRepository, at string) (ports.Clone, string, error) {
	found, unread, err := kept.Find(ctx, []string{at}, 0)
	if err != nil {
		return ports.Clone{}, "", err
	}
	at = filepath.Clean(at)
	top := slices.IndexFunc(found, func(clone ports.Clone) bool { return filepath.Clean(clone.Path) == at })
	if top < 0 {
		reasons := make([]string, 0, len(unread))
		for _, path := range unread {
			reasons = append(reasons, path.Reason)
		}
		return ports.Clone{}, fmt.Sprintf("no git clone is at %s (%s)", at, cmp.Or(strings.Join(reasons, "; "), "a clone holds the path")), nil
	}
	clone := found[top]
	if entry.Remote != "" && clone.Remote != "" && ledger.RepositoryIdentity(entry.Remote, "") != ledger.RepositoryIdentity(clone.Remote, clone.ID) {
		return ports.Clone{}, fmt.Sprintf("the clone at %s has remote %s, and the manifest names %s", at, clone.Remote, entry.Remote), nil
	}
	clone.Directory = entry.Name
	return clone, "", nil
}
