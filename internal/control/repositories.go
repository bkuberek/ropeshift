package control

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/measures"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Repositories holds the use cases over the record of each repository. Git gives the remote and the
// default branch, and only a member gives the rest (FR-REPO-1).
type Repositories struct {
	store ports.Store
}

// NewRepositories returns the use cases over a Store.
func NewRepositories(store ports.Store) *Repositories {
	return &Repositories{store: store}
}

// DetectedRepository is what git said of a repository when a source was read.
type DetectedRepository struct {
	ID            string
	Remote        string
	DefaultBranch string
	// Directory is the base name of the clone's directory, which names a new record (ADR-0022).
	Directory string
}

// RepositoryChange is what a member changes. A nil field stays, and an empty Base follows the default
// branch again.
type RepositoryChange struct {
	// Name is what commands and a work order's `repo` call the repository, unique in its project.
	Name        *string
	Base        *string
	MergeMethod *ledger.MergeMethod
	StartFrom   *ledger.StartFrom
	// ReviewTarget is where accepted work is reviewed and lands.
	ReviewTarget *ledger.ReviewTarget
	// ForgeBinding names the repository on its forge as HOST/OWNER/REPO, which review target pull_request
	// needs. Empty takes it from the remote again.
	ForgeBinding *string
	// MergeAuthority is how much of each change the person decides after the review.
	MergeAuthority *ledger.MergeAuthority
	// Planning names this repository the project's planning repository, and takes that from any other, or
	// with false takes it from this one (ADR-0022).
	Planning *bool
}

// detectedEvent is the data of EventRepositoryDetected. Name is there when the read named the record.
type detectedEvent struct {
	Name          string `json:"name,omitempty"`
	Remote        string `json:"remote,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
}

// changedEvent is the data of EventRepositoryChanged: the record before and after, so the audit reads
// without a diff.
type changedEvent struct {
	Before ledger.Repository `json:"before"`
	After  ledger.Repository `json:"after"`
}

func repositoryEvent(eventType, id string, data any, actor ledger.Actor) (ledger.EventDraft, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return ledger.EventDraft{}, err
	}
	return ledger.EventDraft{
		Type: eventType, Actor: actor, Subject: ledger.Subject{Kind: ledger.SubjectRepository, ID: id}, Data: raw,
	}, nil
}

// Detect keeps what git said of a repository. The first look makes the record with the settings of a
// person who chose nothing and a name (ADR-0022), a later look refreshes the remote and the default
// branch and names a record made before names, and a look that finds the same facts changes nothing. A
// member's settings are never touched here.
func (r *Repositories) Detect(ctx context.Context, scope ledger.WorkspaceID, found DetectedRepository, actor ledger.Actor) (ledger.Repository, error) {
	if found.ID == "" {
		return ledger.Repository{}, fmt.Errorf("%w: no id", ledger.ErrInvalidRepository)
	}
	var record ledger.Repository
	err := r.store.Write(ctx, scope, "repository.detect", func(tx ports.WriteTx) error {
		look, err := detectIn(tx.Repositories(), found, nil)
		if record = look.record; err != nil || !look.moved {
			return err
		}
		if err := appendUnshadowed(tx, look, actor); err != nil {
			return err
		}
		event, err := repositoryEvent(ledger.EventRepositoryDetected, found.ID, detectedEvent{Name: look.named, Remote: found.Remote, DefaultBranch: found.DefaultBranch}, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	if err != nil {
		return ledger.Repository{}, err
	}
	return record, nil
}

// detection is what one look at a repository did to its record.
type detection struct {
	record ledger.Repository
	// unshadowed are the records the look named again, because each name spelled the new record's identity.
	unshadowed []changedEvent
	// named is the name the look gave the record, if it gave one.
	named string
	// made says the look made the record, and moved that it changed it, because a look that finds the
	// same facts appends nothing.
	made, moved bool
}

// errRemoved refuses to read a repository its project removed, until a member registers it again.
func errRemoved(record ledger.Repository) error {
	return fmt.Errorf("%w: repository %s was removed from this project; register it again with repository add",
		ledger.ErrRepositoryState, cmp.Or(record.Name, record.ID))
}

// detectIn makes or refreshes a record in a transaction. A new name avoids the names the project's
// records hold and the ones claimed, which a run that registers several clones claims as it goes.
func detectIn(repositories ports.RepositoryWriter, found DetectedRepository, claimed map[string]bool) (detection, error) {
	record, err := repositories.Repository(found.ID)
	if errors.Is(err, ports.ErrNotFound) {
		unshadowed, err := unshadow(repositories, found.ID, claimed)
		if err != nil {
			return detection{}, err
		}
		record = ledger.NewRepository(found.ID)
		record.Remote, record.DefaultBranch = found.Remote, found.DefaultBranch
		if record.Name, err = freeName(repositories, found, claimed); err != nil {
			return detection{}, err
		}
		if _, err := repositories.Insert(record); err != nil {
			return detection{}, err
		}
		return detection{record: record, unshadowed: unshadowed, named: record.Name, made: true, moved: true}, nil
	}
	if err != nil {
		return detection{}, err
	}
	if record.Removed() {
		return detection{record: record}, errRemoved(record)
	}
	moved, err := repositories.Detected(found.ID, found.Remote, found.DefaultBranch)
	if err != nil {
		return detection{}, err
	}
	var named string
	if record.Name == "" {
		if named, err = freeName(repositories, found, claimed); err != nil {
			return detection{}, err
		}
	}
	if !moved && named == "" {
		return detection{record: record}, nil
	}
	if record, err = repositories.Repository(found.ID); err != nil {
		return detection{}, err
	}
	if named != "" {
		record.Name = named
		if err := repositories.Settings(record); err != nil {
			return detection{}, err
		}
	}
	return detection{record: record, named: named, moved: true}, nil
}

// appendUnshadowed records each rename a look made to keep a new identity its own record's.
func appendUnshadowed(tx ports.WriteTx, look detection, actor ledger.Actor) error {
	for _, change := range look.unshadowed {
		event, err := repositoryEvent(ledger.EventRepositoryChanged, change.After.ID, change, actor)
		if err != nil {
			return err
		}
		if _, err := tx.Append(event); err != nil {
			return err
		}
	}
	return nil
}

// freeName names a repository as ADR-0022 does, among the names the project's records hold and the ones
// claimed.
func freeName(repositories ports.RepositoryReader, found DetectedRepository, claimed map[string]bool) (string, error) {
	all, err := repositories.Repositories()
	if err != nil {
		return "", err
	}
	taken := maps.Clone(claimed)
	if taken == nil {
		taken = map[string]bool{}
	}
	for _, record := range all {
		taken[record.Name] = true
	}
	shadows := func(name string) bool {
		return ledger.IdentityShaped(name) || slices.ContainsFunc(all, func(record ledger.Repository) bool {
			return record.ID != found.ID && ledger.ShadowsIdentity(name, record.ID)
		})
	}
	return ledger.NameRepository(found.Directory, found.Remote, found.ID, func(name string) bool { return taken[name] || shadows(name) }), nil
}

// unshadow names again each record whose name spells a new repository's identity, or a prefix that would
// name it, as automatic naming names a record, so that identity names only its own record. Each rename is
// a change the caller records.
func unshadow(repositories ports.RepositoryWriter, id string, claimed map[string]bool) ([]changedEvent, error) {
	all, err := repositories.Repositories()
	if err != nil {
		return nil, err
	}
	taken := maps.Clone(claimed)
	if taken == nil {
		taken = map[string]bool{}
	}
	for _, record := range all {
		taken[record.Name] = true
	}
	shadows := func(name string) bool {
		return ledger.IdentityShaped(name) || ledger.ShadowsIdentity(name, id) || slices.ContainsFunc(all, func(record ledger.Repository) bool {
			return ledger.ShadowsIdentity(name, record.ID)
		})
	}
	var renamed []changedEvent
	for _, record := range all {
		if record.Name == "" || !ledger.ShadowsIdentity(record.Name, id) {
			continue
		}
		before := record
		record.Name = ledger.NameRepository(record.Name, "", record.ID, func(name string) bool { return taken[name] || shadows(name) })
		taken[record.Name] = true
		if err := repositories.Settings(record); err != nil {
			return nil, err
		}
		renamed = append(renamed, changedEvent{Before: before, After: record})
	}
	return renamed, nil
}

// Change applies a member's settings to a repository. The record that results must hold together, and a
// change that changes nothing appends nothing.
func (r *Repositories) Change(ctx context.Context, scope ledger.WorkspaceID, id string, change RepositoryChange, actor ledger.Actor) (ledger.Repository, error) {
	if !authority.Allowed(actor, authority.ActionSetRepository) {
		return ledger.Repository{}, workorders.ErrForbidden
	}
	var after ledger.Repository
	err := r.store.Write(ctx, scope, "repository.change", func(tx ports.WriteTx) error {
		before, err := tx.Repositories().Repository(id)
		if err != nil {
			return err
		}
		after = apply(before, change)
		if err := after.Validate(); err != nil {
			return err
		}
		// Raising merge authority and moving review to a pull request are never a delegate's to run with no
		// member, so each needs an action of its own beyond changing a repository's settings (ADR-0024).
		if after.Level().Rank() > before.Level().Rank() && !authority.Allowed(actor, authority.ActionRaiseMergeAuthority) {
			return workorders.ErrForbidden
		}
		if after.ReviewTarget == ledger.ReviewOnPullRequest && before.ReviewTarget != ledger.ReviewOnPullRequest && !authority.Allowed(actor, authority.ActionReviewPullRequest) {
			return workorders.ErrForbidden
		}
		if err := needsBinding(after, change); err != nil {
			return err
		}
		if after == before {
			return nil
		}
		if change.Name != nil {
			if err := nameIsFree(tx.Repositories(), after); err != nil {
				return err
			}
		}
		if after.Planning && !before.Planning {
			if err := takePlanning(tx, id, actor); err != nil {
				return err
			}
		}
		return saveSettings(tx, before, after, actor)
	})
	if err != nil {
		return ledger.Repository{}, err
	}
	return after, nil
}

// saveSettings keeps a member's change to one record, with the event that names it before and after.
func saveSettings(tx ports.WriteTx, before, after ledger.Repository, actor ledger.Actor) error {
	if err := tx.Repositories().Settings(after); err != nil {
		return err
	}
	event, err := repositoryEvent(ledger.EventRepositoryChanged, after.ID, changedEvent{Before: before, After: after}, actor)
	if err != nil {
		return err
	}
	_, err = tx.Append(event)
	return err
}

// takePlanning takes the plan of record from every other repository of the project, each with an event of
// its own, so a project names at most one planning repository.
func takePlanning(tx ports.WriteTx, id string, actor ledger.Actor) error {
	all, err := tx.Repositories().Repositories()
	if err != nil {
		return err
	}
	for _, other := range all {
		if other.ID == id || !other.Planning {
			continue
		}
		cleared := other
		cleared.Planning = false
		if err := saveSettings(tx, other, cleared, actor); err != nil {
			return err
		}
	}
	return nil
}

// PlanningIn finds the project's planning repository: the one a member named, or else its one active
// repository (ADR-0022). A project of several repositories that named none has none, which is ErrNotFound.
func PlanningIn(repositories ports.RepositoryReader) (ledger.Repository, error) {
	all, err := repositories.Repositories()
	if err != nil {
		return ledger.Repository{}, err
	}
	var active []ledger.Repository
	for _, record := range all {
		if record.Removed() {
			continue
		}
		if record.Planning {
			return record, nil
		}
		active = append(active, record)
	}
	if len(active) == 1 {
		return active[0], nil
	}
	return ledger.Repository{}, fmt.Errorf("the project names no planning repository: %w", ports.ErrNotFound)
}

// Planning reads the project's planning repository, or ErrNotFound when it has none.
func (r *Repositories) Planning(ctx context.Context, scope ledger.WorkspaceID) (ledger.Repository, error) {
	var record ledger.Repository
	err := r.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		record, err = PlanningIn(tx.Repositories())
		return err
	})
	return record, err
}

// removedEvent is the data of EventRepositoryRemoved.
type removedEvent struct {
	Name     string `json:"name,omitempty"`
	Identity string `json:"identity"`
}

// Remove takes a repository out of its project once no open work order targets it (ADR-0021). The record
// stays as history with state removed and keeps its name, its identity leaves the repository index so
// another project may register it, and this worker forgets its checkout, when it keeps checkouts.
func (r *Repositories) Remove(ctx context.Context, scope ledger.WorkspaceID, id string, kept ports.Checkouts, actor ledger.Actor) (ledger.Repository, error) {
	if !authority.Allowed(actor, authority.ActionRegisterRepository) {
		return ledger.Repository{}, workorders.ErrForbidden
	}
	var record ledger.Repository
	err := r.store.Write(ctx, scope, "repository.remove", func(tx ports.WriteTx) error {
		var err error
		if record, err = tx.Repositories().Repository(id); err != nil {
			return err
		}
		if record.Removed() {
			return fmt.Errorf("%w: repository %s is removed already", ledger.ErrRepositoryState, cmp.Or(record.Name, id))
		}
		orders, err := tx.WorkOrders().List()
		if err != nil {
			return err
		}
		for _, order := range orders {
			if order.RepositoryID == id && !workorders.Terminal(order.State) {
				return fmt.Errorf("%w: work order %s targets repository %s and is %s", ledger.ErrRepositoryState, order.ID, cmp.Or(record.Name, id), order.State)
			}
		}
		if err := tx.Repositories().Remove(id); err != nil {
			return err
		}
		record.State, record.Planning = ledger.RepositoryRemoved, false
		event, err := repositoryEvent(ledger.EventRepositoryRemoved, id, removedEvent{Name: record.Name, Identity: ledger.RepositoryIdentity(record.Remote, id)}, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	if err != nil || kept == nil {
		return record, err
	}
	return record, kept.Forget(ctx, scope, id)
}

// nameIsFree refuses a name another record of the project holds, because a name names one repository,
// and an empty one, which names none.
func nameIsFree(repositories ports.RepositoryReader, record ledger.Repository) error {
	if err := ledger.CheckRepositoryName(record.Name); err != nil {
		return err
	}
	if ledger.IdentityShaped(record.Name) {
		return fmt.Errorf("%w: name %q has the shape of a repository's identity", ledger.ErrInvalidRepository, record.Name)
	}
	all, err := repositories.Repositories()
	if err != nil {
		return err
	}
	for _, other := range all {
		if other.ID != record.ID && ledger.ShadowsIdentity(record.Name, other.ID) {
			return fmt.Errorf("%w: name %q reads as the identity of repository %s", ledger.ErrInvalidRepository, record.Name, other.ID)
		}
	}
	holder, err := repositories.Named(record.Name)
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return nil
	case err != nil:
		return err
	case holder.ID != record.ID:
		return fmt.Errorf("%w: repository %s is named %s", ledger.ErrRepositoryState, holder.ID, record.Name)
	}
	return nil
}

func apply(record ledger.Repository, change RepositoryChange) ledger.Repository {
	if change.Name != nil {
		record.Name = *change.Name
	}
	if change.Base != nil {
		record.BaseBranch = *change.Base
	}
	if change.MergeMethod != nil {
		record.MergeMethod = *change.MergeMethod
	}
	if change.StartFrom != nil {
		record.StartFrom = *change.StartFrom
	}
	if change.ReviewTarget != nil {
		record.ReviewTarget = *change.ReviewTarget
	}
	if change.ForgeBinding != nil {
		record.ForgeBinding = *change.ForgeBinding
	}
	if record.Target() == ledger.ReviewOnPullRequest && record.ForgeBinding == "" {
		record.ForgeBinding = ledger.BindingFromRemote(record.Remote)
	}
	if change.MergeAuthority != nil {
		record.MergeAuthority = *change.MergeAuthority
	}
	if change.Planning != nil {
		record.Planning = *change.Planning
	}
	return record
}

// needsBinding refuses a change that sets review target pull_request, or a binding, and leaves a record that
// reviews through pull requests with no repository on a forge to open them in (FR-MERGE-1). Ropeshift takes
// the binding from a remote on a forge, and a member names it for any other.
func needsBinding(after ledger.Repository, change RepositoryChange) error {
	touched := change.ForgeBinding != nil || (change.ReviewTarget != nil && *change.ReviewTarget == ledger.ReviewOnPullRequest)
	if !touched || after.Target() != ledger.ReviewOnPullRequest || after.ForgeBinding != "" {
		return nil
	}
	remote := after.Remote
	if remote == "" {
		remote = "none"
	}
	return fmt.Errorf("%w: review target pull_request needs a forge binding, and the remote (%s) names no repository on a forge; name one as HOST/OWNER/REPO",
		ledger.ErrInvalidRepository, remote)
}

// Get reads one record, or ErrNotFound.
func (r *Repositories) Get(ctx context.Context, scope ledger.WorkspaceID, id string) (ledger.Repository, error) {
	var record ledger.Repository
	err := r.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		record, err = tx.Repositories().Repository(id)
		return err
	})
	return record, err
}

// List reads every record, by ID.
func (r *Repositories) List(ctx context.Context, scope ledger.WorkspaceID) ([]ledger.Repository, error) {
	var records []ledger.Repository
	err := r.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		records, err = tx.Repositories().Repositories()
		return err
	})
	return records, err
}

// Resolve finds the record an ID, a name, or a unique prefix of an ID names, because an identity is a
// commit hash and nobody types forty characters. A whole identity comes first, so it always names its own
// record whatever another record is called, then a name, because a person chose it, then a prefix.
func (r *Repositories) Resolve(ctx context.Context, scope ledger.WorkspaceID, name string) (ledger.Repository, error) {
	all, err := r.List(ctx, scope)
	if err != nil {
		return ledger.Repository{}, err
	}
	for _, record := range all {
		if record.ID == name {
			return record, nil
		}
	}
	for _, record := range all {
		if record.Name != "" && record.Name == name {
			return record, nil
		}
	}
	var found []ledger.Repository
	for _, record := range all {
		if len(name) >= ledger.MinRepositoryPrefix && len(record.ID) > len(name) && record.ID[:len(name)] == name {
			found = append(found, record)
		}
	}
	switch len(found) {
	case 0:
		return ledger.Repository{}, fmt.Errorf("repository %s: %w", name, ports.ErrNotFound)
	case 1:
		return found[0], nil
	}
	return ledger.Repository{}, fmt.Errorf("%w: %q names %d repositories", ledger.ErrInvalidRepository, name, len(found))
}

// ForgeHealth reads what each worker last said of its forge sign-in, by repository, with each worker's name.
func (r *Repositories) ForgeHealth(ctx context.Context, scope ledger.WorkspaceID) ([]ledger.ForgeHealth, error) {
	var out []ledger.ForgeHealth
	err := r.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		out, err = ForgeHealthIn(tx)
		return err
	})
	return out, err
}

// ForgeHealthIn reads the forge health inside a read, with each worker's name.
func ForgeHealthIn(tx ports.ReadTx) ([]ledger.ForgeHealth, error) {
	all, err := tx.Forge().Health()
	if err != nil {
		return nil, err
	}
	for i := range all {
		all[i].WorkerName = workerName(tx.Workers(), all[i].Worker)
	}
	return all, nil
}

// ReviewAgreement reads how a member's decisions stood against the review agent's verdicts, for the workspace
// and for each repository (FR-CAL-2). It reads the records of those decisions and no event.
func (r *Repositories) ReviewAgreement(ctx context.Context, scope ledger.WorkspaceID) (measures.ReviewAgreement, error) {
	var out measures.ReviewAgreement
	err := r.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		out, err = ReviewAgreementIn(tx)
		return err
	})
	return out, err
}
