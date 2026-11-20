package api

import (
	"context"
	"net/http"
	"net/url"
)

// SkipRepository is a member's word that a repository a plan version creates stays uncreated, with why. The reason is
// one to 2000 characters.
type SkipRepository struct {
	Reason string `json:"reason"`
}

// RetryRepository runs the creation of one repository an approved plan version names again, as one entry of a run under
// that approval, and answers the approval with what the run left of each repository.
func (c *ProjectClient) RetryRepository(ctx context.Context, approval, name string) (ApprovalDecided, error) {
	var out ApprovalDecided
	err := c.do(ctx, http.MethodPost, repositoryCreationPath(approval, name, "retry"), nil, &out)
	return out, err
}

// SkipRepository records that a member chose to leave one repository an approved plan version names uncreated, and
// answers the approval with what is left of each repository.
func (c *ProjectClient) SkipRepository(ctx context.Context, approval, name string, in SkipRepository) (ApprovalDecided, error) {
	var out ApprovalDecided
	err := c.do(ctx, http.MethodPost, repositoryCreationPath(approval, name, "skip"), in, &out)
	return out, err
}

// repositoryCreationPath is where one entry's retry or skip lives, as the route reads under a project.
func repositoryCreationPath(approval, name, verb string) string {
	return "/v1/approvals/" + url.PathEscape(approval) + "/repositories/" + url.PathEscape(name) + "/" + verb
}
