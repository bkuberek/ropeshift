package api

import (
	"context"
	"net/http"
	"net/url"

	"ropeshift.com/ropeshift/internal/ledger"
)

// RepositoryView is a repository's record as a client reads it.
type RepositoryView struct {
	ledger.Repository
	// Base is the branch lanes start from: the one a member named, or else the default branch.
	Base string `json:"base,omitempty"`
}

// RepositoryList is every repository the workspace keeps a record of.
type RepositoryList struct {
	Repositories []RepositoryView `json:"repositories"`
}

// RepositoryChange is what a member changes. A field left out stays, and an empty Base follows the
// default branch again.
type RepositoryChange struct {
	Base        *string             `json:"base,omitempty"`
	MergeMethod *ledger.MergeMethod `json:"merge_method,omitempty"`
	StartFrom   *ledger.StartFrom   `json:"start_from,omitempty"`
	Integration *bool               `json:"integration,omitempty"`
	// MergeAuthority is how much of each change the person decides after the review.
	MergeAuthority *ledger.MergeAuthority `json:"merge_authority,omitempty"`
}

// ViewOf reads a record with the base it resolves to.
func ViewOf(record ledger.Repository) RepositoryView {
	return RepositoryView{Repository: record, Base: record.Base()}
}

// Repositories lists the records.
func (c *Client) Repositories(ctx context.Context) (RepositoryList, error) {
	var out RepositoryList
	err := c.Do(ctx, http.MethodGet, "/v1/repositories", nil, &out)
	return out, err
}

// Repository reads one record, by its identity or a unique prefix of it.
func (c *Client) Repository(ctx context.Context, id string) (RepositoryView, error) {
	var out RepositoryView
	err := c.Do(ctx, http.MethodGet, repositoryPath(id), nil, &out)
	return out, err
}

// SetRepository changes the settings of one record, which only a member can do.
func (c *Client) SetRepository(ctx context.Context, id string, in RepositoryChange) (RepositoryView, error) {
	var out RepositoryView
	err := c.Do(ctx, http.MethodPatch, repositoryPath(id), in, &out)
	return out, err
}

func repositoryPath(id string) string { return "/v1/repositories/" + url.PathEscape(id) }
