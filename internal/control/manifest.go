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
// removed, said with why, by its name, by the remote the entry names, and by the remote its clone has, because
// only a member's own registration brings one back. An entry with `create` that no work order of the version
// names in `repo` is planned, and the approval creates and registers nothing for it. Registration is a
// member's act, so only the member who approves a plan runs this, never a tick or a delegate. Until the
// version is approved it registers nothing, and a dry run, which a member reads before approving, says what
// approving would register. Approving records the outcome on the approval, which RegistrationOf reads, and
// registers once: an approval that recorded its outcome answers with that record, and a later approval of a
// new version is what registers a repository whose clone came after.
func (r *Repositories) RegisterApproved(ctx context.Context, scope ledger.WorkspaceID, approval string, kept ports.Checkouts, creator ports.RepositoryCreator, project string, dryRun bool, actor ledger.Actor) ([]Added, error) {
	if !authority.Allowed(actor, authority.ActionApprovePlan) || !authority.Allowed(actor, authority.ActionRegisterManifest) {
		return nil, workorders.ErrForbidden
	}
	if !dryRun {
		// A second word on one approval waits for the first, so it finds what the first recorded.
		r.registering.Lock()
		defer r.registering.Unlock()
	}
	var entries []planning.ManifestRepository
	var targets []string
	var cause registrationCause
	var recorded *ledger.ManifestRegistration
	var replayed []Added
	err := r.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		row, err := tx.Approvals().Approval(approval)
		if err != nil || row.Kind != authority.KindPlan {
			return err
		}
		if row.State == authority.ApprovalApproved && !dryRun {
			if recorded, err = RegistrationOf(tx, row.ID); err != nil || recorded != nil {
				if err == nil {
					replayed, err = recordedAdded(tx.Repositories(), recorded)
				}
				return err
			}
		}
		if row.State == authority.ApprovalApproved || (dryRun && row.Open()) {
			if entries, err = VersionManifest(tx, row); err != nil {
				return err
			}
			if targets, err = VersionTargets(tx, row); err != nil {
				return err
			}
		}
		cause = registrationCause{Approval: row.ID, Plan: row.Plan, PlanVersion: row.PlanVersion}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if recorded != nil {
		// The approval registered once, and a member's word again, a second press or a command run twice,
		// reads that and runs nothing, so what the first run found stays what the approval says.
		if recorded.Problem != "" {
			err = errors.New(recorded.Problem)
		}
		return replayed, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	targeted := map[string]bool{}
	for _, name := range targets {
		targeted[name] = true
	}
	added, err := r.registerEntries(ctx, scope, entries, kept, manifestRun{dryRun: dryRun, cause: cause, targets: targeted, creator: creator, project: project}, actor)
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

// recordedAdded reads what an approval recorded it registered as the outcomes a run gives, each with the
// record it names as it stands now.
func recordedAdded(repositories ports.RepositoryReader, record *ledger.ManifestRegistration) ([]Added, error) {
	out := make([]Added, 0, len(record.Repositories))
	for _, one := range record.Repositories {
		added := Added{Name: one.Name, Manifest: one.Path, Remote: one.Remote, Path: one.Resolved, Outcome: one.Outcome, Checkout: one.Checkout, Reason: one.Reason, Creation: one.Creation}
		if one.Repository != "" {
			held, err := repositories.Repository(one.Repository)
			switch {
			case err == nil:
				added.Repository = held
			case !errors.Is(err, ports.ErrNotFound):
				return nil, err
			}
		}
		out = append(out, added)
	}
	return out, nil
}

// outcomesOf is what a registration from a manifest keeps of each outcome.
func outcomesOf(added []Added) []ledger.ManifestOutcome {
	out := make([]ledger.ManifestOutcome, 0, len(added))
	for _, one := range added {
		out = append(out, ledger.ManifestOutcome{
			Name: one.Name, Path: one.Manifest, Resolved: one.Path, Remote: one.Remote, Outcome: one.Outcome,
			Repository: one.Repository.ID, Checkout: one.Checkout, Reason: one.Reason, Creation: one.Creation,
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
// manifest that plan version covers. A version the ledger does not hold yet, a branch's that intake records
// only once a member approves it, names those its request carries from the branch's commit. A name two
// manifests list is read once.
func VersionManifest(tx ports.ReadTx, row authority.Approval) ([]planning.ManifestRepository, error) {
	version, held, err := heldVersion(tx, row)
	if err != nil {
		return nil, err
	}
	if !held {
		return planDetailOf(row).Manifest, nil
	}
	if version.Digest != row.SubjectDigest {
		return nil, nil
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

// heldVersion reads the plan version an approval is about, and reports false when the ledger does not hold it.
func heldVersion(tx ports.ReadTx, row authority.Approval) (planning.PlanVersion, bool, error) {
	if row.Plan == "" {
		return planning.PlanVersion{}, false, nil
	}
	version, err := tx.Planning().PlanVersion(row.Plan, row.PlanVersion)
	if errors.Is(err, ports.ErrNotFound) {
		return planning.PlanVersion{}, false, nil
	}
	return version, err == nil, err
}

// VersionTargets lists the repository names the work orders of one plan version name in `repo`, sorted and once
// each: of each contract version the plan version covers, the work orders that version lists, each by the name
// of the repository it targets. A version the ledger does not hold yet names those its request carries from the
// branch's commit.
func VersionTargets(tx ports.ReadTx, row authority.Approval) ([]string, error) {
	version, held, err := heldVersion(tx, row)
	if err != nil {
		return nil, err
	}
	if !held {
		return planDetailOf(row).Targets, nil
	}
	if version.Digest != row.SubjectDigest {
		return nil, nil
	}
	native, err := readNativeIDs(tx.Planning())
	if err != nil {
		return nil, err
	}
	var out []string
	for _, entry := range version.Records {
		if planning.EntryKind(entry.Ref) != "contract" || entry.Record == "" {
			continue
		}
		contract, found, err := contractAt(tx.Planning(), entry)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		listed := map[string]bool{}
		for _, part := range contract.Orders {
			listed[part.Ref] = true
		}
		orders, err := tx.WorkOrders().ByContract(contract.ID)
		if err != nil {
			return nil, err
		}
		for _, order := range orders {
			if !listed["work_order:"+native[nativeKey(workorders.SubjectKind, order.ID)]] {
				continue
			}
			name, err := targetOf(tx.Repositories(), order)
			if err != nil {
				return nil, err
			}
			if name != "" {
				out = append(out, name)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// targetOf is the name of the repository a work order targets: the name its contract gave while the project
// holds none by it, or else the name of the repository it holds.
func targetOf(repositories ports.RepositoryReader, order workorders.WorkOrder) (string, error) {
	if order.UnknownRepository != "" || order.RepositoryID == "" {
		return order.UnknownRepository, nil
	}
	record, err := repositories.Repository(order.RepositoryID)
	if errors.Is(err, ports.ErrNotFound) {
		return "", nil
	}
	return record.Name, err
}

// contractAt reads the version of a contract a plan version names by its record and digest.
func contractAt(planned ports.PlanningReader, entry planning.Entry) (planning.Contract, bool, error) {
	latest, err := planned.LatestContract(entry.Record)
	if errors.Is(err, ports.ErrNotFound) {
		return planning.Contract{}, false, nil
	}
	if err != nil {
		return planning.Contract{}, false, err
	}
	for version := latest.Version; version >= 1; version-- {
		held, err := planned.Contract(entry.Record, version)
		if errors.Is(err, ports.ErrNotFound) {
			continue
		}
		if err != nil {
			return planning.Contract{}, false, err
		}
		if held.Digest == entry.Digest {
			return held, true, nil
		}
	}
	return planning.Contract{}, false, nil
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
	held, removed, removedRemotes := map[string]ledger.Repository{}, map[string]ledger.Repository{}, map[string]ledger.Repository{}
	err := r.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		all, err := tx.Repositories().Repositories()
		for _, record := range all {
			if record.Removed() && record.Remote != "" {
				removedRemotes[ledger.RepositoryIdentity(record.Remote, record.ID)] = record
			}
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
	claimed, seen, forged := map[string]bool{}, seenRun{}, map[string]string{}
	// The project's one repository plans in it until a member names one, so the first registration that
	// adds a second one names it, or the next registration would find no planning repository.
	var naming string
	if dir.root != "" && !dir.planner.Planning {
		naming = dir.planner.ID
	}
	for i, entry := range entries {
		added, err := r.registerEntry(ctx, scope, entry, kept, entryRun{manifestRun: run, dir: dir, why: why, held: held, removed: removed, removedRemotes: removedRemotes, checkouts: checkouts, claimed: claimed, planning: naming, seen: seen, forged: forged}, actor)
		// Each outcome names what the manifest wrote for it, so a member reads what they consent to. An entry that
		// creates its repository writes no remote, and its remote is the one the creation adds.
		added.Name, added.Manifest, added.Remote = entry.Name, entry.Path, entry.Remote
		if added.Creation != nil && added.Remote == "" {
			added.Remote = added.Creation.URL
		}
		out = append(out, added)
		if err != nil {
			// A failure stops the run, and each entry after it is said to be unreached, so a member reads
			// every repository the manifest lists and which of them a later run must register.
			return append(out, unreached(entries[i+1:], dir, entry.Name)...), err
		}
		if (added.Outcome == ledger.Registered || added.Outcome == ledger.Created) && !run.dryRun {
			naming = ""
		}
	}
	return out, nil
}

// unreached is the outcome of each entry of a manifest a run stopped before, with the entry it stopped at.
func unreached(entries []planning.ManifestRepository, dir planningDirectory, stoppedAt string) []Added {
	out := make([]Added, 0, len(entries))
	for _, entry := range entries {
		at := entry.Path
		if dir.root != "" {
			at = filepath.Join(dir.root, filepath.FromSlash(entry.Path))
		}
		out = append(out, Added{
			Name: entry.Name, Manifest: entry.Path, Remote: entry.Remote, Path: at, Outcome: ledger.NotAttempted,
			Reason: fmt.Sprintf("%s was not attempted: the run stopped at %s", entry.Name, stoppedAt),
		})
	}
	return out
}

// entryRun is what registering one entry of a manifest reads: the run, where the planning repository's clone
// is or why there is none, the records the project holds and removed by name, the removed ones again by the
// identity of their remote, this worker's checkouts, the names the run has taken, and the repository to name
// the planning repository, if any.
type entryRun struct {
	manifestRun
	dir            planningDirectory
	why            string
	held, removed  map[string]ledger.Repository
	removedRemotes map[string]ledger.Repository
	checkouts      map[string]string
	claimed        map[string]bool
	planning       string
	// seen are the clones the run registered so far, by the record each came to.
	seen seenRun
	// forged are the forge repositories the entries of the run so far would create, by identity in lowercase, each
	// with the name of the entry that would.
	forged map[string]string
}

// removedAt finds the repository the project removed whose remote is the one given. A remote names the
// repository whatever the clone's history or the name a manifest gives it, so a clone of it with a history of
// its own, or listed under another name, is that repository, which only a member's registration by hand
// brings back. A remote that is empty names none.
func (run entryRun) removedAt(remote string) (ledger.Repository, bool) {
	if remote == "" {
		return ledger.Repository{}, false
	}
	record, ok := run.removedRemotes[ledger.RepositoryIdentity(remote, "")]
	return record, ok
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
	creates := run.creates(entry)
	if record, ok := run.held[entry.Name]; ok {
		named := entry
		if made, err := creationOf(entry); creates && err == nil {
			// An entry that creates its repository names the one it would create.
			named.Remote = made.url
		}
		path, other := heldAs(ctx, kept, dir, named, record, run.checkouts[record.ID], at)
		if other != "" {
			return refused(path, fmt.Sprintf("its name is held by another repository: the project's %s is %s, and the manifest's is %s",
				entry.Name, ledger.RepositoryIdentity(record.Remote, record.ID), other)), nil
		}
		held, err := r.withRootNote(ctx, scope, kept, Added{Path: path, Outcome: ledger.AlreadyRegistered, Repository: record, Checkout: run.checkouts[record.ID]}, run.checkouts[record.ID])
		if err != nil {
			return held, err
		}
		return withUnignoredNote(ctx, kept, dir, entry, held, at)
	}
	if record, ok := run.removed[entry.Name]; ok && !run.restore {
		return refused(at, staysRemoved(record)), nil
	}
	if record, ok := run.removedAt(entry.Remote); ok && !run.restore {
		return refused(at, staysRemoved(record)), nil
	}
	if run.planned(entry) {
		return Added{Outcome: ledger.Planned, Reason: fmt.Sprintf("no work order of this version targets %s", entry.Name)}, nil
	}
	if creates {
		return r.registerCreation(ctx, scope, entry, kept, run, at, actor)
	}
	return r.registerClone(ctx, scope, entry, kept, run, at, nil, actor)
}

// registerClone registers the clone at the path an entry resolves to, or says why there is none to register. An entry
// that creates its repository registers only a clone whose origin is the repository it creates.
func (r *Repositories) registerClone(ctx context.Context, scope ledger.WorkspaceID, entry planning.ManifestRepository, kept ports.Checkouts, run entryRun, at string, made *creation, actor ledger.Actor) (Added, error) {
	dir := run.dir
	refused := func(path, reason string) Added {
		return Added{Path: path, Outcome: ledger.RegistrationRefused, Reason: fmt.Sprintf("%s is not registered: %s", entry.Name, reason)}
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
	if reason = made.refuses(clone, real); reason != "" {
		return refused(real, reason), nil
	}
	if record, ok := run.removedAt(clone.Remote); ok && !run.restore {
		return refused(real, staysRemoved(record)), nil
	}
	added, again, err := r.register(ctx, scope, clone, registration{
		claimed: run.claimed, dryRun: run.dryRun, restore: run.restore, planning: run.planning, cause: run.cause, kept: kept, held: run.checkouts, seen: run.seen,
	}, actor)
	if err != nil {
		return refused(real, err.Error()), err
	}
	added.Path = real
	if again || added.Outcome == ledger.RegistrationRefused {
		return added, nil
	}
	run.claimed[added.Repository.Name] = true
	if added.Checkout, err = keepCheckout(ctx, kept, scope, run.checkouts, clone, added.Repository.ID, run.dryRun); err != nil {
		return added, err
	}
	if added, err = r.withRootNote(ctx, scope, kept, added, added.Checkout); err != nil {
		return added, err
	}
	if added, err = withUnignoredNote(ctx, kept, dir, entry, added, at); err != nil {
		return added, err
	}
	run.seen.note(clone, added)
	r.requestPractices(ctx, scope, added, run.dryRun, actor)
	return added, nil
}

// manifestRun is how a run registers what a manifest lists.
type manifestRun struct {
	dryRun bool
	// restore brings back a repository the project removed, which a member's registration by hand does and
	// a plan approval never does.
	restore bool
	// cause is what each registration's event names it came from.
	cause registrationCause
	// targets are the repository names the work orders of the plan version give in `repo`, for a run of a plan
	// approval, and nil for a manifest a member registers by hand.
	targets map[string]bool
	// creator creates the repositories a plan approval names, on the machine of the worker whose clones kept reads, and
	// project is the name of the project they are created for. A run of a manifest a member registers by hand has neither.
	creator ports.RepositoryCreator
	project string
}

// planned says an entry with `create` is one no work order of the run's plan version names, so the version's
// approval creates and registers nothing for it.
func (run manifestRun) planned(entry planning.ManifestRepository) bool {
	return entry.Create != "" && run.targets != nil && !run.targets[entry.Name]
}

// creates says an entry with `create` is one a work order of the run's plan version names in `repo`, so approving the
// version creates its repository.
func (run manifestRun) creates(entry planning.ManifestRepository) bool {
	return entry.Create != "" && run.targets != nil && run.targets[entry.Name]
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
	real, _, reason := placeAt(ctx, kept, dir, at)
	return real, reason
}

// placeAt is placeOf, and says when the reason it gives is that nothing is at the path.
func placeAt(ctx context.Context, kept ports.Checkouts, dir planningDirectory, at string) (real string, missing bool, reason string) {
	if !within(filepath.Dir(dir.root), at) && at != dir.root {
		return "", false, fmt.Sprintf("its path leads to %s, outside %s, the directory that holds the planning repository's clone", at, filepath.Dir(dir.root))
	}
	real, reserved, err := kept.Resolve(ctx, at)
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return "", true, fmt.Sprintf("no git clone is at %s, which does not exist", at)
	case err != nil:
		return "", false, fmt.Sprintf("%s cannot be read: %v", at, err)
	case reserved:
		return real, false, fmt.Sprintf("%s is inside a directory this installation keeps for itself", real)
	}
	// The clone's directory may be spelled another way, as a disk that ignores case allows, so the paths are
	// compared by the file each names, and not as strings.
	own := real == dir.clone
	if !own {
		var err error
		if own, err = kept.SameFile(ctx, real, dir.clone); err != nil && !errors.Is(err, ports.ErrNotFound) {
			return real, false, fmt.Sprintf("%s cannot be compared with the planning repository's clone: %v", real, err)
		}
	}
	switch {
	case own:
		return real, false, fmt.Sprintf("%s is the planning repository's own clone", real)
	case !within(dir.parent, real) && real != at:
		return real, false, fmt.Sprintf("%s resolves to %s, outside %s, the directory that holds the planning repository's clone", at, real, dir.parent)
	case !within(dir.parent, real):
		return real, false, fmt.Sprintf("%s is outside %s, the directory that holds the planning repository's clone", real, dir.parent)
	}
	return real, false, ""
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
	case dir.root == "" || path == checkout || (checkout != "" && ports.SamePath(ctx, kept, path, checkout)):
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
	if top := slices.IndexFunc(found, func(clone ports.Clone) bool { return filepath.Clean(clone.Path) == path }); top >= 0 && !holdsRoot(found[top], record.ID) {
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
	clone, _, reason := topAt(ctx, kept, real)
	if reason != "" {
		return ports.Clone{}, reason
	}
	// A remote either side leaves empty cannot be compared, so the clone is taken on its path alone.
	if entry.Remote != "" && clone.Remote != "" && ledger.RepositoryIdentity(entry.Remote, "") != ledger.RepositoryIdentity(clone.Remote, clone.ID) {
		return ports.Clone{}, fmt.Sprintf("the clone at %s has remote %s, and the manifest names %s", real, clone.Remote, entry.Remote)
	}
	clone.Directory = entry.Name
	return clone, ""
}

// topAt reads the clone whose top is at a resolved path. It returns why there is none when there is none, and says
// none when the path holds no clone, which a folder that is no clone does.
func topAt(ctx context.Context, kept ports.Checkouts, real string) (clone ports.Clone, none bool, reason string) {
	found, unread, err := kept.Find(ctx, []string{real}, 0)
	if err != nil {
		return ports.Clone{}, false, fmt.Sprintf("%s cannot be read: %v", real, err)
	}
	top := slices.IndexFunc(found, func(clone ports.Clone) bool { return filepath.Clean(clone.Path) == real })
	if top < 0 {
		for _, path := range unread {
			if strings.HasPrefix(path.Reason, "is a git clone") || path.Reason == ledger.ShallowClone {
				return ports.Clone{}, false, fmt.Sprintf("%s %s", real, path.Reason)
			}
		}
		return ports.Clone{}, true, fmt.Sprintf("no git clone is at %s", real)
	}
	return found[top], false, ""
}
