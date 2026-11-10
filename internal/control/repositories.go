package control

import (
	"context"
	"encoding/json"
	"fmt"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
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
}

// RepositoryChange is what a member changes. A nil field stays, and an empty Base follows the default
// branch again.
type RepositoryChange struct {
	Base        *string
	MergeMethod *ledger.MergeMethod
	StartFrom   *ledger.StartFrom
	Integration *bool
}

// detectedEvent is the data of EventRepositoryDetected.
type detectedEvent struct {
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
// person who chose nothing, a later look refreshes the remote and the default branch, and a look that
// finds the same facts changes nothing. A member's settings are never touched here.
func (r *Repositories) Detect(ctx context.Context, scope ledger.WorkspaceID, found DetectedRepository, actor ledger.Actor) (ledger.Repository, error) {
	if found.ID == "" {
		return ledger.Repository{}, fmt.Errorf("%w: no id", ledger.ErrInvalidRepository)
	}
	var record ledger.Repository
	err := r.store.Write(ctx, scope, "repository.detect", func(tx ports.WriteTx) error {
		record = ledger.NewRepository(found.ID)
		record.Remote, record.DefaultBranch = found.Remote, found.DefaultBranch
		made, err := tx.Repositories().Insert(record)
		if err != nil {
			return err
		}
		if !made {
			moved, err := tx.Repositories().Detected(found.ID, found.Remote, found.DefaultBranch)
			if err != nil {
				return err
			}
			if record, err = tx.Repositories().Repository(found.ID); err != nil || !moved {
				return err
			}
		}
		event, err := repositoryEvent(ledger.EventRepositoryDetected, found.ID, detectedEvent{Remote: found.Remote, DefaultBranch: found.DefaultBranch}, actor)
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

func apply(record ledger.Repository, change RepositoryChange) ledger.Repository {
	if change.Base != nil {
		record.BaseBranch = *change.Base
	}
	if change.MergeMethod != nil {
		record.MergeMethod = *change.MergeMethod
	}
	if change.StartFrom != nil {
		record.StartFrom = *change.StartFrom
	}
	if change.Integration != nil {
		record.Integration = *change.Integration
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

// Resolve finds the record an ID or a unique prefix of one names, because an identity is a commit hash
// and nobody types forty characters.
func (r *Repositories) Resolve(ctx context.Context, scope ledger.WorkspaceID, name string) (ledger.Repository, error) {
	all, err := r.List(ctx, scope)
	if err != nil {
		return ledger.Repository{}, err
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
