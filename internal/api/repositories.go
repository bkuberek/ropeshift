package api

import (
	"context"
	"net/http"
	"net/url"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/measures"
)

// RepositoryView is a repository's record as a client reads it.
type RepositoryView struct {
	ledger.Repository
	// Base is the branch lanes start from: the one a member named, or else the default branch.
	Base string `json:"base,omitempty"`
	// ReviewAgreement is how this member's decisions stood against the review agent's verdicts here, over
	// the last 30 days and over all time. The level above is the one in force, and nothing here changes it.
	ReviewAgreement measures.AgreementPeriods `json:"review_agreement"`
}

// RepositoryList is every repository the workspace keeps a record of.
type RepositoryList struct {
	Repositories []RepositoryView `json:"repositories"`
}

// RepositoryChange is what a member changes. A field left out stays, and an empty Base follows the
// default branch again.
type RepositoryChange struct {
	// Name is what commands and a work order's `repo` call the repository, unique in its project.
	Name        *string             `json:"name,omitempty"`
	Base        *string             `json:"base,omitempty"`
	MergeMethod *ledger.MergeMethod `json:"merge_method,omitempty"`
	StartFrom   *ledger.StartFrom   `json:"start_from,omitempty"`
	// ReviewTarget is where accepted work is reviewed and lands: branch or pull_request.
	ReviewTarget *ledger.ReviewTarget `json:"review_target,omitempty"`
	// MergeAuthority is how much of each change the person decides after the review.
	MergeAuthority *ledger.MergeAuthority `json:"merge_authority,omitempty"`
}

// RepositoryAdd registers, in a project, the repositories of clones on the worker of the daemon's machine
// (ADR-0022).
type RepositoryAdd struct {
	// Paths are absolute paths on the worker's machine. A path that is the top of a clone is that clone,
	// and any other directory holds the clones among its children, down to Depth levels.
	Paths []string `json:"paths"`
	// Depth is how many levels below a path are searched. Zero searches one.
	Depth int `json:"depth,omitempty"`
	// DryRun says what would be registered and refused, and records nothing.
	DryRun bool `json:"dry_run,omitempty"`
}

// RepositoryAdded is what became of one clone, or of a path that holds none.
type RepositoryAdded struct {
	Path    string                   `json:"path"`
	Outcome ledger.RepositoryOutcome `json:"outcome"`
	// Repository is the record as it stands, or would after a dry run. A path with no clone has none.
	Repository *ledger.Repository `json:"repository,omitempty"`
	// Checkout is the path the worker keeps for the repository.
	Checkout string `json:"checkout,omitempty"`
	// Reason says why it was refused, or that a clone is a second one of a repository.
	Reason string `json:"reason,omitempty"`
}

// RepositoryAddReport is the outcome of every clone and path, in the order they were found.
type RepositoryAddReport struct {
	DryRun       bool              `json:"dry_run"`
	Repositories []RepositoryAdded `json:"repositories"`
}

// Refused counts the clones and paths that registered nothing because of a refusal.
func (r RepositoryAddReport) Refused() int {
	n := 0
	for _, added := range r.Repositories {
		if added.Outcome == ledger.RegistrationRefused {
			n++
		}
	}
	return n
}

// ViewOf reads a record with the base it resolves to, and the review agreement of its repository when the
// workspace has any to give.
func ViewOf(record ledger.Repository, agreement measures.ReviewAgreement) RepositoryView {
	view := RepositoryView{Repository: record, Base: record.Base()}
	for _, repo := range agreement.Repositories {
		if repo.Repository == record.ID {
			view.ReviewAgreement = repo.AgreementPeriods
		}
	}
	return view
}

// Repositories lists the records.
func (c *ProjectClient) Repositories(ctx context.Context) (RepositoryList, error) {
	var out RepositoryList
	err := c.do(ctx, http.MethodGet, "/v1/repositories", nil, &out)
	return out, err
}

// Repository reads one record, by its name, its identity, or a unique prefix of the identity.
func (c *ProjectClient) Repository(ctx context.Context, id string) (RepositoryView, error) {
	var out RepositoryView
	err := c.do(ctx, http.MethodGet, repositoryPath(id), nil, &out)
	return out, err
}

// SetRepository changes the settings of one record, which only a member can do.
func (c *ProjectClient) SetRepository(ctx context.Context, id string, in RepositoryChange) (RepositoryView, error) {
	var out RepositoryView
	err := c.do(ctx, http.MethodPatch, repositoryPath(id), in, &out)
	return out, err
}

// AddRepositories registers the repositories of the clones at paths, which only a member can do.
func (c *ProjectClient) AddRepositories(ctx context.Context, in RepositoryAdd) (RepositoryAddReport, error) {
	var out RepositoryAddReport
	err := c.do(ctx, http.MethodPost, "/v1/repositories", in, &out)
	return out, err
}

// RemoveRepository takes a repository out of its project, which only a member can do. Its record stays as
// history.
func (c *ProjectClient) RemoveRepository(ctx context.Context, id string) (RepositoryView, error) {
	var out RepositoryView
	err := c.do(ctx, http.MethodPost, repositoryPath(id)+"/remove", struct{}{}, &out)
	return out, err
}

func repositoryPath(id string) string { return "/v1/repositories/" + url.PathEscape(id) }
