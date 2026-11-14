package control

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// RegisterApproved registers, for the member who approved a plan version, each repository the manifest of
// that version lists and the project lacks (ADR-0022, FR-REPO-8). The worker whose machine keeps the
// planning repository's clone reads each path beside it; a repository with no clone at its path is not
// registered, says why, and a later approval or a member's registration by hand registers it. A repository
// the project holds is named as held, and nothing changes its settings. A repository a member removed stays
// removed, said with why, because only a member's own registration brings one back. Registration is a
// member's act, so only the member who approves a plan runs this, never a tick or a delegate. Until the
// version is approved it registers nothing, and a dry run, which a member reads before approving, says what
// approving would register. Approving records the outcome on the approval, which RegistrationOf reads.
func (r *Repositories) RegisterApproved(ctx context.Context, scope ledger.WorkspaceID, approval string, kept ports.Checkouts, dryRun bool, actor ledger.Actor) ([]Added, error) {
	if !authority.Allowed(actor, authority.ActionApprovePlan) || !authority.Allowed(actor, authority.ActionRegisterManifest) {
		return nil, workorders.ErrForbidden
	}
	var entries []planning.ManifestRepository
	var cause registrationCause
	err := r.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		row, err := tx.Approvals().Approval(approval)
		if err != nil || row.Kind != authority.KindPlan {
			return err
		}
		if row.State == authority.ApprovalApproved || (dryRun && row.Open()) {
			entries, err = VersionManifest(tx, row)
		}
		cause = registrationCause{Approval: row.ID, Plan: row.Plan, PlanVersion: row.PlanVersion}
		return err
	})
	if err != nil || len(entries) == 0 {
		return nil, err
	}
	added, err := r.registerEntries(ctx, scope, entries, kept, manifestRun{dryRun: dryRun, cause: cause}, actor)
	if dryRun {
		return added, err
	}
	// The approval keeps what it registered, so a member who approved on a card, or reads it later, sees it.
	record := ledger.ManifestRegistration{Repositories: outcomesOf(added)}
	if err != nil {
		record.Problem = err.Error()
	}
	if failed := r.recordRegistration(ctx, scope, approval, record, actor); failed != nil {
		return added, errors.Join(err, failed)
	}
	return added, err
}

// outcomesOf is what a registration from a manifest keeps of each outcome.
func outcomesOf(added []Added) []ledger.ManifestOutcome {
	out := make([]ledger.ManifestOutcome, 0, len(added))
	for _, one := range added {
		out = append(out, ledger.ManifestOutcome{
			Name: one.Name, Path: one.Manifest, Resolved: one.Path, Remote: one.Remote, Outcome: one.Outcome,
			Repository: one.Repository.ID, Checkout: one.Checkout, Reason: one.Reason,
		})
	}
	return out
}

// recordRegistration appends what approving a plan version registered, about the approval.
func (r *Repositories) recordRegistration(ctx context.Context, scope ledger.WorkspaceID, approval string, record ledger.ManifestRegistration, actor ledger.Actor) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return r.store.Write(ctx, scope, "approval.manifest_registered", func(tx ports.WriteTx) error {
		_, err := tx.Append(ledger.EventDraft{
			Type: ledger.EventManifestRegistered, Actor: actor, Subject: ledger.Subject{Kind: authority.SubjectApproval, ID: approval}, Data: data,
		})
		return err
	})
}

// RegistrationOf reads what approving a plan version registered of its manifest's repositories, as the
// approval recorded it last, or nil when it recorded nothing.
func RegistrationOf(tx ports.ReadTx, approval string) (*ledger.ManifestRegistration, error) {
	events, err := tx.EventsAbout(ledger.Subject{Kind: authority.SubjectApproval, ID: approval})
	if err != nil {
		return nil, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != ledger.EventManifestRegistered {
			continue
		}
		var record ledger.ManifestRegistration
		if err := json.Unmarshal(events[i].Data, &record); err != nil {
			return nil, fmt.Errorf("approval %s: rev %d: %w", approval, events[i].Rev, err)
		}
		return &record, nil
	}
	return nil, nil
}

// RegisterManifest registers what one manifest lists, as a member setting a project up by hand does with
// no plan approval. Only a member does, because a delegate's card would show a manifest's text and not where
// each path leads. Its paths are read beside the planning repository's clone on this worker's machine, and
// it brings back a repository the project removed, as registering its clone by hand does.
func (r *Repositories) RegisterManifest(ctx context.Context, scope ledger.WorkspaceID, manifest planning.RepositoriesFile, kept ports.Checkouts, dryRun bool, actor ledger.Actor) ([]Added, error) {
	if !authority.Allowed(actor, authority.ActionRegisterManifest) {
		return nil, workorders.ErrForbidden
	}
	return r.registerEntries(ctx, scope, manifest.Repositories, kept, manifestRun{dryRun: dryRun, restore: true, cause: registrationCause{Manifest: manifest.ID}}, actor)
}

// VersionManifest lists the repositories the manifests of one plan version name, at the version of each
// manifest that plan version covers. A version the ledger does not hold yet, as a branch nothing has read,
// names none, and a name two manifests list is read once.
func VersionManifest(tx ports.ReadTx, row authority.Approval) ([]planning.ManifestRepository, error) {
	if row.Plan == "" {
		return nil, nil
	}
	version, err := tx.Planning().PlanVersion(row.Plan, row.PlanVersion)
	if errors.Is(err, ports.ErrNotFound) {
		return nil, nil
	}
	if err != nil || version.Digest != row.SubjectDigest {
		return nil, err
	}
	var out []planning.ManifestRepository
	seen := map[string]bool{}
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
func (r *Repositories) registerEntries(ctx context.Context, scope ledger.WorkspaceID, entries []planning.ManifestRepository, kept ports.Checkouts, run manifestRun, actor ledger.Actor) ([]Added, error) {
	held, removed := map[string]ledger.Repository{}, map[string]ledger.Repository{}
	err := r.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		all, err := tx.Repositories().Repositories()
		for _, record := range all {
			switch {
			case record.Name == "":
			case record.Removed():
				removed[record.Name] = record
			default:
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
	// The project's one repository plans in it until a member names one, so the first registration that
	// adds a second one names it, or the next registration would find no planning repository.
	var naming string
	if dir.root != "" && !dir.planner.Planning {
		naming = dir.planner.ID
	}
	for _, entry := range entries {
		added, err := r.registerEntry(ctx, scope, entry, kept, entryRun{manifestRun: run, dir: dir, why: why, held: held, removed: removed, checkouts: checkouts, claimed: claimed, planning: naming}, actor)
		// Each outcome names what the manifest wrote for it, so a member reads what they consent to.
		added.Name, added.Manifest, added.Remote = entry.Name, entry.Path, entry.Remote
		out = append(out, added)
		if err != nil {
			return out, err
		}
		if added.Outcome == ledger.Registered && !run.dryRun {
			naming = ""
		}
	}
	return out, nil
}

// entryRun is what registering one entry of a manifest reads: the run, where the planning repository's clone
// is or why there is none, the records the project holds and removed by name, this worker's checkouts, the
// names the run has taken, and the repository to name the planning repository, if any.
type entryRun struct {
	manifestRun
	dir           planningDirectory
	why           string
	held, removed map[string]ledger.Repository
	checkouts     map[string]string
	claimed       map[string]bool
	planning      string
}

// registerEntry registers one repository a manifest lists, or says why not. An error is one the store or
// the worker's checkouts gave once the run had begun, and the outcome says how far this entry came.
func (r *Repositories) registerEntry(ctx context.Context, scope ledger.WorkspaceID, entry planning.ManifestRepository, kept ports.Checkouts, run entryRun, actor ledger.Actor) (Added, error) {
	dir := run.dir
	at := entry.Path
	if dir.root != "" {
		at = filepath.Join(dir.root, filepath.FromSlash(entry.Path))
	}
	refused := func(path, reason string) Added {
		return Added{Path: path, Outcome: ledger.RegistrationRefused, Reason: fmt.Sprintf("%s is not registered: %s", entry.Name, reason)}
	}
	if record, ok := run.held[entry.Name]; ok {
		path, other := heldAs(ctx, kept, dir, entry, record, run.checkouts[record.ID], at)
		if other != "" {
			return refused(path, fmt.Sprintf("its name is held by another repository: the project's %s is %s, and the manifest's is %s",
				entry.Name, ledger.RepositoryIdentity(record.Remote, record.ID), other)), nil
		}
		return Added{Path: path, Outcome: ledger.AlreadyRegistered, Repository: record, Checkout: run.checkouts[record.ID]}, nil
	}
	if record, ok := run.removed[entry.Name]; ok && !run.restore {
		return refused(at, staysRemoved(record)), nil
	}
	if dir.root == "" {
		return refused(at, run.why), nil
	}
	real, reason := placeOf(ctx, kept, dir, at)
	if reason != "" {
		return refused(cmp.Or(real, at), reason), nil
	}
	clone, reason := cloneAt(ctx, kept, entry, real)
	if reason != "" {
		return refused(real, reason), nil
	}
	added, err := r.register(ctx, scope, clone, registration{claimed: run.claimed, dryRun: run.dryRun, restore: run.restore, planning: run.planning, cause: run.cause}, actor)
	if err != nil {
		return refused(real, err.Error()), err
	}
	added.Path = real
	if added.Outcome == ledger.RegistrationRefused {
		return added, nil
	}
	run.claimed[added.Repository.Name] = true
	added.Checkout, err = keepCheckout(ctx, kept, scope, run.checkouts, clone, run.dryRun)
	return added, err
}

// manifestRun is how a run registers what a manifest lists.
type manifestRun struct {
	dryRun bool
	// restore brings back a repository the project removed, which a member's registration by hand does and
	// a plan approval never does.
	restore bool
	// cause is what each registration's event names it came from.
	cause registrationCause
}

// staysRemoved says why a repository a member removed is not registered again.
func staysRemoved(record ledger.Repository) string {
	return fmt.Sprintf("repository %s was removed from this project, and only a member's repository add brings it back", cmp.Or(record.Name, record.ID))
}

// planningDirectory is where this worker's machine keeps the planning repository's clone, which a manifest's
// paths are relative to: the checkout as the worker keeps it, the path that resolves to, and the directory
// that holds the checkout, resolved, which every path a manifest names must stay inside (ADR-0022).
type planningDirectory struct {
	planner             ledger.Repository
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
	dir := planningDirectory{planner: planner, root: root}
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

// heldAs reads whether the repository the project holds under a manifest entry's name is the one the entry
// names: by remote, when both name one, or else by the clone at the entry's path, where this worker's
// checkout of the record or the planning repository's clone says so with no git. It returns the path the
// entry resolves to, and the identity of the repository the entry names when that is another. A path it
// cannot read, or that leads outside the bound, tells nothing, and the name reads as the record's.
func heldAs(ctx context.Context, kept ports.Checkouts, dir planningDirectory, entry planning.ManifestRepository, record ledger.Repository, checkout, at string) (string, string) {
	path := at
	if dir.root != "" {
		if real, reserved, err := kept.Resolve(ctx, at); err == nil && !reserved {
			path = real
		}
	}
	if entry.Remote != "" && record.Remote != "" {
		if named := ledger.RepositoryIdentity(entry.Remote, ""); named != ledger.RepositoryIdentity(record.Remote, record.ID) {
			return path, named
		}
		return path, ""
	}
	switch {
	case dir.root == "" || path == checkout:
		return path, ""
	case path == dir.clone && record.ID != dir.planner.ID:
		return path, ledger.RepositoryIdentity(dir.planner.Remote, dir.planner.ID) + ", the planning repository"
	case path == dir.clone || !within(dir.parent, path):
		return path, ""
	}
	found, _, err := kept.Find(ctx, []string{path}, 0)
	if err != nil {
		return path, ""
	}
	if top := slices.IndexFunc(found, func(clone ports.Clone) bool { return filepath.Clean(clone.Path) == path }); top >= 0 && found[top].ID != record.ID {
		return path, ledger.RepositoryIdentity(found[top].Remote, found[top].ID)
	}
	return path, ""
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
