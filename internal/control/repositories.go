package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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
	// MergeAuthority is how much of each change the person decides after the review.
	MergeAuthority *ledger.MergeAuthority
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
		var named string
		var moved bool
		var err error
		if record, named, moved, err = detectIn(tx.Repositories(), found); err != nil || !moved {
			return err
		}
		event, err := repositoryEvent(ledger.EventRepositoryDetected, found.ID, detectedEvent{Name: named, Remote: found.Remote, DefaultBranch: found.DefaultBranch}, actor)
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

// detectIn makes or refreshes a record in a transaction, and says the name it gave the record, if it gave
// one, and whether anything moved, because a look that finds the same facts appends nothing.
func detectIn(repositories ports.RepositoryWriter, found DetectedRepository) (ledger.Repository, string, bool, error) {
	record, err := repositories.Repository(found.ID)
	if errors.Is(err, ports.ErrNotFound) {
		record = ledger.NewRepository(found.ID)
		record.Remote, record.DefaultBranch = found.Remote, found.DefaultBranch
		if record.Name, err = freeName(repositories, found); err != nil {
			return ledger.Repository{}, "", false, err
		}
		if _, err := repositories.Insert(record); err != nil {
			return ledger.Repository{}, "", false, err
		}
		return record, record.Name, true, nil
	}
	if err != nil {
		return ledger.Repository{}, "", false, err
	}
	moved, err := repositories.Detected(found.ID, found.Remote, found.DefaultBranch)
	if err != nil {
		return ledger.Repository{}, "", false, err
	}
	var named string
	if record.Name == "" {
		if named, err = freeName(repositories, found); err != nil {
			return ledger.Repository{}, "", false, err
		}
	}
	if !moved && named == "" {
		return record, "", false, nil
	}
	if record, err = repositories.Repository(found.ID); err != nil {
		return ledger.Repository{}, "", false, err
	}
	if named != "" {
		record.Name = named
		if err := repositories.Settings(record); err != nil {
			return ledger.Repository{}, "", false, err
		}
	}
	return record, named, true, nil
}

// freeName names a repository as ADR-0022 does, among the names the project's records hold.
func freeName(repositories ports.RepositoryReader, found DetectedRepository) (string, error) {
	all, err := repositories.Repositories()
	if err != nil {
		return "", err
	}
	taken := map[string]bool{}
	for _, record := range all {
		taken[record.Name] = true
	}
	return ledger.NameRepository(found.Directory, found.Remote, found.ID, func(name string) bool { return taken[name] }), nil
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
		if after == before {
			return nil
		}
		if change.Name != nil {
			if err := nameIsFree(tx.Repositories(), after); err != nil {
				return err
			}
		}
		if err := tx.Repositories().Settings(after); err != nil {
			return err
		}
		event, err := repositoryEvent(ledger.EventRepositoryChanged, id, changedEvent{Before: before, After: after}, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	if err != nil {
		return ledger.Repository{}, err
	}
	return after, nil
}

// nameIsFree refuses a name another record of the project holds, because a name names one repository,
// and an empty one, which names none.
func nameIsFree(repositories ports.RepositoryReader, record ledger.Repository) error {
	if err := ledger.CheckRepositoryName(record.Name); err != nil {
		return err
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
	if change.MergeAuthority != nil {
		record.MergeAuthority = *change.MergeAuthority
	}
	return record
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

// Resolve finds the record a name, an ID, or a unique prefix of an ID names, because an identity is a
// commit hash and nobody types forty characters. A name comes first, because a person chose it.
func (r *Repositories) Resolve(ctx context.Context, scope ledger.WorkspaceID, name string) (ledger.Repository, error) {
	all, err := r.List(ctx, scope)
	if err != nil {
		return ledger.Repository{}, err
	}
	for _, record := range all {
		if record.Name != "" && record.Name == name {
			return record, nil
		}
	}
	var found []ledger.Repository
	for _, record := range all {
		switch {
		case record.ID == name:
			return record, nil
		case len(name) >= minRepositoryPrefix && len(record.ID) > len(name) && record.ID[:len(name)] == name:
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

// minRepositoryPrefix is the shortest prefix of an identity that names a repository.
const minRepositoryPrefix = 7

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
