package api

import (
	"context"
	"net/http"
	"net/url"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/planning"
)

// OutlineDocument is a planning document a lane's branch added or changed since the commit it began at.
// Status is new or changed.
type OutlineDocument struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status"`
}

// OutlineMilestone is a milestone the branch holds.
type OutlineMilestone struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Priority int64  `json:"priority"`
}

// OutlineContract is a contract the branch holds, with how many criteria and work orders it lists.
type OutlineContract struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Criteria   int    `json:"criteria"`
	WorkOrders int    `json:"work_orders"`
}

// OutlineQuestion is a question of the planning work order that no one has answered.
type OutlineQuestion struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	Blocking bool   `json:"blocking,omitempty"`
}

// OutlineApproval is the request to approve the plan the branch's tip holds.
type OutlineApproval struct {
	ID      string                  `json:"id"`
	State   authority.ApprovalState `json:"state"`
	Version int64                   `json:"version"`
}

// PlanOutline is what a planning lane's branch holds at its current tip: the plan as the lane has
// committed it so far, read from the repository and not from the ledger, beside the ledger's own record of
// the session's open questions. Only committed files show. A branch that cannot be read, because the lane
// runs on another machine or has no commit yet, answers Unread with the reason, and nothing else.
type PlanOutline struct {
	Unread     string                   `json:"unread,omitempty"`
	Commit     string                   `json:"commit,omitempty"`
	Documents  []OutlineDocument        `json:"documents,omitempty"`
	Milestones []OutlineMilestone       `json:"milestones,omitempty"`
	Contracts  []OutlineContract        `json:"contracts,omitempty"`
	Decisions  []planning.DraftDecision `json:"decisions,omitempty"`
	Questions  []OutlineQuestion        `json:"questions,omitempty"`
	// Approval is set when a request to approve the plan stands for the branch's tip. NotSubmitted says why
	// none does while a blocking question is open.
	Approval     *OutlineApproval `json:"approval,omitempty"`
	NotSubmitted string           `json:"not_submitted,omitempty"`
}

// Outline reads the plan a planning lane has committed to its branch.
func (c *ProjectClient) Outline(ctx context.Context, lane string) (PlanOutline, error) {
	var out PlanOutline
	err := c.do(ctx, http.MethodGet, "/v1/lanes/"+url.PathEscape(lane)+"/outline", nil, &out)
	return out, err
}
